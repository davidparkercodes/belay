package selfupdate

import (
	"context"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
)

type Checker struct {
	Client   *Client
	Updater  *Updater
	Current  string
	CacheDir string
	Now      func() time.Time
	Getenv   func(string) string
	Mode     func() string
	Binary   func() (Install, error)
	Logf     func(string, ...interface{})
}

type TickResult struct {
	Checked   bool
	Latest    string
	Attempted bool
	Installed bool
	Err       error
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Checker) logf(format string, args ...interface{}) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

func (c *Checker) Tick(ctx context.Context) TickResult {
	var res TickResult
	if c.Getenv != nil && Disabled(c.Getenv) {
		return res
	}
	mode := config.UpdateNotify
	if c.Mode != nil {
		mode = c.Mode()
	}
	if mode == config.UpdateOff {
		return res
	}

	path := CachePath(c.CacheDir)
	st, _ := ReadState(path)
	now := c.now()
	if !st.Due(now) && !c.autoDue(mode, st, now) {
		return res
	}

	lock, ok := acquireLock(c.CacheDir, now)
	if !ok {
		return res
	}
	defer lock.release()

	st, _ = ReadState(path)
	if st.Due(now) {
		res.Checked = true
		latest, err := c.Client.LatestTag(ctx)
		st.CheckedAt = now
		st.CheckedBy = c.Current
		if err != nil {
			st.Error = err.Error()
			res.Err = err
			c.logf("update check failed, will retry in %s: %v", CheckInterval, err)
		} else {
			st.Error = ""
			st.Latest = latest
			res.Latest = latest
		}
		_ = WriteState(path, st)
	}

	if !c.autoDue(mode, st, now) {
		return res
	}
	res.Attempted = true
	st.AutoTarget = st.Latest
	st.AutoAt = now
	installed, err := c.autoInstall(ctx, st.Latest)
	if err != nil {
		st.AutoError = err.Error()
		res.Err = err
		c.logf("auto-update to %s failed, will retry on the next check: %v", st.Latest, err)
	} else {
		st.AutoError = ""
		st.AutoVersion = st.Latest
		res.Installed = installed
	}
	_ = WriteState(path, st)
	return res
}

func (c *Checker) autoDue(mode string, st State, now time.Time) bool {
	if mode != config.UpdateAuto || !IsNewer(st.Latest, c.Current) {
		return false
	}
	if st.AutoTarget != st.Latest {
		return true
	}
	return st.AutoError != "" && now.Sub(st.AutoAt) >= CheckInterval
}

func (c *Checker) autoInstall(ctx context.Context, tag string) (bool, error) {
	inst, err := c.Binary()
	if err != nil {
		return false, err
	}
	res, err := c.Updater.Apply(ctx, inst, c.Current, tag, false)
	if err != nil {
		return false, err
	}
	if !res.Ran {
		c.logf("auto-update: %s is available; run %s", tag, strings.Join(res.Command, " "))
		return false, nil
	}
	c.logf("auto-update: installed %s over %s (%s, %s)", tag, Tag(c.Current), inst.Method, inst.Path)
	return true, nil
}
