# Belay Guard: secret protection

Status: approved design, in build.

## Problem

Plain-text copies of credentials pile up around AI agent work. A key pasted into a chat, an `.env` an agent edits, a password typed into a prompt: each one ends up in three places Belay touches.

1. Belay's content store (`.belay/objects/`), which keeps every saved version of every file.
2. Belay's git projection (`belay project`), which pushes net session changes to a remote.
3. Agent session logs (for example Claude Code transcripts), which Belay reads but does not own.

Today the only protection is the default `.belayignore` entries for `.env` files. Existing transcript scrubbers miss credentials written in ordinary sentences, such as a Google app password pasted as "here is the app password: <four groups of four lowercase letters>".

## Principles

These are binding. A change that breaks one of them is a bug.

- **Never get in the way.** Belay never blocks a save, a session, a prompt, or a push. If someone uninstalls Belay because it got in their way, the feature failed.
- **Never alter what is sent to an AI.** There is no prompt guard.
- **Never change a byte the user would want back.** Restore stays byte-exact. The store is encrypted, never redacted.
- **Never modify the user's working files.**
- **Users stay in charge of their own comfort level.** Defaults are safe and silent. Anything that rewrites data Belay does not own is opt-in.

Rule of thumb: Belay encrypts its own copies, holds secrets back from what it pushes, and only offers to clean up anything else.

## Overview

| Part | What it protects | Default | Can it block? |
|---|---|---|---|
| Encryption at rest | `.belay/objects/` | On, silent | No |
| Secret detector | (library used by the parts below) | n/a | n/a |
| Projection hold-back | `belay project` commits and pushes | On | No. The commit still happens, the file is left out |
| Agent log scrub | Agent session logs | `notice` (report only) | No |

## 1. Encryption at rest

Every object Belay saves is encrypted, not only the ones the detector flags. Detector misses therefore do not matter for the store.

### Keys

- One 32-byte random master key per user, shared by every Belay project on the machine.
- Two subkeys are derived with HKDF-SHA256: `enc` (encryption) and `id` (content ids). The master key is never used directly.
- Key storage, in order:
  1. OS keychain. macOS Keychain through the `security` CLI, Linux Secret Service through `secret-tool`. Service `belay`, account `store-key`. No cgo.
  2. Fallback key file at `<user config dir>/belay/store.key`, mode `0600`, parent `0700`. Used silently when no keychain is available (headless Linux, containers, CI).
- `BELAY_KEY_FILE` overrides the location (tests and advanced setups).
- The key is created on first use. No prompt. Creation runs under a lock next to the key file and never replaces a key that appeared meanwhile, so concurrent first uses all end with one key. When the keychain cannot be asked (an error, not "not found"), no key is created; the store is unavailable for that call, hooks still queue the event, and the daemon waits.

### Commands

- `belay key status`: where the key lives, its key id, store format and migration progress.
- `belay key export [--out FILE]`: writes the key (base64) for backup. Prints a plain warning that anyone with the key and a copy of the store can read the history.
- `belay key import FILE`: installs a key, for example on a new machine with a copied store. Refuses to replace an existing different key unless `--force`.

**Losing the key loses the history.** There is no recovery. `belay init` and the README say this in one sentence, and `belay key export` is the backup path.

### Object format

```
magic   4 bytes  "BLY" 0x01
flags   1 byte   bit 0 = gzip-compressed plaintext
nonce  24 bytes
ciphertext       XChaCha20-Poly1305(enc, nonce, gzip(plaintext), aad = content id)
```

- Compress, then encrypt. Random 24-byte nonces (XChaCha20-Poly1305 from `golang.org/x/crypto`).
- The content id is the AEAD associated data, so a blob cannot be swapped under another id.
- Reads detect the magic. Objects without it are legacy (plain or gzip, sha256 id) and are still readable, so migration can run in the background without breaking restore.

### Content addressing

A plain SHA-256 of content lets anyone holding the index confirm a guessed secret. Content ids become keyed:

```
id = hex(HMAC-SHA256(id-subkey, plaintext))
```

