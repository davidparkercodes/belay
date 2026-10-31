package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/selfupdate"

	"github.com/spf13/cobra"
)

var (
	updateNewClient = func(version string) *selfupdate.Client {
		return selfupdate.NewClient("belay/" + version)
	}
	updateExecutable           = os.Executable
	updateEnv                  = selfupdate.DefaultEnv
	updateGOOS                 = runtime.GOOS
	updateGOARCH               = runtime.GOARCH
	updateIsTerminal           = stdinIsTerminal
	updateStdin      io.Reader = os.Stdin
)

type updateReport struct {
	Current         string             `json:"current"`
	Latest          string             `json:"latest,omitempty"`
	Target          string             `json:"target,omitempty"`
	UpdateAvailable bool               `json:"update_available"`
	Method          selfupdate.Method  `json:"method"`
	Path            string             `json:"path"`
	Updated         bool               `json:"updated"`
	Command         []string           `json:"command,omitempty"`
	Message         string             `json:"message,omitempty"`
	Result          *selfupdate.Result `json:"result,omitempty"`
}

func newUpdateCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update Belay to the latest release",
		Long: `Check for a newer Belay release and install it the same way this copy
was installed: Homebrew runs brew upgrade, go install runs go install, and
a release binary is downloaded, verified against checksums.txt and swapped
in place (the previous binary is kept as belay.prev).

Running daemons notice the new binary and restart into it once idle.

--mode sets the per-user update setting: notify (the default, one line in
belay status), auto (the daemon installs new releases itself) or off.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(cmd, version)
		},
	}
	cmd.Flags().Bool("check", false, "Only report whether an update is available")
	cmd.Flags().String("version", "", "Install this version (for example v1.2.3) instead of the latest")
	cmd.Flags().BoolP("yes", "y", false, "Do not ask for confirmation")
	cmd.Flags().Bool("json", false, "Output as JSON")
	cmd.Flags().String("auto", "", "Turn automatic updates on or off (on = mode auto, off = mode notify)")
	cmd.Flags().String("mode", "", "Set the update mode: notify, auto or off")
	return cmd
}

func runUpdate(cmd *cobra.Command, version string) error {
	out := cmd.OutOrStdout()
	jsonOut, _ := cmd.Flags().GetBool("json")
	auto, _ := cmd.Flags().GetString("auto")
	mode, _ := cmd.Flags().GetString("mode")

	if auto != "" || mode != "" {
		return runUpdateSetMode(out, auto, mode, jsonOut)
	}

	checkOnly, _ := cmd.Flags().GetBool("check")
	pinned, _ := cmd.Flags().GetString("version")
	yes, _ := cmd.Flags().GetBool("yes")

	exe, err := updateExecutable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	inst := selfupdate.Detect(exe, version, updateEnv())
	rep := updateReport{Current: version, Method: inst.Method, Path: inst.Path}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	client := updateNewClient(version)

	if pinned != "" {
		if _, ok := selfupdate.ParseVersion(pinned); !ok {
			return fmt.Errorf("invalid --version %q (expected vX.Y.Z)", pinned)
		}
		rep.Target = selfupdate.Tag(pinned)
	}
	if pinned == "" || checkOnly {
		latest, err := client.LatestTag(ctx)
		if err != nil {
			return err
		}
		rep.Latest = latest
		recordCheck(latest, version)
		if rep.Target == "" {
			rep.Target = latest
		}
	}
	rep.UpdateAvailable = selfupdate.IsNewer(rep.Latest, version)

	if checkOnly {
		switch {
		case rep.UpdateAvailable:
			rep.Message = selfupdate.NoticeLine(rep.Latest, version)
		case inst.Method == selfupdate.MethodDev:
			rep.Message = fmt.Sprintf("Latest release is %s. This is a %s.", rep.Latest, inst.Reason)
		default:
			rep.Message = fmt.Sprintf("Belay %s is up to date.", selfupdate.Tag(version))
		}
		return printUpdate(out, rep, jsonOut)
	}

	if pinned == "" && !rep.UpdateAvailable && inst.Method != selfupdate.MethodDev {
		rep.Message = fmt.Sprintf("Belay %s is up to date.", selfupdate.Tag(version))
		return printUpdate(out, rep, jsonOut)
	}
	if pinned != "" && selfupdate.Tag(pinned) == selfupdate.Tag(version) {
		rep.Message = fmt.Sprintf("Belay %s is already installed.", selfupdate.Tag(version))
		return printUpdate(out, rep, jsonOut)
	}

	if !yes && !jsonOut && inst.Method != selfupdate.MethodDev && updateIsTerminal() {
		if !confirmUpdate(out, rep) {
			rep.Message = "Update cancelled."
			return printUpdate(out, rep, jsonOut)
		}
	}

	updater := &selfupdate.Updater{Client: client, GOOS: updateGOOS, GOARCH: updateGOARCH}
	res, err := updater.Apply(ctx, inst, version, rep.Target, pinned != "")
	if err != nil {
		var nw *selfupdate.NotWritableError
		if errors.As(err, &nw) || errors.Is(err, selfupdate.ErrDevBuild) {
			return err
		}
		return fmt.Errorf("update to %s failed: %w", rep.Target, err)
	}
	rep.Result = &res
	rep.Command = res.Command
	rep.Updated = res.Ran
	if res.Ran {
		rep.Message = fmt.Sprintf("Updated Belay %s -> %s (%s, %s). Running daemons restart into it once idle.", selfupdate.Tag(version), rep.Target, inst.Method, inst.Path)
	} else {
		rep.Message = fmt.Sprintf("Belay %s is available. Go is not on PATH; run: %s", rep.Target, strings.Join(res.Command, " "))
	}
	return printUpdate(out, rep, jsonOut)
}

func confirmUpdate(out io.Writer, rep updateReport) bool {
	fmt.Fprintf(out, "Update Belay %s -> %s (%s, %s)? [y/N] ", selfupdate.Tag(rep.Current), rep.Target, rep.Method, rep.Path)
	line, _ := bufio.NewReader(updateStdin).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func printUpdate(out io.Writer, rep updateReport, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	if rep.Result != nil && rep.Result.Output != "" {
		fmt.Fprintln(out, rep.Result.Output)
	}
	fmt.Fprintln(out, rep.Message)
	return nil
}

func recordCheck(latest, version string) {
	dir, err := selfupdate.CacheDir()
	if err != nil {
		return
	}
	path := selfupdate.CachePath(dir)
	st, _ := selfupdate.ReadState(path)
	st.CheckedAt = time.Now()
	st.CheckedBy = version
	st.Latest = latest
	st.Error = ""
	_ = selfupdate.WriteState(path, st)
}

func runUpdateSetMode(out io.Writer, auto, mode string, jsonOut bool) error {
	if auto != "" && mode != "" {
		return errors.New("use either --auto or --mode, not both")
	}
	if auto != "" {
		switch strings.ToLower(strings.TrimSpace(auto)) {
		case "on", "true", "yes":
			mode = config.UpdateAuto
		case "off", "false", "no":
			mode = config.UpdateNotify
		default:
			return fmt.Errorf("invalid --auto %q (use on or off)", auto)
		}
	}
	path, err := config.UserConfigPath()
	if err != nil {
		return err
	}
	if err := config.SetUserUpdateMode(path, mode); err != nil {
		return err
	}
	normalized, _ := config.NormalizeUpdateMode(mode)
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]string{"mode": normalized, "config": path})
	}
	fmt.Fprintf(out, "Update mode set to %s (%s).\n", normalized, path)
	return nil
}
