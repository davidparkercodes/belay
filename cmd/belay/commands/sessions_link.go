package commands

import (
	"fmt"
	"time"

	"github.com/davidparkercodes/belay/internal/apiclient"
	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/session"

	"github.com/spf13/cobra"
)

type sessionLinkRequest struct {
	SessionID string `json:"session_id"`
	PID       int    `json:"pid"`
	ToolName  string `json:"tool_name,omitempty"`
}

type sessionLinkResponse struct {
	Status    string   `json:"status"`
	SessionID string   `json:"session_id"`
	PID       int      `json:"pid"`
	Linked    []string `json:"linked"`
}

func newSessionsLinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link <session-id>",
		Short: "Tie the calling agent process to its tool session id (used by hooks)",
		Long: `Tell the daemon which agent process a tool session id belongs to.

Belay attributes filesystem writes to the agent process that made them and names
that session after the process (for example claude-pid-1234). The tool's own
session id only reaches Belay through hooks. Linking the two means every write
by that process, including ones made through a shell rather than an editing
tool, resolves to the tool's session id, so 'belay log --session', 'belay diff
--session', 'belay restore --session' and 'belay project --session' all see them.

Run it from a hook (the agent is then an ancestor of this process) and the PID
is found automatically; otherwise pass --pid.`,
		Args: cobra.ExactArgs(1),
		RunE: runSessionsLink,
	}
	cmd.Flags().Int("pid", 0, "Agent process id (default: nearest agent ancestor of this process)")
	cmd.Flags().String("tool", "", "Tool name (e.g. claude-code)")
	cmd.Flags().Bool("quiet", false, "Print nothing on success")
	return cmd
}

func runSessionsLink(cmd *cobra.Command, args []string) error {
	pid, _ := cmd.Flags().GetInt("pid")
	toolName, _ := cmd.Flags().GetString("tool")
	quiet, _ := cmd.Flags().GetBool("quiet")

	if pid <= 0 {
		pid = session.CallerAgentPID()
	}
	if pid <= 0 {
		return fmt.Errorf("no agent process found above this one; pass --pid")
	}

	projectRoot, err := config.FindProjectRoot()
	if err != nil {
		return fmt.Errorf("not a belay project: %w", err)
	}
	cfg, err := config.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	req := sessionLinkRequest{SessionID: args[0], PID: pid, ToolName: toolName}
	var out sessionLinkResponse
	if err := apiclient.New(cfg, 2*time.Second).PostJSON("/api/sessions/link", &req, &out); err != nil {
		return err
	}
	if quiet {
		return nil
	}
	if len(out.Linked) == 0 {
		fmt.Printf("Session %s linked to PID %d (no process session tracked yet; future writes resolve to it)\n", out.SessionID, out.PID)
		return nil
	}
	for _, l := range out.Linked {
		fmt.Printf("Session %s now resolves to %s (PID %d)\n", l, out.SessionID, out.PID)
	}
	return nil
}