- Same shape as before (64 lowercase hex characters), so index columns, API fields (`?hash=`), and JSON output keep their format. Only the values change.
- Dedupe still works: same content, same key, same id.
- Every place that compares a fresh hash of file content against a stored id must go through the store's hasher (`Store.ContentID`), never `sha256` directly. `schema.ContentHashForBytes` stays for non-store uses only.
- A store manifest (`.belay/store.json`) records `format` (1 = legacy, 2 = encrypted), and a key id (`HMAC(id-subkey, "belay-key-id")`, first 16 hex). Opening a format-2 store with the wrong key fails with a clear message instead of reporting corruption.

### Opt-out

`[store] encryption = false` in `.belay/config.toml` keeps writing legacy objects in a store that has never encrypted. Default is on. Once a store is format 2 it keeps writing encrypted objects whenever its key is loaded; turning the setting off does not decrypt existing objects or start writing plaintext.

### Migration

Existing stores are migrated in place, in the background, resumably. This is the highest-risk part of Guard.

1. Runs inside the daemon (the single writer) or under the store lock when run by hand (`belay key migrate`). Never two at once.
2. For each legacy object: read it, verify its sha256, compute the new id, write the encrypted object, read it back, decrypt, and compare bytes with the original. Record `(old, new)` in a migration table in the index database.
3. Objects that fail verification (already corrupt) are recorded and skipped, never deleted.
4. When every object is copied and verified, one transaction rewrites every column that holds a content id (events `content_hash` and `previous_hash`, and any other table that stores ids) through the mapping. The append-only event log segments also carry content ids and `belay rebuild` regenerates the index from them, so segments are rewritten through the mapping too: write a new segment beside the old one, verify it parses to the same events with mapped ids, then rename over the old one. A rebuild after migration must point at encrypted objects only.
5. Only after that transaction commits are legacy blobs deleted, and only those whose new copy verified.
6. The mapping table is dropped at the end. It holds plaintext sha256 values, so keeping it would defeat keyed ids. Old sha256 ids stop resolving after migration completes.
7. Writes during migration use new ids immediately. Rows not in the mapping keep their ids while their object exists.
8. Ids in the index or the segments whose object no longer exists (history that retention already collected) are replaced by a tombstone, an HMAC of the old id under a key derived from the master key. Equal ids stay equal, the SHA-256 is not revealed, and reading a tombstone reports a missing object like any collected one.
9. Every encrypted copy is verified again right before the remap and again in cleanup, and re-copied from its legacy object when it does not verify. If a copy still cannot be verified, cleanup keeps the legacy object and the mapping, the migration stays unfinished, and garbage collection keeps every mapped legacy object until it finishes.
10. Pinned legacy objects (for example scrub originals saved before encryption was on) are kept.
11. At start the daemon checks object headers; if legacy objects appear in a finished store (an older copy synced over it), the migration runs again. Corrupt and pinned objects kept on purpose do not count.
12. Every step is idempotent. A crash at any point resumes from the table. Progress shows in `belay key status` and `belay status`.

**Invariant: no plaintext blob is deleted until its encrypted copy has been written, read back, decrypted, and compared byte-for-byte.**

### What it protects against, honestly

Protects: backups, cloud sync, copied disks, stolen disks, other tools that scan plain text on disk (search indexers, secret scanners, AI agents grepping a home directory).

Does not protect: malware or anyone running as the same user, who can read the keychain or key file. The local API serves decrypted content to the local user, same as before.

## 2. Secret detector

Go package `internal/secrets`. Pure function, no I/O, no network, no model.

```go
type Finding struct {
    Rule  string
    Line  int
    Start int
    End   int
}
func Scan(data []byte) []Finding
```

Rules:

