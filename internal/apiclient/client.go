package apiclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
)

const ProjectHeader = "X-Belay-Project"

type MismatchError struct {
	Port   int
	Served string
}

func (e *MismatchError) Error() string {
	if e.Served == "" {
		return fmt.Sprintf("port %d is served by a Belay daemon for a different project", e.Port)
	}
	return fmt.Sprintf("port %d is served by the Belay daemon for %s, not this project", e.Port, e.Served)
}

type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.Status, e.Message)
}

func IsUnreachable(err error) bool {
	if err == nil {
		return false
	}
	var mismatch *MismatchError
	var status *StatusError
	return !errors.As(err, &mismatch) && !errors.As(err, &status)
}

func Port(cfg *config.Config) int {
	if p, ok := ReadPortFile(cfg); ok {
		return p
	}
	if cfg.API.Port > 0 {
		return cfg.API.Port
	}
	return config.DefaultAPIPort
}

func ReadPortFile(cfg *config.Config) (int, bool) {
	data, err := os.ReadFile(cfg.APIPortPath())
	if err != nil {
		return 0, false
	}
	p, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || p <= 0 || p > 65535 {
		return 0, false
	}
	return p, true
}

func WritePortFile(cfg *config.Config, port int) error {
	tmp, err := os.CreateTemp(cfg.BelayPath, ".daemon.port-*")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(strconv.Itoa(port) + "\n"); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), cfg.APIPortPath())
}

func RemovePortFile(cfg *config.Config, port int) {
	if p, ok := ReadPortFile(cfg); ok && p == port {
		_ = os.Remove(cfg.APIPortPath())
	}
}

type Client struct {
	cfg  *config.Config
	port int
	http *http.Client
}

func New(cfg *config.Config, timeout time.Duration) *Client {
	return &Client{cfg: cfg, port: Port(cfg), http: &http.Client{Timeout: timeout}}
}

func (c *Client) Port() int {
	return c.port
}

func (c *Client) Get(path string, out interface{}) error {
	return c.do(http.MethodGet, path, nil, out)
}

func (c *Client) PostJSON(path string, in, out interface{}) error {
	return c.do(http.MethodPost, path, in, out)
}

func (c *Client) do(method, path string, in, out interface{}) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", c.port, path), body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(ProjectHeader, filepath.Clean(c.cfg.ProjectRoot))

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if resp.StatusCode == http.StatusConflict && errResp["code"] == "project_mismatch" {
			return &MismatchError{Port: c.port, Served: errResp["project_root"]}
		}
		return &StatusError{Status: resp.StatusCode, Message: errResp["error"]}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
