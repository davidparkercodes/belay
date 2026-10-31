package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/daemon"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/store"
	"github.com/davidparkercodes/belay/internal/storekey"

	"github.com/spf13/cobra"
)

var keyProvider = storekey.Default

const keyLossWarning = "Losing the key loses the history: back it up with 'belay key export'."

func newKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Manage the key that encrypts the Belay content store",
		Long: `Belay encrypts every stored file version with one per-user key, kept in the
OS keychain when available and otherwise in a 0600 key file. BELAY_KEY_FILE
overrides the key file location and skips the keychain.

` + keyLossWarning,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newKeyStatusCmd(), newKeyExportCmd(), newKeyImportCmd(), newKeyMigrateCmd())
	return cmd
}

type keyStatusOutput struct {
	KeyFound  bool                   `json:"key_found"`
	KeySource string                 `json:"key_source,omitempty"`
	KeyID     string                 `json:"key_id,omitempty"`
	Lookup    string                 `json:"lookup"`
	Project   string                 `json:"project,omitempty"`
	Store     *store.MigrationStatus `json:"store,omitempty"`
	KeyMatch  *bool                  `json:"key_matches_store,omitempty"`
	Encrypt   *bool                  `json:"encryption_enabled,omitempty"`
}

func newKeyStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show where the key lives, its id, and the store's format and migration progress",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonOut, _ := cmd.Flags().GetBool("json")
			p := keyProvider()
			out := keyStatusOutput{Lookup: p.Describe()}
			keys, err := p.Keys()
			if err != nil {
				return err
			}

			var cfg *config.Config
			if root, rerr := config.FindProjectRoot(); rerr == nil {
				if c, lerr := config.Load(root); lerr == nil {
					cfg = c
					out.Project = root
				}
			}

			var manifestKeyID string
			if cfg != nil {
				var idx *index.Index
				if _, serr := os.Stat(cfg.IndexPath()); serr == nil {
					if opened, oerr := index.Open(cfg.IndexPath()); oerr == nil {
						idx = opened
						defer idx.Close()
					}
				}
				st := store.ReadMigrationStatus(idx, cfg.StoreManifestPath())
				out.Store = &st
				manifestKeyID = st.KeyID
				enc := cfg.Store.Encryption
				out.Encrypt = &enc
			}

			chosen := pickKey(keys, manifestKeyID)
			if chosen != nil {
				out.KeyFound = true
				out.KeySource = chosen.Source
				out.KeyID = store.KeyID(chosen.Master)
			}
			if manifestKeyID != "" {
				match := out.KeyID == manifestKeyID
				out.KeyMatch = &match
			}

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
			printKeyStatus(out)
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "Output as JSON")
	return cmd
}

func pickKey(keys []storekey.Key, wantID string) *storekey.Key {
	if len(keys) == 0 {
		return nil
	}
	if wantID != "" {
		for i := range keys {
			if store.KeyID(keys[i].Master) == wantID {
				return &keys[i]
			}
		}
	}
	return &keys[0]
}

func printKeyStatus(out keyStatusOutput) {
	fmt.Println("Belay store key")
	fmt.Println("===============")
	if out.KeyFound {
		fmt.Printf("  Key:       %s\n", out.KeySource)
		fmt.Printf("  Key id:    %s\n", out.KeyID)
	} else {
		fmt.Println("  Key:       none yet (created on first use)")
	}
	fmt.Printf("  Lookup:    %s\n", out.Lookup)
	if out.Store == nil {
		fmt.Println()
		fmt.Println("  " + keyLossWarning)
		return
	}
	fmt.Println()
	fmt.Printf("  Project:   %s\n", out.Project)
	fmt.Printf("  Store:     %s\n", storeFormatLine(*out.Store))
	if out.KeyMatch != nil && !*out.KeyMatch {
		fmt.Printf("  WARNING:   the store needs key %s, which was not found. Install it with 'belay key import FILE'.\n", out.Store.KeyID)
	}
	if out.Encrypt != nil && !*out.Encrypt {
		fmt.Println("  Writes:    unencrypted ([store] encryption = false)")
	}
	fmt.Printf("  Migration: %s\n", migrationLine(*out.Store))
	fmt.Println()
	fmt.Println("  " + keyLossWarning)
}

func storeFormatLine(st store.MigrationStatus) string {
	if st.Format == store.FormatEncrypted {
		return fmt.Sprintf("encrypted (format %d, key id %s)", st.Format, st.KeyID)
	}
	return fmt.Sprintf("unencrypted (format %d)", st.Format)
}

