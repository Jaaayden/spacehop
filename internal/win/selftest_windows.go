package win

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"spacehop/internal/config"
)

// The self-test runs the real program on a real Windows desktop: it presses
// keys through the system, watches what reaches other programs, checks the
// hint window and exercises the actions against Notepad. CI runs it on
// every build; the report and screenshots are kept as build artifacts.

type selfTest struct {
	a   *App
	out string

	mu     sync.Mutex
	seen   []string // key events that got past SpaceHop, in order
	ran    []string // shortcuts the engine ran
	report []string
	failed int
}

var (
	theTest    *selfTest
	sinkProcCB = syscall.NewCallback(sinkProc)
)

// sinkProc is a second keyboard hook, installed before the real one, so it
// is called after it: it sees exactly what other programs would get.
func sinkProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) == 0 {
		k := (*kbdllHook)(ptr(lParam))
		s := config.KeyName(k.VK)
		if wParam == wmKeyDown || wParam == wmSysKeyDown {
			s += "↓"
		} else {
			s += "↑"
		}
		if k.Extra == injectMagic {
			s = "*" + s // synthesised by SpaceHop
		}
		theTest.mu.Lock()
		theTest.seen = append(theTest.seen, s)
		theTest.mu.Unlock()
	}
	r, _, _ := pCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
}

func (t *selfTest) take() (seen, ran string) {
	time.Sleep(120 * time.Millisecond)
	t.mu.Lock()
	defer t.mu.Unlock()
	seen, ran = strings.Join(t.seen, " "), strings.Join(t.ran, ",")
	t.seen, t.ran = nil, nil
	return
}

func (t *selfTest) logf(status, name, format string, args ...any) {
	line := fmt.Sprintf("%-4s %-28s %s", status, name, fmt.Sprintf(format, args...))
	t.mu.Lock()
	t.report = append(t.report, line)
	if status == "FAIL" {
		t.failed++
	}
	t.mu.Unlock()
}

func (t *selfTest) check(name string, ok bool, format string, args ...any) bool {
	if ok {
		t.logf("PASS", name, format, args...)
	} else {
		t.logf("FAIL", name, format, args...)
	}
	return ok
}

// press sends one key event the way a keyboard would (no SpaceHop mark).
func press(vk uint32, down bool) {
	scan, _, _ := pMapVirtualKey.Call(uintptr(vk), 0)
	in := keybdInput{Type: inputKeyboard, VK: uint16(vk), Scan: uint16(scan)}
	if config.IsExtended(vk) {
		in.Flags |= keyExtended
	}
	if !down {
		in.Flags |= keyUp
	}
	pSendInput.Call(1, uintptr(unsafe.Pointer(&in)), inputSize)
}

