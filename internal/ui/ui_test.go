package ui

import (
	"testing"
	"time"

	"spacehop/internal/config"
	"spacehop/internal/raster"
)

// fakeText stands in for the platform's text drawing.
type fakeText struct{ drawn []TextOp }

func (f *fakeText) Width(font Font, s string) float64 {
	w := 0.0
	for _, r := range s {
		if r > 0x2E80 {
			w += 1
		} else {
			w += 0.55
		}
	}
	return w * FontSize[font] * 2
}
func (f *fakeText) Height(font Font, s string, width float64) float64 {
	lines := int(f.Width(font, s)/width) + 1
	return float64(lines) * FontSize[font] * 2 * 1.4
}
func (f *fakeText) Draw(ops []TextOp) { f.drawn = append(f.drawn, ops...) }

type fakeHost struct {
	cfg   *config.Config
	saves int
	later []func()

	upd      config.UpdateInfo
	checks   int
	installs int
}

func (h *fakeHost) Config() *config.Config { return h.cfg }
func (h *fakeHost) Save(c *config.Config) (*config.Config, error) {
	data := configJSON(c)
	n, err := config.Parse(data)
	if err != nil {
		return nil, err
	}
	h.cfg = n
	h.saves++
	return n, nil
}
func (h *fakeHost) Apps() []config.AppInfo {
	return []config.AppInfo{{Name: "Editor", Path: `C:\Apps\editor.exe`, Exe: "editor.exe", Source: "running"}}
}
func (h *fakeHost) Layouts() []config.LayoutInfo           { return nil }
func (h *fakeHost) Autostart() bool                        { return false }
func (h *fakeHost) SetAutostart(bool) error                { return nil }
func (h *fakeHost) RunAction(config.Action) error          { return nil }
func (h *fakeHost) Usage() (int, map[string]int)           { return 12, map[string]int{} }
func (h *fakeHost) Activity(bool) []config.LogEntry        { return nil }
func (h *fakeHost) Version() string                        { return "0.0.0+test" }
func (h *fakeHost) ConfigPath() string                     { return `C:\Users\me\AppData\Roaming\SpaceHop\config.json` }
func (h *fakeHost) Backups() []config.Backup               { return nil }
func (h *fakeHost) Restore(string) (*config.Config, error) { return h.cfg, nil }
func (h *fakeHost) Async(work func() any, done func(any))  { done(work()) }
func (h *fakeHost) Later(ms int, f func())                 { h.later = append(h.later, f) }
func (h *fakeHost) Menu([]string, int) int                 { return -1 }
func (h *fakeHost) Confirm(string) bool                    { return true }
func (h *fakeHost) OpenFile() ([]byte, bool)               { return nil, false }
func (h *fakeHost) SaveFile(string, []byte) bool           { return true }
func (h *fakeHost) Clipboard() string                      { return "" }
func (h *fakeHost) SetClipboard(string)                    {}
func (h *fakeHost) Record(bool)                            {}
func (h *fakeHost) Invalidate()                            {}
func (h *fakeHost) UpdateInfo() config.UpdateInfo          { return h.upd }
func (h *fakeHost) CheckUpdate()                           { h.checks++ }
func (h *fakeHost) InstallUpdate()                         { h.installs++ }

type rig struct {
	app  *App
	ctx  *Ctx
	host *fakeHost
	text *fakeText
	cv   *raster.Canvas
}

func newRig(t testing.TB) *rig {
	c := config.Default()
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	h := &fakeHost{cfg: c}
	tx := &fakeText{}
	const w, hh = 2260, 1580 // the test VM's window, at 200% scaling
	return &rig{app: New(h), ctx: NewCtx(tx), host: h, text: tx, cv: &raster.Canvas{W: w, H: hh, Pix: make([]uint32, w*hh)}}
}

func (r *rig) frame() {
	r.text.drawn = r.text.drawn[:0]
	r.ctx.Begin(r.cv, 2, 0.016, false)
	r.app.Frame(r.ctx)
	r.ctx.End()
	for _, f := range r.app.TakeAfter() {
		f()
	}
}

// click presses and releases at a point given in units.
func (r *rig) click(x, y float64) {
	r.ctx.MX, r.ctx.MY = x, y
	r.frame()
	r.ctx.Pressed, r.ctx.Down = true, true
	r.frame()
	r.ctx.Released, r.ctx.Down = true, false
	r.frame()
	r.frame()
}

