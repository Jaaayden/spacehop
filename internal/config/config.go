// Package config holds the on-disk configuration: leader keys, timing, the
// hint window and the list of key bindings.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"spacehop/internal/i18n"
)

const (
	MaxBindings = 2000
	MaxKeys     = 8
	MaxLeaders  = 8
	MaxCycle    = 8
	MaxBackups  = 30
	// A new timestamped backup is written at most this often; the .bak file
	// always holds the version just before the last save.
	backupEvery = 10 * time.Minute
)

type Config struct {
	Version int  `json:"version"`
	Enabled bool `json:"enabled"`
	// Language of the interface: "auto" (follow Windows), "zh" or "en".
	Language string `json:"language"`
	// Theme of the settings window: "auto", "dark" or "light".
	Theme string `json:"theme"`
	// Leaders are the keys that start a shortcut. The first one is the
	// default for new bindings.
	Leaders             []Leader `json:"leaders"`
	Timing              Timing   `json:"timing"`
	Hint                Hint     `json:"hint"`
	DisabledApps        []string `json:"disabledApps"`
	DisableInFullscreen bool     `json:"disableInFullscreen"`
	// NoUpdateCheck turns the daily look for a newer release off.
	NoUpdateCheck bool      `json:"noUpdateCheck"`
	Bindings      []Binding `json:"bindings"`

	// Legacy is the single leader of version 1 files.
	Legacy *struct {
		Key     string `json:"key"`
		KeepTap bool   `json:"keepTap"`
	} `json:"leader,omitempty"`
}

// Leader modes.
const (
	Hold = "hold" // hold the key, press triggers while it is down
	Tap  = "tap"  // press and release, then press triggers
)

type Leader struct {
	ID   string `json:"id"`
	Mode string `json:"mode"`
	// Hold: one key ("Space", "CapsLock", "F12"). Tap: a combination
	// ("Alt+S") or a single modifier ("RCtrl").
	Key string `json:"key"`
	// KeepTap (hold only): a quick tap still types the key.
	KeepTap bool `json:"keepTap"`
}

type Timing struct {
	// A key pressed sooner than this after the leader counts as typing.
	ActivationMs int `json:"activationMs"`
	// Same, but applied when the leader follows other typing closely.
	TypingSuppressMs int `json:"typingSuppressMs"`
	// Releasing the leader within this window types it; later it is dropped.
	TapReplayMs int `json:"tapReplayMs"`
	HintDelayMs int `json:"hintDelayMs"`
	// A tap leader waits this long for the next trigger key.
	TapTimeoutMs int `json:"tapTimeoutMs"`
	// A single-modifier leader counts as tapped when released within this.
	ModTapMs int `json:"modTapMs"`
}

type Hint struct {
	Show   bool   `json:"show"`
	Layout string `json:"layout"` // "modern" | "classic"
	Theme  string `json:"theme"`  // "auto" | "dark" | "light"
	// X is the window centre, in percent of the screen width; Y is the gap
	// below the window, in percent of the screen height.
	X int `json:"x"`
	Y int `json:"y"`
	// Width of one column in pixels at 100% scaling.
	Width   int    `json:"width"`
	Monitor string `json:"monitor"` // "focus" | "pointer"
	// SearchKey opens search while the hint is active; "" turns it off.
	SearchKey string `json:"searchKey"`
	// Dim makes often-used shortcuts less prominent.
	Dim bool `json:"dim"`
}

type Binding struct {
	ID      string   `json:"id"`
	Enabled bool     `json:"enabled"`
	Leader  string   `json:"leader,omitempty"` // leader id; "" is the default leader
	Keys    []string `json:"keys"`
	Name    string   `json:"name,omitempty"`
	Action  Action   `json:"action"`
	Scope   Scope    `json:"scope"`
	// After says what happens once the action ran: "" (hold: back to the
	// top, tap: finish), "stay" (keep this level), "root", "stop".
	After string `json:"after,omitempty"`
}

