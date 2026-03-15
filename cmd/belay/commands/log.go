package commands

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"

	"github.com/spf13/cobra"
)

func newLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Browse event history",
		Long: `Show the event history with session attribution and color coding.
Similar to git log, but with AI session awareness.`,
		RunE: runLog,
	}

	cmd.Flags().String("session", "", "Filter by session ID")
	cmd.Flags().String("file", "", "Filter by file path (supports globs)")
	cmd.Flags().String("since", "", "Show events after this time (e.g., 1h, 30m, 2d)")
	cmd.Flags().String("until", "", "Show events before this time")
	cmd.Flags().String("op", "", "Filter by operation type (create, modify, delete, rename)")
	cmd.Flags().Int("limit", 50, "Number of events to show")
	cmd.Flags().Bool("json", false, "Output as JSON")

	return cmd
}

func runLog(cmd *cobra.Command, args []string) error {
	sessionFilter, _ := cmd.Flags().GetString("session")
	fileFilter, _ := cmd.Flags().GetString("file")
	sinceStr, _ := cmd.Flags().GetString("since")
	untilStr, _ := cmd.Flags().GetString("until")
	opFilter, _ := cmd.Flags().GetString("op")
	limit, _ := cmd.Flags().GetInt("limit")
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
		Limit:     limit,
		OrderDesc: true,
	}

	if sessionFilter != "" {
		q.Sessions = []string{sessionFilter}
	}
	if fileFilter != "" {
		q.FilePaths = []string{fileFilter}
	}
	if opFilter != "" {
		q.Operations = strings.Split(opFilter, ",")
	}
	if sinceStr != "" {
		t, err := parseRelativeTime(sinceStr)
		if err != nil {
			return fmt.Errorf("invalid --since value: %w", err)
		}
		q.Since = t.UnixNano()
	}
	if untilStr != "" {
		t, err := parseRelativeTime(untilStr)
		if err != nil {
			return fmt.Errorf("invalid --until value: %w", err)
		}
		q.Until = t.UnixNano()
	}

	events, err := idx.QueryEvents(q)
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}

	if len(events) == 0 {
		fmt.Println("No events found.")
		return nil
	}

	if jsonOutput {
		var jsonEvents []interface{}
		for _, e := range events {
			jsonEvents = append(jsonEvents, e.ToJSON())
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(jsonEvents)
	}

	for _, e := range events {
		ts := e.Timestamp().Format("2006-01-02 15:04:05.000")

		sessionStr := "unattributed"
		if e.SessionID != "" {
			color := sessionColor(e.SessionID)
			sid := e.SessionID
			if len(sid) > 12 {
				sid = sid[:12]
			}
			sessionStr = fmt.Sprintf("\033[%sm%s\033[0m", color, sid)
		}

		opStr := colorOp(e.Op.String())

		sizeStr := ""
		if e.ContentSize > 0 {
			sizeStr = fmt.Sprintf(" (%s)", humanBytes(e.ContentSize))
		}

		conflictStr := ""
		if e.IsConflict {
			conflictStr = " \033[31m[CONFLICT]\033[0m"
		}

		fmt.Printf("[%s] [%s] %s %s%s%s\n",
			ts, sessionStr, opStr, e.FilePath, sizeStr, conflictStr)
	}

	count, _ := idx.CountEvents()
	fmt.Printf("\nShowing %d of %d total events\n", len(events), count)

	return nil
}

func parseRelativeTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time string")
	}

	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}

	if len(s) < 2 {
		return time.Time{}, fmt.Errorf("invalid relative time: %s", s)
	}

	unit := s[len(s)-1]
	numStr := s[:len(s)-1]

	var num float64
	if _, err := fmt.Sscanf(numStr, "%f", &num); err != nil {
		return time.Time{}, fmt.Errorf("invalid number in time: %s", s)
	}

	var d time.Duration
	switch unit {
	case 's':
		d = time.Duration(num * float64(time.Second))
	case 'm':
		d = time.Duration(num * float64(time.Minute))
	case 'h':
		d = time.Duration(num * float64(time.Hour))
	case 'd':
		d = time.Duration(num * 24 * float64(time.Hour))
	case 'w':
		d = time.Duration(num * 7 * 24 * float64(time.Hour))
	default:
		return time.Time{}, fmt.Errorf("unknown time unit: %c (use s, m, h, d, w)", unit)
	}

	return time.Now().Add(-d), nil
}

func sessionColor(sessionID string) string {
	h := sha256.Sum256([]byte(sessionID))
	colors := []string{"91", "92", "93", "94", "95", "96", "33", "36"}
	i := int(h[0]) % len(colors)
	return colors[i]
}

func colorOp(op string) string {
	switch op {
	case "CREATE":
		return "\033[32mCREATE\033[0m"
	case "MODIFY":
		return "\033[33mMODIFY\033[0m"
	case "DELETE":
		return "\033[31mDELETE\033[0m"
	case "RENAME":
		return "\033[36mRENAME\033[0m"
	default:
		return op
	}
}