func wait(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

func tap(vk uint32) {
	press(vk, true)
	wait(25)
	press(vk, false)
	wait(25)
}

func vkOf(name string) uint32 { v, _ := config.VK(name); return v }

func testConfig() *config.Config {
	c := config.Default()
	c.Timing.HintDelayMs = 250
	c.Leaders = []config.Leader{
		{ID: "space", Mode: config.Hold, Key: "Space", KeepTap: true},
		{ID: "f9", Mode: config.Hold, Key: "F9"},
		{ID: "chord", Mode: config.Tap, Key: "Ctrl+Alt+J"},
		{ID: "mod", Mode: config.Tap, Key: "RCtrl"},
	}
	all := config.Scope{Mode: "all"}
	b := func(leader, name string, a config.Action, keys ...string) config.Binding {
		return config.Binding{ID: config.NewID(), Enabled: true, Leader: leader, Name: name, Keys: keys, Action: a, Scope: all}
	}
	c.Bindings = []config.Binding{
		b("", "Notepad", config.Action{Type: config.OpenApp, Path: "notepad.exe"}, "N"),
		b("", "Browser", config.Action{Type: config.OpenURL, URL: "https://example.com"}, "B"),
		b("", "", config.Action{Type: config.OpenFile, Path: `%USERPROFILE%\Downloads`}, "F", "D"),
		b("", "", config.Action{Type: config.OpenFile, Path: `%USERPROFILE%\Documents`}, "F", "O"),
		b("", "", config.Action{Type: config.Window, Window: "left"}, "W", "H"),
		b("", "", config.Action{Type: config.Window, Window: "right"}, "W", "L"),
		b("", "", config.Action{Type: config.Window, Window: "maximize"}, "W", "K"),
		b("", "Zoom in", config.Action{Type: config.Keystroke, Chord: "Ctrl+="}, "="),
		b("", "Signature", config.Action{Type: config.InsertText, Text: "Best regards"}, "S"),
		b("", "Export PDF", config.Action{Type: config.MenuCommand, Menu: "File->Export as PDF..."}, "E"),
		b("", "Build", config.Action{Type: config.RunCommand, Command: "echo build"}, "R"),
		b("", "", config.Action{Type: config.System, System: "lock"}, "L"),
		b("", "", config.Action{Type: config.InputMethod, Layout: "next"}, "I"),
		b("", "Stand-up", config.Action{Type: config.Notify, Title: "Stand-up", Text: "in 5 minutes"}, "T"),
		b("f9", "On F9", config.Action{Type: config.OpenURL, URL: "https://example.org"}, "A"),
		b("chord", "On chord", config.Action{Type: config.OpenURL, URL: "https://example.net"}, "K"),
		b("mod", "On RCtrl", config.Action{Type: config.OpenURL, URL: "https://example.edu"}, "K"),
	}
	return c
}

// SelfTest runs the checks, writes report.txt and screenshots into out and
// returns the process exit code.
func SelfTest(out, version string) int {
	_ = os.MkdirAll(out, 0o755)
	dir, _ := os.MkdirTemp("", "spacehop-selftest")
	os.Setenv("SPACEHOP_DIR", dir)
	cfg := testConfig()
	if err := cfg.Normalize(); err != nil {
		_ = os.WriteFile(filepath.Join(out, "report.txt"), []byte("FAIL config: "+err.Error()+"\n"), 0o644)
		return 1
	}
	a := &App{Version: version, hookNs: make([]int64, 0, 4096)}
	t := &selfTest{a: a, out: out}
	theTest = t
	a.runHook = func(b *config.Binding) {
		t.mu.Lock()
		t.ran = append(t.ran, b.Label())
		t.mu.Unlock()
	}
	a.beforeHook = func() {
		inst, _, _ := pGetModuleHandle.Call(0)
		pSetWindowsHookEx.Call(whKeyboardLL, sinkProcCB, inst, 0)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for a.kbHook == 0 { // wait for the message loop to be ready
			wait(20)
		}
		wait(300)
		t.run()
		a.onUI(a.Quit)
	}()
	go func() {
		select {
		case <-done:
		case <-time.After(150 * time.Second):
			t.logf("FAIL", "timeout", "the self-test did not finish in time")
			t.write()
			os.Exit(2)
		}
	}()
	if err := a.Main(cfg, nil); err != nil {
		t.logf("FAIL", "start", "%v", err)
	}
	<-done
	t.write()
	_ = exec.Command("taskkill", "/F", "/IM", "notepad.exe").Run()
	if t.failed > 0 {
		return 1
	}
	return 0
}

func (t *selfTest) write() {
	t.mu.Lock()
	defer t.mu.Unlock()
	_ = os.WriteFile(filepath.Join(t.out, "report.txt"), []byte(strings.Join(t.report, "\n")+"\n"), 0o644)
}

func (t *selfTest) hintVisible() bool {
	v, _, _ := pIsWindowVisible.Call(t.a.overlay.hwnd)
	return v != 0
}

func (t *selfTest) apply(change func(c *config.Config)) {
	c := *t.a.Config()
	change(&c)
	_ = t.a.Apply(&c)
	wait(150)
}

func (t *selfTest) run() {
	a := t.a
	sp, f9 := uint32(config.VKSpace), vkOf("F9")
	t.take()
	t.check("tray icon", a.tray.ok, "accepted by the shell: %v", a.tray.ok)

	// ---- the leader key against real input ----
	tap(sp)
	seen, ran := t.take()
	t.check("tap types space", seen == "*Space↓ *Space↑" && ran == "", "seen %q ran %q", seen, ran)

	press(sp, true)
	wait(120)
	tap('N')
	press(sp, false)
	seen, ran = t.take()
	t.check("hold runs shortcut", seen == "" && ran == "Notepad", "seen %q ran %q", seen, ran)

	tap('X') // typing just before
	t.take()
	press(sp, true)
	wait(10)
	press('N', true)
	wait(20)
	press(sp, false)
	wait(20)
	press('N', false)
	seen, ran = t.take()
	t.check("fast typing passes", seen == "*Space↓ *N↓ Space↑ N↑" && ran == "", "seen %q ran %q", seen, ran)

	wait(300)
	press(sp, true)
	wait(150)
	tap('Q') // not a shortcut
	press(sp, false)
	seen, ran = t.take()
	t.check("unbound key types", seen == "*Space↓ *Q↓ Q↑ Space↑" && ran == "", "seen %q ran %q", seen, ran)

	press(vkOf("LShift"), true)
	wait(30)
	tap(sp)
	press(vkOf("LShift"), false)
	seen, _ = t.take()
	t.check("shift+space untouched", seen == "LShift↓ Space↓ Space↑ LShift↑", "seen %q", seen)

	// ---- hint window ----
	press(sp, true)
	wait(150)
	early := t.hintVisible()
	wait(500)
	t.check("hint appears after delay", !early && t.hintVisible(), "early %v, later %v", early, t.hintVisible())
	t.shot("hint-dark")
	tap('F')
	wait(150)
	t.shot("hint-level2")
	tap(vkOf("Backspace"))
	tap(vkOf("/"))
	wait(150)
	t.shot("hint-search")
	tap('E')
	tap('X')
	wait(150)
	t.shot("hint-search-typed")
	press(sp, false)
	tap(vkOf("Enter"))
	seen, ran = t.take()
	t.check("search runs result", seen == "" && ran != "" && !t.hintVisible(), "seen %q ran %q", seen, ran)

	press(sp, true)
	wait(650)
	press(sp, false)
	seen, ran = t.take()
	t.check("long hold types nothing", seen == "" && ran == "" && !t.hintVisible(), "seen %q ran %q", seen, ran)

	press(sp, true)
	wait(150)
	tap('F')
	tap('D')
	tap('W')
	tap('L')
	press(sp, false)
	seen, ran = t.take()
	t.check("sequences", seen == "" && ran == "Downloads,右半屏", "seen %q ran %q", seen, ran)

	press(sp, true)
	wait(150)
	tap('F')
	tap(vkOf("Esc"))
	tap('D')
	press(sp, false)
	seen, ran = t.take()
	t.check("esc cancels", seen == "" && ran == "", "seen %q ran %q", seen, ran)

	t.apply(func(c *config.Config) { c.Hint.Theme, c.Hint.Layout = "light", "modern" })
	press(sp, true)
	wait(600)
	t.shot("hint-light")
	press(sp, false)
	t.apply(func(c *config.Config) { c.Hint.Theme, c.Hint.Layout = "dark", "classic" })
	press(sp, true)
	wait(600)
	t.shot("hint-classic")
	press(sp, false)
	t.take()

	// ---- other leaders ----
	press(f9, true)
	wait(5)
	tap('A')
	press(f9, false)
	seen, ran = t.take()
	t.check("dedicated hold leader", seen == "" && ran == "On F9", "seen %q ran %q", seen, ran)

	ctrl, alt := vkOf("LCtrl"), vkOf("LAlt")
	press(ctrl, true)
	press(alt, true)
	wait(30)
	tap('J')
	press(alt, false)
	press(ctrl, false)
	wait(60)
	tap('K')
	seen, ran = t.take()
	t.check("tap combination leader", !strings.Contains(seen, "J") && !strings.Contains(seen, "K") && ran == "On chord", "seen %q ran %q", seen, ran)

	tap(vkOf("RCtrl"))
	wait(60)
	tap('K')
	seen, ran = t.take()
	t.check("tap modifier leader", !strings.Contains(seen, "K") && ran == "On RCtrl", "seen %q ran %q", seen, ran)

	press(vkOf("RCtrl"), true)
	wait(30)
	tap('C')
	press(vkOf("RCtrl"), false)
	wait(60)
	tap('K')
	seen, ran = t.take()
	t.check("ctrl+c is not a tap", strings.Contains(seen, "K↓") && ran == "", "seen %q ran %q", seen, ran)

	// ---- cost of the hook ----
	ns := append([]int64(nil), a.hookNs...)
	sort.Slice(ns, func(i, j int) bool { return ns[i] < ns[j] })
	if len(ns) > 0 {
		med, max := time.Duration(ns[len(ns)/2]), time.Duration(ns[len(ns)-1])
		t.check("hook latency", max < 50*time.Millisecond, "%d key events, median %v, max %v", len(ns), med, max)
	}

	t.actions()
}

// notepadText reads what is in Notepad's edit box.
func notepadText(win uintptr) (string, bool) {
	var edit uintptr
	for _, class := range []string{"Edit", "RichEditD2DPT"} {
		edit, _, _ = pFindWindowEx.Call(win, 0, uintptr(unsafe.Pointer(u16(class))), 0)
		if edit != 0 {
			break
		}
	}
	if edit == 0 {
		return "", false
	}
	var buf [4096]uint16
	var n uintptr
	pSendMessageTimeout.Call(edit, 0x000D /* WM_GETTEXT */, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])), 0x2, 2000, uintptr(unsafe.Pointer(&n)))
	return syscall.UTF16ToString(buf[:]), true
}

