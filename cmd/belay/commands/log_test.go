package commands

import (
	"strings"
	"testing"
	"time"
)

func TestLogCmd_BasicProperties(t *testing.T) {
	cmd := newLogCmd()

	if cmd.Use != "log" {
		t.Errorf("Use = %q, want %q", cmd.Use, "log")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
}

func TestLogCmd_Flags(t *testing.T) {
	cmd := newLogCmd()

	tests := []struct {
		name     string
		defValue string
	}{
		{"session", ""},
		{"file", ""},
		{"since", ""},
		{"until", ""},
		{"op", ""},
		{"limit", "50"},
		{"json", "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := cmd.Flags().Lookup(tt.name)
			if f == nil {
				t.Fatalf("flag --%s not registered", tt.name)
			}
			if f.DefValue != tt.defValue {
				t.Errorf("--%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
			}
		})
	}
}

// --- parseRelativeTime tests ---

func TestParseRelativeTime_RelativeDurations(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantDur  time.Duration
		wantErr  bool
	}{
		{"seconds", "30s", 30 * time.Second, false},
		{"minutes", "5m", 5 * time.Minute, false},
		{"hours", "2h", 2 * time.Hour, false},
		{"days", "1d", 24 * time.Hour, false},
		{"weeks", "1w", 7 * 24 * time.Hour, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()
			result, err := parseRelativeTime(tt.input)
			after := time.Now()

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// The result should be approximately (now - duration)
			expectedMin := before.Add(-tt.wantDur)
			expectedMax := after.Add(-tt.wantDur)

			if result.Before(expectedMax.Add(-time.Second)) || result.After(expectedMin.Add(time.Second)) {
				t.Errorf("parseRelativeTime(%q) = %v, expected around %v",
					tt.input, result, before.Add(-tt.wantDur))
			}
		})
	}
}

func TestParseRelativeTime_AbsoluteFormats(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string // expected date portion
	}{
		{"RFC3339", "2024-01-15T10:30:00Z", "2024-01-15"},
		{"datetime no TZ", "2024-01-15T10:30:00", "2024-01-15"},
		{"datetime space", "2024-01-15 10:30:00", "2024-01-15"},
		{"date only", "2024-01-15", "2024-01-15"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseRelativeTime(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(result.Format("2006-01-02"), tt.want) {
				t.Errorf("parseRelativeTime(%q) = %v, want date %s",
					tt.input, result, tt.want)
			}
		})
	}
}

func TestParseRelativeTime_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"single char", "x"},
		{"no number", "h"},
		{"unknown unit", "5x"},
		{"garbage", "not-a-time"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseRelativeTime(tt.input)
			if err == nil {
				t.Errorf("parseRelativeTime(%q) should return error", tt.input)
			}
		})
	}
}

func TestParseRelativeTime_WhitespaceHandling(t *testing.T) {
	// Leading/trailing whitespace should be trimmed
	_, err := parseRelativeTime("  1h  ")
	if err != nil {
		t.Errorf("parseRelativeTime with whitespace should work: %v", err)
	}
}

// --- sessionColor tests ---

func TestSessionColor_Deterministic(t *testing.T) {
	// Same session ID should always produce the same color
	color1 := sessionColor("session-123")
	color2 := sessionColor("session-123")
	if color1 != color2 {
		t.Errorf("sessionColor should be deterministic: %q != %q", color1, color2)
	}
}

func TestSessionColor_DifferentInputs(t *testing.T) {
	// Different session IDs should have a reasonable chance of different colors
	// (not guaranteed, but let's test with enough variety)
	colors := make(map[string]bool)
	inputs := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for _, input := range inputs {
		colors[sessionColor(input)] = true
	}
	// We expect at least 2 different colors from 8 inputs
	if len(colors) < 2 {
		t.Error("sessionColor should produce at least 2 different colors for 8 different inputs")
	}
}

func TestSessionColor_ValidANSI(t *testing.T) {
	validColors := map[string]bool{
		"91": true, "92": true, "93": true, "94": true,
		"95": true, "96": true, "33": true, "36": true,
	}
	color := sessionColor("test-session")
	if !validColors[color] {
		t.Errorf("sessionColor returned %q, which is not a known ANSI color code", color)
	}
}

// --- colorOp tests ---

func TestColorOp(t *testing.T) {
	tests := []struct {
		input    string
		contains string
	}{
		{"CREATE", "CREATE"},
		{"MODIFY", "MODIFY"},
		{"DELETE", "DELETE"},
		{"RENAME", "RENAME"},
		{"UNKNOWN", "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := colorOp(tt.input)
			if !strings.Contains(result, tt.contains) {
				t.Errorf("colorOp(%q) = %q, should contain %q", tt.input, result, tt.contains)
			}
		})
	}
}

func TestColorOp_ANSICodes(t *testing.T) {
	// Known operations should have ANSI color codes
	knownOps := []string{"CREATE", "MODIFY", "DELETE", "RENAME"}
	for _, op := range knownOps {
		result := colorOp(op)
		if !strings.Contains(result, "\033[") {
			t.Errorf("colorOp(%q) should contain ANSI escape code", op)
		}
	}

	// Unknown operations should be returned as-is
	result := colorOp("UNKNOWN_OP")
	if strings.Contains(result, "\033[") {
		t.Error("colorOp for unknown ops should not contain ANSI codes")
	}
}
