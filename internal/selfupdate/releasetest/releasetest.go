package releasetest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/davidparkercodes/belay/internal/selfupdate"
)

type Server struct {
	*httptest.Server
	Latest string

	mu        sync.Mutex
	Assets    map[string][]byte
	Checksums map[string]string

	APIRequests      atomic.Int64
	DownloadRequests atomic.Int64
}

func New(t testing.TB, tag string, binary []byte, platforms ...[2]string) *Server {
	t.Helper()
	s := &Server{Latest: tag, Assets: map[string][]byte{}, Checksums: map[string]string{}}
	for _, p := range platforms {
		s.AddArchive(t, tag, p[0], p[1], binary)
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *Server) AddArchive(t testing.TB, tag, goos, goarch string, binary []byte) {
	t.Helper()
	name := selfupdate.AssetName(tag, goos, goarch)
	data := Archive(t, goos, binary)
	sum := sha256.Sum256(data)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Assets[tag+"/"+name] = data
	s.Checksums[tag+"/"+name] = hex.EncodeToString(sum[:])
}

func (s *Server) Corrupt(tag, goos, goarch string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tag + "/" + selfupdate.AssetName(tag, goos, goarch)
	s.Assets[key] = append(append([]byte(nil), s.Assets[key]...), 0)
}

func (s *Server) DropChecksum(tag, goos, goarch string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Checksums, tag+"/"+selfupdate.AssetName(tag, goos, goarch))
}

func (s *Server) Client() *selfupdate.Client {
	c := selfupdate.NewClient("belay-test")
	c.HTTP = s.Server.Client()
	c.APIBase = s.URL
	c.DownloadBase = s.URL
	return c
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/repos/"+selfupdate.Repo+"/releases/latest" {
		s.APIRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"tag_name":%q}`, s.Latest)
		return
	}
	prefix := "/" + selfupdate.Repo + "/releases/download/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	s.DownloadRequests.Add(1)
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	tag, name, ok := strings.Cut(rest, "/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == selfupdate.ChecksumFile {
		var b strings.Builder
		for key, sum := range s.Checksums {
			if t, n, _ := strings.Cut(key, "/"); t == tag {
				fmt.Fprintf(&b, "%s  %s\n", sum, n)
			}
		}
		if b.Len() == 0 {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(b.String()))
		return
	}
	data, ok := s.Assets[tag+"/"+name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(data)
}

func Archive(t testing.TB, goos string, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if goos == "windows" {
		zw := zip.NewWriter(&buf)
		for _, f := range []struct {
			name string
			data []byte
		}{{"README.md", []byte("readme")}, {"belay.exe", binary}} {
			w, err := zw.Create(f.name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(f.data); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		data []byte
		mode int64
	}{{"LICENSE", []byte("license"), 0o644}, {"belay", binary, 0o755}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