// Action types.
const (
	OpenApp     = "openApp"
	OpenURL     = "openURL"
	OpenFile    = "openFile"
	RunCommand  = "runCommand"
	ExecuteFile = "executeFile"
	Keystroke   = "keystroke"
	InsertText  = "insertText"
	MenuCommand = "menu"
	Window      = "window"
	InputMethod = "inputMethod"
	Notify      = "notify"
	System      = "system"
)

type AppRef struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path"`
	Exe  string `json:"exe,omitempty"`
}

type Action struct {
	Type string `json:"type"`
	// openApp / openFile / executeFile: target path (exe, .lnk, file, folder,
	// or a Start-menu AppID such as "Microsoft.WindowsCalculator_8wekyb3d8bbwe!App").
	Path string `json:"path,omitempty"`
	Args string `json:"args,omitempty"`
	// openApp: process file name used to find an already running window.
	Exe string `json:"exe,omitempty"`
	// openApp, when the program is already in front: "activate" (default,
	// next window of the program), "hide", "previous" (back to the program
	// used before), "new" (always start another copy).
	IfRunning string `json:"ifRunning,omitempty"`
	// openApp: more programs to rotate through with the same shortcut.
	Cycle []AppRef `json:"cycle,omitempty"`
	URL   string   `json:"url,omitempty"`
	// openURL / openFile: optional program to open with.
	With    string `json:"with,omitempty"`
	Command string `json:"command,omitempty"`
	Shell   string `json:"shell,omitempty"` // "cmd" (default) | "powershell"
	Hidden  bool   `json:"hidden,omitempty"`
	Chord   string `json:"chord,omitempty"` // keystroke, e.g. "Ctrl+Shift+T"
	// insertText / notify body.
	Text string `json:"text,omitempty"`
	// insertText: paste through the clipboard instead of typing.
	Paste bool `json:"paste,omitempty"`
	// menu: path in the front window's menu bar, "File->Save As...".
	Menu string `json:"menu,omitempty"`
	// window: a layout name (see WindowLayouts) or "custom" with Rect.
	Window string `json:"window,omitempty"`
	// window "custom": left, top, width, height in percent of the work area.
	Rect []int `json:"rect,omitempty"`
	// inputMethod: keyboard layout id ("00000804") or "next".
	Layout string `json:"layout,omitempty"`
	Title  string `json:"title,omitempty"`  // notify
	System string `json:"system,omitempty"` // see SystemActions
}

// LabelOf returns the translated label of an entry of WindowLayouts or
// SystemActions.
func LabelOf(table [][2]string, name string) string { s, _ := labelOf(table, name); return s }

// WindowLayouts and SystemActions list the accepted names with their labels.
var (
	WindowLayouts = [][2]string{
		{"left", i18n.N("左半屏")}, {"right", i18n.N("右半屏")}, {"top", i18n.N("上半屏")}, {"bottom", i18n.N("下半屏")},
		{"left-third", i18n.N("左三分之一")}, {"center-third", i18n.N("中间三分之一")}, {"right-third", i18n.N("右三分之一")},
		{"left-two-thirds", i18n.N("左侧三分之二")}, {"right-two-thirds", i18n.N("右侧三分之二")},
		{"top-left", i18n.N("左上四分之一")}, {"top-right", i18n.N("右上四分之一")}, {"bottom-left", i18n.N("左下四分之一")}, {"bottom-right", i18n.N("右下四分之一")},
		{"maximize", i18n.N("最大化")}, {"center", i18n.N("居中")}, {"restore", i18n.N("还原")}, {"minimize", i18n.N("最小化")},
		{"next-monitor", i18n.N("移到下一个显示器")}, {"close", i18n.N("关闭窗口")},
		{"switch-previous", i18n.N("切换到上一个窗口")}, {"switch-same-app", i18n.N("同一程序的下一个窗口")},
		{"custom", i18n.N("自定义位置和大小")},
	}
	SystemActions = [][2]string{
		{"lock", i18n.N("锁定电脑")}, {"sleep", i18n.N("睡眠")}, {"display-off", i18n.N("关闭显示器")}, {"show-desktop", i18n.N("显示桌面")},
		{"settings", i18n.N("打开 SpaceHop 设置")}, {"toggle", i18n.N("暂停 / 恢复 SpaceHop")},
	}
)

