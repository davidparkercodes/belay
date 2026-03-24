package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/store"

	"github.com/spf13/cobra"
)

func newReplayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "replay [session-id]",
		Short: "Replay a session's file changes",
		Long: `Reconstruct all file changes from a specific AI session.

Produces a summary of what the session did, and can output as a
patch file or apply changes to a directory.`,
		Args: cobra.ExactArgs(1),
		RunE: runReplay,
	}

	cmd.Flags().Bool("patch", false, "Output as unified patch")
	cmd.Flags().String("output", "", "Write session's file states to directory")
	cmd.Flags().Bool("execute", false, "Actually write files when using --output (requires safety.allow_writes)")
	cmd.Flags().Bool("stat", false, "Show summary statistics only")
	cmd.Flags().Bool("json", false, "Output as JSON")

	return cmd
}

func runReplay(cmd *cobra.Command, args []string) error {
	sessionID := args[0]
	showPatch, _ := cmd.Flags().GetBool("patch")
	outputDir, _ := cmd.Flags().GetString("output")
	execute, _ := cmd.Flags().GetBool("execute")
	statOnly, _ := cmd.Flags().GetBool("stat")
	jsonOutput, _ := cmd.Flags().GetBool("json")

	projectRoot, err := config.FindProjectRoot()
	if err != nil {
		return fmt.Errorf("not a belay project: %w", err)
	}

	cfg, err := config.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	idx, err := index.Open(cfg.IndexPath())
	if err != nil {
		return fmt.Errorf("open index: %w", err)
	}
	defer idx.Close()

	objStore, err := store.NewStore(cfg.ObjectsDir(), cfg.Storage.CompressionEnabled)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer objStore.Close()

	events, err := idx.QueryEvents(&index.Query{
		Sessions:  []string{sessionID},
		OrderDesc: false,
	})
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}

	if len(events) == 0 {
		return fmt.Errorf("no events found for session %s", sessionID)
	}

	type fileResult struct {
		Path      string `json:"path"`
		NetOp     string `json:"operation"`
		OldHash   string `json:"old_hash,omitempty"`
		NewHash   string `json:"new_hash,omitempty"`
		Events    int    `json:"events"`
		SizeDelta int64  `json:"size_delta"`
	}

	files := make(map[string]*fileResult)
	fileOrder := []string{}

	for _, e := range events {
		fr, exists := files[e.FilePath]
		if !exists {
			fr = &fileResult{
				Path:    e.FilePath,
				OldHash: e.PreviousHash,
			}
			files[e.FilePath] = fr
			fileOrder = append(fileOrder, e.FilePath)
		}
		fr.NewHash = e.ContentHash
		fr.Events++
		fr.SizeDelta += e.ContentSize

		switch {
		case e.Op.String() == "DELETE":
			fr.NetOp = "delete"
		case fr.OldHash == "" && e.Op.String() == "CREATE":
			fr.NetOp = "create"
		default:
			if fr.NetOp != "create" {
				fr.NetOp = "modify"
			}
		}
	}

	for path, fr := range files {
		if fr.NetOp == "delete" && fr.OldHash == "" {
			delete(files, path)
		}
	}

	if jsonOutput {
		var results []*fileResult
		for _, path := range fileOrder {
			if fr, ok := files[path]; ok {
				results = append(results, fr)
			}
		}
		result := map[string]interface{}{
			"session_id": sessionID,
			"events":     len(events),
			"files":      results,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	var created, modified, deleted int
	for _, fr := range files {
		switch fr.NetOp {
		case "create":
			created++
		case "modify":
			modified++
		case "delete":
			deleted++
		}
	}

	fmt.Printf("Session: %s\n", sessionID)
	fmt.Printf("Events:  %d\n", len(events))
	fmt.Printf("Files:   %d (%d created, %d modified, %d deleted)\n\n",
		len(files), created, modified, deleted)

	if statOnly {
		for _, path := range fileOrder {
			fr, ok := files[path]
			if !ok {
				continue
			}
			switch fr.NetOp {
			case "create":
				fmt.Printf("  \033[32m+ %s\033[0m (%d events)\n", path, fr.Events)
			case "modify":
				fmt.Printf("  \033[33m~ %s\033[0m (%d events)\n", path, fr.Events)
			case "delete":
				fmt.Printf("  \033[31m- %s\033[0m (%d events)\n", path, fr.Events)
			}
		}
		return nil
	}

	if showPatch {
		for _, path := range fileOrder {
			fr, ok := files[path]
			if !ok {
				continue
			}

			var oldContent, newContent string
			if fr.OldHash != "" {
				data, _ := objStore.Get(fr.OldHash)
				oldContent = string(data)
			}
			if fr.NewHash != "" && fr.NetOp != "delete" {
				data, _ := objStore.Get(fr.NewHash)
				newContent = string(data)
			}

			if fr.NetOp == "delete" {
				fmt.Printf("--- a/%s\n", path)
				fmt.Printf("+++ /dev/null\n")
			} else if fr.NetOp == "create" {
				fmt.Printf("--- /dev/null\n")
				fmt.Printf("+++ b/%s\n", path)
			} else {
				fmt.Printf("--- a/%s\n", path)
				fmt.Printf("+++ b/%s\n", path)
			}
			printDiffLines(oldContent, newContent, path)
			fmt.Println()
		}
		return nil
	}

	if outputDir != "" {
		if err := checkSafetyGate(cfg, execute, "replay", "--output <dir> <session-id>"); err != nil {
			fmt.Printf("\n%s\n", err)
			return nil
		}

		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}

		for _, path := range fileOrder {
			fr, ok := files[path]
			if !ok || fr.NetOp == "delete" {
				continue
			}
			if fr.NewHash == "" {
				continue
			}

			data, err := objStore.Get(fr.NewHash)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: cannot retrieve %s: %v\n", path, err)
				continue
			}

			outPath := filepath.Join(outputDir, filepath.Clean(path))
			// Resolve symlinks before checking prefix to prevent symlink escapes
			realOutPath, evalErr := filepath.EvalSymlinks(outPath)
			if evalErr != nil {
				// file may not exist yet, fall back to cleaned path
				realOutPath = outPath
			}
			realOutDir, evalErr := filepath.EvalSymlinks(outputDir)
			if evalErr != nil {
				realOutDir = outputDir
			}
			if !strings.HasPrefix(realOutPath, realOutDir+string(filepath.Separator)) && realOutPath != realOutDir {
				fmt.Fprintf(os.Stderr, "warning: skipping %s: path escapes output directory\n", path)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
				fmt.Fprintf(os.Stderr, "warning: cannot create dir for %s: %v\n", outPath, err)
				continue
			}
			if err := os.WriteFile(outPath, data, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "warning: cannot write %s: %v\n", outPath, err)
				continue
			}
			fmt.Printf("  wrote %s (%s)\n", path, humanBytes(int64(len(data))))
		}
		fmt.Printf("\nSession files written to %s\n", outputDir)
		return nil
	}

	for _, path := range fileOrder {
		fr, ok := files[path]
		if !ok {
			continue
		}
		switch fr.NetOp {
		case "create":
			fmt.Printf("  \033[32m+ %s\033[0m (%d events)\n", path, fr.Events)
		case "modify":
			fmt.Printf("  \033[33m~ %s\033[0m (%d events)\n", path, fr.Events)
		case "delete":
			fmt.Printf("  \033[31m- %s\033[0m (%d events)\n", path, fr.Events)
		}
	}

	return nil
}
