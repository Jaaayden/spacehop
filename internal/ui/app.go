package ui

import (
	"strings"

	"spacehop/internal/config"
	"spacehop/internal/i18n"
	"spacehop/internal/icon"
	"spacehop/internal/raster"
)

// Host is what the settings window needs from the running program and from
// the window it lives in.
type Host interface {
	Config() *config.Config
	// Save validates, stores and applies a configuration and returns it as
	// it is now in effect.
	Save(*config.Config) (*config.Config, error)
	Apps() []config.AppInfo
	Layouts() []config.LayoutInfo
	Autostart() bool
	SetAutostart(bool) error
	RunAction(config.Action) error
	Usage() (total int, counts map[string]int)
	Activity(on bool) []config.LogEntry
	Version() string
	ConfigPath() string
	Backups() []config.Backup
	Restore(name string) (*config.Config, error)

	// Async runs work off the window's thread, then done on it.
	Async(work func() any, done func(any))
	// Later runs f on the window's thread after ms.
	Later(ms int, f func())
	// Menu shows a popup menu at the pointer and returns the choice or -1.
	Menu(items []string, checked int) int
	Confirm(text string) bool
	OpenFile() ([]byte, bool)
	SaveFile(name string, data []byte) bool
	Clipboard() string
	SetClipboard(string)
	// Record turns key capture on or off; keys arrive through OnRecordKey.
	Record(on bool)
	Invalidate()
}

const (
	pageKeys = iota
	pageLeader
	pageHint
	pageGeneral
	pageActivity
	pageAbout
)

var pageNames = []string{i18n.N("快捷键"), i18n.N("引导键"), i18n.N("提示窗"), i18n.N("通用"), i18n.N("活动"), i18n.N("关于")}
var pageIcons = []string{IconKeys, IconLeader, IconHint, IconGeneral, IconActivity, IconAbout}

const railW = 216

type App struct {
	H   Host
	cfg *config.Config // working copy; saved through H

	page   int
	leader string
	path   []string
	q      string
	sel    map[string]bool
	shown  int

	draft    *config.Binding
	draftNew bool
	rectS    [4]string
	rec      string // which recorder is live: "keys", "chord", "search", "lead:N"
	recMods  []string
	recLone  string

	dialog   string // "apps", "import", "merge", "full"
	pickFor  string
	pickQ    string
	apps     []config.AppInfo
	appsBusy bool
	layouts  []config.LayoutInfo
	incoming *bundle
	full     *config.Config

	toast    string
	toastErr bool
	toastSeq int
	after    []func()

	usageTotal int
	usage      map[string]int
	autostart  bool
	backups    []config.Backup
	backupsOK  bool
	logOn      bool
	logEntries []config.LogEntry
	logSeq     int
	numS       map[string]*string
	issues     map[string]issue
	iconPx     int
	iconPix    []uint8
	dark       bool
	base       []uint32 // the page as drawn under a modal layer
	baseOK     bool
	baseDark   bool
}

func New(h Host) *App {
	a := &App{H: h, sel: map[string]bool{}, shown: 200, numS: map[string]*string{}}
	a.reload()
	return a
}

// reload takes the configuration in effect as the working copy.
func (a *App) reload() {
	a.baseOK = false
	a.cfg = clone(a.H.Config())
	a.usageTotal, a.usage = a.H.Usage()
	a.autostart = a.H.Autostart()
	a.issues = analyse(a.cfg)
	a.numS = map[string]*string{}
}

// Refresh is called when the window is shown again or the configuration
// changed outside the window.
func (a *App) Refresh() {
	if a.draft == nil {
		a.reload()
	}
}

// OnDeactivate is called when the window stops being the active one.
func (a *App) OnDeactivate() { a.stopRec() }

// SetDark tells the window whether the system uses dark colours.
func (a *App) SetDark(dark bool) { a.dark = dark }

// Dark reports whether the window is drawn dark, for the title bar.
func (a *App) Dark() bool {
	switch a.cfg.Theme {
	case "dark":
		return true
	case "light":
		return false
	}
	return a.dark
}

// Goto switches page (used by the self-test); it returns the page count.
func (a *App) Goto(page int) int {
	if page >= 0 && page < len(pageNames) {
		a.page = page
	}
	return len(pageNames)
}

// TestOpenEditor opens the editor on the first binding (self-test).
func (a *App) TestOpenEditor(c *Ctx) {
	if len(a.cfg.Bindings) > 0 {
		a.openEditor(c, &a.cfg.Bindings[0], false)
		a.stopRec()
	}
}

