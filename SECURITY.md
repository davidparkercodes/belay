# Security Policy

## Threat Model

Belay is a **local development tool** that watches filesystem changes and stores file content history. It is designed to run on your local machine, bound to `127.0.0.1` by default.

**Important:** Belay has no authentication mechanism. The API server relies on network-level access control (localhost binding) for security. If you configure `host = "0.0.0.0"` in `.belay/config.toml`, the entire file change history API becomes accessible to anyone on your network. Only bind to non-localhost addresses on trusted networks.

## Supported Versions

Security updates are applied to the latest release only.

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly:

1. **Do NOT open a public GitHub issue.**
2. Email **david@davidparker.codes** with:
   - Description of the vulnerability
   - Steps to reproduce
   - Potential impact
3. You will receive an acknowledgment within 72 hours.
4. A fix will be developed privately and released as a patch.

Alternatively, use [GitHub Security Advisories](https://github.com/davidparkercodes/belay/security/advisories/new) to report privately through GitHub's built-in mechanism.

## Security Considerations

- **No authentication:** The API has no auth tokens or API keys. Security relies on binding to `127.0.0.1` (the default). Do not expose Belay's API port to untrusted networks.
- **File content storage:** Belay stores full file contents in a content-addressable store. Sensitive files (secrets, credentials) should be excluded via `.belayignore`.
- **CORS policy:** The API restricts cross-origin requests to `localhost` and `127.0.0.1` origins only.