func labelOf(table [][2]string, name string) (string, bool) {
	for _, r := range table {
		if r[0] == name {
			return i18n.T(r[1]), true
		}
	}
	return "", false
}

type Scope struct {
	Mode string   `json:"mode"` // "all" | "only" | "except"
	Apps []string `json:"apps,omitempty"`
}

func Default() *Config {
	all := Scope{Mode: "all"}
	b := func(name string, a Action, keys ...string) Binding {
		return Binding{ID: NewID(), Enabled: true, Keys: keys, Name: name, Action: a, Scope: all}
	}
	stay := func(x Binding) Binding { x.After = "stay"; return x }
	return &Config{
		Version:             2,
		Enabled:             true,
		Language:            "auto",
		Theme:               "auto",
		Leaders:             []Leader{{ID: "space", Mode: Hold, Key: "Space", KeepTap: true}},
		Timing:              Timing{ActivationMs: 50, TypingSuppressMs: 200, TapReplayMs: 400, HintDelayMs: 500, TapTimeoutMs: 3000, ModTapMs: 300},
		Hint:                Hint{Show: true, Layout: "modern", Theme: "auto", X: 50, Y: 8, Width: 220, Monitor: "focus", SearchKey: "/", Dim: true},
		DisableInFullscreen: true,
		DisabledApps:        []string{},
		Bindings: []Binding{
			b(i18n.T("资源管理器"), Action{Type: OpenApp, Path: "explorer.exe", Exe: "explorer.exe", IfRunning: "new"}, "E"),
			b(i18n.T("记事本"), Action{Type: OpenApp, Path: "notepad.exe", Exe: "notepad.exe"}, "N"),
			b(i18n.T("下载"), Action{Type: OpenFile, Path: `%USERPROFILE%\Downloads`}, "F", "D"),
			b(i18n.T("个人文件夹"), Action{Type: OpenFile, Path: `%USERPROFILE%`}, "F", "H"),
			stay(b("", Action{Type: Window, Window: "left"}, "W", "H")),
			stay(b("", Action{Type: Window, Window: "right"}, "W", "L")),
			stay(b("", Action{Type: Window, Window: "maximize"}, "W", "K")),
			stay(b("", Action{Type: Window, Window: "center"}, "W", "J")),
			b("", Action{Type: Window, Window: "switch-previous"}, "Tab"),
			b("", Action{Type: System, System: "settings"}, ","),
		},
	}
}

func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func oneOf(v string, def string, allowed ...string) string {
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return def
}

// LeaderOf returns the leader a binding uses.
func (c *Config) LeaderOf(b *Binding) *Leader {
	for i := range c.Leaders {
		if c.Leaders[i].ID == b.Leader {
			return &c.Leaders[i]
		}
	}
	return &c.Leaders[0]
}

// normalizeLeader validates one leader and returns the keys it occupies.
func (l *Leader) normalize() error {
	l.Mode = oneOf(l.Mode, Hold, Hold, Tap)
	key, ok := CanonicalChord(l.Key)
	if !ok {
		return fmt.Errorf(i18n.T("引导键 %q 无法识别"), l.Key)
	}
	l.Key = key
	mods, vk, _ := ParseChord(key)
	if l.Mode == Hold {
		if len(mods) > 0 || IsModifier(vk) {
			return fmt.Errorf(i18n.T("按住型引导键 %s 必须是单个普通按键"), key)
		}
		return nil
	}
	l.KeepTap = false
	if (len(mods) == 0) != IsModifier(vk) {
		return fmt.Errorf(i18n.T("轻按型引导键 %s 需要是组合键（如 Alt+S）或单个修饰键（如 RCtrl）"), key)
	}
	return nil
}