func (r *rig) find(s string) (TextOp, bool) {
	for _, op := range r.text.drawn {
		if op.S == s {
			return op, true
		}
	}
	return TextOp{}, false
}

// clickText clicks the middle of a piece of text that is on screen.
func (r *rig) clickText(t *testing.T, s string) {
	t.Helper()
	op, ok := r.find(s)
	if !ok {
		t.Fatalf("%q is not on screen", s)
	}
	r.click(float64(op.X0+op.X1)/4, float64(op.Y0+op.Y1)/4)
}

func TestPagesDraw(t *testing.T) {
	r := newRig(t)
	for p := range pageNames {
		r.app.Goto(p)
		r.frame()
		if len(r.text.drawn) < 10 {
			t.Fatalf("page %d drew almost nothing", p)
		}
	}
}

func TestNavigateAndToggle(t *testing.T) {
	r := newRig(t)
	r.frame()
	r.clickText(t, T("通用"))
	if r.app.page != pageGeneral {
		t.Fatalf("page %d", r.app.page)
	}
	if _, ok := r.find(T("按键时序")); !ok {
		t.Fatal("general page content missing")
	}
	// The master switch in the rail.
	r.click(12+10+19, r.ctx.H-70+17+11)
	if r.host.cfg.Enabled || r.host.saves != 1 {
		t.Fatalf("enabled=%v saves=%d", r.host.cfg.Enabled, r.host.saves)
	}
}

func TestCreateBindingFromKeyboard(t *testing.T) {
	r := newRig(t)
	r.frame()
	before := len(r.host.cfg.Bindings)
	r.clickText(t, "Q") // an unused key on the map opens the editor for Space › Q
	if r.app.draft == nil || len(r.app.draft.Keys) != 1 || r.app.draft.Keys[0] != "Q" {
		t.Fatalf("draft %+v", r.app.draft)
	}
	for i := 0; i < 60; i++ { // let the editor slide in
		r.frame()
	}
	r.clickText(t, T("打开网址"))
	r.app.draft.Action.URL = "https://example.com"
	r.frame()
	r.clickText(t, T("保存"))
	if r.app.draft != nil || len(r.host.cfg.Bindings) != before+1 {
		t.Fatalf("not saved: %d bindings, draft %v", len(r.host.cfg.Bindings), r.app.draft)
	}
	b := r.host.cfg.Bindings[before]
	if b.Action.Type != config.OpenURL || b.Keys[0] != "Q" || !b.Enabled {
		t.Fatalf("%+v", b)
	}
}

func TestRecordLeader(t *testing.T) {
	r := newRig(t)
	r.app.Goto(pageLeader)
	r.frame()
	r.app.startRec("lead:0")
	r.app.OnRecordKey("F12", true)
	if r.host.cfg.Leaders[0].Key != "F12" || r.app.rec != "" {
		t.Fatalf("%+v", r.host.cfg.Leaders[0])
	}
	// A tap leader takes a combination, or a modifier pressed alone.
	r.app.cfg.Leaders[0].Mode = config.Tap
	r.app.cfg.Leaders[0].Key = "Alt+S"
	r.app.cfg.Bindings = nil
	r.app.save(true)
	r.app.startRec("lead:0")
	r.app.OnRecordKey("LCtrl", true)
	r.app.OnRecordKey("LAlt", true)
	r.app.OnRecordKey("J", true)
	if r.host.cfg.Leaders[0].Key != "Ctrl+Alt+J" {
		t.Fatalf("%+v", r.host.cfg.Leaders[0])
	}
	r.app.OnRecordKey("J", false)
	r.app.OnRecordKey("LAlt", false)
	r.app.OnRecordKey("LCtrl", false)
	r.app.startRec("lead:0")
	r.app.OnRecordKey("RCtrl", true)
	r.app.OnRecordKey("RCtrl", false)
	if r.host.cfg.Leaders[0].Key != "RCtrl" {
		t.Fatalf("%+v", r.host.cfg.Leaders[0])
	}
}