- **Known token formats**: Anthropic (`sk-ant-`), OpenAI (`sk-proj-`, `sk-`), GitHub (`ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`, `github_pat_`), AWS access key ids (`AKIA`, `ASIA`) and secret keys next to them, Stripe (`sk_live_`, `rk_live_`), Slack (`xox[abprs]-`), Google API keys (`AIza`), private key PEM headers, JWTs, and similar.
- **Key/value assignments**: names like `password`, `passwd`, `secret`, `token`, `api_key`, `apikey`, `auth`, `credential`, `private_key` with a literal value in env, shell, YAML, TOML, JSON, or code syntax. The value must be long enough and not a placeholder (`your-api-key`, `xxxx`, `changeme`, `${VAR}`, `os.getenv(...)`, `process.env.X`, `<token>`, empty, type annotations).
- **Entropy**: high-entropy strings only when next to a credential keyword. Entropy alone is too noisy (git SHAs, lockfile hashes, UUIDs, base64 assets).
- **Credentials in prose**: phrases like "the password for x@y.com is ...", "app password: ...", "token: ...". Google app passwords are four groups of four lowercase letters (separated by spaces, or written without them). They count only with a credential word or an email address nearby, and not when all four groups are common English words.

A local model can plug in later behind the same interface. Not required.

### Test corpus

`internal/secrets/testdata/` holds positive and negative cases, each tagged with the expected rule. It must include the prose app-password case. Negatives cover lockfiles, git SHAs, UUIDs, base64 images, minified JS, type annotations like `password: string`, placeholders, env var references, and ordinary English that happens to contain four 4-letter words. The test asserts zero false positives on the negative set.

Fixtures that look like real tokens are assembled at test time from parts, so the public repo never contains a string that trips hosting-provider secret scanning.

## 3. Projection hold-back

`belay project` keeps making its commit. Before writing the tree, it scans the net content of each changed file.

- A file whose net content has a finding is left out of that commit. A modified file stays at its previously projected version; a new file is not added; a deletion is still projected.
- One line on stderr: `belay: held back 2 files with likely credentials: config/prod.yml, notes.md (allow with .belayallow)`. The JSON result gets a `held_back` list with path and rule, never the secret.
- Applies to every projection path: session projection, working-tree reconcile, and the daemon projector queue.
- When the secret is removed, the next projection includes the file again, because the scan runs on net content each time.

Overrides:

- `.belayallow` at the project root, gitignore syntax for paths, plus `rule:<name>` lines to switch off a rule for the project.
- `belay project --allow-secrets` for one run.
- `[projection] hold_back_secrets = false` turns the feature off.

History never stops flowing. A detector error is logged and the file is projected as before, so a bug in Guard cannot stall projection.

## 4. Agent log scrub

Agent logs belong to the agent, not to Belay, so this is opt-in.

Setting `[scrub] mode` in `.belay/config.toml`:

- `off`: nothing.
- `notice` (default): `belay status` shows one line when logs for this project contain findings, for example `3 credentials in plain text in agent logs. Run belay scrub to redact.` Nothing else. No popups, no repeats elsewhere.
- `auto`: the daemon redacts idle logs in the background.

`belay init` asks one question, default No: `Automatically redact credentials from AI session logs? [y/N]`. Yes sets `auto`, No sets `notice`.

`belay scrub`:

- Covers Claude Code transcripts for the project first. Other agents plug in through the same session adapters Belay already has.
- Only touches closed or idle logs (no write in the last 30 minutes and not an active Belay session), because agents read their own logs to resume. `--dry-run` lists what it would change.
- Redacts inside decoded JSON string values and re-encodes the line, so JSONL stays valid. Replacement text: `[redacted:<rule>]`.
- Before rewriting a log, the original is saved into Belay's encrypted store. `belay scrub --undo <log>` puts it back byte-exact.
- Writes through a temp file and rename, preserving the file mode.
- The status count comes from a cache keyed by log path, size, and mtime, so `belay status` stays fast.

## Build plan

| Wave | Task | Depends on | Risk |
|---|---|---|---|
| 1 | A. Encryption at rest, keychain, keyed ids, migration, `belay key` | none | High: migration and content addressing |
| 1 | B. Detector and test corpus | none | Medium: false positives |
| 2 | C. Projection hold-back | B | Medium |
| 2 | D. Scrub command, setting, init question, status line | B (and A for `--undo`) | Medium |
