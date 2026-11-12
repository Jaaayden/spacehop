package win

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"spacehop/internal/config"
	"spacehop/internal/engine"
)

const (
	mainClass   = "SpaceHopMain"
	msgTray     = wmApp + 1
	msgApply    = wmApp + 2
	msgSettings = wmApp + 3
	msgNotify   = wmApp + 4
	msgCall     = wmApp + 5
	timerHint   = 1
	timerAnim   = 2
	timerSeq    = 3
	timerStats  = 4

	maxLog = 300 // activity entries kept in memory
)

type notice struct{ title, text, url string }

// App owns the UI thread: the hidden main window, the hooks, the hint
// window and the tray icon. Everything that touches the engine runs on it.
type App struct {
	Version      string
	OpenSettings func() // set by main; opens the settings page

	// Set only by the self-test: called on the UI thread before the keyboard
	// hook is installed, and instead of running a shortcut's action.
	beforeHook func()
	runHook    func(*config.Binding)
	hookNs     []int64

	hwnd      uintptr
	overlay   overlay
	kbHook    uintptr
	mouseHook uintptr
	eng       *engine.Engine
	tray      tray

	current           atomic.Pointer[config.Config] // read from any thread
	pending           atomic.Pointer[config.Config]
	msgTaskbarCreated uint32

	mu       sync.Mutex // guards the fields below
	notices  []notice
	calls    []func()
	clickURL string
	stats    *config.Stats
	dirty    bool
	logOn    bool
	logBuf   []config.LogEntry
}

var theApp *App

var (
	kbProcCB    = syscall.NewCallback(kbProc)
	mouseProcCB = syscall.NewCallback(mouseProc)
	wndProcCB   = syscall.NewCallback(wndProc)
)

// AlreadyRunning reports whether another copy is running; if so it passes
// the command line on to it ("--settings" when there is none).
func AlreadyRunning(args []string) bool {
	// The handle is kept for the life of the process on purpose.
	_, _, e := pCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(u16(`Local\SpaceHop.Instance`))))
	if errno, ok := e.(syscall.Errno); !ok || errno != 183 { // ERROR_ALREADY_EXISTS
		return false
	}
	h, _, _ := pFindWindow.Call(uintptr(unsafe.Pointer(u16(mainClass))), 0)
	if h == 0 {
		return true
	}
	if len(args) == 0 {
		pPostMessage.Call(h, msgSettings, 0, 0)
		return true
	}
	data := []byte(strings.Join(args, "\n"))
	cds := struct {
		Tag  uintptr
		Size uint32
		Data unsafe.Pointer
	}{1, uint32(len(data)), unsafe.Pointer(&data[0])}
	var res uintptr
	pSendMessageTimeout.Call(h, wmCopyData, 0, uintptr(unsafe.Pointer(&cds)), 0x2, 3000, uintptr(unsafe.Pointer(&res)))
	return true
}

// Config returns the configuration in effect. Treat it as read-only.
func (a *App) Config() *config.Config { return a.current.Load() }

// Apply switches to a new configuration; safe to call from any thread.
func (a *App) Apply(c *config.Config) error {
	a.pending.Store(c)
	a.current.Store(c)
	pPostMessage.Call(a.hwnd, msgApply, 0, 0)
	return nil
}

// Notify shows a notification from the tray icon; safe from any thread.
func (a *App) Notify(text string) { a.NotifyURL("SpaceHop", text, "") }

// NotifyURL is Notify with a title and a link opened when it is clicked.
func (a *App) NotifyURL(title, text, url string) {
	a.mu.Lock()
	if len(a.notices) < 8 {
		a.notices = append(a.notices, notice{title, text, url})
	}
	a.mu.Unlock()
	pPostMessage.Call(a.hwnd, msgNotify, 0, 0)
}

// onUI runs f on the UI thread.
func (a *App) onUI(f func()) {
	a.mu.Lock()
	a.calls = append(a.calls, f)
	a.mu.Unlock()
	pPostMessage.Call(a.hwnd, msgCall, 0, 0)
}

