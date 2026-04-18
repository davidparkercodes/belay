package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"

	"github.com/spf13/cobra"
)

func newGrepCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "grep PATTERN",
		Short: "Find events where the occurrence count of PATTERN changed (pickaxe)",
		Long: `Search history for events that added or removed occurrences of PATTERN,
similar to git log -S. Compares the content before and after each event and
reports changes where the occurrence count of PATTERN differs.

PATTERN is a literal substring by default. Use -G to interpret it as a regex.

Examples:
  belay grep "Shift+Enter"                       # any event that added/removed the phrase
  belay grep "TODO" --file "src/**"              # restrict to a path glob
  belay grep --session abc123 "localhost"        # only events in one session
  belay grep -G "fooBar[0-9]+" -i --since 7d     # regex, case-insensitive, last 7 days`,
		Args: cobra.ExactArgs(1),
		RunE: runGrep,
	}

	cmd.Flags().String("session", "", "Filter by session ID")
	cmd.Flags().String("file", "", "Filter by file path (supports globs)")
	cmd.Flags().String("since", "", "Only consider events after this time (e.g., 1h, 30m, 2d)")
	cmd.Flags().String("until", "", "Only consider events before this time")
	cmd.Flags().BoolP("ignore-case", "i", false, "Case-insensitive match")
	cmd.Flags().BoolP("regex", "G", false, "Treat PATTERN as a Go regular expression")
	cmd.Flags().Int("limit", 50, "Maximum number of matching events to show")
	cmd.Flags().Int("scan-limit", 20000, "Maximum number of events to scan (safety cap; 0 = unlimited)")
	cmd.Flags().Bool("json", false, "Output as JSON")

	return cmd
}

type grepMatch struct {
	Event *schema.Event
	Delta int
}

type grepMatchJSON struct {
	EventID     string `json:"event_id"`
	Timestamp   string `json:"timestamp"`
	FilePath    string `json:"file_path"`
	Operation   string `json:"operation"`
	SessionID   string `json:"session_id,omitempty"`
	Delta       int    `json:"delta"`
	ContentHash string `json:"content_hash,omitempty"`
	Previous    string `json:"previous_hash,omitempty"`
}

func runGrep(cmd *cobra.Command, args []string) error {
	pattern := args[0]
	if pattern == "" {
		return fmt.Errorf("pattern must not be empty")
	}

	sessionFilter, _ := cmd.Flags().GetString("session")
	fileFilter, _ := cmd.Flags().GetString("file")
	sinceStr, _ := cmd.Flags().GetString("since")
	untilStr, _ := cmd.Flags().GetString("until")
	ignoreCase, _ := cmd.Flags().GetBool("ignore-case")
	asRegex, _ := cmd.Flags().GetBool("regex")
	limit, _ := cmd.Flags().GetInt("limit")
	scanLimit, _ := cmd.Flags().GetInt("scan-limit")
	jsonOutput, _ := cmd.Flags().GetBool("json")

	matcher, err := buildMatcher(pattern, ignoreCase, asRegex)
	if err != nil {
		return err
	}

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

	q := &index.Query{
		OrderDesc:  true,
		Operations: []string{"CREATE", "MODIFY", "DELETE"},
	}
	if sessionFilter != "" {
		q.Sessions = []string{sessionFilter}
	}
	if fileFilter != "" {
		q.FilePaths = []string{fileFilter}
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
	if scanLimit > 0 {
		q.Limit = scanLimit
	}

	events, err := idx.QueryEvents(q)
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}

	matches, scanned, err := pickaxeEvents(events, objStore, matcher, limit)
	if err != nil {
		return err
	}

	if jsonOutput {
		out := make([]grepMatchJSON, 0, len(matches))
		for _, m := range matches {
			out = append(out, grepMatchJSON{
				EventID:     m.Event.EventID,
				Timestamp:   m.Event.Timestamp().Format("2006-01-02T15:04:05.000Z07:00"),
				FilePath:    m.Event.FilePath,
				Operation:   m.Event.Op.String(),
				SessionID:   m.Event.SessionID,
				Delta:       m.Delta,
				ContentHash: m.Event.ContentHash,
				Previous:    m.Event.PreviousHash,
			})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if len(matches) == 0 {
		fmt.Fprintf(os.Stderr, "No events changed the occurrence count of %q (scanned %d events).\n", pattern, scanned)
		return nil
	}

	for _, m := range matches {
		ts := m.Event.Timestamp().Format("2006-01-02 15:04:05.000")

		sessionStr := "unattributed"
		if m.Event.SessionID != "" {
			color := sessionColor(m.Event.SessionID)
			sid := m.Event.SessionID
			if len(sid) > 12 {
				sid = sid[:12]
			}
			sessionStr = fmt.Sprintf("\033[%sm%s\033[0m", color, sid)
		}

		deltaStr := formatDelta(m.Delta)
		opStr := colorOp(m.Event.Op.String())

		fmt.Printf("[%s] [%s] %s %s %s\n", ts, sessionStr, deltaStr, opStr, m.Event.FilePath)
	}

	fmt.Printf("\n%d matching event(s) from %d scanned.\n", len(matches), scanned)
	return nil
}

// matcher abstracts "how many times does the pattern appear in these bytes".
type matcher struct {
	literal    string
	literalLow string
	ignoreCase bool
	re         *regexp.Regexp
}

func buildMatcher(pattern string, ignoreCase, asRegex bool) (*matcher, error) {
	m := &matcher{ignoreCase: ignoreCase}
	if asRegex {
		expr := pattern
		if ignoreCase {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
		m.re = re
		return m, nil
	}
	m.literal = pattern
	if ignoreCase {
		m.literalLow = strings.ToLower(pattern)
	}
	return m, nil
}

func (m *matcher) count(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	if m.re != nil {
		return len(m.re.FindAllIndex(data, -1))
	}
	if m.ignoreCase {
		return strings.Count(strings.ToLower(string(data)), m.literalLow)
	}
	return strings.Count(string(data), m.literal)
}

// pickaxeEvents walks events (expected in desc order) and keeps those whose
// pattern occurrence count differs between previous_hash and content_hash.
// Blob→count lookups are cached so files touched repeatedly only pay once.
func pickaxeEvents(events []*schema.Event, objStore *store.Store, m *matcher, limit int) ([]grepMatch, int, error) {
	countCache := make(map[string]int)

	countFor := func(hash string) (int, error) {
		if hash == "" {
			return 0, nil
		}
		if c, ok := countCache[hash]; ok {
			return c, nil
		}
		data, err := objStore.Get(hash)
		if err != nil {
			countCache[hash] = 0
			return 0, nil
		}
		c := m.count(data)
		countCache[hash] = c
		return c, nil
	}

	var matches []grepMatch
	scanned := 0
	for _, e := range events {
		scanned++
		newCount, err := countFor(e.ContentHash)
		if err != nil {
			return nil, scanned, err
		}
		oldCount, err := countFor(e.PreviousHash)
		if err != nil {
			return nil, scanned, err
		}
		if newCount == oldCount {
			continue
		}
		matches = append(matches, grepMatch{
			Event: e,
			Delta: newCount - oldCount,
		})
		if limit > 0 && len(matches) >= limit {
			break
		}
	}
	return matches, scanned, nil
}

func formatDelta(d int) string {
	switch {
	case d > 0:
		return fmt.Sprintf("\033[32m+%d\033[0m", d)
	case d < 0:
		return fmt.Sprintf("\033[31m%d\033[0m", d)
	default:
		return "0"
	}
}
