package githooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	markerStart = "# BELAY_HOOK_START"
	markerEnd   = "# BELAY_HOOK_END"
	belayPort   = "33412"
)

var supportedHooks = []string{"post-checkout", "post-merge", "post-rewrite"}

func postCheckoutScript() string {
	return fmt.Sprintf(`%s
(
  [ -d ".belay" ] || exit 0
  curl -sf --max-time 1 "http://localhost:%s/health" >/dev/null 2>&1 || exit 0

  prev_head="$1"
  new_head="$2"
  branch_flag="$3"
  branch=$(git symbolic-ref --short HEAD 2>/dev/null || echo "detached")
  files_changed=0
  if [ "$prev_head" != "$new_head" ]; then
    files_changed=$(git diff --stat "$prev_head" "$new_head" 2>/dev/null | tail -1 | grep -oE '[0-9]+' | head -1)
    [ -z "$files_changed" ] && files_changed=0
  fi

  curl -sf --max-time 1 -X POST "http://localhost:%s/api/events/git" \
    -H "Content-Type: application/json" \
    -d "{\"operation\":\"checkout\",\"ref_from\":\"$prev_head\",\"ref_to\":\"$new_head\",\"branch\":\"$branch\",\"files_changed\":${files_changed}}" \
    >/dev/null 2>&1
) &
%s`, markerStart, belayPort, belayPort, markerEnd)
}

func postMergeScript() string {
	return fmt.Sprintf(`%s
(
  [ -d ".belay" ] || exit 0
  curl -sf --max-time 1 "http://localhost:%s/health" >/dev/null 2>&1 || exit 0

  squash_flag="$1"
  prev_head=$(git rev-parse ORIG_HEAD 2>/dev/null || echo "unknown")
  new_head=$(git rev-parse HEAD 2>/dev/null || echo "unknown")
  branch=$(git symbolic-ref --short HEAD 2>/dev/null || echo "detached")
  files_changed=0
  if [ "$prev_head" != "unknown" ] && [ "$new_head" != "unknown" ]; then
    files_changed=$(git diff --stat "$prev_head" "$new_head" 2>/dev/null | tail -1 | grep -oE '[0-9]+' | head -1)
    [ -z "$files_changed" ] && files_changed=0
  fi

  curl -sf --max-time 1 -X POST "http://localhost:%s/api/events/git" \
    -H "Content-Type: application/json" \
    -d "{\"operation\":\"merge\",\"ref_from\":\"$prev_head\",\"ref_to\":\"$new_head\",\"branch\":\"$branch\",\"files_changed\":${files_changed}}" \
    >/dev/null 2>&1
) &
%s`, markerStart, belayPort, belayPort, markerEnd)
}

func postRewriteScript() string {
	return fmt.Sprintf(`%s
(
  [ -d ".belay" ] || exit 0
  curl -sf --max-time 1 "http://localhost:%s/health" >/dev/null 2>&1 || exit 0

  command_name="$1"
  prev_head=$(git rev-parse ORIG_HEAD 2>/dev/null || echo "unknown")
  new_head=$(git rev-parse HEAD 2>/dev/null || echo "unknown")
  branch=$(git symbolic-ref --short HEAD 2>/dev/null || echo "detached")
  files_changed=0
  if [ "$prev_head" != "unknown" ] && [ "$new_head" != "unknown" ]; then
    files_changed=$(git diff --stat "$prev_head" "$new_head" 2>/dev/null | tail -1 | grep -oE '[0-9]+' | head -1)
    [ -z "$files_changed" ] && files_changed=0
  fi

  curl -sf --max-time 1 -X POST "http://localhost:%s/api/events/git" \
    -H "Content-Type: application/json" \
    -d "{\"operation\":\"rewrite\",\"ref_from\":\"$prev_head\",\"ref_to\":\"$new_head\",\"branch\":\"$branch\",\"files_changed\":${files_changed}}" \
    >/dev/null 2>&1
) &
%s`, markerStart, belayPort, belayPort, markerEnd)
}

func hookScript(name string) string {
	switch name {
	case "post-checkout":
		return postCheckoutScript()
	case "post-merge":
		return postMergeScript()
	case "post-rewrite":
		return postRewriteScript()
	default:
		return ""
	}
}

func findGitDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		gitDir := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
			return gitDir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("not a git repository (or any parent)")
}

func hooksDir() (string, error) {
	gitDir, err := findGitDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(gitDir, "hooks"), nil
}

func Install() ([]string, error) {
	dir, err := hooksDir()
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create hooks directory: %w", err)
	}

	var installed []string
	for _, name := range supportedHooks {
		hookPath := filepath.Join(dir, name)
		script := hookScript(name)
		if script == "" {
			continue
		}

		existing, err := os.ReadFile(hookPath)
		if err != nil && !os.IsNotExist(err) {
			return installed, fmt.Errorf("read %s: %w", name, err)
		}

		var newContent string
		if os.IsNotExist(err) || len(existing) == 0 {
			newContent = "#!/bin/sh\n" + script + "\n"
		} else {
			content := string(existing)
			content = removeBelaySection(content)
			if !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			newContent = content + script + "\n"
		}

		if err := os.WriteFile(hookPath, []byte(newContent), 0755); err != nil {
			return installed, fmt.Errorf("write %s: %w", name, err)
		}

		installed = append(installed, name)
	}

	return installed, nil
}

func Remove() ([]string, error) {
	dir, err := hooksDir()
	if err != nil {
		return nil, err
	}

	var removed []string
	for _, name := range supportedHooks {
		hookPath := filepath.Join(dir, name)
		content, err := os.ReadFile(hookPath)
		if err != nil {
			continue
		}

		original := string(content)
		cleaned := removeBelaySection(original)

		if cleaned == original {
			continue
		}

		trimmed := strings.TrimSpace(cleaned)
		if trimmed == "" || trimmed == "#!/bin/sh" || trimmed == "#!/bin/bash" {
			if err := os.Remove(hookPath); err != nil && !os.IsNotExist(err) {
				return removed, fmt.Errorf("remove %s: %w", name, err)
			}
		} else {
			if err := os.WriteFile(hookPath, []byte(cleaned), 0755); err != nil {
				return removed, fmt.Errorf("write %s: %w", name, err)
			}
		}

		removed = append(removed, name)
	}

	return removed, nil
}

type HookStatus struct {
	Name      string
	Installed bool
}

func Status() ([]HookStatus, error) {
	dir, err := hooksDir()
	if err != nil {
		return nil, err
	}

	var statuses []HookStatus
	for _, name := range supportedHooks {
		hookPath := filepath.Join(dir, name)
		content, err := os.ReadFile(hookPath)
		installed := false
		if err == nil {
			installed = strings.Contains(string(content), markerStart)
		}
		statuses = append(statuses, HookStatus{Name: name, Installed: installed})
	}

	return statuses, nil
}

func removeBelaySection(content string) string {
	for {
		startIdx := strings.Index(content, markerStart)
		if startIdx == -1 {
			break
		}
		endIdx := strings.Index(content[startIdx:], markerEnd)
		if endIdx == -1 {
			content = content[:startIdx]
			break
		}
		endIdx += startIdx + len(markerEnd)
		if endIdx < len(content) && content[endIdx] == '\n' {
			endIdx++
		}
		content = content[:startIdx] + content[endIdx:]
	}
	return content
}
