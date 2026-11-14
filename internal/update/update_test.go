package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"0.2.2+f884d13", "0.3.0", true},
		{"0.2.2", "0.2.2", false},
		{"0.2.10", "0.2.9", false},
		{"0.2.9", "0.2.10", true},
		{"1.0.0", "0.9.9", false},
		{"dev", "9.9.9", false}, // a development build is left alone
		{"0.2.2", "garbage", false},
		{"v0.2.2", "v0.2.3", true},
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestSumFor(t *testing.T) {
	h := strings.Repeat("ab", 32)
	sums := h + "  SpaceHop-amd64.exe\n" + strings.Repeat("cd", 32) + " *SpaceHop-arm64.exe\r\n"
	if got, ok := SumFor(sums, "SpaceHop-amd64.exe"); !ok || got != h {
		t.Fatalf("amd64: %q %v", got, ok)
	}
	if _, ok := SumFor(sums, "SpaceHop-arm64.exe"); !ok {
		t.Fatal("arm64 with * and CRLF not found")
	}
	if _, ok := SumFor(sums, "other.exe"); ok {
		t.Fatal("found a file that is not listed")
	}
}

// httpGetter is a Getter on net/http (only tests use it; the program itself
// uses the system's WinHTTP and leaves net/http out of the executable).
func httpGetter(ctx context.Context, rawURL, accept string) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	return &Response{Status: resp.StatusCode, Location: resp.Header.Get("Location"), Length: resp.ContentLength, Body: resp.Body}, nil
}

