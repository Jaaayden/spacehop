package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"spacehop/internal/config"
)

type fake struct {
	log        []string
	exe        string
	fullscreen bool
	mods       uint8
	timeout    int
	hint       bool
	mouse      bool
}

func (f *fake) Inject(s []Stroke) {
	for _, k := range s {
		d := "up"
		if k.Down {
			d = "down"
		}
		f.log = append(f.log, config.KeyName(k.VK)+":"+d)
	}
}
func (f *fake) Run(b *config.Binding)   { f.log = append(f.log, "run:"+b.Label()) }
func (f *fake) Hint(d int)              { f.hint = true }
func (f *fake) HideHint()               { f.hint = false }
func (f *fake) WatchMouse(on bool)      { f.mouse = on }
func (f *fake) Context() (string, bool) { return f.exe, f.fullscreen }
func (f *fake) Mods() uint8             { return f.mods }
func (f *fake) Timeout(ms int)          { f.timeout = ms }
func (f *fake) take() string {
	s := strings.Join(f.log, " ")
	f.log = nil
	return s
}

func bind(name string, keys ...string) config.Binding {
	return config.Binding{ID: name, Enabled: true, Keys: keys, Name: name,
		Action: config.Action{Type: config.OpenApp, Path: name + ".exe"}, Scope: config.Scope{Mode: "all"}}
}

func setup(t *testing.T, bs ...config.Binding) (*Engine, *fake) {
	t.Helper()
	c := config.Default()
	c.Bindings = bs
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	f := &fake{exe: "notepad.exe"}
	return New(f, c), f
}

const (
	sp = config.VKSpace
	kA = uint32('A')
	kB = uint32('B')
	kS = uint32('S')
)

func TestTapTypesSpace(t *testing.T) {
	e, f := setup(t, bind("a", "A"))
	if !e.OnKey(sp, true, 1000) {
		t.Fatal("leader down should be swallowed")
	}
	if !e.OnKey(sp, false, 1080) {
		t.Fatal("leader up should be swallowed")
	}
	if got := f.take(); got != "Space:down Space:up" {
		t.Fatalf("got %q", got)
	}
	if f.hint || f.mouse {
		t.Fatal("hint and mouse watch must be off after release")
	}
}

