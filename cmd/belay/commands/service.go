package commands

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/daemon"

	"github.com/spf13/cobra"
)

var serviceRunCommand = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

var (
	serviceUserHomeDir = os.UserHomeDir
	serviceGOOS        = runtime.GOOS
	serviceUID         = os.Getuid
	serviceExecutable  = os.Executable
	serviceLookPath    = exec.LookPath
	serviceGetenv      = os.Getenv
)

var serviceStopRunningDaemon = func(projectRoot string) (int, error) {
	cfg, err := config.Load(projectRoot)
	if err != nil {
		return 0, fmt.Errorf("load config: %w", err)
	}
	running, pid := daemon.IsRunning(cfg)
	if !running {
		return 0, nil
	}
	if err := daemon.Stop(cfg); err != nil {
		return pid, fmt.Errorf("stop running daemon: %w", err)
	}
	return pid, nil
}

type serviceSpec struct {
	goos        string
	projectRoot string
	binary      string
	name        string
	unitPath    string
}

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Run the daemon under the OS service manager",
		Long: `Install, remove, or inspect a per-user service (launchd on macOS,
systemd --user on Linux) that keeps the Belay daemon running for this
project, including after a reboot.`,
	}

	install := &cobra.Command{
		Use:   "install",
		Short: "Install and start the service for this project",
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			root, err := serviceProjectRoot()
			if err != nil {
				return err
			}
			return runServiceInstall(cmd.OutOrStdout(), root, dryRun)
		},
	}
	install.Flags().Bool("dry-run", false, "Print the unit file and target path without changing anything")

	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the service for this project",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := serviceProjectRoot()
			if err != nil {
				return err
			}
			return runServiceUninstall(cmd.OutOrStdout(), root)
		},
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Show whether the service is installed and running",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := serviceProjectRoot()
			if err != nil {
				return err
			}
			return runServiceStatus(cmd.OutOrStdout(), root)
		},
	}

	cmd.AddCommand(install, uninstall, status)
	return cmd
}

func serviceProjectRoot() (string, error) {
	root, err := config.FindProjectRoot()
	if err != nil {
		return "", fmt.Errorf("not a belay project: %w\nRun 'belay init' first", err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	return abs, nil
}

func serviceSlug(projectRoot string) string {
	base := strings.ToLower(filepath.Base(filepath.Clean(projectRoot)))
	var b strings.Builder
	lastDash := false
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.TrimRight(b.String(), "-")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	if slug == "" {
		slug = "project"
	}
	return slug
}

func serviceHash(projectRoot string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(projectRoot)))
	return hex.EncodeToString(sum[:])[:8]
}

func serviceLaunchdLabel(projectRoot string) string {
	return "dev.belay.daemon." + serviceSlug(projectRoot) + "-" + serviceHash(projectRoot)
}

func serviceSystemdUnitName(projectRoot string) string {
	return "belay-" + serviceSlug(projectRoot) + "-" + serviceHash(projectRoot) + ".service"
}

func serviceResolveBinary() (string, error) {
	if override := serviceGetenv("BELAY_BIN"); override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("resolve BELAY_BIN: %w", err)
		}
		return abs, nil
	}
	return stableExecutable()
}

func stableExecutable() (string, error) {
	exe, err := serviceExecutable()
	if err != nil {
		return "", fmt.Errorf("find executable: %w", err)
	}

	if onPath, err := serviceLookPath("belay"); err == nil {
		if abs, err := filepath.Abs(onPath); err == nil && serviceSameFile(abs, exe) {
			return abs, nil
		}
	}
	return exe, nil
}

func serviceSameFile(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	if ra == rb {
		return true
	}
	sa, err := os.Stat(ra)
	if err != nil {
		return false
	}
	sb, err := os.Stat(rb)
	if err != nil {
		return false
	}
	return os.SameFile(sa, sb)
}