// TestDraftArgs returns the arguments being edited (self-test).
func (a *App) TestDraftArgs() string {
	if a.draft == nil {
		return ""
	}
	return a.draft.Action.Args
}

// TakeAfter returns the actions queued during the frame. The platform runs
// them once the frame is over, because they may open menus or dialogs.
func (a *App) TakeAfter() []func() {
	f := a.after
	a.after = nil
	return f
}

func (a *App) defer_(f func()) { a.after = append(a.after, f) }

func (a *App) say(msg string, err bool) {
	a.toast, a.toastErr = msg, err
	a.toastSeq++
	seq := a.toastSeq
	ms := 1800
	if err {
		ms = 4500
	}
	a.H.Later(ms, func() {
		if a.toastSeq == seq {
			a.toast = ""
			a.H.Invalidate()
		}
	})
}

// save stores the working copy; on failure the working copy is put back.
func (a *App) save(quiet bool) bool {
	a.baseOK = false
	c, err := a.H.Save(a.cfg)
	if err != nil {
		a.say(err.Error(), true)
		a.cfg = clone(a.H.Config())
		a.issues = analyse(a.cfg)
		return false
	}
	a.cfg = clone(c)
	a.issues = analyse(a.cfg)
	a.usageTotal, a.usage = a.H.Usage()
	if !quiet {
		a.say(T("已保存"), false)
	}
	return true
}

func (a *App) leaderOf(b *config.Binding) *config.Leader { return a.cfg.LeaderOf(b) }

func (a *App) curLeader() *config.Leader {
	for i := range a.cfg.Leaders {
		if a.cfg.Leaders[i].ID == a.leader {
			return &a.cfg.Leaders[i]
		}
	}
	return &a.cfg.Leaders[0]
}

func (a *App) modal() bool { return a.draft != nil || a.dialog != "" }

// Frame describes and draws the whole window.
func (a *App) Frame(c *Ctx) {
	c.P = &Light
	if a.Dark() {
		c.P = &Dark
	}
	p := c.P
	c.Fill(Rect{0, 0, c.W, c.H}, 0, raster.RGB(p.Bg))
	if c.Key == "Esc" {
		switch {
		case a.rec != "":
			a.stopRec()
		case a.dialog != "":
			a.dialog = ""
		case a.draft != nil:
			a.closeEditor(c)
		case len(a.sel) > 0:
			a.sel = map[string]bool{}
		}
		c.Blur()
	}
	// Under an open editor or dialog the page does not change, so it is
	// drawn once and kept as a picture.
	modal := a.modal()
	if !modal {
		a.baseOK = false
	}
	if modal && a.baseOK && len(a.base) == len(c.C.Pix) && a.baseDark == p.Dark {
		if !c.Dry {
			copy(c.C.Pix, a.base)
		}
		a.drawer(c)
		a.dialogs(c)
		a.toasts(c)
		return
	}
	if modal {
		c.inputOff++
	}
	a.rail(c)
	view := Rect{railW, 0, c.W - railW, c.H}
	switch a.page {
	case pageKeys:
		a.pageKeys(c, view)
	case pageLeader:
		a.pageLeader(c, view)
	case pageHint:
		a.pageHint(c, view)
	case pageGeneral:
		a.pageGeneral(c, view)
	case pageActivity:
		a.pageActivity(c, view)
	case pageAbout:
		a.pageAbout(c, view)
	}
	c.Flush()
	if modal {
		c.inputOff--
		if !c.Dry && !c.Animating { // settled: keep it
			a.base = append(a.base[:0], c.C.Pix...)
			a.baseOK, a.baseDark = true, p.Dark
		}
	}
	a.drawer(c)
	a.dialogs(c)
	a.toasts(c)
	if c.Tip != "" && !a.modal() {
		w := c.TextW(FSmall, c.Tip) + 18
		r := Rect{c.tipRect.X, c.tipRect.Y + c.tipRect.H + 6, w, 26}
		if r.X+w > c.W-8 {
			r.X = c.W - 8 - w
		}
		c.Fill(r, 7, raster.RGB(p.Text))
		c.Text(c.Tip, FSmall, r, p.Bg, Center)
	}
}