func TestLongHoldWithoutActionTypesNothing(t *testing.T) {
	e, f := setup(t, bind("a", "A"))
	e.OnKey(sp, true, 1000)
	e.OnKey(sp, true, 1500) // auto-repeat
	e.OnKey(sp, false, 2000)
	if got := f.take(); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestHoldRunsAction(t *testing.T) {
	e, f := setup(t, bind("a", "A"))
	e.OnKey(sp, true, 1000)
	if !e.OnKey(kA, true, 1100) || !e.OnKey(kA, false, 1150) {
		t.Fatal("trigger key must be swallowed, down and up")
	}
	e.OnKey(sp, false, 1200)
	if got := f.take(); got != "run:a" {
		t.Fatalf("got %q", got)
	}
}

func TestTriggerHeldRunsOnce(t *testing.T) {
	e, f := setup(t, bind("a", "A"))
	e.OnKey(sp, true, 1000)
	e.OnKey(kA, true, 1100)
	e.OnKey(kA, true, 1600)
	e.OnKey(kA, true, 1630)
	if got := f.take(); got != "run:a" {
		t.Fatalf("got %q", got)
	}
}

func TestKeystrokeRepeats(t *testing.T) {
	b := bind("zoom", "=")
	b.Action = config.Action{Type: config.Keystroke, Chord: "Ctrl+="}
	e, f := setup(t, b)
	eq, _ := config.VK("=")
	e.OnKey(sp, true, 1000)
	e.OnKey(eq, true, 1100)
	e.OnKey(eq, true, 1600)
	e.OnKey(eq, false, 1650)
	e.OnKey(eq, true, 1700)
	if got := f.take(); got != "run:zoom run:zoom run:zoom" {
		t.Fatalf("got %q", got)
	}
}

func TestSequence(t *testing.T) {
	e, f := setup(t, bind("sa", "S", "A"), bind("sb", "S", "B"))
	e.OnKey(sp, true, 1000)
	e.OnKey(kS, true, 1100)
	e.OnKey(kS, false, 1150)
	e.OnKey(kB, true, 1300)
	e.OnKey(kB, false, 1350)
	// The leader is still held: a second shortcut works without releasing.
	e.OnKey(kS, true, 1500)
	e.OnKey(kA, true, 1600)
	e.OnKey(sp, false, 1700)
	if got := f.take(); got != "run:sb run:sa" {
		t.Fatalf("got %q", got)
	}
}

func TestFastTypingRollover(t *testing.T) {
	e, f := setup(t, bind("a", "A"))
	// "x", then Space and A overlapping, as in fast typing.
	e.OnKey('X', true, 900)
	e.OnKey('X', false, 950)
	e.OnKey(sp, true, 1000)
	if !e.OnKey(kA, true, 1120) {
		t.Fatal("the key is re-sent after the space, so the original is swallowed")
	}
	if e.OnKey(sp, false, 1150) || e.OnKey(kA, false, 1180) {
		t.Fatal("releases pass through after rollover")
	}
	if got := f.take(); got != "Space:down A:down" {
		t.Fatalf("got %q", got)
	}
}

func TestUnboundKeyTypesBoth(t *testing.T) {
	e, f := setup(t, bind("a", "A"))
	e.OnKey(sp, true, 1000)
	e.OnKey(kB, true, 1300)
	if got := f.take(); got != "Space:down B:down" {
		t.Fatalf("got %q", got)
	}
}

func TestModifierHeldBypasses(t *testing.T) {
	e, f := setup(t, bind("a", "A"))
	f.mods = config.ModShift
	if e.OnKey(sp, true, 1000) || e.OnKey(sp, true, 1030) || e.OnKey(kA, true, 1100) || e.OnKey(sp, false, 1200) {
		t.Fatal("Shift+Space etc. must pass through untouched")
	}
	f.mods = 0
	if !e.OnKey(sp, true, 2000) {
		t.Fatal("works again after release")
	}
}

func TestDisabledAppAndFullscreen(t *testing.T) {
	e, f := setup(t, bind("a", "A"))
	e.cfg.DisabledApps = []string{"game.exe"}
	e.SetConfig(e.cfg)
	f.exe = "game.exe"
	if e.OnKey(sp, true, 1000) {
		t.Fatal("excluded app")
	}
	e.OnKey(sp, false, 1100)
	f.exe, f.fullscreen = "other.exe", true
	if e.OnKey(sp, true, 2000) {
		t.Fatal("full-screen game")
	}
}

func TestScope(t *testing.T) {
	only := bind("only-word", "C")
	only.Scope = config.Scope{Mode: "only", Apps: []string{"WINWORD.EXE"}}
	e, f := setup(t, only)
	e.OnKey(sp, true, 1000)
	e.OnKey('C', true, 1300)
	if got := f.take(); got != "Space:down C:down" {
		t.Fatalf("out of scope should type normally, got %q", got)
	}
	e.OnKey('C', false, 1350)
	e.OnKey(sp, false, 1400)
	f.exe = "winword.exe"
	e.OnKey(sp, true, 2000)
	e.OnKey('C', true, 2300)
	if got := f.take(); got != "run:only-word" {
		t.Fatalf("got %q", got)
	}
}

func TestEscCancels(t *testing.T) {
	e, f := setup(t, bind("sa", "S", "A"))
	e.OnKey(sp, true, 1000)
	e.OnKey(kS, true, 1200)
	e.OnKey(config.VKEsc, true, 1300)
	if !e.OnKey(kA, true, 1400) {
		t.Fatal("keys after cancel are swallowed until the leader is released")
	}
	e.OnKey(sp, false, 1500)
	if got := f.take(); got != "" {
		t.Fatalf("got %q", got)
	}
	if e.OnKey(kA, true, 1600) {
		t.Fatal("typing works after release")
	}
}

func TestMouseDragPassesSpace(t *testing.T) {
	e, f := setup(t, bind("a", "A"))
	e.OnKey(sp, true, 1000)
	if !f.mouse {
		t.Fatal("mouse watch should be on while undecided")
	}
	if !e.OnMouseDown(config.VKLButton) {
		t.Fatal("click is re-sent after the space")
	}
	if got := f.take(); got != "Space:down VK01:down" {
		t.Fatalf("got %q", got)
	}
	if f.mouse || e.OnKey(sp, false, 1500) {
		t.Fatal("space release passes through")
	}
}

func TestTickWrap(t *testing.T) {
	e, f := setup(t, bind("a", "A"))
	e.OnKey(sp, true, 0xFFFFFFF0)
	e.OnKey(sp, false, 0x00000020)
	if got := f.take(); got != "Space:down Space:up" {
		t.Fatalf("got %q", got)
	}
}

// Scale: the per-key cost must not grow with the number of bindings.
func TestScale(t *testing.T) {
	keys := "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var bs []config.Binding
	for i := 0; i < config.MaxBindings; i++ {
		k := []string{string(keys[i%36]), string(keys[(i/36)%36]), string(keys[(i/1296)%36])}
		bs = append(bs, bind(fmt.Sprintf("b%d", i), k...))
	}
	e, f := setup(t, bs...)
	f.exe = "x.exe"
	start := time.Now()
	const rounds = 20000
	for i := 0; i < rounds; i++ {
		t0 := uint32(i * 1000)
		e.OnKey(sp, true, t0)
		e.OnKey(kA, true, t0+300)
		e.OnKey(kA, false, t0+320)
		e.OnKey(kB, true, t0+340)
		e.OnKey(kB, false, t0+360)
		e.OnKey(kA, true, t0+380)
		e.OnKey(kA, false, t0+400)
		e.OnKey(sp, false, t0+420)
		f.log = f.log[:0]
	}
	per := time.Since(start) / (rounds * 8)
	t.Logf("%d bindings: %v per key event", len(bs), per)
	if per > 200*time.Microsecond {
		t.Fatalf("too slow: %v per event", per)
	}
}

// ---- more leaders ----

func setupCfg(t *testing.T, c *config.Config) (*Engine, *fake) {
	t.Helper()
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	f := &fake{exe: "notepad.exe"}
	return New(f, c), f
}

func on(b config.Binding, leader string) config.Binding { b.Leader = leader; return b }

func TestDedicatedHoldLeader(t *testing.T) {
	c := config.Default()
	c.Leaders = append(c.Leaders, config.Leader{ID: "f12", Mode: config.Hold, Key: "F12"})
	c.Bindings = []config.Binding{on(bind("a", "A"), "f12"), bind("space-a", "A")}
	e, f := setupCfg(t, c)
	f12, _ := config.VK("F12")
	e.OnKey('X', true, 990) // just typed: a dedicated key needs no typing guard
	e.OnKey(f12, true, 1000)
	if !e.OnKey(kA, true, 1005) {
		t.Fatal("ready immediately")
	}
	e.OnKey(kA, false, 1010)
	e.OnKey(f12, false, 1020)
	e.OnKey(f12, true, 2000)
	if !e.OnKey(kB, true, 2100) { // unbound: swallowed, never typed
		t.Fatal("a dedicated leader never types")
	}
	e.OnKey(f12, false, 2200)
	if got := f.take(); got != "run:a" {
		t.Fatalf("got %q", got)
	}
}

func TestTapChordLeader(t *testing.T) {
	c := config.Default()
	c.Leaders = []config.Leader{{ID: "t", Mode: config.Tap, Key: "Alt+S"}}
	c.Bindings = []config.Binding{bind("fd", "F", "D"), bind("x", "X")}
	e, f := setupCfg(t, c)
	if e.OnKey(kS, true, 1000) {
		t.Fatal("plain S is typing")
	}
	e.OnKey(kS, false, 1010)
	f.mods = config.ModAlt
	if !e.OnKey(kS, true, 2000) || !e.OnKey(kS, false, 2050) {
		t.Fatal("the combination is swallowed")
	}
	f.mods = 0
	if f.timeout == 0 || !e.Active() {
		t.Fatal("waiting for a trigger with a timeout")
	}
	e.OnKey('F', true, 2500)
	e.OnKey('F', false, 2550)
	if !e.OnKey('D', true, 2700) {
		t.Fatal("trigger swallowed")
	}
	if got := f.take(); got != "run:fd" || e.Active() {
		t.Fatalf("a tap sequence ends after its action; got %q", got)
	}
	// Timeout and stray keys end the sequence.
	f.mods = config.ModAlt
	e.OnKey(kS, true, 5000)
	f.mods = 0
	e.OnTimeout()
	if e.Active() || e.OnKey('X', true, 9000) {
		t.Fatal("timed out: typing again")
	}
	f.mods = config.ModAlt
	e.OnKey(kS, true, 10000)
	f.mods = 0
	if !e.OnKey('Q', true, 10100) || e.Active() {
		t.Fatal("a stray key is swallowed and cancels")
	}
	if got := f.take(); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestModifierTapLeader(t *testing.T) {
	c := config.Default()
	c.Leaders = []config.Leader{{ID: "m", Mode: config.Tap, Key: "RCtrl"}}
	only := bind("alone")
	c.Bindings = []config.Binding{only, bind("a", "A")}
	e, f := setupCfg(t, c)
	const rctrl, lctrl = 0xA3, 0xA2
	// Ctrl+C is not a tap.
	e.OnKey(rctrl, true, 1000)
	e.OnKey('C', true, 1050)
	e.OnKey('C', false, 1080)
	if e.OnKey(rctrl, false, 1100) || e.Active() {
		t.Fatal("a modifier used with another key is not a leader tap")
	}
	// Held too long.
	e.OnKey(rctrl, true, 2000)
	if e.OnKey(rctrl, false, 2900) || e.Active() {
		t.Fatal("a long press is not a tap")
	}
	// Ctrl+click.
	e.OnKey(rctrl, true, 3000)
	e.OnMouseDown(config.VKLButton)
	if e.OnKey(rctrl, false, 3100) || e.Active() {
		t.Fatal("a click cancels the tap")
	}
	// The other side is not the leader.
	e.OnKey(lctrl, true, 4000)
	if e.OnKey(lctrl, false, 4100) || e.Active() {
		t.Fatal("left Ctrl is not the leader")
	}
	f.take()
	e.OnKey(rctrl, true, 5000)
	if !e.OnKey(rctrl, false, 5100) || !e.Active() {
		t.Fatal("a clean tap starts the sequence")
	}
	if got := f.take(); got != "VKE8:down VKE8:up RCtrl:up run:alone" {
		t.Fatalf("got %q", got)
	}
	e.OnKey(kA, true, 5300)
	if got := f.take(); got != "run:a" {
		t.Fatalf("got %q", got)
	}
}

func TestAfterRun(t *testing.T) {
	stay := bind("left", "W", "H")
	stay.After = "stay"
	stop := bind("once", "O")
	stop.After = "stop"
	e, f := setup(t, stay, bind("right", "W", "L"), stop, bind("a", "A"))
	e.OnKey(sp, true, 1000)
	e.OnKey('W', true, 1200)
	e.OnKey('W', false, 1250)
	e.OnKey('H', true, 1300)
	e.OnKey('H', false, 1350)
	e.OnKey('H', true, 1400) // still at the W level
	e.OnKey('H', false, 1450)
	e.OnKey('L', true, 1500) // back to the top after this one
	e.OnKey('L', false, 1550)
	e.OnKey('O', true, 1600)
	e.OnKey('O', false, 1650)
	if !e.OnKey(kA, true, 1700) { // stopped: ignored until release
		t.Fatal("swallowed after stop")
	}
	e.OnKey(sp, false, 1800)
	if got := f.take(); got != "run:left run:left run:right run:once" {
		t.Fatalf("got %q", got)
	}
}

func TestPrefixAction(t *testing.T) {
	e, f := setup(t, bind("g", "G"), bind("gh", "G", "H"))
	e.OnKey(sp, true, 1000)
	e.OnKey('G', true, 1200)
	e.OnKey('G', false, 1250)
	e.OnKey('H', true, 1300)
	if got := f.take(); got != "run:g run:gh" {
		t.Fatalf("got %q", got)
	}
}

func TestSearch(t *testing.T) {
	e, f := setup(t, bind("alpha", "A"), bind("beta", "F", "B"), bind("gamma", "F", "G"))
	slash, _ := config.VK("/")
	e.OnKey(sp, true, 1000)
	if !e.OnKey(slash, true, 1300) {
		t.Fatal("search key swallowed")
	}
	v, _ := e.View()
	if !v.Search || len(v.Items) != 3 {
		t.Fatalf("all shortcuts listed at first: %+v", v)
	}
	e.OnKey(sp, false, 1400) // search stays open without the leader
	for _, k := range "MM" {
		e.OnKey(uint32(k), true, 1500)
		e.OnKey(uint32(k), false, 1510)
	}
	v, _ = e.View()
	if v.Query != "mm" || len(v.Items) != 1 || v.Items[0].Label != "gamma" || strings.Join(v.Items[0].Keys, " ") != "Space F G" {
		t.Fatalf("got %+v", v)
	}
	e.OnKey(config.VKBack, true, 1600)
	e.OnKey(config.VKBack, true, 1610)
	e.OnKey(0x28, true, 1700) // Down
	e.OnKey(config.VKEnter, true, 1800)
	if got := f.take(); got != "run:beta" || e.Active() || f.hint {
		t.Fatalf("got %q", got)
	}
	if e.OnKey(kA, true, 2000) {
		t.Fatal("typing again")
	}
}

func TestLegacyConfig(t *testing.T) {
	c, err := config.Parse([]byte(`{"version":1,"enabled":true,"leader":{"key":"capslock","keepTap":false},
		"bindings":[{"keys":["a"],"enabled":true,"action":{"type":"openURL","url":"example.com"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Leaders) != 1 || c.Leaders[0].Key != "CapsLock" || c.Leaders[0].KeepTap || c.Bindings[0].Keys[0] != "A" {
		t.Fatalf("%+v", c.Leaders)
	}
}
