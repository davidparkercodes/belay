package chaos

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// ChaosReport is the top-level test output.
type ChaosReport struct {
	RunID        string           `json:"run_id"`
	Timestamp    string           `json:"timestamp"`
	BelayVersion string           `json:"belay_version"`
	Platform     string           `json:"platform"`
	GoVersion    string           `json:"go_version"`
	RaceDet      bool             `json:"race_detector"`
	Duration     string           `json:"total_duration"`
	DurationMs   int64            `json:"total_duration_ms"`
	Summary      ReportSummary    `json:"summary"`
	Scenarios    []ScenarioResult `json:"scenarios"`
}

// ReportSummary holds aggregate stats.
type ReportSummary struct {
	Total          int     `json:"total"`
	Passed         int     `json:"passed"`
	Failed         int     `json:"failed"`
	TotalEvents    int     `json:"total_events_processed"`
	TotalFiles     int     `json:"total_files_tested"`
	TotalRecovered int     `json:"total_recovered"`
	PassRate       float64 `json:"pass_rate"`
}

// ScenarioResult holds a single scenario's outcome.
type ScenarioResult struct {
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name"`
	Description string         `json:"description"`
	Status      string         `json:"status"` // "pass" or "fail"
	DurationMs  int64          `json:"duration_ms"`
	Duration    string         `json:"duration"`
	Metrics     map[string]int `json:"metrics"`
	Category    string         `json:"category"` // "stress", "recovery", "concurrency", "integrity"
	Error       string         `json:"error,omitempty"`
}

var (
	tracker   *scenarioTracker
	trackerMu sync.Mutex
)

type scenarioTracker struct {
	results []ScenarioResult
	start   time.Time
}

// StartRun initialises the tracker for a new test run.
func StartRun() {
	trackerMu.Lock()
	defer trackerMu.Unlock()
	tracker = &scenarioTracker{start: time.Now()}
}

// RecordScenario appends a completed scenario to the tracker.
func RecordScenario(name, displayName, description, category string, durationMs int64, metrics map[string]int, passed bool, errMsg string) {
	status := "pass"
	if !passed {
		status = "fail"
	}
	trackerMu.Lock()
	defer trackerMu.Unlock()
	if tracker == nil {
		tracker = &scenarioTracker{start: time.Now()}
	}
	tracker.results = append(tracker.results, ScenarioResult{
		Name:        name,
		DisplayName: displayName,
		Description: description,
		Status:      status,
		DurationMs:  durationMs,
		Duration:    fmt.Sprintf("%.2fs", float64(durationMs)/1000),
		Metrics:     metrics,
		Category:    category,
		Error:       errMsg,
	})
}

// GenerateReport builds the final report from collected results.
func GenerateReport() *ChaosReport {
	trackerMu.Lock()
	defer trackerMu.Unlock()
	if tracker == nil {
		tracker = &scenarioTracker{start: time.Now()}
	}

	totalDur := time.Since(tracker.start)

	var totalEvents, totalFiles, totalRecovered, passed, failed int
	for _, s := range tracker.results {
		if s.Status == "pass" {
			passed++
		} else {
			failed++
		}
		for k, v := range s.Metrics {
			switch k {
			case "events", "total_events":
				totalEvents += v
			case "files":
				totalFiles += v
			case "recovered":
				totalRecovered += v
			}
		}
		// writers * events_per_writer
		if epw, ok := s.Metrics["events_per_writer"]; ok {
			if w, ok2 := s.Metrics["writers"]; ok2 {
				totalEvents += epw * w
			}
		}
	}

	total := len(tracker.results)
	passRate := 0.0
	if total > 0 {
		passRate = float64(passed) / float64(total) * 100
	}

	now := time.Now().UTC()
	return &ChaosReport{
		RunID:        now.Format("20060102-150405"),
		Timestamp:    now.Format(time.RFC3339),
		BelayVersion: "dev",
		Platform:     fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		GoVersion:    runtime.Version(),
		DurationMs:   totalDur.Milliseconds(),
		Duration:     fmt.Sprintf("%.2fs", totalDur.Seconds()),
		Summary: ReportSummary{
			Total:          total,
			Passed:         passed,
			Failed:         failed,
			TotalEvents:    totalEvents,
			TotalFiles:     totalFiles,
			TotalRecovered: totalRecovered,
			PassRate:       passRate,
		},
		Scenarios: tracker.results,
	}
}

// WriteReport writes the report JSON to a directory, creating both a
// timestamped file and a latest.json symlink.
func WriteReport(report *ChaosReport, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}

	filename := fmt.Sprintf("chaos-results-%s.json", report.RunID)
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}

	latestPath := filepath.Join(dir, "latest.json")
	_ = os.WriteFile(latestPath, data, 0o644)

	return path, nil
}

// FindWebsiteResultsDir walks up from cwd looking for the belay-website public dir.
func FindWebsiteResultsDir() string {
	dir, _ := os.Getwd()
	for {
		candidate := filepath.Join(dir, "domains", "agentic-development", "belay-website", "public", "chaos-results")
		parent := filepath.Dir(candidate)
		if fi, err := os.Stat(filepath.Dir(parent)); err == nil && fi.IsDir() {
			return candidate
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	return ""
}