func (a *App) rail(c *Ctx) {
	p := c.P
	c.Fill(Rect{0, 0, railW, c.H}, 0, raster.RGB(p.Surface))
	c.Fill(Rect{railW - 1, 0, 1, c.H}, 0, raster.RGB(p.Line))
	// Logo.
	if px := int(c.px(34)); px != a.iconPx {
		a.iconPx, a.iconPix = px, icon.Render(px, false).Pix
	}
	c.Image(a.iconPix, a.iconPx, a.iconPx, 20, 20)
	c.Text("SpaceHop", FBold, Rect{64, 19, 140, 20}, p.Text, Left)
	c.Text("v"+strings.SplitN(a.H.Version(), "+", 2)[0], FSmall, Rect{64, 38, 140, 16}, p.Muted, Left)

	y0 := 76.0
	py := c.Anim("nav", float64(a.page))
	pill := Rect{12, y0 + py*38, railW - 24, 36}
	c.Box(pill, 9, raster.RGB(p.Raised), raster.RGB(p.Line))
	c.Fill(Rect{12, pill.Y + 9, 3, 18}, 1.5, raster.RGB(p.Accent))
	for i, name := range pageNames {
		r := Rect{12, y0 + float64(i)*38, railW - 24, 36}
		id := "nav/" + itoa(i)
		if c.Click(id, r) && a.page != i {
			a.page = i
			a.sel = map[string]bool{}
			a.stopRec()
			c.Blur()
			if i != pageActivity {
				a.logSeq++
			}
			if i == pageGeneral {
				a.backupsOK = false
			}
		}
		col, f := p.Muted, FText
		if i == a.page {
			col, f = p.Text, FBold
		} else if c.hot == id {
			col = p.Text
		}
		c.Text(pageIcons[i], FIcon, Rect{r.X + 10, r.Y, 22, r.H}, col, Center)
		c.Text(T(name), f, Rect{r.X + 42, r.Y, r.W - 80, r.H}, col, Left)
		if i == pageKeys {
			c.Text(itoa(len(a.cfg.Bindings)), FSmall, Rect{r.X, r.Y, r.W - 10, r.H}, p.Faint, Right)
		}
	}
	// Master switch.
	box := Rect{12, c.H - 70, railW - 24, 56}
	c.Box(box, 12, raster.RGB(p.Raised), raster.RGB(p.Line))
	if c.Toggle("power", box.X+10, box.Y+17, a.cfg.Enabled) {
		a.cfg.Enabled = !a.cfg.Enabled
		a.save(true)
	}
	state := T("已启用")
	if !a.cfg.Enabled {
		state = T("已暂停")
	}
	c.Text(state, FBold, Rect{box.X + 58, box.Y + 9, 120, 20}, p.Text, Left)
	c.Text(i18n.F("已运行 %d 次", a.usageTotal), FSmall, Rect{box.X + 58, box.Y + 28, 130, 16}, p.Muted, Left)
}

// header draws a page title and returns the y below it.
func (a *App) header(c *Ctx, view Rect, title, sub string) float64 {
	c.Text(title, FTitle, Rect{view.X + 32, view.Y + 24, view.W - 64, 30}, c.P.Text, Left)
	c.Text(sub, FText, Rect{view.X + 32, view.Y + 56, view.W - 64, 20}, c.P.Muted, Left)
	return view.Y + 94
}

func (a *App) toasts(c *Ctx) {
	t := c.Anim("toast", b2f(a.toast != ""))
	if t < 0.02 || a.toast == "" {
		return
	}
	p := c.P
	w := c.TextW(FText, a.toast) + 34
	if w > c.W-80 {
		w = c.W - 80
	}
	r := Rect{(c.W - w) / 2, c.H - 30 - 38*t, w, 38}
	bg, fg := p.Text, p.Bg
	if a.toastErr {
		bg, fg = p.Danger, 0xFFFFFF
	}
	c.Shadow(Rect{r.X, r.Y + 4, r.W, r.H}, 11, 14, raster.RGB(0).Alpha(0.25*t))
	c.Fill(r, 11, raster.RGB(bg).Alpha(t))
	if t > 0.6 {
		c.Text(a.toast, FText, r, fg, Center)
	}
	c.Flush()
}

// ---- key recording ----

func isMod(k string) bool {
	switch strings.TrimLeft(k, "LR") {
	case "Shift", "Ctrl", "Alt", "Win":
		return k != "L" && k != "R"
	}
	return false
}

func genericMod(k string) string {
	if k == "RWin" {
		return "Win"
	}
	if len(k) > 1 && (k[0] == 'L' || k[0] == 'R') && isMod(k) {
		return k[1:]
	}
	return k
}

func (a *App) startRec(id string) {
	a.rec, a.recMods, a.recLone = id, nil, ""
	if id == "keys" && a.draft != nil {
		a.draft.Keys = []string{}
	}
	a.H.Record(true)
}

func (a *App) stopRec() {
	if a.rec != "" {
		a.rec = ""
		a.H.Record(false)
	}
}