// Main creates the windows and hooks and blocks in the message loop.
func (a *App) Main(cfg *config.Config, loadErr error) error {
	runtime.LockOSThread()
	theApp = a
	// Per-monitor DPI awareness, so the hint window is sharp on every screen.
	pSetDpiAwarenessCtx.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)

	inst, _, _ := pGetModuleHandle.Call(0)
	wc := wndClassEx{WndProc: wndProcCB, Instance: inst, ClassName: u16(mainClass)}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, e := pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return e
	}
	// A normal (never shown) top-level window: message-only windows do not
	// receive the power and taskbar broadcasts we rely on.
	a.hwnd, _, _ = pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(u16(mainClass))),
		uintptr(unsafe.Pointer(u16("SpaceHop"))), 0, 0, 0, 0, 0, 0, 0, inst, 0)
	if a.hwnd == 0 {
		return errors.New("无法创建窗口")
	}
	r, _, _ := pRegisterWindowMsg.Call(uintptr(unsafe.Pointer(u16("TaskbarCreated"))))
	a.msgTaskbarCreated = uint32(r)

	a.stats = config.LoadStats()
	a.current.Store(cfg)
	a.eng = engine.New(a, cfg)
	a.eng.Trace = a.trace
	if err := a.overlay.create(inst); err != nil {
		return err
	}
	a.tray.add(a.hwnd, cfg.Enabled)
	if a.beforeHook != nil {
		a.beforeHook()
	}
	if err := a.installHook(); err != nil {
		return err
	}
	if loadErr != nil {
		a.Notify(loadErr.Error() + "。已暂时使用默认配置，原文件没有改动。")
	}
	go a.watchConfig()

	var m msg
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	a.removeHooks()
	a.tray.remove()
	a.saveStats()
	return nil
}

func (a *App) installHook() error {
	if a.kbHook != 0 {
		pUnhookWindowsHookEx.Call(a.kbHook)
		a.kbHook = 0
	}
	inst, _, _ := pGetModuleHandle.Call(0)
	h, _, e := pSetWindowsHookEx.Call(whKeyboardLL, kbProcCB, inst, 0)
	if h == 0 {
		return e
	}
	a.kbHook = h
	return nil
}

func (a *App) removeHooks() {
	if a.kbHook != 0 {
		pUnhookWindowsHookEx.Call(a.kbHook)
		a.kbHook = 0
	}
	a.WatchMouse(false)
}