func serviceSpecFor(projectRoot, binary string) (*serviceSpec, error) {
	if strings.ContainsAny(projectRoot+binary, "\n\r\x00") {
		return nil, errors.New("project root and binary path must not contain newlines or NUL bytes")
	}
	home, err := serviceUserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("find home directory: %w", err)
	}

	spec := &serviceSpec{goos: serviceGOOS, projectRoot: projectRoot, binary: binary}
	switch serviceGOOS {
	case "darwin":
		spec.name = serviceLaunchdLabel(projectRoot)
		spec.unitPath = filepath.Join(home, "Library", "LaunchAgents", spec.name+".plist")
	case "linux":
		spec.name = serviceSystemdUnitName(projectRoot)
		spec.unitPath = filepath.Join(home, ".config", "systemd", "user", spec.name)
	default:
		return nil, fmt.Errorf("belay service is not supported on %s", serviceGOOS)
	}
	return spec, nil
}

func (s *serviceSpec) render() string {
	if s.goos == "darwin" {
		return serviceRenderPlist(s.name, s.binary, s.projectRoot)
	}
	return serviceRenderSystemdUnit(s.binary, s.projectRoot)
}

func serviceXMLEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func serviceRenderPlist(label, binary, projectRoot string) string {
	e := serviceXMLEscape
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	fmt.Fprintf(&b, "\t<key>Label</key>\n\t<string>%s</string>\n", e(label))
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, arg := range []string{binary, "daemon", "start", "--foreground"} {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", e(arg))
	}
	b.WriteString("\t</array>\n")
	fmt.Fprintf(&b, "\t<key>WorkingDirectory</key>\n\t<string>%s</string>\n", e(projectRoot))
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>\n")
	b.WriteString("\t<key>StandardOutPath</key>\n\t<string>/dev/null</string>\n")
	b.WriteString("\t<key>StandardErrorPath</key>\n\t<string>/dev/null</string>\n")
	b.WriteString("\t<key>ProcessType</key>\n\t<string>Background</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func serviceSystemdQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`)
	return `"` + r.Replace(s) + `"`
}

func serviceSystemdPath(s string) string {
	return strings.ReplaceAll(s, "%", "%%")
}

func serviceRenderSystemdUnit(binary, projectRoot string) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	fmt.Fprintf(&b, "Description=Belay daemon for %s\n", serviceSystemdPath(projectRoot))
	b.WriteString("\n[Service]\n")
	b.WriteString("Type=simple\n")
	fmt.Fprintf(&b, "ExecStart=%s daemon start --foreground\n", serviceSystemdQuote(binary))
	fmt.Fprintf(&b, "WorkingDirectory=%s\n", serviceSystemdPath(projectRoot))
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=5\n")
	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return b.String()
}

func serviceFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func serviceCmdError(action string, out []byte, err error) error {
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, msg)
}

func (s *serviceSpec) launchdDomain() string {
	return "gui/" + strconv.Itoa(serviceUID())
}

func (s *serviceSpec) launchdTarget() string {
	return s.launchdDomain() + "/" + s.name
}

func (s *serviceSpec) launchdLoaded() bool {
	_, err := serviceRunCommand("launchctl", "print", s.launchdTarget())
	return err == nil
}

func (s *serviceSpec) load() error {
	if s.goos == "darwin" {
		out, err := serviceRunCommand("launchctl", "bootstrap", s.launchdDomain(), s.unitPath)
		if err == nil {
			return nil
		}
		if !s.launchdLoaded() {
			return serviceCmdError("launchctl bootstrap", out, err)
		}
		if out, err := serviceRunCommand("launchctl", "bootout", s.launchdTarget()); err != nil {
			return serviceCmdError("launchctl bootout", out, err)
		}
		if out, err := serviceRunCommand("launchctl", "bootstrap", s.launchdDomain(), s.unitPath); err != nil {
			return serviceCmdError("launchctl bootstrap", out, err)
		}
		return nil
	}

	if out, err := serviceRunCommand("systemctl", "--user", "daemon-reload"); err != nil {
		return serviceCmdError("systemctl --user daemon-reload", out, err)
	}
	if out, err := serviceRunCommand("systemctl", "--user", "enable", "--now", s.name); err != nil {
		return serviceCmdError("systemctl --user enable --now", out, err)
	}
	return nil
}

