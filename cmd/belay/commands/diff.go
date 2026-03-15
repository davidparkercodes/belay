package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/store"

	"github.com/spf13/cobra"
)

func newDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff [file]",
		Short: "View file changes between time points or sessions",
		Long: `Show the content differences for files at different points in time
or between different AI sessions.`,
		RunE: runDiff,
	}

	cmd.Flags().String("session", "", "Show all changes from a session")
	cmd.Flags().String("at", "", "Compare file at this time vs current")
	cmd.Flags().String("from", "", "Start time for range comparison")
	cmd.Flags().String("to", "", "End time for range comparison")
	cmd.Flags().Bool("stat", false, "Show diffstat summary only")

	return cmd
}

func runDiff(cmd *cobra.Command, args []string) error {
	sessionFilter, _ := cmd.Flags().GetString("session")
	atTime, _ := cmd.Flags().GetString("at")
	statOnly, _ := cmd.Flags().GetBool("stat")

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

	if sessionFilter != "" {
		return diffSession(idx, objStore, sessionFilter, statOnly)
	}

	if len(args) > 0 && atTime != "" {
		t, err := parseRelativeTime(atTime)
		if err != nil {
			return fmt.Errorf("invalid --at: %w", err)
		}
		return diffFileAtTime(idx, objStore, projectRoot, args[0], t.UnixNano(), statOnly)
	}

	if len(args) > 0 {
		return diffFileVsLatest(idx, objStore, projectRoot, args[0], statOnly)
	}

	return fmt.Errorf("usage: belay diff <file> or belay diff --session <id>")
}

func diffSession(idx *index.Index, objStore *store.Store, sessionID string, statOnly bool) error {
	events, err := idx.QueryEvents(&index.Query{
		Sessions:  []string{sessionID},
		OrderDesc: false,
	})
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}

	if len(events) == 0 {
		fmt.Println("No events found for this session.")
		return nil
	}

	type fileChange struct {
		firstHash string
		lastHash  string
		lastOp    string
		count     int
	}
	changes := make(map[string]*fileChange)

	for _, e := range events {
		fc, exists := changes[e.FilePath]
		if !exists {
			fc = &fileChange{firstHash: e.PreviousHash}
			changes[e.FilePath] = fc
		}
		fc.lastHash = e.ContentHash
		fc.lastOp = e.Op.String()
		fc.count++
	}

	if statOnly {
		var added, modified, deleted int
		for path, fc := range changes {
			switch {
			case fc.firstHash == "" && fc.lastHash != "":
				added++
				fmt.Printf(" \033[32m+++ %s\033[0m (new, %d events)\n", path, fc.count)
			case fc.lastOp == "DELETE":
				deleted++
				fmt.Printf(" \033[31m--- %s\033[0m (deleted, %d events)\n", path, fc.count)
			default:
				modified++
				fmt.Printf(" \033[33m~~~ %s\033[0m (%d events)\n", path, fc.count)
			}
		}
		fmt.Printf("\n%d files changed: %d added, %d modified, %d deleted\n",
			len(changes), added, modified, deleted)
		return nil
	}

	for path, fc := range changes {
		if fc.lastOp == "DELETE" {
			fmt.Printf("\033[31m--- a/%s\033[0m\n", path)
			fmt.Printf("\033[31m+++ /dev/null\033[0m\n")
			if fc.firstHash != "" {
				oldContent, err := objStore.Get(fc.firstHash)
				if err == nil {
					printDiffLines(string(oldContent), "", path)
				}
			}
			continue
		}

		var oldContent, newContent string
		if fc.firstHash != "" {
			data, err := objStore.Get(fc.firstHash)
			if err == nil {
				oldContent = string(data)
			}
		}
		if fc.lastHash != "" {
			data, err := objStore.Get(fc.lastHash)
			if err == nil {
				newContent = string(data)
			}
		}

		if oldContent == newContent {
			continue
		}

		if fc.firstHash == "" {
			fmt.Printf("\033[32m--- /dev/null\033[0m\n")
			fmt.Printf("\033[32m+++ b/%s\033[0m\n", path)
		} else {
			fmt.Printf("--- a/%s\n", path)
			fmt.Printf("+++ b/%s\n", path)
		}
		printDiffLines(oldContent, newContent, path)
		fmt.Println()
	}

	return nil
}