func kbProc(nCode, wParam, lParam uintptr) uintptr {
	a := theApp
	if int32(nCode) == 0 {
		k := (*kbdllHook)(ptr(lParam))
		if k.Extra != injectMagic {
			down := wParam == wmKeyDown || wParam == wmSysKeyDown
			if a.hookNs != nil {
				start := time.Now()
				eat := a.eng.OnKey(k.VK, down, k.Time)
				a.hookNs = append(a.hookNs, int64(time.Since(start)))
				if eat {
					return 1
				}
			} else if a.eng.OnKey(k.VK, down, k.Time) {
				return 1
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
}

func mouseProc(nCode, wParam, lParam uintptr) uintptr {
	a := theApp
	if int32(nCode) == 0 {
		var button uint32
		switch wParam {
		case wmLButtonDown:
			button = config.VKLButton
		case wmRButtonDown:
			button = config.VKRButton
		case wmMButtonDown:
			button = config.VKMButton
		}
		if button != 0 {
			m := (*msllHook)(ptr(lParam))
			if m.Extra != injectMagic && a.eng.OnMouseDown(button) {
				return 1
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
}

func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	a := theApp
	switch uint32(message) {
	case msgTray:
		switch loword(lParam) {
		case wmRButtonUp:
			a.trayMenu()
		case wmLButtonUp:
			a.openSettings()
		case 0x405: // NIN_BALLOONUSERCLICK
			a.mu.Lock()
			u := a.clickURL
			a.mu.Unlock()
			if u != "" {
				go func() { _ = Open(u, "") }()
			}
		}
		return 0
	case msgSettings:
		a.openSettings()
		return 0
	case msgApply:
		if c := a.pending.Swap(nil); c != nil {
			a.eng.SetConfig(c)
			a.tray.update(c.Enabled)
		}
		return 0
	case msgNotify:
		a.mu.Lock()
		list := a.notices
		a.notices = nil
		if len(list) > 0 {
			a.clickURL = list[len(list)-1].url
		}
		a.mu.Unlock()
		for _, n := range list {
			a.tray.balloon(n.title, n.text)
		}
		return 0
	case msgCall:
		a.mu.Lock()
		calls := a.calls
		a.calls = nil
		a.mu.Unlock()
		for _, f := range calls {
			f()
		}
		return 0
	case wmCopyData:
		cds := (*struct {
			Tag  uintptr
			Size uint32
			Data unsafe.Pointer
		})(ptr(lParam))
		if cds.Tag == 1 && cds.Size > 0 && cds.Size < 4096 {
			args := strings.Split(string(unsafe.Slice((*byte)(cds.Data), cds.Size)), "\n")
			a.onUI(func() { a.command(args) })
		}
		return 1
	case wmTimer:
		switch wParam {
		case timerHint:
			pKillTimer.Call(hwnd, timerHint)
			a.drawHint()
		case timerAnim:
			a.overlay.tick()
		case timerSeq:
			pKillTimer.Call(hwnd, timerSeq)
			a.eng.OnTimeout()
		case timerStats:
			pKillTimer.Call(hwnd, timerStats)
			a.saveStats()
		}
		return 0
	case wmPowerBroadcast:
		// Windows can drop low-level hooks across sleep; put ours back.
		if wParam == 0x12 || wParam == 0x7 { // resume automatic / resume suspend
			if err := a.installHook(); err != nil {
				log.Printf("reinstall hook: %v", err)
			}
		}
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	default:
		if a != nil && uint32(message) == a.msgTaskbarCreated && a.msgTaskbarCreated != 0 {
			a.tray.add(a.hwnd, a.Config().Enabled) // Explorer restarted
			return 0
		}
	}
	r, _, _ := pDefWindowProc.Call(hwnd, message, wParam, lParam)
	return r
}

// command carries out a command line passed on by a second copy:
//
//	--settings | --toggle | --enable | --disable | --reload | --quit | --run <name or id>
func (a *App) command(args []string) {
	if len(args) == 0 {
		return
	}
	switch args[0] {
	case "--settings":
		a.openSettings()
	case "--toggle":
		a.SetEnabled(!a.Config().Enabled)
	case "--enable":
		a.SetEnabled(true)
	case "--disable":
		a.SetEnabled(false)
	case "--reload":
		a.reload(true)
	case "--quit":
		a.Quit()
	case "--run":
		if len(args) < 2 {
			return
		}
		cfg := a.Config()
		for i := range cfg.Bindings {
			if b := &cfg.Bindings[i]; b.ID == args[1] || strings.EqualFold(b.Label(), args[1]) {
				a.Run(b)
				return
			}
		}
		a.Notify("没有叫 “" + args[1] + "” 的快捷键")
	}
}

func (a *App) openSettings() {
	if a.OpenSettings != nil {
		go a.OpenSettings()
	}
}

// ---- engine.Effects ----

func (a *App) Inject(s []engine.Stroke) { sendStrokes(s) }

func (a *App) Run(b *config.Binding) {
	a.mu.Lock()
	a.stats.Total++
	a.stats.Counts[b.ID]++
	if !a.dirty {
		a.dirty = true
		pSetTimer.Call(a.hwnd, timerStats, 30000, 0)
	}
	a.mu.Unlock()
	if a.runHook != nil {
		a.runHook(b)
		return
	}
	action, label := b.Action, b.Label()
	go func() {
		if err := a.RunAction(action); err != nil {
			a.Notify(label + "：" + err.Error())
		}
	}()
}

func (a *App) Hint(delayMs int) {
	if delayMs <= 0 {
		pKillTimer.Call(a.hwnd, timerHint)
		a.drawHint()
		return
	}
	pSetTimer.Call(a.hwnd, timerHint, uintptr(delayMs), 0)
}

func (a *App) HideHint() {
	pKillTimer.Call(a.hwnd, timerHint)
	a.overlay.hide()
}

func (a *App) Timeout(ms int) {
	if ms <= 0 {
		pKillTimer.Call(a.hwnd, timerSeq)
		return
	}
	pSetTimer.Call(a.hwnd, timerSeq, uintptr(ms), 0)
}

func (a *App) drawHint() {
	v, ok := a.eng.View()
	if !ok {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.overlay.show(v, a.Config(), func(id string) int { return a.stats.Counts[id] })
}

// WatchMouse installs the mouse hook only while a leader is undecided, so
// mouse movement costs nothing the rest of the time.
func (a *App) WatchMouse(on bool) {
	if on == (a.mouseHook != 0) {
		return
	}
	if !on {
		pUnhookWindowsHookEx.Call(a.mouseHook)
		a.mouseHook = 0
		return
	}
	inst, _, _ := pGetModuleHandle.Call(0)
	a.mouseHook, _, _ = pSetWindowsHookEx.Call(whMouseLL, mouseProcCB, inst, 0)
}

func foregroundExe() string {
	if fg, _, _ := pGetForegroundWindow.Call(); fg != 0 {
		pid, _ := windowPID(fg)
		return baseLower(processPath(pid))
	}
	return ""
}

func (a *App) Context() (string, bool) {
	var state uint32
	pSHQueryNotifyState.Call(uintptr(unsafe.Pointer(&state)))
	return foregroundExe(), state == 3 // QUNS_RUNNING_D3D_FULL_SCREEN
}

func (a *App) Mods() uint8 {
	var m uint8
	for _, k := range [...]struct {
		vk  uintptr
		bit uint8
	}{{config.VKShift, config.ModShift}, {config.VKCtrl, config.ModCtrl}, {config.VKAlt, config.ModAlt},
		{config.VKLWin, config.ModWin}, {config.VKRWin, config.ModWin}} {
		if r, _, _ := pGetAsyncKeyState.Call(k.vk); r&0x8000 != 0 {
			m |= k.bit
		}
	}
	return m
}

// ---- activity log and usage ----

func (a *App) trace(text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.logOn {
		return
	}
	if len(a.logBuf) >= maxLog {
		a.logBuf = append(a.logBuf[:0], a.logBuf[len(a.logBuf)-maxLog/2:]...)
	}
	a.logBuf = append(a.logBuf, config.LogEntry{Time: time.Now().Format("15:04:05.000"), Text: text, App: foregroundExe()})
}

// Activity turns recording on or off and returns what was recorded. The
// log lives in memory only.
func (a *App) Activity(on bool) []config.LogEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logOn = on
	if !on {
		a.logBuf = nil
	}
	return append([]config.LogEntry{}, a.logBuf...)
}

// Usage returns how many shortcuts ran in total, and per binding.
func (a *App) Usage() (int, map[string]int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	counts := make(map[string]int, len(a.stats.Counts))
	for k, v := range a.stats.Counts {
		counts[k] = v
	}
	return a.stats.Total, counts
}

func (a *App) saveStats() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dirty {
		a.dirty = false
		if err := a.stats.Save(a.Config()); err != nil {
			log.Printf("stats: %v", err)
		}
	}
}

// ---- configuration ----

// SetEnabled flips the master switch and saves it.
func (a *App) SetEnabled(on bool) {
	c := *a.Config()
	c.Enabled = on
	if err := config.Save(&c); err != nil {
		log.Printf("save: %v", err)
	}
	_ = a.Apply(&c)
}

// reload re-reads the config file when it differs from what is in effect.
func (a *App) reload(report bool) {
	data, err := os.ReadFile(config.Path())
	if err != nil {
		return
	}
	cur, _ := json.MarshalIndent(a.Config(), "", "  ")
	if bytes.Equal(bytes.TrimSpace(data), bytes.TrimSpace(cur)) {
		return
	}
	c, err := config.Parse(data)
	if err != nil {
		a.Notify("配置文件被改动了，但内容有问题，仍在使用原来的配置：" + err.Error())
		return
	}
	_ = a.Apply(c)
	if report {
		a.Notify("已重新载入配置文件")
	}
}

// watchConfig reloads the configuration when the file is edited by hand.
// It blocks in the kernel between changes; nothing is polled.
func (a *App) watchConfig() {
	h, _, _ := pFindFirstChange.Call(uintptr(unsafe.Pointer(u16(config.Dir()))), 0, 0x10|0x1) // LAST_WRITE | FILE_NAME
	if h == 0 || h == ^uintptr(0) {
		return
	}
	for {
		if r, _, _ := pWaitForSingleObj.Call(h, 0xFFFFFFFF); r != 0 {
			return
		}
		time.Sleep(300 * time.Millisecond) // let the writer finish
		a.reload(true)
		if r, _, _ := pFindNextChange.Call(h); r == 0 {
			return
		}
	}
}

func (a *App) Quit() { pDestroyWindow.Call(a.hwnd) }

// SetupLog sends the log to a small file next to the config.
func SetupLog() {
	p := filepath.Join(config.Dir(), "spacehop.log")
	_ = os.MkdirAll(config.Dir(), 0o755)
	if st, err := os.Stat(p); err == nil && st.Size() > 1<<20 {
		_ = os.Rename(p, p+".old")
	}
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		log.SetOutput(f)
	}
}

// Fatal shows an error box; there is no console to print to.
func Fatal(text string) {
	pMessageBox.Call(0, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16("SpaceHop"))), 0x10)
}