func TestMergeImport(t *testing.T) {
	r := newRig(t)
	all := []*config.Binding{}
	for i := range r.app.cfg.Bindings {
		all = append(all, &r.app.cfg.Bindings[i])
	}
	data := makeBundle(r.app.cfg, all)
	b, full := parseImport(data)
	if b == nil || full != nil {
		t.Fatal("bundle not recognised")
	}
	n := len(r.app.cfg.Bindings)
	added, replaced, skipped := merge(r.app.cfg, b, "skip")
	if added != 0 || replaced != 0 || skipped != n {
		t.Fatalf("%d %d %d", added, replaced, skipped)
	}
	b.Bindings[0].Keys = []string{"Z", "Z"}
	b.Bindings[1].LeaderKey, b.Bindings[1].LeaderMode = "F12", config.Hold
	added, _, _ = merge(r.app.cfg, b, "skip")
	if added != 2 || len(r.app.cfg.Leaders) != 2 {
		t.Fatalf("added %d, leaders %d", added, len(r.app.cfg.Leaders))
	}
	if _, full := parseImport(configJSON(r.app.cfg)); full == nil {
		t.Fatal("full configuration not recognised")
	}
}

// A redraw of the busiest page must fit in a frame at the size and scaling
// of a large high-density window.
func TestFrameCost(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 60; i++ { // a long list
		b := config.Binding{Enabled: true, Keys: []string{string(rune('A' + i%26)), string(rune('A' + i/26))}, Action: config.Action{Type: config.OpenURL, URL: "https://example.com"}, Scope: config.Scope{Mode: "all"}}
		r.app.cfg.Bindings = append(r.app.cfg.Bindings, b)
	}
	r.app.save(true)
	r.frame()
	measure := func(name string) {
		const n = 20
		start := time.Now()
		for i := 0; i < n; i++ {
			r.frame()
		}
		per := time.Since(start) / n
		t.Logf("%s: %v per frame, %d pieces of text", name, per, len(r.text.drawn))
		if per > 12*time.Millisecond && !testing.Short() {
			t.Errorf("%s: %v per frame is too slow", name, per)
		}
	}
	measure("shortcuts page")
	r.app.openEditor(r.ctx, &r.app.cfg.Bindings[0], false)
	r.app.stopRec()
	for i := 0; i < 30; i++ {
		r.frame()
	}
	measure("editor open")
}

// The check box and the switch sit on top of a row that is itself
// clickable; each must get its own clicks.
func TestRowControls(t *testing.T) {
	r := newRig(t)
	r.frame()
	b := r.host.cfg.Bindings[0]
	// The name is also printed on the keyboard map; the list comes last.
	var op TextOp
	ok := false
	for _, o := range r.text.drawn {
		if o.S == label(&b) {
			op, ok = o, true
		}
	}
	if !ok {
		t.Fatal("first binding is not listed")
	}
	y := float64(op.Y0+op.Y1) / 4
	left, right := float64(railW+32), r.ctx.W-32
	r.click(left+22, y)
	if !r.app.sel[b.ID] || r.app.draft != nil {
		t.Fatalf("check box: selected=%v, editor open=%v", r.app.sel[b.ID], r.app.draft != nil)
	}
	if _, ok := r.find(T("删除")); !ok {
		t.Fatal("the bar for the selection did not appear")
	}
	r.click(left+22, y)
	if r.app.sel[b.ID] {
		t.Fatal("check box does not clear")
	}
	r.click(right-52+19, y)
	if r.host.cfg.Bindings[0].Enabled || r.app.draft != nil {
		t.Fatal("the switch did not turn the shortcut off")
	}
	r.click(left+400, y)
	if r.app.draft == nil {
		t.Fatal("the row itself opens the editor")
	}
}

func TestTimingFieldSaves(t *testing.T) {
	r := newRig(t)
	r.app.Goto(pageGeneral)
	r.frame()
	*r.app.num("activationMs", 0) = "8" // typing "80"
	r.frame()
	*r.app.num("activationMs", 0) = "80"
	r.frame()
	if got := r.host.cfg.Timing.ActivationMs; got != 80 {
		t.Fatalf("activationMs = %d", got)
	}
	saves := r.host.saves
	r.frame()
	r.frame()
	if r.host.saves != saves {
		t.Fatal("saved again without a change")
	}
	// Another page and back: the value is still there.
	r.app.Goto(pageAbout)
	r.frame()
	r.app.Goto(pageGeneral)
	r.frame()
	if _, ok := r.find("80"); !ok || r.app.cfg.Timing.ActivationMs != 80 {
		t.Fatal("value lost after leaving the page")
	}
	// Out of range: clamped by the configuration, shown once the field is left.
	*r.app.num("tapReplayMs", 0) = "7"
	r.frame()
	r.frame()
	r.frame()
	if got := r.host.cfg.Timing.TapReplayMs; got != 50 {
		t.Fatalf("tapReplayMs = %d", got)
	}
	if _, ok := r.find("50"); !ok {
		t.Fatal("the accepted value is not shown")
	}
}