func diffFileAtTime(idx *index.Index, objStore *store.Store, projectRoot, filePath string, timestampNano int64, statOnly bool) error {
	events, err := idx.QueryEvents(&index.Query{
		FilePaths: []string{filePath},
		Until:     timestampNano,
		OrderDesc: true,
		Limit:     1,
	})
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}

	if len(events) == 0 {
		return fmt.Errorf("no events found for %s before the specified time", filePath)
	}

	oldHash := events[0].ContentHash
	var oldContent string
	if oldHash != "" {
		data, err := objStore.Get(oldHash)
		if err == nil {
			oldContent = string(data)
		}
	}

	absPath := filepath.Join(projectRoot, filepath.Clean(filePath))
	// Resolve symlinks before checking prefix to prevent symlink escapes
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// file may not exist yet, fall back to cleaned path
		realPath = absPath
	}
	realRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		realRoot = projectRoot
	}
	if !strings.HasPrefix(realPath, realRoot+string(filepath.Separator)) && realPath != realRoot {
		return fmt.Errorf("file path escapes project root")
	}
	currentData, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read current file: %w", err)
	}
	currentContent := string(currentData)

	if statOnly {
		oldLines := len(strings.Split(oldContent, "\n"))
		newLines := len(strings.Split(currentContent, "\n"))
		fmt.Printf("%s: %d -> %d lines\n", filePath, oldLines, newLines)
		return nil
	}

	fmt.Printf("--- a/%s (at %s)\n", filePath, events[0].Timestamp().Format("2006-01-02 15:04:05"))
	fmt.Printf("+++ b/%s (current)\n", filePath)
	printDiffLines(oldContent, currentContent, filePath)
	return nil
}

func diffFileVsLatest(idx *index.Index, objStore *store.Store, projectRoot, filePath string, statOnly bool) error {
	event, err := idx.LatestEvent(filePath)
	if err != nil {
		return fmt.Errorf("no Belay history for %s", filePath)
	}

	var snapshotContent string
	if event.ContentHash != "" {
		data, err := objStore.Get(event.ContentHash)
		if err == nil {
			snapshotContent = string(data)
		}
	}

	absPath := filepath.Join(projectRoot, filepath.Clean(filePath))
	// Resolve symlinks before checking prefix to prevent symlink escapes
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// file may not exist yet, fall back to cleaned path
		realPath = absPath
	}
	realRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		realRoot = projectRoot
	}
	if !strings.HasPrefix(realPath, realRoot+string(filepath.Separator)) && realPath != realRoot {
		return fmt.Errorf("file path escapes project root")
	}
	currentData, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read current file: %w", err)
	}
	currentContent := string(currentData)

	if snapshotContent == currentContent {
		fmt.Println("No changes since last Belay snapshot.")
		return nil
	}

	if statOnly {
		oldLines := len(strings.Split(snapshotContent, "\n"))
		newLines := len(strings.Split(currentContent, "\n"))
		fmt.Printf("%s: %d -> %d lines\n", filePath, oldLines, newLines)
		return nil
	}

	fmt.Printf("--- a/%s (belay snapshot at %s)\n", filePath, event.Timestamp().Format("2006-01-02 15:04:05"))
	fmt.Printf("+++ b/%s (working copy)\n", filePath)
	printDiffLines(snapshotContent, currentContent, filePath)
	return nil
}

func printDiffLines(old, new, path string) {
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")

	maxLines := len(oldLines)
	if len(newLines) > maxLines {
		maxLines = len(newLines)
	}

	i, j := 0, 0
	for i < len(oldLines) || j < len(newLines) {
		if i < len(oldLines) && j < len(newLines) && oldLines[i] == newLines[j] {
			fmt.Printf(" %s\n", oldLines[i])
			i++
			j++
		} else if j < len(newLines) && (i >= len(oldLines) || !containsLine(oldLines[i:], newLines[j])) {
			fmt.Printf("\033[32m+%s\033[0m\n", newLines[j])
			j++
		} else if i < len(oldLines) {
			fmt.Printf("\033[31m-%s\033[0m\n", oldLines[i])
			i++
		}
	}
}

func containsLine(lines []string, target string) bool {
	for _, l := range lines {
		if l == target {
			return true
		}
	}
	return false
}
