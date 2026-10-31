package scrub

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/davidparkercodes/belay/internal/secrets"
)

var redactedMarker = []byte("[redacted:")

// ErrBudget is returned when a time budget runs out before a log is fully scanned.
var ErrBudget = errors.New("scan time budget exhausted")

// Counts maps a detector rule to the number of findings for it.
type Counts map[string]int

func (c Counts) Total() int {
	n := 0
	for _, v := range c {
		n += v
	}
	return n
}

func (c Counts) add(o Counts) {
	for k, v := range o {
		c[k] += v
	}
}

// RedactLog returns data with every finding replaced by "[redacted:<rule>]". Lines that
// parse as JSON are redacted inside their decoded string values and only those
// string literals are re-encoded, so every other byte of the line is kept and the
// line stays valid JSON. Other lines are redacted as plain text. A zero deadline
// means no time budget.
func RedactLog(data []byte, sc *secrets.Scanner, deadline time.Time) ([]byte, Counts, error) {
	counts := Counts{}
	out := make([]byte, 0, len(data))
	for i, start := 0, 0; start < len(data); i++ {
		if i%64 == 0 && !deadline.IsZero() && time.Now().After(deadline) {
			return nil, nil, ErrBudget
		}
		end := bytes.IndexByte(data[start:], '\n')
		var line, term []byte
		if end < 0 {
			line = data[start:]
			start = len(data)
		} else {
			line = data[start : start+end]
			term = data[start+end : start+end+1]
			start += end + 1
		}
		if n := len(line); n > 0 && line[n-1] == '\r' {
			term = append([]byte{'\r'}, term...)
			line = line[:n-1]
		}
		red, c := redactLine(line, sc)
		counts.add(c)
		out = append(out, red...)
		out = append(out, term...)
	}
	return out, counts, nil
}

func redactLine(line []byte, sc *secrets.Scanner) ([]byte, Counts) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid(line) {
		if out, c, ok := redactJSONLine(line, sc); ok {
			return out, c
		}
	}
	fs := usable(line, sc.Scan(line))
	if len(fs) == 0 {
		return line, nil
	}
	return secrets.Redact(line, fs), countFindings(fs)
}

func redactJSONLine(line []byte, sc *secrets.Scanner) ([]byte, Counts, bool) {
	var out []byte
	counts := Counts{}
	last := 0
	key := ""
	for i := 0; i < len(line); i++ {
		if line[i] != '"' {
			continue
		}
		j := i + 1
		for j < len(line) && line[j] != '"' {
			if line[j] == '\\' {
				j++
			}
			j++
		}
		if j >= len(line) {
			return nil, nil, false
		}
		lit := line[i : j+1]
		k := j + 1
		for k < len(line) && (line[k] == ' ' || line[k] == '\t') {
			k++
		}
		isKey := k < len(line) && line[k] == ':'
		var val string
		if err := json.Unmarshal(lit, &val); err != nil {
			return nil, nil, false
		}
		if isKey {
			key = val
			i = j
			continue
		}
		red, c := redactValue(key, val, sc)
		if c != nil {
			enc, err := encodeString(red)
			if err != nil {
				return nil, nil, false
			}
			out = append(out, line[last:i]...)
			out = append(out, enc...)
			last = j + 1
			counts.add(c)
		}
		i = j
	}
	if out == nil {
		return line, nil, true
	}
	out = append(out, line[last:]...)
	if !json.Valid(out) {
		return nil, nil, false
	}
	return out, counts, true
}

func redactValue(key, val string, sc *secrets.Scanner) (string, Counts) {
	data := []byte(val)
	fs := usable(data, sc.Scan(data))
	if key != "" && len(key) < 64 && len(val) < 512 && bytes.IndexByte(data, '\n') < 0 {
		prefix := key + ": "
		syn := []byte(prefix + val)
		for _, f := range usable(syn, sc.Scan(syn)) {
			if f.Start < len(prefix) {
				continue
			}
			f.Start -= len(prefix)
			f.End -= len(prefix)
			fs = append(fs, f)
		}
		fs = dedupe(fs)
	}
	if len(fs) == 0 {
		return val, nil
	}
	return string(secrets.Redact(data, fs)), countFindings(fs)
}

func encodeString(s string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func usable(data []byte, fs []secrets.Finding) []secrets.Finding {
	out := fs[:0:0]
	for _, f := range fs {
		if f.Start < 0 || f.End > len(data) || f.Start >= f.End {
			continue
		}
		if bytes.Contains(data[f.Start:f.End], redactedMarker) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func dedupe(fs []secrets.Finding) []secrets.Finding {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Start != fs[j].Start {
			return fs[i].Start < fs[j].Start
		}
		return fs[i].End > fs[j].End
	})
	out := fs[:0]
	end := -1
	for _, f := range fs {
		if f.Start < end {
			continue
		}
		out = append(out, f)
		end = f.End
	}
	return out
}

func countFindings(fs []secrets.Finding) Counts {
	c := Counts{}
	for _, f := range fs {
		c[f.Rule]++
	}
	return c
}