// A key people type with keeps its quick tap when it becomes a hold
// leader, however it got there.
func TestTypingKeyKeepsTap(t *testing.T) {
	r := newRig(t)
	r.app.cfg.Leaders[0] = config.Leader{ID: "space", Mode: config.Hold, Key: "F12"}
	r.app.save(true)
	r.app.startRec("lead:0")
	r.app.OnRecordKey("Space", true)
	if l := r.host.cfg.Leaders[0]; l.Key != "Space" || !l.KeepTap {
		t.Fatalf("recording Space: %+v", l)
	}
	r.app.startRec("lead:0")
	r.app.OnRecordKey("CapsLock", true)
	r.app.cfg.Leaders[0].KeepTap = false
	r.app.cfg.Leaders[0].Mode, r.app.cfg.Leaders[0].Key = config.Tap, "Alt+S"
	r.app.cfg.Bindings = nil
	r.app.save(true)
	r.app.Goto(pageLeader)
	r.frame()
	r.clickText(t, T("按住")) // first time in hold mode for this session: the safe default
	if l := r.host.cfg.Leaders[0]; l.Mode != config.Hold || l.Key != "Space" || !l.KeepTap {
		t.Fatalf("switching to hold: %+v", l)
	}
}

func TestLeaderModeRoundTrip(t *testing.T) {
	r := newRig(t)
	r.app.Goto(pageLeader)
	r.frame()
	r.clickText(t, T("轻按"))
	if l := r.host.cfg.Leaders[0]; l.Mode != config.Tap || l.Key != "Alt+S" {
		t.Fatalf("%+v", l)
	}
	for i := 0; i < 30; i++ {
		r.frame()
	}
	r.clickText(t, T("按住"))
	if l := r.host.cfg.Leaders[0]; l.Mode != config.Hold || l.Key != "Space" || !l.KeepTap {
		t.Fatalf("back to hold lost the key: %+v", l)
	}
	for i := 0; i < 30; i++ {
		r.frame()
	}
	r.clickText(t, T("轻按"))
	if l := r.host.cfg.Leaders[0]; l.Key != "Alt+S" {
		t.Fatalf("%+v", l)
	}
}

func TestUpdateCard(t *testing.T) {
	r := newRig(t)
	r.app.Goto(pageAbout)
	for _, st := range []string{"idle", "checking", "uptodate", "error", "downloading"} {
		r.host.upd = config.UpdateInfo{State: st, Percent: 40, Err: "no route", Page: "https://example.org"}
		r.frame()
	}
	// Nothing found yet: "check now" asks the host.
	r.host.upd = config.UpdateInfo{State: "idle", Percent: -1}
	r.frame()
	r.clickText(t, T("检查更新"))
	if r.host.checks != 1 {
		t.Fatalf("checks=%d", r.host.checks)
	}
	// A newer version: the button installs it.
	r.host.upd = config.UpdateInfo{State: "available", Latest: "9.9.9", Percent: -1}
	r.frame()
	r.clickText(t, T("更新并重启"))
	if r.host.installs != 1 {
		t.Fatalf("installs=%d", r.host.installs)
	}
	// The switch turns the daily look off and saves it.
	if r.host.cfg.NoUpdateCheck {
		t.Fatal("update check should default to on")
	}
	r.host.upd = config.UpdateInfo{State: "idle", Percent: -1}
	r.frame()
	lbl, ok := r.find(T("自动检查更新"))
	if !ok {
		t.Fatal("update card incomplete")
	}
	// The switch sits at the right end of its row: sweep along the row.
	y := float64(lbl.Y0+lbl.Y1) / 4
	for x := 900.0; x < 1130 && !r.host.cfg.NoUpdateCheck; x += 8 {
		r.click(x, y)
	}
	if !r.host.cfg.NoUpdateCheck || r.host.saves == 0 {
		t.Fatalf("NoUpdateCheck=%v saves=%d", r.host.cfg.NoUpdateCheck, r.host.saves)
	}
}
