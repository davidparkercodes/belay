package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/conflict"
	"github.com/davidparkercodes/belay/internal/index"

	"github.com/spf13/cobra"
)

func newConflictsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "conflicts",
		Short: "Detect and view session conflicts",
		Long: `Show moments where multiple AI sessions modified the same file
in overlapping time windows.`,
		RunE: runConflicts,
	}

	cmd.Flags().String("since", "24h", "Look for conflicts after this time (default: 24h)")
	cmd.Flags().String("file", "", "Check conflicts for a specific file")
	cmd.Flags().Bool("json", false, "Output as JSON")

	return cmd
}

func runConflicts(cmd *cobra.Command, args []string) error {
	sinceStr, _ := cmd.Flags().GetString("since")
	fileFilter, _ := cmd.Flags().GetString("file")
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

	since, err := parseRelativeTime(sinceStr)
	if err != nil {
		return fmt.Errorf("invalid --since: %w", err)
	}

	detector := conflict.NewDetector(idx, 60*time.Second)

	var conflicts []*conflict.Conflict
	if fileFilter != "" {
		conflicts, err = detector.DetectForFile(fileFilter, since)
	} else {
		conflicts, err = detector.DetectSince(since)
	}
	if err != nil {
		return fmt.Errorf("detect conflicts: %w", err)
	}

	if len(conflicts) == 0 {
		fmt.Println("No conflicts detected.")
		return nil
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(conflicts)
	}

	fmt.Printf("Found %d conflict(s):\n\n", len(conflicts))

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSEVERITY\tFILE\tSESSIONS\tWINDOW\tDETECTED")
	fmt.Fprintln(w, "--\t--------\t----\t--------\t------\t--------")

	for _, c := range conflicts {
		severity := c.Severity.String()
		switch c.Severity {
		case conflict.SeverityCritical:
			severity = "\033[31m" + severity + "\033[0m"
		case conflict.SeverityHigh:
			severity = "\033[33m" + severity + "\033[0m"
		}

		sessions := fmt.Sprintf("%d", len(c.Sessions))
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			c.ID[:16], severity, c.FilePath,
			sessions, c.Window.Round(time.Second).String(),
			c.DetectedAt.Format("15:04:05"))
	}
	w.Flush()

	return nil
}