// Normalize fixes spelling and fills defaults; it returns an error for
// things the user has to correct.
func (c *Config) Normalize() error {
	c.Version = 2
	c.Language = oneOf(c.Language, "auto", "auto", "zh", "en")
	c.Theme = oneOf(c.Theme, "auto", "auto", "dark", "light")
	if len(c.Leaders) == 0 && c.Legacy != nil {
		c.Leaders = []Leader{{ID: "space", Mode: Hold, Key: c.Legacy.Key, KeepTap: c.Legacy.KeepTap}}
	}
	c.Legacy = nil
	if len(c.Leaders) == 0 {
		c.Leaders = Default().Leaders
	}
	if len(c.Leaders) > MaxLeaders {
		return fmt.Errorf(i18n.T("引导键最多 %d 个"), MaxLeaders)
	}
	ids, keys := map[string]bool{}, map[string]bool{}
	for i := range c.Leaders {
		l := &c.Leaders[i]
		if err := l.normalize(); err != nil {
			return err
		}
		if l.ID == "" || ids[l.ID] {
			l.ID = NewID()
		}
		if keys[l.Key] {
			return fmt.Errorf(i18n.T("引导键 %s 重复了"), l.Key)
		}
		ids[l.ID], keys[l.Key] = true, true
	}

	t, d := &c.Timing, Default().Timing
	t.ActivationMs = clamp(t.ActivationMs, 0, 1000)
	t.TypingSuppressMs = clamp(t.TypingSuppressMs, 0, 2000)
	t.TapReplayMs = clamp(t.TapReplayMs, 50, 60000)
	t.HintDelayMs = clamp(t.HintDelayMs, 0, 10000)
	if t.TapTimeoutMs == 0 {
		t.TapTimeoutMs = d.TapTimeoutMs
	}
	if t.ModTapMs == 0 {
		t.ModTapMs = d.ModTapMs
	}
	t.TapTimeoutMs = clamp(t.TapTimeoutMs, 300, 60000)
	t.ModTapMs = clamp(t.ModTapMs, 80, 2000)

	h := &c.Hint
	h.Layout = oneOf(h.Layout, "modern", "modern", "classic")
	h.Theme = oneOf(h.Theme, "auto", "auto", "dark", "light")
	h.Monitor = oneOf(h.Monitor, "focus", "focus", "pointer")
	if h.Width == 0 {
		h.Width, h.X, h.Y = 220, 50, 8
	}
	h.Width = clamp(h.Width, 160, 480)
	h.X = clamp(h.X, 0, 100)
	h.Y = clamp(h.Y, 0, 100)
	if h.SearchKey != "" {
		k, ok := CanonicalKey(h.SearchKey)
		if v, _ := VK(k); !ok || IsModifier(v) || v == VKEsc || v == VKBack || v == VKEnter {
			return fmt.Errorf(i18n.T("搜索键 %q 不能用"), h.SearchKey)
		}
		h.SearchKey = k
	}

	c.DisabledApps = cleanApps(c.DisabledApps)
	if len(c.Bindings) > MaxBindings {
		return fmt.Errorf(i18n.T("快捷键数量超过上限 %d"), MaxBindings)
	}
	if c.Bindings == nil {
		c.Bindings = []Binding{}
	}
	seen := map[string]bool{}
	for i := range c.Bindings {
		b := &c.Bindings[i]
		if b.ID == "" || seen[b.ID] {
			b.ID = NewID()
		}
		seen[b.ID] = true
		if !ids[b.Leader] || b.Leader == c.Leaders[0].ID {
			b.Leader = ""
		}
		if err := c.normalizeBinding(b); err != nil {
			return fmt.Errorf(i18n.T("第 %d 条快捷键：%w"), i+1, err)
		}
	}
	return nil
}

