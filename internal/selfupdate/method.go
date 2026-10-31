package selfupdate

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

type Method string

const (
	MethodHomebrew  Method = "homebrew"
	MethodGoInstall Method = "go-install"
	MethodRelease   Method = "release"
	MethodDev       Method = "dev"
)

type Install struct {
	Method Method `json:"method"`
	Path   string `json:"path"`
	Prefix string `json:"-"`
	Reason string `json:"reason,omitempty"`
}

type Env struct {
	Getenv  func(string) string
	HomeDir string
}

func DefaultEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{Getenv: os.Getenv, HomeDir: home}
}

func Detect(exe, version string, env Env) Install {
	if env.Getenv == nil {
		env.Getenv = func(string) string { return "" }
	}
	exe = filepath.Clean(exe)
	inst := Install{Path: exe}

	if _, ok := ParseVersion(version); !ok {
		inst.Method = MethodDev
		inst.Reason = "development build (version " + quoteVersion(version) + ")"
		return inst
	}
	if root := sourceCheckout(filepath.Dir(exe)); root != "" {
		inst.Method = MethodDev
		inst.Reason = "binary is inside a source checkout at " + root
		return inst
	}
	if prefix, ok := homebrewPrefix(exe, env); ok {
		inst.Method = MethodHomebrew
		inst.Prefix = prefix
		return inst
	}
	if goBinDir(filepath.Dir(exe), env) {
		inst.Method = MethodGoInstall
		return inst
	}
	inst.Method = MethodRelease
	return inst
}

func quoteVersion(v string) string {
	if v == "" {
		return "unset"
	}
	return v
}

func sourceCheckout(dir string) string {
	for {
		if isBelayModule(filepath.Join(dir, "go.mod")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func isBelayModule(goMod string) bool {
	f, err := os.Open(goMod)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")) == ModulePath
		}
	}
	return false
}

func homebrewPrefix(exe string, env Env) (string, bool) {
	slash := filepath.ToSlash(exe)
	for _, marker := range []string{"/Caskroom/", "/Cellar/"} {
		if i := strings.Index(slash, marker); i > 0 {
			return filepath.FromSlash(slash[:i]), true
		}
	}
	if p := env.Getenv("HOMEBREW_PREFIX"); p != "" && within(exe, p) {
		return filepath.Clean(p), true
	}
	for _, p := range []string{"/opt/homebrew", "/home/linuxbrew/.linuxbrew"} {
		if within(exe, p) {
			return p, true
		}
	}
	return "", false
}

func goBinDir(dir string, env Env) bool {
	if gobin := env.Getenv("GOBIN"); gobin != "" && samePath(dir, gobin) {
		return true
	}
	gopath := env.Getenv("GOPATH")
	if gopath == "" && env.HomeDir != "" {
		gopath = filepath.Join(env.HomeDir, "go")
	}
	for _, p := range filepath.SplitList(gopath) {
		if p != "" && samePath(dir, filepath.Join(p, "bin")) {
			return true
		}
	}
	return false
}

func within(p, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return ra == rb
}
