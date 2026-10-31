package testenv

import (
	"os"
	"path/filepath"
)

const gitConfig = `[user]
	name = Belay Test
	email = test@belay.dev
[init]
	defaultBranch = main
[commit]
	gpgsign = false
[tag]
	gpgsign = false
[core]
	autocrlf = false
`

var gitEnvToClear = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_COMMON_DIR",
	"GIT_NAMESPACE",
	"GIT_CEILING_DIRECTORIES",
	"GIT_AUTHOR_NAME",
	"GIT_AUTHOR_EMAIL",
	"GIT_AUTHOR_DATE",
	"GIT_COMMITTER_NAME",
	"GIT_COMMITTER_EMAIL",
	"GIT_COMMITTER_DATE",
	"GIT_CONFIG",
	"GIT_CONFIG_PARAMETERS",
	"GIT_CONFIG_COUNT",
}

// IsolateGit hides the user's global and system git config so tests do not depend on the machine.
func IsolateGit() (func(), error) {
	dir, err := os.MkdirTemp("", "belay-gitconfig-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(path, []byte(gitConfig), 0o644); err != nil {
		cleanup()
		return nil, err
	}
	for _, k := range gitEnvToClear {
		if err := os.Unsetenv(k); err != nil {
			cleanup()
			return nil, err
		}
	}
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL":   path,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_TERMINAL_PROMPT": "0",
	} {
		if err := os.Setenv(k, v); err != nil {
			cleanup()
			return nil, err
		}
	}
	return cleanup, nil
}
