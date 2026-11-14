// Package update finds, downloads and installs new releases of SpaceHop.
//
// It knows nothing about windows or the registry, so it is tested on any
// system. The steps are kept separate so the caller can check the download
// before it replaces anything: Latest, Download, then Swap (and Rollback if
// the new program turns out not to start).
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Repo is where releases are published.
const Repo = "Jaaayden/spacehop"

// maxSize caps a download; the program is a few MB.
const maxSize = 64 << 20

// Release is the newest published version and where to get it.
type Release struct {
	Tag     string // "v0.3.0"
	Version string // "0.3.0"
	Notes   string
	Page    string // the release's web page
	ExeURL  string // the executable for this architecture
	ExeName string
	SumsURL string // SHA256SUMS.txt
}

// Response is one answer to a GET. The caller never follows redirects: Client
// does, so that it can refuse to leave GitHub.
type Response struct {
	Status   int
	Location string // for a redirect
	Length   int64  // -1 when unknown
	Body     io.ReadCloser
}

// Getter makes one GET request without following redirects.
type Getter func(ctx context.Context, rawURL, accept string) (*Response, error)

// Client talks to the release API.
type Client struct {
	Get  Getter // required
	API  string // latest-release endpoint; GitHub's by default
	Arch string // "amd64" or "arm64"
	// Allowed decides which hosts may be contacted; GitHub's by default.
	Allowed func(*url.URL) bool
}

func (c *Client) api() string {
	if c.API != "" {
		return c.API
	}
	return "https://api.github.com/repos/" + Repo + "/releases/latest"
}

func (c *Client) allowed(u *url.URL) bool {
	if c.Allowed != nil {
		return c.Allowed(u)
	}
	return u.Scheme == "https" && (u.Host == "github.com" || u.Host == "api.github.com" ||
		strings.HasSuffix(u.Host, ".githubusercontent.com"))
}

// get follows up to five redirects, every one of which must stay on an
// allowed host.
func (c *Client) get(ctx context.Context, rawURL, accept string) (*Response, error) {
	for i := 0; i <= 5; i++ {
		u, err := url.Parse(rawURL)
		if err != nil || !c.allowed(u) {
			return nil, fmt.Errorf("address not allowed: %s", rawURL)
		}
		resp, err := c.Get(ctx, rawURL, accept)
		if err != nil {
			return nil, err
		}
		switch {
		case resp.Status == 200:
			return resp, nil
		case resp.Status >= 300 && resp.Status < 400 && resp.Location != "":
			resp.Body.Close()
			next, err := u.Parse(resp.Location)
			if err != nil {
				return nil, err
			}
			rawURL = next.String()
		default:
			resp.Body.Close()
			return nil, fmt.Errorf("server answered %d", resp.Status)
		}
	}
	return nil, errors.New("too many redirects")
}

type apiRelease struct {
	Tag        string `json:"tag_name"`
	Body       string `json:"body"`
	Page       string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Latest asks for the newest published (not draft, not pre-release) version.
func (c *Client) Latest(ctx context.Context) (*Release, error) {
	resp, err := c.get(ctx, c.api(), "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var ar apiRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&ar); err != nil {
		return nil, err
	}
	if ar.Draft || ar.Prerelease || ar.Tag == "" {
		return nil, errors.New("no published release")
	}
	arch := c.Arch
	if arch == "" {
		arch = "amd64"
	}
	r := &Release{Tag: ar.Tag, Version: strings.TrimPrefix(ar.Tag, "v"), Notes: ar.Body, Page: ar.Page}
	r.ExeName = "SpaceHop-" + arch + ".exe"
	for _, a := range ar.Assets {
		switch a.Name {
		case r.ExeName:
			r.ExeURL = a.URL
		case "SHA256SUMS.txt":
			r.SumsURL = a.URL
		}
	}
	if _, ok := parse(r.Version); !ok {
		return nil, fmt.Errorf("unreadable version %q", ar.Tag)
	}
	return r, nil
}

// parse reads "1.2.3", "v1.2.3" or "1.2.3+abcdef"; ok is false for anything
// else, including a development build ("dev").
func parse(v string) (n [3]int, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "+-"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return n, false
	}
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 {
			return n, false
		}
		n[i] = x
	}
	return n, true
}

// Newer reports whether latest is a later version than current. A build
// whose version cannot be read (a development build) is never updated.
func Newer(current, latest string) bool {
	a, ok1 := parse(current)
	b, ok2 := parse(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return b[i] > a[i]
		}
	}
	return false
}

