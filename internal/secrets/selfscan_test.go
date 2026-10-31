package secrets

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type hit struct {
	path   string
	f      Finding
	text   string
	before string
}

func scanTree(t *testing.T, root string, skip func(rel string) bool) ([]hit, int, int64) {
	var hits []hit
	files := 0
	var total int64
	var scanTime time.Duration
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			name := d.Name()
			if rel != "." && (name == ".git" || name == "node_modules" || name == ".belay" || name == "vendor" || name == "bin" || skip(rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || skip(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 32<<20 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		files++
		total += int64(len(data))
		t0 := time.Now()
		found := Scan(data)
		scanTime += time.Since(t0)
		for _, f := range found {
			lo := max(f.Start-30, 0)
			hits = append(hits, hit{rel, f, string(data[f.Start:f.End]), strings.ReplaceAll(string(data[lo:f.Start]), "\n", " ")})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanTime > 0 {
		t.Logf("scan time %v, %.1f MB/s", scanTime, float64(total)/1e6/scanTime.Seconds())
	}
	return hits, files, total
}

func TestSelfScanRepo(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skip("repo root not found")
	}
	own := filepath.Join("internal", "secrets")
	hits, files, total := scanTree(t, root, func(rel string) bool {
		return rel == own || strings.HasPrefix(rel, own+string(filepath.Separator))
	})
	t.Logf("scanned %d files, %d bytes", files, total)
	for _, h := range hits {
		t.Errorf("%s:%d %s %q", h.path, h.f.Line, h.f.Rule, h.text)
	}
}

func TestScanDirFromEnv(t *testing.T) {
	root := os.Getenv("SECRETS_SCAN_DIR")
	if root == "" {
		t.Skip("set SECRETS_SCAN_DIR to scan a directory and log findings")
	}
	hits, files, total := scanTree(t, root, func(string) bool { return false })
	t.Logf("scanned %d files, %d bytes, %d findings", files, total, len(hits))
	mask := os.Getenv("SECRETS_SCAN_MASK") != ""
	for _, h := range hits {
		text := h.text
		if mask {
			text = shape(text)
		}
		t.Logf("%s:%d %s %q => %q", h.path, h.f.Line, h.f.Rule, h.before, text)
	}
}

func shape(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c >= 'A' && c <= 'Z':
			b[i] = 'A'
		case c >= 'a' && c <= 'z':
			b[i] = 'a'
		case c >= '0' && c <= '9':
			b[i] = '9'
		}
	}
	return string(b)
}