func migrationLine(st store.MigrationStatus) string {
	if st.Format != store.FormatEncrypted {
		return "not started"
	}
	if st.Migration != store.MigrationPending {
		if len(st.CorruptIDs) > 0 {
			return fmt.Sprintf("complete (%d corrupt legacy objects kept as they were)", len(st.CorruptIDs))
		}
		return "complete"
	}
	phase := st.Phase
	if phase == "" || phase == store.PhaseDone {
		phase = "waiting"
	}
	line := fmt.Sprintf("in progress (%s), %d of %d legacy objects encrypted", phase, st.Copied, st.Total)
	if st.Corrupt > 0 {
		line += fmt.Sprintf(", %d corrupt kept", st.Corrupt)
	}
	return line
}

func newKeyExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write the store key (base64) for backup",
		RunE: func(cmd *cobra.Command, args []string) error {
			outPath, _ := cmd.Flags().GetString("out")
			keys, err := keyProvider().Keys()
			if err != nil {
				return err
			}
			var wantID string
			if root, rerr := config.FindProjectRoot(); rerr == nil {
				if cfg, lerr := config.Load(root); lerr == nil {
					if m, ok, _ := store.ReadManifest(cfg.StoreManifestPath()); ok {
						wantID = m.KeyID
					}
				}
			}
			k := pickKey(keys, wantID)
			if k == nil {
				return errors.New("no store key exists yet; one is created the first time Belay stores a file")
			}
			fmt.Fprintln(os.Stderr, "Warning: anyone with this key and a copy of a Belay store can read its history. Keep the export somewhere safe.")
			line := storekey.Encode(k.Master) + "\n"
			if outPath == "" || outPath == "-" {
				_, err := io.WriteString(os.Stdout, line)
				return err
			}
			f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
			if err != nil {
				return fmt.Errorf("create %s: %w", outPath, err)
			}
			if _, err := f.WriteString(line); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Key %s written to %s\n", store.KeyID(k.Master), outPath)
			return nil
		},
	}
	cmd.Flags().String("out", "", "Write the key to this file (mode 0600) instead of stdout")
	return cmd
}

func newKeyImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import FILE",
		Short: "Install a store key, for example on a new machine with a copied store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			var data []byte
			var err error
			if args[0] == "-" {
				data, err = io.ReadAll(os.Stdin)
			} else {
				data, err = os.ReadFile(args[0])
			}
			if err != nil {
				return fmt.Errorf("read key: %w", err)
			}
			master, err := storekey.Decode(strings.TrimSpace(string(data)))
			if err != nil {
				return err
			}
			k, err := keyProvider().Install(master, force)
			if err != nil {
				return err
			}
			fmt.Printf("Installed key %s in %s\n", store.KeyID(k.Master), k.Source)
			return nil
		},
	}
	cmd.Flags().Bool("force", false, "Replace an existing, different key")
	return cmd
}

func newKeyMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Encrypt legacy objects in this project's store now",
		Long: `Encrypts every legacy (unencrypted) object in place. The daemon does this in
the background on its own; run this only when the daemon is stopped. It is safe
to interrupt and run again: progress resumes where it stopped.`,
		RunE: runKeyMigrate,
	}
	return cmd
}

func runKeyMigrate(cmd *cobra.Command, args []string) error {
	root, err := config.FindProjectRoot()
	if err != nil {
		return fmt.Errorf("not a belay project: %w", err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if !cfg.Store.Encryption {
		return errors.New("store encryption is off ([store] encryption = false in .belay/config.toml)")
	}
	if running, pid := daemon.IsRunning(cfg); running {
		fmt.Printf("The daemon (PID %d) migrates this store in the background. Check progress with 'belay key status'.\n", pid)
		return nil
	}
	release, err := daemon.HoldDaemonLock(cfg.PIDPath())
	if err != nil {
		return fmt.Errorf("cannot lock the store: %w", err)
	}
	defer release()

	objStore, err := store.OpenConfigWithKeys(cfg, keyProvider())
	if err != nil {
		return err
	}
	idx, err := index.Open(cfg.IndexPath())
	if err != nil {
		return fmt.Errorf("open index: %w", err)
	}
	defer idx.Close()

	m := &store.Migrator{
		Store:     objStore,
		Index:     idx,
		EventsDir: cfg.EventsDir(),
		LockPath:  store.MigrationLockPath(cfg.BelayPath),
		Logf: func(format string, a ...interface{}) {
			fmt.Printf("  "+format+"\n", a...)
		},
	}
	res, err := m.Run()
	if err != nil {
		return fmt.Errorf("migration stopped (run again to resume): %w", err)
	}
	st := store.ReadMigrationStatus(idx, cfg.StoreManifestPath())
	fmt.Printf("Store migration: %s\n", migrationLine(st))
	if res.Copied+res.Corrupt+res.LegacyDeleted > 0 {
		fmt.Printf("  This run: %d encrypted, %d corrupt kept, %d legacy objects removed\n", res.Copied, res.Corrupt, res.LegacyDeleted)
	}
	return nil
}

func openStore(cfg *config.Config) (*store.Store, error) {
	return store.OpenConfigWithKeys(cfg, keyProvider())
}