func (t *selfTest) actions() {
	a := t.a
	run := func(act config.Action) error { return a.RunAction(act) }

	t.check("list programs", len(a.Apps()) > 0, "%d found", len(a.Apps()))
	ls := a.Layouts()
	t.check("list keyboard layouts", len(ls) > 0, "%v", ls)
	err1 := a.SetAutostart(true)
	on := a.Autostart()
	err2 := a.SetAutostart(false)
	t.check("start at login", err1 == nil && err2 == nil && on && !a.Autostart(), "on=%v errors %v %v", on, err1, err2)
	t.check("clipboard", writeClipboard("spacehop ✓") == nil && func() bool { s, _ := readClipboard(); return s == "spacehop ✓" }(), "round trip")
	t.check("notification", run(config.Action{Type: config.Notify, Title: "SpaceHop", Text: "self-test"}) == nil, "sent")

	err := run(config.Action{Type: config.OpenApp, Path: "notepad.exe"})
	var np uintptr
	for i := 0; i < 50 && np == 0; i++ {
		wait(200)
		if wins := windowsOf("notepad.exe"); len(wins) > 0 {
			np = wins[0]
		}
	}
	if !t.check("open program", err == nil && np != 0, "notepad window %#x, error %v", np, err) {
		return
	}
	wait(500)
	activate(np)
	wait(300)
	fg, _, _ := pGetForegroundWindow.Call()
	if fg != np {
		t.logf("SKIP", "actions in a window", "Notepad could not be brought to the front on this desktop (front is %q)", className(fg))
		return
	}
	t.logf("PASS", "bring to front", "Notepad is the active window")
	if _, ok := notepadText(np); !ok {
		t.logf("SKIP", "text actions", "this Notepad has no classic edit box to read back")
	} else {
		_ = run(config.Action{Type: config.InsertText, Text: "hello {{cursor}}world"})
		wait(300)
		_ = run(config.Action{Type: config.InsertText, Text: "X"})
		wait(300)
		got, _ := notepadText(np)
		t.check("insert text + cursor", got == "hello Xworld", "%q", got)

		_ = run(config.Action{Type: config.Keystroke, Chord: "Ctrl+A"})
		wait(200)
		_ = run(config.Action{Type: config.InsertText, Text: "粘贴 ✓ paste", Paste: true})
		wait(300)
		got, _ = notepadText(np)
		clip, _ := readClipboard()
		t.check("keystroke + paste", got == "粘贴 ✓ paste" && clip == "spacehop ✓", "text %q, clipboard restored to %q", got, clip)

		err = run(config.Action{Type: config.MenuCommand, Menu: "Edit->Time/Date"})
		wait(400)
		after, _ := notepadText(np)
		if err != nil && strings.Contains(err.Error(), "标准菜单栏") {
			t.logf("SKIP", "menu command", "%v", err)
		} else {
			t.check("menu command", err == nil && len(after) > len(got), "error %v, text %q", err, after)
		}
		err = run(config.Action{Type: config.MenuCommand, Menu: "Edit->No Such Item"})
		t.check("menu command missing", err != nil, "%v", err)
	}

	mon, _, _ := pMonitorFromWindow.Call(np, 2)
	mi, _ := monitorOf(mon)
	wa := mi.Work
	visible := func() rect {
		o, l, tp, r, b := frame(np)
		return rect{o.Left + l, o.Top + tp, o.Right - r, o.Bottom - b}
	}
	near := func(x, y int32) bool { d := x - y; return d > -3 && d < 3 }
	_ = run(config.Action{Type: config.Window, Window: "left"})
	wait(400)
	v := visible()
	t.check("window left half", near(v.Left, wa.Left) && near(v.Top, wa.Top) && near(v.Right, wa.Left+(wa.Right-wa.Left)/2) && near(v.Bottom, wa.Bottom),
		"window %v in work area %v", v, wa)
	_ = run(config.Action{Type: config.Window, Window: "right-third"})
	wait(400)
	v = visible()
	t.check("window right third", near(v.Right, wa.Right) && near(v.Left, wa.Left+(wa.Right-wa.Left)*4/6), "window %v", v)
	_ = run(config.Action{Type: config.Window, Window: "maximize"})
	wait(400)
	z, _, _ := pIsZoomed.Call(np)
	t.check("window maximize", z != 0, "zoomed %v", z != 0)
	_ = run(config.Action{Type: config.Window, Window: "center"})
	wait(400)
	v = visible()
	t.check("window center", near(v.Left-wa.Left, wa.Right-v.Right) && near(v.Top-wa.Top, wa.Bottom-v.Bottom), "window %v", v)
	t.shot("desktop")

	_ = run(config.Action{Type: config.OpenApp, Path: "notepad.exe", IfRunning: "hide"})
	wait(400)
	min, _, _ := pIsIconic.Call(np)
	t.check("hide when in front", min != 0, "minimised %v", min != 0)
}

