package git

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/davidparkercodes/belay/internal/ignore"
	"github.com/davidparkercodes/belay/internal/secrets"
)

// AllowFileName is the per-project allowlist for the projection secret guard.
const AllowFileName = ".belayallow"

const guardMaxScanBytes = 8 << 20

// HeldBackFile is a changed file left out of a projection because its net content
// looks like it contains a credential. It never carries the secret itself.
type HeldBackFile struct {
	Path  string   `json:"path"`
	Rules []string `json:"rules"`
}

// SecretGuard holds back files with likely credentials while a projection tree is
// built. It fails open: any error or panic leaves the file projected as before.
type SecretGuard struct {
	allow    *ignore.Matcher
	scan     func([]byte) []secrets.Finding
	maxBytes int64
	Warnings []string
}

// LoadSecretGuard builds a guard for projectRoot from its .belayallow file:
// gitignore-style lines name paths that are never held back, and rule:<name>
// lines switch a detector rule off. Unknown rule names become warnings.
func LoadSecretGuard(projectRoot string) *SecretGuard {
	g := &SecretGuard{maxBytes: guardMaxScanBytes}
	var disabled, paths []string
	data, err := os.ReadFile(filepath.Join(projectRoot, AllowFileName))
	if err != nil && !os.IsNotExist(err) {
		g.Warnings = append(g.Warnings, fmt.Sprintf("read %s: %v", AllowFileName, err))
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "rule:") {
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "rule:"))
			if secrets.IsRule(name) {
				disabled = append(disabled, name)
			} else {
				g.Warnings = append(g.Warnings, fmt.Sprintf("%s: unknown rule %q ignored", AllowFileName, name))
			}
			continue
		}
		paths = append(paths, line)
	}
	g.allow = ignore.NewMatcherFromLines(paths)
	g.scan = secrets.NewScanner(secrets.Options{DisabledRules: disabled}).Scan
	return g
}

type guardCandidate struct {
	oldMode, oldSHA, newSHA, path string
	size                          int64
}

// apply scans every regular file whose staged blob in the throwaway index (env)
// differs from refTree, and resets flagged ones to their refTree entry: a modified
// file goes back to its previously projected version, a new file is dropped.
// Deletions are never touched.
func (g *SecretGuard) apply(projectRoot string, env []string, refTree string) (held []HeldBackFile, warnings []string) {
	defer func() {
		if r := recover(); r != nil {
			held = nil
			warnings = append(warnings, fmt.Sprintf("secret guard skipped after panic: %v", r))
		}
	}()
	fail := func(err error) ([]HeldBackFile, []string) {
		return nil, append(warnings, "secret guard skipped: "+err.Error())
	}

	if refTree == "" {
		t, err := emptyTree(projectRoot, env)
		if err != nil {
			return fail(err)
		}
		refTree = t
	}

	out, err := gitOutput(projectRoot, env, nil, "diff-index", "--cached", "-r", "-z", "--no-renames", refTree)
	if err != nil {
		return fail(err)
	}
	var cands []*guardCandidate
	fields := strings.Split(string(out), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		meta := strings.Fields(strings.TrimPrefix(fields[i], ":"))
		path := fields[i+1]
		if len(meta) < 5 {
			continue
		}
		status, newMode := meta[4], meta[1]
		if status != "A" && status != "M" && status != "T" {
			continue
		}
		if newMode != "100644" && newMode != "100755" {
			continue
		}
		if g.allow != nil && g.allow.ShouldIgnore(path) {
			continue
		}
		cands = append(cands, &guardCandidate{oldMode: meta[0], oldSHA: meta[2], newSHA: meta[3], path: path})
	}
	if len(cands) == 0 {
		return nil, warnings
	}

	if err := g.fillSizes(projectRoot, env, cands); err != nil {
		return fail(err)
	}
	var scannable []*guardCandidate
	for _, c := range cands {
		if c.size >= 0 && c.size <= g.maxBytes {
			scannable = append(scannable, c)
		}
	}
	contents, err := readBlobs(projectRoot, env, scannable)
	if err != nil {
		return fail(err)
	}

	var reset bytes.Buffer
	for i, c := range scannable {
		data := contents[i]
		if secrets.IsBinary(data) {
			continue
		}
		rules, err := g.scanSafe(data)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("secret guard: %s projected unscanned: %v", c.path, err))
			continue
		}
		if len(rules) == 0 {
			continue
		}
		mode := c.oldMode
		if strings.Trim(c.oldSHA, "0") == "" {
			mode = "0"
		}
		fmt.Fprintf(&reset, "%s %s\t%s\x00", mode, c.oldSHA, c.path)
		held = append(held, HeldBackFile{Path: c.path, Rules: rules})
	}
	if len(held) == 0 {
		return nil, warnings
	}
	if _, err := gitOutput(projectRoot, env, &reset, "update-index", "-z", "--index-info"); err != nil {
		return fail(err)
	}
	return held, warnings
}