// server serves a fake release; exe is the executable it offers.
func server(t *testing.T, exe []byte, sumOf []byte) (*httptest.Server, *Client) {
	t.Helper()
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v0.3.0","body":"notes","html_url":"%s/page","assets":[
			{"name":"SpaceHop-amd64.exe","browser_download_url":"%s/exe"},
			{"name":"SHA256SUMS.txt","browser_download_url":"%s/sums"}]}`, base, base, base)
	})
	mux.HandleFunc("/exe", func(w http.ResponseWriter, r *http.Request) { w.Write(exe) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		s := sha256.Sum256(sumOf)
		fmt.Fprintf(w, "%s  SpaceHop-amd64.exe\n", hex.EncodeToString(s[:]))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base = srv.URL
	return srv, &Client{
		Get: httpGetter, API: srv.URL + "/latest", Arch: "amd64",
		Allowed: func(u *url.URL) bool { return u.Host == strings.TrimPrefix(srv.URL, "http://") },
	}
}

func TestLatestAndDownload(t *testing.T) {
	exe := append([]byte("MZ"), []byte(strings.Repeat("x", 1<<20))...)
	_, c := server(t, exe, exe)
	r, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != "0.3.0" || r.ExeURL == "" || r.SumsURL == "" || r.Notes != "notes" {
		t.Fatalf("release: %+v", r)
	}
	dst := filepath.Join(t.TempDir(), "x.new")
	var last int64
	if err := c.Download(context.Background(), r, dst, func(done, total int64) { last = done }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); len(b) != len(exe) || last != int64(len(exe)) {
		t.Fatalf("downloaded %d bytes, progress %d", len(b), last)
	}
}

func TestDownloadRefusesBadFiles(t *testing.T) {
	good := append([]byte("MZ"), 1, 2, 3)
	// The checksum is of different bytes: a damaged or swapped download.
	_, c := server(t, good, []byte("something else"))
	r, _ := c.Latest(context.Background())
	dst := filepath.Join(t.TempDir(), "x.new")
	err := c.Download(context.Background(), r, dst, nil)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("want checksum error, got %v", err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("a bad download was left on disk")
	}
	// Right checksum but not a program.
	notExe := []byte("<html>hi</html>")
	_, c = server(t, notExe, notExe)
	r, _ = c.Latest(context.Background())
	if err := c.Download(context.Background(), r, dst, nil); err == nil {
		t.Fatal("accepted a file that is not a program")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("a non-program was left on disk")
	}
}

func TestRedirectsStayOnAllowedHosts(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/final", http.StatusFound) })
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/away", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example/x", http.StatusFound)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	c := &Client{Get: httpGetter, Allowed: func(u *url.URL) bool { return u.Host == host }}
	if resp, err := c.get(context.Background(), srv.URL+"/start", ""); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	if _, err := c.get(context.Background(), srv.URL+"/away", ""); err == nil {
		t.Fatal("followed a redirect off the allowed host")
	}
	if _, err := c.get(context.Background(), srv.URL+"/loop", ""); err == nil {
		t.Fatal("followed a redirect loop")
	}
}

func TestHostsAreRestricted(t *testing.T) {
	c := &Client{}
	for _, bad := range []string{"http://github.com/x", "https://evil.example/x", "https://github.com.evil.example/x"} {
		u, _ := url.Parse(bad)
		if c.allowed(u) {
			t.Errorf("%s allowed", bad)
		}
	}
	for _, ok := range []string{"https://api.github.com/x", "https://github.com/x", "https://objects.githubusercontent.com/x"} {
		u, _ := url.Parse(ok)
		if !c.allowed(u) {
			t.Errorf("%s refused", ok)
		}
	}
}

func TestSwapRollbackCleanup(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "SpaceHop.exe")
	os.WriteFile(exe, []byte("MZold"), 0o755)
	fresh := NewPath(exe)
	os.WriteFile(fresh, []byte("MZnew"), 0o755)
	os.WriteFile(exe+".old", []byte("stale"), 0o755) // an earlier update's leftover

	old, err := Swap(exe, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "MZnew" {
		t.Fatalf("exe is %q after swap", b)
	}
	if b, _ := os.ReadFile(old); string(b) != "MZold" {
		t.Fatalf("old is %q", b)
	}
	if _, err := os.Stat(fresh); err == nil {
		t.Fatal("staged file still there")
	}

	if _, err := Rollback(exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "MZold" {
		t.Fatalf("exe is %q after rollback", b)
	}
	if b, _ := os.ReadFile(exe + ".bad"); string(b) != "MZnew" {
		t.Fatalf("bad is %q", b)
	}
	Cleanup(exe)
	for _, s := range []string{".old", ".bad", ".new"} {
		if _, err := os.Stat(exe + s); err == nil {
			t.Errorf("%s not cleaned up", s)
		}
	}
}

func TestSwapFailureChangesNothing(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "SpaceHop.exe")
	os.WriteFile(exe, []byte("MZold"), 0o755)
	// The staged file is missing, so the second rename fails.
	if _, err := Swap(exe, filepath.Join(dir, "missing.new")); err == nil {
		t.Fatal("swap succeeded without a new file")
	}
	if b, _ := os.ReadFile(exe); string(b) != "MZold" {
		t.Fatalf("the old program was lost: %q", b)
	}
}

func TestState(t *testing.T) {
	dir := t.TempDir()
	if s := LoadState(dir); s.LastCheck != 0 {
		t.Fatal("missing state not empty")
	}
	SaveState(dir, State{LastCheck: 5, Told: "0.3.0"})
	if s := LoadState(dir); s.LastCheck != 5 || s.Told != "0.3.0" {
		t.Fatalf("%+v", s)
	}
	os.WriteFile(filepath.Join(dir, "update.json"), []byte("{broken"), 0o644)
	if s := LoadState(dir); s.LastCheck != 0 {
		t.Fatal("damaged state not empty")
	}
}

func TestSum256MatchesStandard(t *testing.T) {
	for _, n := range []int{0, 1, 55, 56, 57, 63, 64, 65, 119, 120, 128, 1000, 100000} {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(i*7 + n)
		}
		want := sha256.Sum256(data)
		// Written in odd-sized pieces, as a download arrives.
		h := newSum256()
		for p := data; len(p) > 0; {
			c := 37
			if c > len(p) {
				c = len(p)
			}
			h.Write(p[:c])
			p = p[c:]
		}
		if got := h.Hex(); got != hex.EncodeToString(want[:]) {
			t.Fatalf("%d bytes: %s, want %x", n, got, want)
		}
	}
}
