package commands

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/scrub"
	"github.com/davidparkercodes/belay/internal/secrets"
	"github.com/spf13/cobra"
)

var scrubSources = scrub.DefaultSources

const scrubStatusBudget = 300 * time.Millisecond

func newScrubCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scrub",
		Short: "Redact credentials from idle AI agent session logs",
		Long: `Redacts likely credentials (API keys, tokens, passwords, including ones written
in plain sentences) from this project's AI agent session logs, such as Claude
Code transcripts. Each one is replaced with [redacted:<rule>].

Only idle logs are touched: no write in the last 30 minutes and not an active
Belay session, because agents read their own logs to resume. JSONL logs are
redacted inside their JSON string values, so every line stays valid JSON.

Before a log is rewritten its original bytes are saved in Belay's encrypted
store; "belay scrub --undo <log>" puts them back byte-exact. Output shows counts
per rule, never the secrets themselves.

Set [scrub] mode = "auto" in .belay/config.toml to let the daemon do this in the
background, or "off" to silence the belay status notice.`,
		Args: cobra.NoArgs,
		RunE: runScrub,
	}
	cmd.Flags().Bool("dry-run", false, "List what would be redacted without changing anything")
	cmd.Flags().Bool("json", false, "Output as JSON")
	cmd.Flags().String("undo", "", "Put back the original of a scrubbed log (byte-exact)")
	cmd.Flags().Bool("all-idle", false, "Include idle logs of every project the agent knows, not only this one")
	return cmd
}

func runScrub(cmd *cobra.Command, args []string) error {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	jsonOut, _ := cmd.Flags().GetBool("json")
	undo, _ := cmd.Flags().GetString("undo")
	allIdle, _ := cmd.Flags().GetBool("all-idle")
	out := cmd.OutOrStdout()

	root, err := config.FindProjectRoot()
	if err != nil {
		return fmt.Errorf("not a belay project: %w", err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	sc := &scrub.Scrubber{
		BelayPath: cfg.BelayPath,
		Sources:   scrubSources(cfg.ProjectRoot),
		Scanner:   secrets.NewScanner(secrets.Options{}),
	}
	if !dryRun || undo != "" {
		objStore, err := openStore(cfg)
		if err != nil {
			return fmt.Errorf("open store: %w", err)
		}
		defer objStore.Close()
		sc.Store = objStore
	}

	if undo != "" {
		res, err := sc.Undo(undo)
		if err != nil {
			return err
		}
		if jsonOut {
			return writeJSON(out, res)
		}
		switch {
		case !res.Restored:
			fmt.Fprintf(out, "%s already matches its original; marked as undone.\n", res.Log)
		case res.KeptAppended > 0:
			fmt.Fprintf(out, "Restored %s (original bytes, plus %d bytes the agent appended after the scrub).\n", res.Log, res.KeptAppended)
		default:
			fmt.Fprintf(out, "Restored %s byte-exact.\n", res.Log)
		}
		if res.Remaining > 0 {
			fmt.Fprintf(out, "%d older scrub(s) of this log can also be undone.\n", res.Remaining)
		}
		return nil
	}

	if idx, err := index.Open(cfg.IndexPath()); err == nil {
		active := scrub.ActiveSessionIDs(idx)
		idx.Close()
		sc.Active = func() map[string]bool { return active }
	}

	rep, err := sc.Run(scrub.RunOptions{DryRun: dryRun, AllProjects: allIdle})
	if err != nil {
		return err
	}
	if jsonOut {
		return writeJSON(out, rep)
	}
	if len(rep.Files) == 0 {
		fmt.Fprintln(out, "No credentials found in agent logs.")
		return nil
	}
	for _, f := range rep.Files {
		line := fmt.Sprintf("  %-15s %s", f.Status, f.Path)
		if c := formatCounts(f.Counts); c != "" {
			line += "  (" + c + ")"
		}
		if f.Reason != "" && f.Status != scrub.StatusRedacted {
			line += "  " + f.Reason
		}
		fmt.Fprintln(out, line)
	}
	fmt.Fprintln(out)
	switch {
	case dryRun:
		fmt.Fprintf(out, "Would redact %d credential(s). Run belay scrub to apply.\n", countWould(rep))
	case rep.Redacted > 0:
		fmt.Fprintf(out, "Redacted %d log(s). Originals are saved; undo with: belay scrub --undo <log>\n", rep.Redacted)
	default:
		fmt.Fprintln(out, "Nothing redacted.")
	}
	return nil
}

func countWould(rep *scrub.Report) int {
	n := 0
	for _, f := range rep.Files {
		if f.Status == scrub.StatusWouldRedact {
			n += f.Total
		}
	}
	return n
}

func formatCounts(c scrub.Counts) string {
	rules := make([]string, 0, len(c))
	for r := range c {
		rules = append(rules, r)
	}
	sort.Strings(rules)
	parts := make([]string, 0, len(rules))
	for _, r := range rules {
		parts = append(parts, fmt.Sprintf("%s %d", r, c[r]))
	}
	return strings.Join(parts, ", ")
}

func writeJSON(w interface{ Write([]byte) (int, error) }, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// scrubNoticeLine returns the single belay status line about agent logs, or "".
func scrubNoticeLine(cfg *config.Config) string {
	if cfg.Scrub.Mode == config.ScrubOff {
		return ""
	}
	if cfg.Scrub.Mode == config.ScrubAuto {
		if e := scrub.LoadCache(cfg.BelayPath).LastAutoError; e != "" {
			return "Agent log scrub failed: " + e
		}
		return ""
	}
	n := scrub.CountFindings(cfg.BelayPath, scrubSources(cfg.ProjectRoot), secrets.NewScanner(secrets.Options{}), scrubStatusBudget)
	if !n.Complete || n.Findings == 0 {
		return ""
	}
	return fmt.Sprintf("%d credentials in plain text in agent logs. Run belay scrub to redact.", n.Findings)
}
