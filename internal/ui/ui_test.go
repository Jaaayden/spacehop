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
