package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"

	"github.com/spf13/cobra"
)

func newCheckpointsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checkpoints",
		Short: "List labeled checkpoints",
		Long: `List CHECKPOINT marker events recorded by ` + "`belay checkpoint`" + `.

Pair with: belay restore --to-checkpoint <id-or-label> --all --execute`,
		RunE: runCheckpoints,
	}

	cmd.Flags().IntP("limit", "n", 50, "Maximum number of checkpoints to show")
	cmd.Flags().String("since", "", "Only show checkpoints since this time (e.g. '1h ago', '2d')")
	cmd.Flags().String("until", "", "Only show checkpoints up to this time")
	cmd.Flags().Bool("json", false, "Output as JSON")

	return cmd
}

func runCheckpoints(cmd *cobra.Command, args []string) error {
	limit, _ := cmd.Flags().GetInt("limit")
	sinceStr, _ := cmd.Flags().GetString("since")
	untilStr, _ := cmd.Flags().GetString("until")
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

	q := &index.Query{
		Operations: []string{"CHECKPOINT"},
		OrderDesc:  true,
		Limit:      limit,
	}
	if sinceStr != "" {
		t, err := parseRelativeTime(sinceStr)
		if err != nil {
			return fmt.Errorf("invalid --since: %w", err)
		}
		q.Since = t.UnixNano()
	}
	if untilStr != "" {
		t, err := parseRelativeTime(untilStr)
		if err != nil {
			return fmt.Errorf("invalid --until: %w", err)
		}
		q.Until = t.UnixNano()
	}

	events, err := idx.QueryEvents(q)
	if err != nil {
		return fmt.Errorf("query checkpoints: %w", err)
	}

	if jsonOutput {
		out := make([]map[string]interface{}, 0, len(events))
		for _, e := range events {
			out = append(out, map[string]interface{}{
				"event_id":       e.EventID,
				"timestamp":      e.Timestamp().Format(time.RFC3339Nano),
				"timestamp_nano": e.TimestampNano,
				"label":          e.Metadata["label"],
				"reason":         e.Metadata["reason"],
				"tool_name":      e.Metadata["tool_name"],
				"session_id":     e.SessionID,
			})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if len(events) == 0 {
		fmt.Println("No checkpoints recorded yet. Create one with: belay checkpoint --label \"<text>\"")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTIME\tLABEL\tSOURCE\tSESSION")
	for _, e := range events {
		id := truncateStr(e.EventID, 12)
		when := e.Timestamp().Format("2006-01-02 15:04:05")
		label := e.Metadata["label"]
		if label == "" {
			label = "(unlabeled)"
		}
		source := e.Metadata["tool_name"]
		if source == "" {
			source = "manual"
		}
		sid := truncateStr(e.SessionID, 8)
		if sid == "" {
			sid = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", id, when, label, source, sid)
	}
	return tw.Flush()
}