// Progress is called while downloading with the bytes so far and the total
// (0 when unknown).
type Progress func(done, total int64)

// Download fetches the release's executable into dst, checks it against the
// release's SHA256SUMS.txt and removes it again if anything is wrong.
func (c *Client) Download(ctx context.Context, r *Release, dst string, progress Progress) (err error) {
	if r.ExeURL == "" || r.SumsURL == "" {
		return errors.New("the release has no file for this computer")
	}
	sresp, err := c.get(ctx, r.SumsURL, "")
	if err != nil {
		return err
	}
	sums, err := io.ReadAll(io.LimitReader(sresp.Body, 1<<20))
	sresp.Body.Close()
	if err != nil {
		return err
	}
	want, ok := SumFor(string(sums), r.ExeName)
	if !ok {
		return errors.New("the release has no checksum for this file")
	}

	resp, err := c.get(ctx, r.ExeURL, "application/octet-stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(dst)
		}
	}()
	h := newSum256()
	total := resp.Length
	if total > maxSize {
		return errors.New("the download is too large")
	}
	var done int64
	buf := make([]byte, 64<<10)
	var last time.Time
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > maxSize {
				return errors.New("the download is too large")
			}
			h.Write(buf[:n])
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			if progress != nil && time.Since(last) > 80*time.Millisecond {
				last = time.Now()
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if progress != nil {
		progress(done, total)
	}
	if got := h.Hex(); got != want {
		return fmt.Errorf("checksum mismatch (expected %s…, got %s…)", want[:12], got[:12])
	}
	return checkPE(dst)
}

// SumFor finds the SHA-256 of name in the text of a checksum file
// ("<hex>  <name>" per line, as sha256sum and shasum write it).
func SumFor(sums, name string) (string, bool) {
	for _, line := range strings.Split(sums, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

// checkPE makes sure the file at least looks like a Windows program.
func checkPE(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var b [2]byte
	if _, err := io.ReadFull(f, b[:]); err != nil || string(b[:]) != "MZ" {
		return errors.New("the download is not a program")
	}
	return nil
}

// Swap puts the new program in place of the running one. The running file is
// renamed to exe+".old" (Windows allows that, but not overwriting it) and kept
// until the new one has started. On failure nothing is changed.
func Swap(exe, fresh string) (old string, err error) {
	old = exe + ".old"
	_ = os.Remove(old) // left over from an earlier update
	if err := os.Rename(exe, old); err != nil {
		return "", err
	}
	if err := os.Rename(fresh, exe); err != nil {
		if rerr := os.Rename(old, exe); rerr != nil {
			return "", fmt.Errorf("%v (and the old program could not be put back: %v)", err, rerr)
		}
		return "", err
	}
	return old, nil
}

// Rollback undoes Swap: the new program is moved aside and the old one put
// back. It returns the path of the restored program.
func Rollback(exe string) (string, error) {
	old := exe + ".old"
	if _, err := os.Stat(old); err != nil {
		return "", err
	}
	bad := exe + ".bad"
	_ = os.Remove(bad)
	if err := os.Rename(exe, bad); err != nil {
		return "", err
	}
	if err := os.Rename(old, exe); err != nil {
		_ = os.Rename(bad, exe)
		return "", err
	}
	return exe, nil
}

// Cleanup removes what an update leaves behind, once the new program runs.
func Cleanup(exe string) {
	_ = os.Remove(exe + ".old")
	_ = os.Remove(exe + ".bad")
	_ = os.Remove(exe + ".new")
}

// NewPath is where the download of an update is staged: next to the program,
// so the final rename stays on one volume.
func NewPath(exe string) string { return exe + ".new" }

// State is what is remembered between runs.
type State struct {
	LastCheck int64  `json:"lastCheck"` // Unix seconds
	Told      string `json:"told"`      // the version the user was last notified about
	Latest    string `json:"latest"`    // the newest version seen
}

// LoadState reads the state file in dir; a missing or damaged one is empty.
func LoadState(dir string) State {
	var s State
	if b, err := os.ReadFile(filepath.Join(dir, "update.json")); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// SaveState writes the state file.
func SaveState(dir string, s State) {
	b, _ := json.Marshal(s)
	tmp := filepath.Join(dir, "update.json.tmp")
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, filepath.Join(dir, "update.json"))
	}
}

// Known reports whether v is a release version (not a development build).
func Known(v string) bool { _, ok := parse(v); return ok }
