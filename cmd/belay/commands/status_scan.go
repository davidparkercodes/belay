package commands

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/apiclient"
	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/daemon"
	"github.com/davidparkercodes/belay/internal/spool"
)

const scanMaxDepth = 4

type projectHealth struct {
	ProjectRoot   string `json:"project_root"`
	DaemonRunning bool   `json:"daemon_running"`
	DaemonPID     int    `json:"daemon_pid,omitempty"`
	APIOK         bool   `json:"api_ok"`
	APIPort       int    `json:"api_port"`
	APIError      string `json:"api_error,omitempty"`
	Spooled       int    `json:"spooled_events"`
	SpoolBad      int    `json:"spool_set_aside,omitempty"`
}

func (h projectHealth) Healthy() bool {
	return h.DaemonRunning && h.APIOK
}

func checkProject(cfg *config.Config) projectHealth {
	h := projectHealth{ProjectRoot: cfg.ProjectRoot}
	h.DaemonRunning, h.DaemonPID = daemon.IsRunning(cfg)
	if pending, err := spool.Pending(cfg.SpoolDir()); err == nil {
		h.Spooled = len(pending)
	}
	if bad, err := spool.Bad(cfg.SpoolDir()); err == nil {
		h.SpoolBad = len(bad)
	}

	client := apiclient.New(cfg, time.Second)
	h.APIPort = client.Port()
	if !h.DaemonRunning {
		h.APIError = "daemon not running"
		return h
	}
	var health map[string]interface{}
	if err := client.Get("/api/health", &health); err != nil {
		h.APIError = err.Error()
		return h
	}
	h.APIOK = true
	return h
}

func findProjects(dirs []string) []string {
	seen := make(map[string]bool)
	var roots []string
	for _, base := range dirs {
		base = filepath.Clean(base)
		baseDepth := strings.Count(base, string(filepath.Separator))
		_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			name := d.Name()
			if path != base && (name == "node_modules" || name == ".git" || name == "vendor" || name == "target") {
				return filepath.SkipDir
			}
			if name == config.BelayDir {
				root := filepath.Dir(path)
				if !seen[root] {
					seen[root] = true
					roots = append(roots, root)
				}
				return filepath.SkipDir
			}
			if strings.Count(path, string(filepath.Separator))-baseDepth >= scanMaxDepth {
				return filepath.SkipDir
			}
			return nil
		})
	}
	return roots
}

func runStatusScan(dirs []string, jsonOutput bool) error {
	var results []projectHealth
	unhealthy := 0
	for _, root := range findProjects(dirs) {
		cfg, err := config.Load(root)
		if err != nil {
			results = append(results, projectHealth{ProjectRoot: root, APIError: "load config: " + err.Error()})
			unhealthy++
			continue
		}
		h := checkProject(cfg)
		if !h.Healthy() {
			unhealthy++
		}
		results = append(results, h)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			return err
		}
	} else {
		if len(results) == 0 {
			fmt.Println("No Belay projects found")
		}
		for _, h := range results {
			state := "\033[32mok\033[0m      "
			detail := fmt.Sprintf("PID %d, API :%d", h.DaemonPID, h.APIPort)
			switch {
			case !h.DaemonRunning:
				state = "\033[31mstopped\033[0m "
				detail = "run 'belay daemon start' or 'belay service install'"
			case !h.APIOK:
				state = "\033[33mno-api\033[0m  "
				detail = fmt.Sprintf("PID %d, API :%d: %s", h.DaemonPID, h.APIPort, h.APIError)
			}
			if h.Spooled > 0 {
				detail += fmt.Sprintf(", %d queued", h.Spooled)
			}
			fmt.Printf("%s %s  (%s)\n", state, h.ProjectRoot, detail)
		}
	}

	if unhealthy > 0 {
		cmdExit(1)
	}
	return nil
}

var cmdExit = os.Exit