// OnRecordKey receives every key while a recorder is live. Names are
// SpaceHop key names; modifiers arrive with their side ("LCtrl").
func (a *App) OnRecordKey(key string, down bool) {
	if a.rec == "" || key == "" {
		return
	}
	defer a.H.Invalidate()
	if isMod(key) {
		if down {
			if len(a.recMods) == 0 {
				a.recLone = key
			} else {
				a.recLone = ""
			}
			for _, m := range a.recMods {
				if m == key {
					return
				}
			}
			a.recMods = append(a.recMods, key)
			return
		}
		lone := a.recLone == key && len(a.recMods) == 1
		for i, m := range a.recMods {
			if m == key {
				a.recMods = append(a.recMods[:i], a.recMods[i+1:]...)
				break
			}
		}
		// A modifier pressed and released alone: a single-modifier leader.
		if lone && strings.HasPrefix(a.rec, "lead:") {
			if l := a.recLeader(); l != nil && l.Mode == config.Tap {
				l.Key = key
				if key == "LWin" {
					l.Key = "Win"
				}
				a.stopRec()
				a.save(true)
			}
		}
		return
	}
	a.recLone = ""
	if !down {
		return
	}
	if key == "Esc" {
		a.stopRec()
		return
	}
	chord := func() string {
		var parts []string
		for _, want := range []string{"Ctrl", "Shift", "Alt", "Win"} {
			for _, m := range a.recMods {
				if genericMod(m) == want {
					parts = append(parts, want)
					break
				}
			}
		}
		return strings.Join(append(parts, key), "+")
	}
	switch {
	case a.rec == "keys" && a.draft != nil:
		l := a.leaderOf(a.draft)
		if (l.Mode == config.Hold && key == l.Key) || (key == "Backspace" && len(a.draft.Keys) > 0) {
			a.say(T("这个键不能用作触发键"), true)
			return
		}
		a.draft.Keys = append(a.draft.Keys, key)
		if len(a.draft.Keys) >= config.MaxKeys {
			a.stopRec()
		}
	case a.rec == "chord" && a.draft != nil:
		a.draft.Action.Chord = chord()
		a.stopRec()
	case a.rec == "search":
		if key == "Enter" || key == "Backspace" {
			return
		}
		a.cfg.Hint.SearchKey = key
		a.stopRec()
		a.save(true)
	case strings.HasPrefix(a.rec, "lead:"):
		l := a.recLeader()
		if l == nil {
			a.stopRec()
			return
		}
		if l.Mode == config.Hold {
			l.Key = key
		} else {
			ch := chord()
			if !strings.Contains(ch, "+") {
				a.say(T("轻按型需要组合键（如 Alt+S），或单独按一下某个修饰键"), true)
				return
			}
			l.Key = ch
		}
		a.stopRec()
		a.save(true)
	}
}

func (a *App) recLeader() *config.Leader {
	i := 0
	for _, ch := range a.rec[len("lead:"):] {
		i = i*10 + int(ch-'0')
	}
	if i < len(a.cfg.Leaders) {
		return &a.cfg.Leaders[i]
	}
	return nil
}

// recorder draws a key-recording box and handles clicks on it.
// draw paints the current value at (x, y) and returns its width.
func (a *App) recorder(c *Ctx, id string, r Rect, empty string, canClear bool, draw func(x, y float64) float64) (cleared bool) {
	p := c.P
	live := a.rec == id
	fill, line := raster.RGB(p.Raised), raster.RGB(p.Line2)
	if live {
		fill, line = raster.RGB(p.AccentSoft), raster.RGB(p.AccentText)
	}
	c.Box(r, 10, fill, line)
	w := draw(r.X+10, r.Y+(r.H-26)/2)
	if w <= 0 && !live {
		c.Text(empty, FText, Rect{r.X + 12, r.Y, r.W - 24, r.H}, p.Muted, Left)
	}
	hint := T("点这里录制")
	if live {
		hint = T("现在按键… Esc 结束")
	}
	hw := c.TextW(FSmall, hint)
	if canClear && !live && w > 0 {
		if ok, _ := c.Button(id+"/clear", r.X+r.W-c.BtnW("", T("清除"))-4, r.Y+(r.H-34)/2, "", T("清除"), BtnGhost, false); ok {
			return true
		}
	} else if r.W-w-30 > hw {
		c.Text(hint, FSmall, Rect{r.X, r.Y, r.W - 12, r.H}, p.Muted, Right)
	}
	if c.Click(id, r) {
		if live {
			a.stopRec()
		} else {
			c.Blur()
			a.startRec(id)
		}
	}
	return false
}