func (c *Config) normalizeBinding(b *Binding) error {
	l := c.LeaderOf(b)
	if b.Keys == nil {
		b.Keys = []string{}
	}
	if len(b.Keys) > MaxKeys {
		return fmt.Errorf(i18n.T("触发键最多 %d 个"), MaxKeys)
	}
	if len(b.Keys) == 0 && l.Mode != Tap {
		return errors.New(i18n.T("需要至少一个触发键（只有轻按型引导键可以不带触发键）"))
	}
	for j, key := range b.Keys {
		ck, ok := CanonicalKey(key)
		if !ok {
			return fmt.Errorf(i18n.T("按键 %q 无法识别"), key)
		}
		v, _ := VK(ck)
		if IsModifier(v) || v == VKEsc || (v == VKBack && j > 0) || (l.Mode == Hold && ck == l.Key) {
			return fmt.Errorf(i18n.T("不能用 %s 作触发键"), ck)
		}
		b.Keys[j] = ck
	}
	b.After = oneOf(b.After, "", "stay", "root", "stop")
	switch b.Scope.Mode {
	case "only", "except":
		b.Scope.Apps = cleanApps(b.Scope.Apps)
	default:
		b.Scope = Scope{Mode: "all"}
	}
	a := &b.Action
	switch a.Type {
	case OpenApp:
		a.IfRunning = oneOf(a.IfRunning, "", "hide", "previous", "new")
		if len(a.Cycle) > MaxCycle {
			return fmt.Errorf(i18n.T("轮换的程序最多 %d 个"), MaxCycle)
		}
	case OpenURL, OpenFile, RunCommand, ExecuteFile, InsertText, MenuCommand, Notify, InputMethod:
	case Keystroke:
		if a.Chord != "" {
			ch, ok := CanonicalChord(a.Chord)
			if !ok {
				return fmt.Errorf(i18n.T("模拟按键 %q 无法识别"), a.Chord)
			}
			a.Chord = ch
		}
	case Window:
		if a.Window != "" {
			if _, ok := labelOf(WindowLayouts, a.Window); !ok {
				return fmt.Errorf(i18n.T("窗口动作 %q 无法识别"), a.Window)
			}
		}
		if a.Window == "custom" {
			if len(a.Rect) != 4 {
				a.Rect = []int{10, 10, 80, 80}
			}
			a.Rect[0], a.Rect[1] = clamp(a.Rect[0], 0, 95), clamp(a.Rect[1], 0, 95)
			a.Rect[2], a.Rect[3] = clamp(a.Rect[2], 5, 100-a.Rect[0]), clamp(a.Rect[3], 5, 100-a.Rect[1])
		} else {
			a.Rect = nil
		}
	case System:
		if a.System != "" {
			if _, ok := labelOf(SystemActions, a.System); !ok {
				return fmt.Errorf(i18n.T("系统动作 %q 无法识别"), a.System)
			}
		}
	default:
		return fmt.Errorf(i18n.T("动作类型 %q 无法识别"), a.Type)
	}
	return nil
}