// shot saves a screenshot of the primary screen.
func (t *selfTest) shot(name string) {
	w, _, _ := pGetSystemMetrics.Call(0)
	h, _, _ := pGetSystemMetrics.Call(1)
	screen, _, _ := pGetDC.Call(0)
	if screen == 0 || w == 0 || h == 0 {
		return
	}
	defer pReleaseDC.Call(0, screen)
	mem, _, _ := pCreateCompatibleDC.Call(screen)
	var bmi struct {
		Size          uint32
		Width, Height int32
		Planes, Bits  uint16
		Compression   uint32
		_             [20]byte
	}
	bmi.Size, bmi.Width, bmi.Height, bmi.Planes, bmi.Bits = 40, int32(w), -int32(h), 1, 32
	var bits uintptr
	dib, _, _ := pCreateDIBSection.Call(mem, uintptr(unsafe.Pointer(&bmi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if dib == 0 {
		return
	}
	old, _, _ := pSelectObject.Call(mem, dib)
	pBitBlt.Call(mem, 0, 0, w, h, screen, 0, 0, 0x00CC0020|0x40000000) // SRCCOPY | CAPTUREBLT
	pGdiFlush.Call()
	px := unsafe.Slice((*[4]byte)(ptr(bits)), int(w*h))
	img := image.NewNRGBA(image.Rect(0, 0, int(w), int(h)))
	for i, p := range px {
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = p[2], p[1], p[0], 255
	}
	pSelectObject.Call(mem, old)
	pDeleteObject.Call(dib)
	if f, err := os.Create(filepath.Join(t.out, name+".png")); err == nil {
		_ = png.Encode(f, img)
		f.Close()
	}
}
