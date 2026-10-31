package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrDevBuild = errors.New("development build")

type NotWritableError struct {
	Dir string
	Err error
}

func (e *NotWritableError) Error() string {
	return fmt.Sprintf("cannot write to %s (%v); rerun with permission to write there, or reinstall into a directory you own with the install script", e.Dir, e.Err)
}

func (e *NotWritableError) Unwrap() error { return e.Err }

type Result struct {
	Method  Method   `json:"method"`
	Path    string   `json:"path"`
	From    string   `json:"from"`
	To      string   `json:"to"`
	Command []string `json:"command,omitempty"`
	Ran     bool     `json:"ran"`
	Output  string   `json:"output,omitempty"`
}

var (
	RunCommand = func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		if env != nil {
			cmd.Env = env
		}
		return cmd.CombinedOutput()
	}
	LookPath = exec.LookPath
)

type Updater struct {
	Client  *Client
	GOOS    string
	GOARCH  string
	Environ func() []string
}

func BrewCommand() []string {
	return []string{"brew", "upgrade", "--cask", CaskName}
}

func GoInstallCommand(tag string) []string {
	return []string{"go", "install", ModulePath + "/cmd/belay@" + Tag(tag)}
}

func (u *Updater) Apply(ctx context.Context, inst Install, current, tag string, pinned bool) (Result, error) {
	tag = Tag(tag)
	res := Result{Method: inst.Method, Path: inst.Path, From: current, To: tag}
	switch inst.Method {
	case MethodDev:
		return res, fmt.Errorf("%w: %s. Update it from source (git pull, then go build), or install a release with Homebrew, go install or the install script", ErrDevBuild, inst.Reason)

	case MethodHomebrew:
		res.Command = BrewCommand()
		if pinned {
			return res, fmt.Errorf("this copy is managed by Homebrew, which installs the tap's current version; run %s, or pin a version with Homebrew itself", strings.Join(res.Command, " "))
		}
		brew := u.findBrew(inst)
		if brew == "" {
			return res, fmt.Errorf("this copy is managed by Homebrew but brew was not found; run %s", strings.Join(res.Command, " "))
		}
		out, err := RunCommand(ctx, nil, brew, res.Command[1:]...)
		res.Output = strings.TrimSpace(string(out))
		if err != nil {
			return res, fmt.Errorf("brew upgrade failed: %w", err)
		}
		res.Ran = true
		return res, nil

	case MethodGoInstall:
		res.Command = GoInstallCommand(tag)
		goBin, err := LookPath("go")
		if err != nil {
			return res, nil
		}
		env := append(u.environ(), "GOBIN="+filepath.Dir(inst.Path))
		out, err := RunCommand(ctx, env, goBin, res.Command[1:]...)
		res.Output = strings.TrimSpace(string(out))
		if err != nil {
			return res, fmt.Errorf("go install failed: %w", err)
		}
		res.Ran = true
		return res, nil
	}

	if err := checkWritable(filepath.Dir(inst.Path)); err != nil {
		return res, err
	}
	data, err := u.Client.DownloadBinary(ctx, tag, u.GOOS, u.GOARCH)
	if err != nil {
		return res, err
	}
	if err := ReplaceBinary(inst.Path, data, u.GOOS); err != nil {
		return res, err
	}
	res.Ran = true
	return res, nil
}

func (u *Updater) environ() []string {
	if u.Environ != nil {
		return u.Environ()
	}
	return os.Environ()
}

func (u *Updater) findBrew(inst Install) string {
	if p, err := LookPath("brew"); err == nil {
		return p
	}
	if inst.Prefix != "" {
		p := filepath.Join(inst.Prefix, "bin", "brew")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".belay-write-check-*")
	if err != nil {
		return &NotWritableError{Dir: dir, Err: err}
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

func PrevPath(target string) string {
	return target + ".prev"
}

func ReplaceBinary(target string, data []byte, goos string) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".belay-update-*")
	if err != nil {
		return &NotWritableError{Dir: dir, Err: err}
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}

	prev := PrevPath(target)
	if err := os.Remove(prev); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove old %s: %w", prev, err)
	}

	if goos == "windows" {
		if err := os.Rename(target, prev); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("move %s aside: %w", target, err)
		}
		if err := os.Rename(tmpName, target); err != nil {
			_ = os.Rename(prev, target)
			return fmt.Errorf("install %s: %w", target, err)
		}
		ok = true
		return nil
	}

	if err := keepPrevious(target, prev); err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("install %s: %w", target, err)
	}
	ok = true
	return nil
}

func keepPrevious(target, prev string) error {
	if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := os.Link(target, prev); err == nil {
		return nil
	}
	src, err := os.Open(target)
	if err != nil {
		return fmt.Errorf("keep previous binary: %w", err)
	}
	defer src.Close()
	dst, err := os.OpenFile(prev, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return fmt.Errorf("keep previous binary: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(prev)
		return fmt.Errorf("keep previous binary: %w", err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(prev)
		return fmt.Errorf("keep previous binary: %w", err)
	}
	return nil
}