// preview reports which of the given net file contents apply would hold back. It is
// read-only: no index, object database or ref is touched.
func (g *SecretGuard) preview(paths []string, content func(path string) ([]byte, error)) (held []HeldBackFile, warnings []string) {
	defer func() {
		if r := recover(); r != nil {
			held = nil
			warnings = append(warnings, fmt.Sprintf("secret guard skipped after panic: %v", r))
		}
	}()
	for _, path := range paths {
		if g.allow != nil && g.allow.ShouldIgnore(path) {
			continue
		}
		data, err := content(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("secret guard: %s not scanned: %v", path, err))
			continue
		}
		if int64(len(data)) > g.maxBytes || secrets.IsBinary(data) {
			continue
		}
		rules, err := g.scanSafe(data)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("secret guard: %s projected unscanned: %v", path, err))
			continue
		}
		if len(rules) > 0 {
			held = append(held, HeldBackFile{Path: path, Rules: rules})
		}
	}
	return held, warnings
}

func (g *SecretGuard) scanSafe(data []byte) (rules []string, err error) {
	defer func() {
		if r := recover(); r != nil {
			rules, err = nil, fmt.Errorf("detector panic: %v", r)
		}
	}()
	seen := map[string]bool{}
	for _, f := range g.scan(data) {
		if !seen[f.Rule] {
			seen[f.Rule] = true
			rules = append(rules, f.Rule)
		}
	}
	sort.Strings(rules)
	return rules, nil
}

func (g *SecretGuard) fillSizes(projectRoot string, env []string, cands []*guardCandidate) error {
	var in bytes.Buffer
	for _, c := range cands {
		in.WriteString(c.newSHA + "\n")
	}
	out, err := gitOutput(projectRoot, env, &in, "cat-file", "--batch-check=%(objectsize)")
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != len(cands) {
		return fmt.Errorf("cat-file --batch-check: got %d lines for %d objects", len(lines), len(cands))
	}
	for i, line := range lines {
		n, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
		if err != nil {
			n = -1
		}
		cands[i].size = n
	}
	return nil
}

func readBlobs(projectRoot string, env []string, cands []*guardCandidate) ([][]byte, error) {
	if len(cands) == 0 {
		return nil, nil
	}
	var in bytes.Buffer
	for _, c := range cands {
		in.WriteString(c.newSHA + "\n")
	}
	out, err := gitOutput(projectRoot, env, &in, "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	r := bufio.NewReader(bytes.NewReader(out))
	blobs := make([][]byte, 0, len(cands))
	for range cands {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("cat-file --batch header: %w", err)
		}
		parts := strings.Fields(header)
		if len(parts) != 3 {
			return nil, fmt.Errorf("cat-file --batch: unexpected header %q", strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(parts[2])
		if err != nil {
			return nil, fmt.Errorf("cat-file --batch: bad size %q", parts[2])
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, fmt.Errorf("cat-file --batch body: %w", err)
		}
		if _, err := r.ReadByte(); err != nil {
			return nil, fmt.Errorf("cat-file --batch trailer: %w", err)
		}
		blobs = append(blobs, data)
	}
	return blobs, nil
}

func emptyTree(projectRoot string, env []string) (string, error) {
	out, err := gitOutput(projectRoot, env, &bytes.Buffer{}, "mktree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func treeOf(projectRoot, commit string) string {
	if commit == "" {
		return ""
	}
	t, _ := gitCmd(projectRoot, "rev-parse", "--verify", "--quiet", commit+"^{tree}")
	return t
}

func gitOutput(dir string, env []string, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(stderr.String()), err)
	}
	return stdout.Bytes(), nil
}

func mergeHeldBack(lists ...[]HeldBackFile) []HeldBackFile {
	byPath := map[string]map[string]bool{}
	var order []string
	for _, list := range lists {
		for _, h := range list {
			if byPath[h.Path] == nil {
				byPath[h.Path] = map[string]bool{}
				order = append(order, h.Path)
			}
			for _, r := range h.Rules {
				byPath[h.Path][r] = true
			}
		}
	}
	sort.Strings(order)
	out := make([]HeldBackFile, 0, len(order))
	for _, p := range order {
		var rules []string
		for r := range byPath[p] {
			rules = append(rules, r)
		}
		sort.Strings(rules)
		out = append(out, HeldBackFile{Path: p, Rules: rules})
	}
	return out
}

// HeldBackNotice renders the one-line stderr notice for held-back files, or "".
func HeldBackNotice(held []HeldBackFile) string {
	if len(held) == 0 {
		return ""
	}
	paths := make([]string, len(held))
	for i, h := range held {
		paths[i] = h.Path
	}
	return fmt.Sprintf("belay: held back %d file(s) with likely credentials: %s (allow with %s)",
		len(held), strings.Join(paths, ", "), AllowFileName)
}
