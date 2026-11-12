// Package server serves the settings page on the loopback interface only.
// The page is the whole settings UI; it talks to this small JSON API.
package server

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"

	"spacehop/internal/config"
	"spacehop/internal/icon"
)

//go:embed index.html
var indexHTML []byte

// Host is what the settings page can do to the running program.
type Host interface {
	Config() *config.Config
	Apply(*config.Config) error
	Apps() []config.AppInfo
	Autostart() bool
	SetAutostart(bool) error
	RunAction(config.Action) error
	Layouts() []config.LayoutInfo
	Usage() (total int, counts map[string]int)
	Activity(on bool) []config.LogEntry
}

type Server struct {
	host    Host
	version string
	token   string
	addr    string
	mu      sync.Mutex // one config write at a time
}

// Start listens on a random loopback port. Only requests that carry the
// per-run token (handed over in the page URL) may use the API.
func Start(host Host, version string) (*Server, error) {
	return StartOn("127.0.0.1:0", host, version)
}

// StartOn is Start with a chosen loopback address (used in development).
func StartOn(addr string, host Host, version string) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	s := &Server{host: host, version: version, token: hex.EncodeToString(b[:]), addr: ln.Addr().String()}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.index)
	mux.HandleFunc("/api/state", s.api(s.state))
	mux.HandleFunc("/api/config", s.api(s.putConfig))
	mux.HandleFunc("/api/apps", s.api(s.apps))
	mux.HandleFunc("/api/autostart", s.api(s.autostart))
	mux.HandleFunc("/api/test", s.api(s.test))
	mux.HandleFunc("/api/layouts", s.api(s.layouts))
	mux.HandleFunc("/api/backups", s.api(s.backups))
	mux.HandleFunc("/api/restore", s.api(s.restore))
	mux.HandleFunc("/api/activity", s.api(s.activity))
	mux.HandleFunc("/icon.png", s.icon)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return s, nil
}

// URL is the address of the settings page, including the access token.
func (s *Server) URL() string { return "http://" + s.addr + "/#t=" + s.token }

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" || r.Host != s.addr {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; frame-ancestors 'none'")
	_, _ = w.Write(indexHTML)
}

type handler func(r *http.Request) (any, int, string)

func (s *Server) api(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The Host check stops DNS-rebinding; the token stops other local
		// pages and programs that do not know it.
		ok := r.Host == s.addr &&
			subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Token")), []byte(s.token)) == 1
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		v, code, msg := h(r)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if msg != "" {
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	}
}

func (s *Server) icon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=86400")
	_, _ = w.Write(iconPNG())
}

var iconPNG = sync.OnceValue(func() []byte { return icon.PNG(128, false) })

func (s *Server) state(r *http.Request) (any, int, string) {
	total, counts := s.host.Usage()
	return map[string]any{
		"version":       s.version,
		"platform":      runtime.GOOS,
		"config":        s.host.Config(),
		"autostart":     s.host.Autostart(),
		"configPath":    config.Path(),
		"usage":         map[string]any{"total": total, "counts": counts},
		"windowLayouts": config.WindowLayouts,
		"systemActions": config.SystemActions,
		"limits":        map[string]int{"bindings": config.MaxBindings, "keys": config.MaxKeys, "leaders": config.MaxLeaders, "cycle": config.MaxCycle},
	}, 200, ""
}

func (s *Server) layouts(r *http.Request) (any, int, string) {
	return map[string]any{"layouts": s.host.Layouts()}, 200, ""
}

func (s *Server) backups(r *http.Request) (any, int, string) {
	return map[string]any{"backups": config.Backups()}, 200, ""
}

func (s *Server) restore(r *http.Request) (any, int, string) {
	if r.Method != http.MethodPost {
		return nil, 405, "method not allowed"
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, 400, err.Error()
	}
	c, err := config.ReadBackup(body.Name)
	if err != nil {
		return nil, 400, "这个备份无法使用：" + err.Error()
	}
	return s.save(c)
}

func (s *Server) activity(r *http.Request) (any, int, string) {
	if r.Method != http.MethodPost {
		return nil, 405, "method not allowed"
	}
	var body struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, 400, err.Error()
	}
	return map[string]any{"entries": s.host.Activity(body.On)}, 200, ""
}

func (s *Server) save(c *config.Config) (any, int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := config.Save(c); err != nil {
		return nil, 500, "保存失败：" + err.Error()
	}
	if err := s.host.Apply(c); err != nil {
		return nil, 500, err.Error()
	}
	return map[string]any{"config": c}, 200, ""
}

func (s *Server) putConfig(r *http.Request) (any, int, string) {
	if r.Method != http.MethodPut {
		return nil, 405, "method not allowed"
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, 400, "请求太大或读取失败"
	}
	c, err := config.Parse(data)
	if err != nil {
		return nil, 400, err.Error()
	}
	return s.save(c)
}

func (s *Server) apps(r *http.Request) (any, int, string) {
	apps := s.host.Apps()
	if apps == nil {
		apps = []config.AppInfo{}
	}
	return map[string]any{"apps": apps}, 200, ""
}

func (s *Server) autostart(r *http.Request) (any, int, string) {
	if r.Method != http.MethodPost {
		return nil, 405, "method not allowed"
	}
	var body struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, 400, err.Error()
	}
	if err := s.host.SetAutostart(body.On); err != nil {
		return nil, 500, "无法修改开机启动：" + err.Error()
	}
	return map[string]any{"autostart": s.host.Autostart()}, 200, ""
}

func (s *Server) test(r *http.Request) (any, int, string) {
	if r.Method != http.MethodPost {
		return nil, 405, "method not allowed"
	}
	var body struct {
		Action  config.Action `json:"action"`
		DelayMs int           `json:"delayMs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, 400, err.Error()
	}
	if body.DelayMs > 0 && body.DelayMs <= 5000 {
		time.Sleep(time.Duration(body.DelayMs) * time.Millisecond)
	}
	if err := s.host.RunAction(body.Action); err != nil {
		return nil, 400, err.Error()
	}
	return map[string]any{"ok": true}, 200, ""
}
