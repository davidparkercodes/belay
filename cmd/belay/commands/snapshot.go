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

func newSnapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Reconstruct project state at a point in time",
		Long: `Export the entire project state as it existed at any point in time.
The ultimate time-travel feature.`,
		RunE: runSnapshot,
	}

	cmd.Flags().String("roughly-around", "", "Timestamp to snapshot (human convenience, accepts relative times like '1h ago')")
	cmd.Flags().String("output", "", "Directory to export files to")
	cmd.Flags().Bool("execute", false, "Actually write files when using --output (requires safety.allow_writes)")
	cmd.Flags().String("file", "", "Show a single file at the given time")
	cmd.Flags().Bool("ls", false, "Just list files that existed at that time")
	cmd.Flags().Bool("json", false, "Output as JSON")
	_ = cmd.MarkFlagRequired("roughly-around")

	return cmd
}

func runSnapshot(cmd *cobra.Command, args []string) error {
	atStr, _ := cmd.Flags().GetString("roughly-around")
	outputDir, _ := cmd.Flags().GetString("output")
	execute, _ := cmd.Flags().GetBool("execute")
	singleFile, _ := cmd.Flags().GetString("file")
	listOnly, _ := cmd.Flags().GetBool("ls")
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

	t, err := parseRelativeTime(atStr)
	if err != nil {
		return fmt.Errorf("invalid --roughly-around: %w", err)
	}
	targetNano := t.UnixNano()

	if singleFile != "" {
		events, err := idx.QueryEvents(&index.Query{
			FilePaths: []string{singleFile},
			Until:     targetNano,
			OrderDesc: true,
			Limit:     1,
		})
		if err != nil || len(events) == 0 {
			return fmt.Errorf("no history for %s at that time", singleFile)
		}
		if events[0].Op.String() == "DELETE" {
			return fmt.Errorf("file was deleted at that point in time")
		}
		data, err := objStore.Get(events[0].ContentHash)
		if err != nil {
			return fmt.Errorf("retrieve content: %w", err)
		}
		os.Stdout.Write(data)
		return nil
	}

	events, err := idx.QueryEvents(&index.Query{
		Until:     targetNano,
		OrderDesc: false,
	})
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}

	type fileState struct {
		Path        string `json:"path"`
		ContentHash string `json:"content_hash"`
		Size        int64  `json:"size"`
		Deleted     bool   `json:"-"`
	}

	files := make(map[string]*fileState)
	for _, e := range events {
		fs, exists := files[e.FilePath]
		if !exists {
			fs = &fileState{Path: e.FilePath}
			files[e.FilePath] = fs
		}
		if e.Op.String() == "DELETE" {
			fs.Deleted = true
		} else {
			fs.Deleted = false
			fs.ContentHash = e.ContentHash
			fs.Size = e.ContentSize
		}
	}

	for path, fs := range files {
		if fs.Deleted {
			delete(files, path)
		}
	}

	fmt.Printf("Project state at %s: %d files\n\n", t.Format("2006-01-02 15:04:05"), len(files))

	if jsonOutput {
		var result []interface{}
		for _, fs := range files {
			result = append(result, fs)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	if listOnly {
		for _, fs := range files {
			fmt.Printf("  %s (%s)\n", fs.Path, humanBytes(fs.Size))
		}
		return nil
	}

	if outputDir != "" {
		if err := checkSafetyGate(cfg, execute, "snapshot", "--roughly-around <time> --output <dir>"); err != nil {
			fmt.Printf("\n%s\n", err)
			return nil
		}

		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}

		written := 0
		for _, fs := range files {
			if fs.ContentHash == "" {
				continue
			}
			data, err := objStore.Get(fs.ContentHash)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: cannot retrieve %s: %v\n", fs.Path, err)
				continue
			}

			outPath := filepath.Join(outputDir, filepath.Clean(fs.Path))
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
				fmt.Fprintf(os.Stderr, "warning: skipping %s: path escapes output directory\n", fs.Path)
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
			written++
		}
		fmt.Printf("Exported %d files to %s\n", written, outputDir)
		return nil
	}

	for _, fs := range files {
		fmt.Printf("  %s (%s)\n", fs.Path, humanBytes(fs.Size))
	}

	return nil
}
