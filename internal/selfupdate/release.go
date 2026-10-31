package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

const (
	Repo         = "davidparkercodes/belay"
	ModulePath   = "github.com/davidparkercodes/belay"
	CaskName     = "davidparkercodes/tap/belay"
	ChecksumFile = "checksums.txt"

	defaultAPIBase      = "https://api.github.com"
	defaultDownloadBase = "https://github.com"

	maxArchiveBytes  = 256 << 20
	maxChecksumBytes = 1 << 20
	maxBinaryBytes   = 512 << 20
)

var (
	ErrChecksumMismatch = errors.New("checksum mismatch")
	ErrChecksumMissing  = errors.New("no checksum listed for the release asset")
	ErrBinaryNotFound   = errors.New("release archive does not contain a belay binary")
)

type Client struct {
	HTTP            *http.Client
	APIBase         string
	DownloadBase    string
	APITimeout      time.Duration
	DownloadTimeout time.Duration
	UserAgent       string
}

func NewClient(userAgent string) *Client {
	return &Client{
		HTTP:            &http.Client{},
		APIBase:         defaultAPIBase,
		DownloadBase:    defaultDownloadBase,
		APITimeout:      10 * time.Second,
		DownloadTimeout: 5 * time.Minute,
		UserAgent:       userAgent,
	}
}

func (c *Client) LatestTag(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.APITimeout)
	defer cancel()
	url := strings.TrimRight(c.APIBase, "/") + "/repos/" + Repo + "/releases/latest"
	body, err := c.get(ctx, url, maxChecksumBytes, "application/vnd.github+json")
	if err != nil {
		return "", fmt.Errorf("query latest release: %w", err)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("decode latest release: %w", err)
	}
	if _, ok := ParseVersion(rel.TagName); !ok {
		return "", fmt.Errorf("latest release has an unexpected tag %q", rel.TagName)
	}
	return Tag(rel.TagName), nil
}

func AssetName(version, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("belay_%s_%s_%s.%s", strings.TrimPrefix(version, "v"), goos, goarch, ext)
}

func (c *Client) assetURL(tag, name string) string {
	return strings.TrimRight(c.DownloadBase, "/") + "/" + Repo + "/releases/download/" + tag + "/" + name
}

func (c *Client) DownloadBinary(ctx context.Context, tag, goos, goarch string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.DownloadTimeout)
	defer cancel()
	tag = Tag(tag)
	name := AssetName(tag, goos, goarch)

	sums, err := c.get(ctx, c.assetURL(tag, ChecksumFile), maxChecksumBytes, "")
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", ChecksumFile, err)
	}
	want, ok := ParseChecksums(sums)[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrChecksumMissing, name)
	}

	archive, err := c.get(ctx, c.assetURL(tag, name), maxArchiveBytes, "")
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", name, err)
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("%w for %s: expected %s, got %s", ErrChecksumMismatch, name, want, got)
	}
	return ExtractBinary(archive, goos)
}

func (c *Client) get(ctx context.Context, url string, limit int64, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", url, limit)
	}
	return body, nil
}

func ParseChecksums(data []byte) map[string]string {
	out := make(map[string]string)
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 || len(fields[0]) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			continue
		}
		out[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	return out
}

func binaryName(goos string) string {
	if goos == "windows" {
		return "belay.exe"
	}
	return "belay"
}

func ExtractBinary(archive []byte, goos string) ([]byte, error) {
	want := binaryName(goos)
	if goos == "windows" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("open zip: %w", err)
		}
		for _, f := range zr.File {
			if path.Base(f.Name) != want || !f.Mode().IsRegular() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return readLimited(rc)
		}
		return nil, ErrBinaryNotFound
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, ErrBinaryNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && path.Base(hdr.Name) == want {
			return readLimited(tr)
		}
	}
}

func readLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBinaryBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBinaryBytes {
		return nil, fmt.Errorf("binary is larger than %d bytes", maxBinaryBytes)
	}
	if len(data) == 0 {
		return nil, ErrBinaryNotFound
	}
	return data, nil
}
