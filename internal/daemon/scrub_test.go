package daemon

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/scrub"
	"github.com/davidparkercodes/belay/internal/store"
)

func TestScrubTick_AutoModeRedactsIdleLogs(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultConfig(root)
	cfg.Scrub.Mode = config.ScrubAuto
	if err := os.MkdirAll(cfg.BelayPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ConfigPath(), []byte(cfg.ToTOML()), 0o644); err != nil {
		t.Fatal(err)
	}
	objStore, err := store.OpenConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := index.Open(cfg.IndexPath())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	logDir := filepath.Join(t.TempDir(), "proj")
	os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "s.jsonl")
	tok := "gh" + "p_" + strings.Repeat("Mn4Bv5Cx6Z", 3) + "a1S2d3"
	os.WriteFile(logPath, []byte(`{"content":"`+tok+`"}`+"\n"), 0o600)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(logPath, old, old)

	prev := scrubSources
	scrubSources = func(string) []scrub.Source { return []scrub.Source{fixedSource{logPath}} }
	defer func() { scrubSources = prev }()

	d := &Daemon{cfg: cfg, objStore: objStore, idx: idx, logger: log.New(io.Discard, "", 0)}
	d.scrubTick()

	got, _ := os.ReadFile(logPath)
	if strings.Contains(string(got), tok) || !strings.Contains(string(got), "[redacted:github-token]") {
		t.Fatalf("log after tick = %s", got)
	}
}

type fixedSource struct{ path string }

func (f fixedSource) Agent() string { return "test" }
func (f fixedSource) ProjectLogs() ([]scrub.Log, error) {
	return []scrub.Log{{Path: f.path, Agent: "test", SessionID: "s"}}, nil
}
func (f fixedSource) AllLogs() ([]scrub.Log, error) { return f.ProjectLogs() }
