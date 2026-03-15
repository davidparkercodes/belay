package ignore

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzShouldIgnore feeds arbitrary ignore-file content and file paths to the
// Matcher and ensures it never panics.
func FuzzShouldIgnore(f *testing.F) {
	// Seed corpus: representative patterns and paths.
	f.Add("*.log\n*.tmp\n", "app.log")
	f.Add("node_modules/\n.git/\n", "node_modules/package.json")
	f.Add("!important.log\n*.log\n", "important.log")
	f.Add("**/*.test.js\n", "src/deep/nested/app.test.js")
	f.Add("src/generated/**\n", "src/generated/types.go")
	f.Add("/build.sh\n", "build.sh")
	f.Add("# comment\n\n*.bak\n", "backup.bak")
	f.Add("vendor/\n!vendor/keep.go\n", "vendor/keep.go")
	f.Add("", "anything.go")
	f.Add("   \n\n  \n", "file.txt")
	f.Add("*.go\n!main.go\n*.go\n", "main.go")
	f.Add("[invalid-glob\n", "test.txt")
	f.Add("\\!escaped.log\n", "!escaped.log")
	f.Add("\x00\xff\xfe\n*.bin\n", "data.bin")

	f.Fuzz(func(t *testing.T, patterns string, filePath string) {
		dir := t.TempDir()
		ignorePath := filepath.Join(dir, ".belayignore")
		if err := os.WriteFile(ignorePath, []byte(patterns), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		m, err := NewMatcher(dir)
		if err != nil {
			// Some pattern files may cause scanner errors; that's OK.
			return
		}

		// Must not panic regardless of pattern content or file path.
		_ = m.ShouldIgnore(filePath)
	})
}

// FuzzParseLine feeds arbitrary strings to parseLine and ensures no panics.
func FuzzParseLine(f *testing.F) {
	f.Add("*.log")
	f.Add("!important.log")
	f.Add("vendor/")
	f.Add("/build.sh")
	f.Add("# comment")
	f.Add("")
	f.Add("   ")
	f.Add("!/keep.log")
	f.Add("!vendor/")
	f.Add("**/*.test.js")
	f.Add("src/**/generated/*.go")
	f.Add("[invalid")
	f.Add("\x00\xff")
	f.Add("!!/double-negation")

	f.Fuzz(func(t *testing.T, line string) {
		// Must not panic.
		_, _ = parseLine(line)
	})
}

// FuzzMatchGlob feeds arbitrary pattern and name strings to matchGlob.
func FuzzMatchGlob(f *testing.F) {
	f.Add("*.log", "app.log")
	f.Add("**/*.js", "src/app.js")
	f.Add("src/**", "src/deep/nested/file.go")
	f.Add("[abc]", "a")
	f.Add("?", "x")
	f.Add("", "")
	f.Add("*", "anything")
	f.Add("[invalid", "test")
	f.Add("\x00", "\x00")

	f.Fuzz(func(t *testing.T, pattern, name string) {
		// Must not panic.
		_ = matchGlob(pattern, name)
	})
}
