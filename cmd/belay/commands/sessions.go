package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"

	"github.com/spf13/cobra"
)

func newSessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List and inspect AI sessions",
		Long:  `View active and historical AI development sessions tracked by Belay.`,
		RunE:  runSessionsList,
	}

	cmd.Flags().Bool("json", false, "Output as JSON")
	cmd.Flags().Bool("active", false, "Show only active sessions")
	cmd.Flags().Bool("with-events", true, "Only show sessions that have at least 1 event (default: true)")
	cmd.Flags().Bool("all", false, "Show all sessions including those with 0 events")
	cmd.Flags().Int("limit", 0, "Maximum number of sessions to return (0 = no limit)")

	cmd.AddCommand(
		newSessionsListCmd(),
		newSessionsShowCmd(),
		newSessionsActiveCmd(),
		newSessionsLabelCmd(),
	)

	return cmd
}

func newSessionsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all sessions",
		RunE:  runSessionsList,
	}
	cmd.Flags().Bool("json", false, "Output as JSON")
	cmd.Flags().Bool("active", false, "Show only active sessions")
	cmd.Flags().Bool("with-events", true, "Only show sessions that have at least 1 event (default: true)")
	cmd.Flags().Bool("all", false, "Show all sessions including those with 0 events")
	cmd.Flags().Int("limit", 0, "Maximum number of sessions to return (0 = no limit)")
	return cmd
}

func runSessionsList(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	activeOnly, _ := cmd.Flags().GetBool("active")
	withEvents, _ := cmd.Flags().GetBool("with-events")
	showAll, _ := cmd.Flags().GetBool("all")
	limit, _ := cmd.Flags().GetInt("limit")

	minEvents := 0
	if withEvents && !showAll {
		minEvents = 1
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

	sessions, err := idx.ListSessions(activeOnly, minEvents, limit)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions found.")
		fmt.Println("Sessions are detected when the Belay daemon is running.")
		return nil
	}

	if jsonOutput {
		var jsonSessions []interface{}
		for _, s := range sessions {
			jsonSessions = append(jsonSessions, s.ToJSON())
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(jsonSessions)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION ID\tTOOL\tSTATUS\tDURATION\tFILES\tEVENTS\tLABEL")
	fmt.Fprintln(w, "----------\t----\t------\t--------\t-----\t------\t-----")

	for _, s := range sessions {
		status := s.Status.String()
		switch s.Status.String() {
		case "active":
			status = "\033[32m" + status + "\033[0m"
		case "crashed":
			status = "\033[31m" + status + "\033[0m"
		}

		label := s.Label
		if label == "" {
			label = "-"
		}

		sid := s.SessionID
		if len(sid) > 16 {
			sid = sid[:16] + "..."
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
			sid, s.ToolName, status,
			s.Duration().Round(1e9).String(),
			s.FilesChanged, s.EventCount, label,
		)
	}
	w.Flush()

	return nil
}

func newSessionsShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show [session-id]",
		Short: "Show session details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			session, err := idx.GetSession(args[0])
			if err != nil {
				return fmt.Errorf("session not found: %s", args[0])
			}

			events, err := idx.QueryEvents(&index.Query{
				Sessions:  []string{args[0]},
				OrderDesc: true,
				Limit:     100,
			})
			if err != nil {
				return fmt.Errorf("query events: %w", err)
			}

			if jsonOutput {
				result := map[string]interface{}{
					"session": session.ToJSON(),
					"events":  len(events),
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			fmt.Printf("Session: %s\n", session.SessionID)
			fmt.Printf("  Tool:     %s\n", session.ToolName)
			fmt.Printf("  Status:   %s\n", session.Status.String())
			fmt.Printf("  PID:      %d\n", session.PID)
			fmt.Printf("  Started:  %s\n", session.StartedAt.Format("2006-01-02 15:04:05"))
			if !session.EndedAt.IsZero() {
				fmt.Printf("  Ended:    %s\n", session.EndedAt.Format("2006-01-02 15:04:05"))
			}
			fmt.Printf("  Duration: %s\n", session.Duration().Round(1e9).String())
			if session.Label != "" {
				fmt.Printf("  Label:    %s\n", session.Label)
			}
			fmt.Printf("  Files:    %d\n", session.FilesChanged)
			fmt.Printf("  Events:   %d\n", session.EventCount)

			if len(events) > 0 {
				fmt.Printf("\nRecent Events (%d shown):\n", len(events))
				for _, e := range events {
					ts := e.Timestamp().Format("15:04:05.000")
					fmt.Printf("  [%s] %s %s", ts, e.Op.String(), e.FilePath)
					if e.ContentSize > 0 {
						fmt.Printf(" (%s)", humanBytes(e.ContentSize))
					}
					fmt.Println()
				}
			}

			return nil
		},
	}
	cmd.Flags().Bool("json", false, "Output as JSON")
	return cmd
}

func newSessionsActiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "active",
		Short: "Show only active sessions",
		RunE:  runSessionsActive,
	}
	cmd.Flags().Bool("json", false, "Output as JSON")
	return cmd
}

func runSessionsActive(cmd *cobra.Command, args []string) error {
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

	sessions, err := idx.ListSessions(true, 0, 0)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No active sessions.")
		return nil
	}

	if jsonOutput {
		var jsonSessions []interface{}
		for _, s := range sessions {
			jsonSessions = append(jsonSessions, s.ToJSON())
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(jsonSessions)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION ID\tTOOL\tSTATUS\tDURATION\tFILES\tEVENTS\tLABEL")
	fmt.Fprintln(w, "----------\t----\t------\t--------\t-----\t------\t-----")

	for _, s := range sessions {
		status := "\033[32m" + s.Status.String() + "\033[0m"

		label := s.Label
		if label == "" {
			label = "-"
		}

		sid := s.SessionID
		if len(sid) > 16 {
			sid = sid[:16] + "..."
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
			sid, s.ToolName, status,
			s.Duration().Round(1e9).String(),
			s.FilesChanged, s.EventCount, label,
		)
	}
	w.Flush()

	return nil
}

func newSessionsLabelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "label [session-id] [label]",
		Short: "Label a session with a human-readable name",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			if err := idx.UpdateSessionLabel(args[0], args[1]); err != nil {
				return err
			}

			fmt.Printf("Session %s labeled as %q\n", args[0], args[1])
			return nil
		},
	}
}