func (s *serviceSpec) unload() error {
	if s.goos == "darwin" {
		if !s.launchdLoaded() {
			return nil
		}
		if out, err := serviceRunCommand("launchctl", "bootout", s.launchdTarget()); err != nil {
			return serviceCmdError("launchctl bootout", out, err)
		}
		return nil
	}
	out, err := serviceRunCommand("systemctl", "--user", "disable", "--now", s.name)
	if err != nil && serviceFileExists(s.unitPath) {
		return serviceCmdError("systemctl --user disable --now", out, err)
	}
	return nil
}

func (s *serviceSpec) active() (bool, string) {
	if s.goos == "darwin" {
		if s.launchdLoaded() {
			return true, "loaded"
		}
		return false, "not loaded"
	}
	out, err := serviceRunCommand("systemctl", "--user", "is-active", s.name)
	state := strings.TrimSpace(string(out))
	if state == "" {
		state = "unknown"
	}
	return err == nil && state == "active", state
}

func runServiceInstall(w io.Writer, projectRoot string, dryRun bool) error {
	binary, err := serviceResolveBinary()
	if err != nil {
		return err
	}
	spec, err := serviceSpecFor(projectRoot, binary)
	if err != nil {
		return err
	}
	content := spec.render()

	if dryRun {
		fmt.Fprintf(w, "Would write %s\n\n%s", spec.unitPath, content)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(spec.unitPath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(spec.unitPath), err)
	}
	if err := os.WriteFile(spec.unitPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", spec.unitPath, err)
	}
	fmt.Fprintf(w, "Wrote %s\n", spec.unitPath)

	pid, err := serviceStopRunningDaemon(projectRoot)
	if err != nil {
		return err
	}
	if pid != 0 {
		fmt.Fprintf(w, "Stopped running daemon (PID %d) so the service manager owns it\n", pid)
	}

	if err := spec.load(); err != nil {
		return err
	}

	if spec.goos == "darwin" {
		fmt.Fprintf(w, "Loaded launchd agent %s\n", spec.name)
	} else {
		fmt.Fprintf(w, "Enabled and started systemd user unit %s\n", spec.name)
		fmt.Fprintln(w, "To start it at boot without a login session, run: loginctl enable-linger $USER")
	}
	fmt.Fprintf(w, "Binary: %s\n", spec.binary)
	return nil
}

func runServiceUninstall(w io.Writer, projectRoot string) error {
	spec, err := serviceSpecFor(projectRoot, "")
	if err != nil {
		return err
	}
	installed := serviceFileExists(spec.unitPath)

	if err := spec.unload(); err != nil {
		return err
	}

	if !installed {
		fmt.Fprintf(w, "Service %s is not installed\n", spec.name)
		return nil
	}

	if err := os.Remove(spec.unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", spec.unitPath, err)
	}
	if spec.goos == "linux" {
		if out, err := serviceRunCommand("systemctl", "--user", "daemon-reload"); err != nil {
			return serviceCmdError("systemctl --user daemon-reload", out, err)
		}
	}
	fmt.Fprintf(w, "Removed service %s (%s)\n", spec.name, spec.unitPath)
	return nil
}

func runServiceStatus(w io.Writer, projectRoot string) error {
	spec, err := serviceSpecFor(projectRoot, "")
	if err != nil {
		return err
	}
	installed := "no"
	if serviceFileExists(spec.unitPath) {
		installed = "yes"
	}
	_, state := spec.active()

	fmt.Fprintf(w, "Installed: %s\n", installed)
	fmt.Fprintf(w, "Name:      %s\n", spec.name)
	fmt.Fprintf(w, "Unit path: %s\n", spec.unitPath)
	fmt.Fprintf(w, "State:     %s\n", state)
	return nil
}