func cleanApps(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, a := range in {
		a = strings.ToLower(strings.TrimSpace(a))
		if i := strings.LastIndexAny(a, `\/`); i >= 0 {
			a = a[i+1:]
		}
		if a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// Matches reports whether the binding applies when exe (lower-case file
// name) is the foreground program.
func (s Scope) Matches(exe string) bool {
	if s.Mode == "all" || s.Mode == "" {
		return true
	}
	in := false
	for _, a := range s.Apps {
		if a == exe {
			in = true
			break
		}
	}
	if s.Mode == "only" {
		return in
	}
	return !in
}

func baseName(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/!`); i >= 0 {
		p = p[i+1:]
	}
	for _, ext := range []string{".exe", ".lnk"} {
		if strings.HasSuffix(strings.ToLower(p), ext) {
			p = p[:len(p)-len(ext)]
		}
	}
	return p
}

// Label is the text shown for a binding in the hint window.
func (b *Binding) Label() string {
	if b.Name != "" {
		return b.Name
	}
	a := b.Action
	switch a.Type {
	case OpenApp, OpenFile, ExecuteFile:
		return baseName(a.Path)
	case OpenURL:
		if u, err := url.Parse(a.URL); err == nil && u.Host != "" {
			return strings.TrimPrefix(u.Host, "www.")
		}
		return a.URL
	case RunCommand:
		return a.Command
	case Keystroke:
		return a.Chord
	case InsertText:
		return a.Text
	case MenuCommand:
		return strings.ReplaceAll(a.Menu, "->", " › ")
	case Window:
		if s, ok := labelOf(WindowLayouts, a.Window); ok {
			return s
		}
	case InputMethod:
		if a.Layout == "next" {
			return i18n.T("下一个输入法")
		}
		return i18n.T("切换输入法")
	case Notify:
		return a.Title
	case System:
		if s, ok := labelOf(SystemActions, a.System); ok {
			return s
		}
	}
	return a.Type
}

// Dir returns the per-user configuration directory.
func Dir() string {
	if d := os.Getenv("SPACEHOP_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, "SpaceHop")
}

func Path() string      { return filepath.Join(Dir(), "config.json") }
func backupDir() string { return filepath.Join(Dir(), "backups") }

// Parse decodes and validates a configuration.
func Parse(data []byte) (*Config, error) {
	c := Default()
	c.Bindings, c.Leaders = nil, nil
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf(i18n.T("无法解析：%w"), err)
	}
	if err := c.Normalize(); err != nil {
		return nil, err
	}
	return c, nil
}

// Load reads the config file, creating it with defaults on first run. A
// file that cannot be used is left untouched and reported.
func Load() (*Config, error) {
	data, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		c := Default()
		return c, Save(c)
	}
	if err != nil {
		return Default(), err
	}
	c, err := Parse(data)
	if err != nil {
		return Default(), fmt.Errorf(i18n.T("配置文件有问题：%w"), err)
	}
	return c, nil
}

// Save writes the config atomically. The previous file is kept as .bak and,
// at most every ten minutes, as a timestamped backup (the newest 30 stay).
// Nothing is written when the content is unchanged.
func Save(c *Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	p := Path()
	if old, err := os.ReadFile(p); err == nil {
		if string(old) == string(data) {
			return nil
		}
		_ = os.WriteFile(p+".bak", old, 0o644)
		backup(old)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

type Backup struct {
	Name     string `json:"name"`
	Time     string `json:"time"`
	Bindings int    `json:"bindings"`
}

func backupNames() []string {
	entries, _ := os.ReadDir(backupDir())
	var names []string
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, "config-") && strings.HasSuffix(n, ".json") {
			names = append(names, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names))) // newest first
	return names
}

const backupStamp = "20060102-150405"

func backup(old []byte) {
	names := backupNames()
	now := time.Now()
	if len(names) > 0 {
		if t, err := time.ParseInLocation("config-"+backupStamp+".json", names[0], time.Local); err == nil && now.Sub(t) < backupEvery && now.After(t) {
			return
		}
	}
	if os.MkdirAll(backupDir(), 0o755) != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(backupDir(), "config-"+now.Format(backupStamp)+".json"), old, 0o644)
	for i := MaxBackups - 1; i < len(names); i++ {
		_ = os.Remove(filepath.Join(backupDir(), names[i]))
	}
}

// Backups lists the automatic backups, newest first.
func Backups() []Backup {
	out := []Backup{}
	for _, n := range backupNames() {
		b := Backup{Name: n}
		if t, err := time.ParseInLocation("config-"+backupStamp+".json", n, time.Local); err == nil {
			b.Time = t.Format("2006-01-02 15:04")
		}
		if data, err := os.ReadFile(filepath.Join(backupDir(), n)); err == nil {
			if c, err := Parse(data); err == nil {
				b.Bindings = len(c.Bindings)
			}
		}
		out = append(out, b)
	}
	return out
}

// ReadBackup loads one automatic backup by its file name.
func ReadBackup(name string) (*Config, error) {
	if name != filepath.Base(name) || !strings.HasPrefix(name, "config-") {
		return nil, errors.New(i18n.T("备份名称不对"))
	}
	data, err := os.ReadFile(filepath.Join(backupDir(), name))
	if err != nil {
		return nil, err
	}
	return Parse(data)
}
