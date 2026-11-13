package win

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"spacehop/internal/config"
	"spacehop/internal/engine"
	"spacehop/internal/i18n"
	"spacehop/internal/lnk"
)

var envRef = regexp.MustCompile(`%([^%]+)%`)

// expand resolves %VAR% references and a leading "~".
func expand(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, `~\`) || strings.HasPrefix(p, "~/") {
		p = os.Getenv("USERPROFILE") + p[1:]
	}
	return envRef.ReplaceAllStringFunc(p, func(m string) string {
		if v, ok := os.LookupEnv(m[1 : len(m)-1]); ok {
			return v
		}
		return m
	})
}

// errNotFront: Windows refused to bring the window forward. It is written
// to the activity log rather than shown, because nothing can be done about
// it at that moment.
var errNotFront = actionError("not in front")

// RunAction performs an action. It is called off the UI thread.
func (a *App) RunAction(act config.Action) error {
	if act.Incomplete() {
		return actionError(i18n.T("这个动作还没有填完整"))
	}
	switch act.Type {
	case config.OpenApp:
		return openApp(act)
	case config.OpenURL:
		u := strings.TrimSpace(act.URL)
		if !strings.Contains(u, ":") {
			u = "https://" + u
		}
		if act.With != "" {
			return shellExecute(expand(act.With), quote(u), "", swShowNormal)
		}
		return shellExecute(u, "", "", swShowNormal)
	case config.OpenFile:
		p := expand(act.Path)
		if act.With != "" {
			return shellExecute(expand(act.With), quote(p), "", swShowNormal)
		}
		return shellExecute(p, "", "", swShowNormal)
	case config.ExecuteFile:
		p := expand(act.Path)
		return shellExecute(p, act.Args, filepath.Dir(p), swShowNormal)
	case config.RunCommand:
		return runCommand(act)
	case config.Keystroke:
		return sendChord(act.Chord)
	case config.InsertText:
		return insertText(act)
	case config.MenuCommand:
		return menuCommand(act.Menu)
	case config.Window:
		return windowAction(act.Window, act.Rect)
	case config.InputMethod:
		return switchLayout(act.Layout)
	case config.Notify:
		a.NotifyURL(act.Title, act.Text, act.URL)
		return nil
	case config.System:
		switch act.System {
		case "lock":
			pLockWorkStation.Call()
		case "sleep":
			a.waitKeysQuiet()
			pSetSuspendState.Call(0, 0, 0)
		case "display-off":
			// Letting go of the shortcut's own keys counts as activity and
			// would wake the screen again at once.
			a.waitKeysQuiet()
			pPostMessage.Call(a.hwnd, wmSysCmd, 0xF170, 2) // SC_MONITORPOWER, off
		case "show-desktop":
			return sendChord("Win+D")
		case "settings":
			a.openSettings()
		case "toggle":
			a.SetEnabled(!a.Config().Enabled)
		default:
			return actionError(i18n.T("未知的系统动作"))
		}
		return nil
	}
	return actionError(i18n.T("未知的动作类型"))
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, "") + `"` }

func runCommand(act config.Action) error {
	var cmd *exec.Cmd
	attr := &syscall.SysProcAttr{}
	if act.Hidden {
		attr.HideWindow = true
		attr.CreationFlags = 0x08000000 // CREATE_NO_WINDOW
	} else {
		attr.CreationFlags = 0x00000010 // CREATE_NEW_CONSOLE
	}
	if act.Shell == "powershell" {
		cmd = exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", act.Command)
	} else {
		cmd = exec.Command("cmd.exe")
		// cmd has its own quoting rules; /S with one outer pair of quotes
		// passes the command through unchanged.
		attr.CmdLine = `cmd.exe /S /C "` + act.Command + `"`
	}
	cmd.SysProcAttr = attr
	cmd.Dir = os.Getenv("USERPROFILE")
	if err := cmd.Start(); err != nil {
		return actionError(i18n.T("命令无法启动：") + err.Error())
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// isAppID reports whether target is a Start-menu application ID (used for
// Store apps) rather than a file path.
func isAppID(target string) bool {
	return strings.Contains(target, "!") && !strings.ContainsAny(target, `\/:`)
}

func exeOf(path, exe string) string {
	exe = strings.ToLower(strings.TrimSpace(exe))
	if exe == "" && strings.HasSuffix(strings.ToLower(path), ".exe") {
		exe = baseLower(path)
	}
	return exe
}

func openApp(act config.Action) error {
	apps := append([]config.AppRef{{Path: act.Path, Exe: act.Exe}}, act.Cycle...)
	front := foregroundExe()
	if len(apps) > 1 {
		// Rotation: from elsewhere open the first; from one of them, the next.
		for i, ap := range apps {
			if e := exeOf(expand(ap.Path), ap.Exe); e != "" && e == front {
				next := apps[(i+1)%len(apps)]
				return launchOrShow(next, act.Args, false)
			}
		}
		return launchOrShow(apps[0], act.Args, false)
	}
	exe := exeOf(expand(act.Path), act.Exe)
	if exe == "" || exe != front || act.IfRunning == "new" {
		return launchOrShow(apps[0], act.Args, act.IfRunning == "new")
	}
	// The program is already in front.
	fg, _, _ := pGetForegroundWindow.Call()
	switch act.IfRunning {
	case "hide":
		pShowWindow.Call(fg, swMinimize)
	case "previous":
		for _, w := range appWindows() {
			if pid, _ := windowPID(w); baseLower(processPath(pid)) != exe {
				activate(w)
				break
			}
		}
	default:
		if wins := windowsOf(exe); len(wins) > 1 {
			activate(wins[len(wins)-1]) // go round the program's windows
		}
	}
	return nil
}

// launchOrShow brings a program's most recent window forward, or starts it.
func launchOrShow(ap config.AppRef, args string, fresh bool) error {
	target := expand(ap.Path)
	if exe := exeOf(target, ap.Exe); exe != "" && !fresh {
		if wins := windowsOf(exe); len(wins) > 0 {
			if !activate(wins[0]) {
				return errNotFront
			}
			return nil
		}
	}
	if isAppID(target) {
		return shellExecute("explorer.exe", `shell:AppsFolder\`+target, "", swShowNormal)
	}
	dir := ""
	if filepath.IsAbs(target) {
		dir = filepath.Dir(target)
	}
	// Starting a Start-menu shortcut through the shell costs a few hundred
	// milliseconds of link resolution. Read the shortcut ourselves and start
	// its program directly: that is what makes opening an app instant.
	if strings.EqualFold(filepath.Ext(target), ".lnk") {
		if l, ok := resolveLink(target); ok {
			if args != "" && l.Args != "" {
				args = l.Args + " " + args
			} else if args == "" {
				args = l.Args
			}
			if l.Dir != "" {
				dir = expand(l.Dir)
			} else {
				dir = filepath.Dir(l.Target)
			}
			if startProgram(l.Target, args, dir) == nil {
				return nil
			}
			return shellExecute(target, "", filepath.Dir(target), swShowNormal) // as the shell would
		}
	}
	return startProgram(target, args, dir)
}

// startProgram starts an executable the most direct way there is, by
// creating the process ourselves. Anything that needs the shell (a document,
// a program that asks for elevation) falls back to ShellExecute.
func startProgram(target, args, dir string) error {
	if !strings.EqualFold(filepath.Ext(target), ".exe") || !filepath.IsAbs(target) {
		return shellExecute(target, args, dir, swShowNormal)
	}
	cmd := exec.Command(target)
	line := `"` + target + `"`
	if args != "" {
		line += " " + args
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		return shellExecute(target, args, dir, swShowNormal)
	}
	_ = cmd.Process.Release()
	return nil
}

type linkEntry struct {
	mod  time.Time
	link lnk.Link
	ok   bool
}

var (
	linkMu    sync.Mutex
	linkCache = map[string]linkEntry{}
)

// resolveLink returns the program behind a shortcut when it can simply be
// started directly. The answer is remembered until the file changes.
func resolveLink(path string) (lnk.Link, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return lnk.Link{}, false
	}
	linkMu.Lock()
	defer linkMu.Unlock()
	if e, hit := linkCache[path]; hit && e.mod.Equal(st.ModTime()) {
		return e.link, e.ok
	}
	if len(linkCache) > 256 {
		linkCache = map[string]linkEntry{}
	}
	l, err := lnk.Read(path)
	ok := err == nil && !l.Special && strings.EqualFold(filepath.Ext(l.Target), ".exe")
	if ok {
		if _, err := os.Stat(l.Target); err != nil {
			ok = false // moved, or a path we could not read exactly
		}
	}
	linkCache[path] = linkEntry{st.ModTime(), l, ok}
	return l, ok
}

// ---- finding and activating windows ----

var (
	enumMu  sync.Mutex
	enumOut []uintptr
	enumCB  = syscall.NewCallback(enumProc)
)

func enumProc(hwnd, _ uintptr) uintptr {
	if isAppWindow(hwnd) {
		enumOut = append(enumOut, hwnd)
	}
	return 1
}

// isAppWindow keeps the windows a user would see in the taskbar.
func isAppWindow(hwnd uintptr) bool {
	if v, _, _ := pIsWindowVisible.Call(hwnd); v == 0 {
		return false
	}
	if owner, _, _ := pGetWindow.Call(hwnd, 4); owner != 0 { // GW_OWNER
		return false
	}
	ex, _, _ := pGetWindowLongPtr.Call(hwnd, ^uintptr(19)) // GWL_EXSTYLE (-20)
	if ex&0x80 != 0 {                                      // WS_EX_TOOLWINDOW
		return false
	}
	if n, _, _ := pGetWindowTextLength.Call(hwnd); n == 0 {
		return false
	}
	switch className(hwnd) {
	case "Progman", "WorkerW": // the desktop belongs to explorer.exe but is not one of its windows
		return false
	}
	var cloaked uint32
	pDwmGetWindowAttr.Call(hwnd, 14, uintptr(unsafe.Pointer(&cloaked)), 4) // DWMWA_CLOAKED
	return cloaked == 0
}

// appWindows lists top-level application windows, front to back.
func appWindows() []uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumOut = enumOut[:0]
	pEnumWindows.Call(enumCB, 0)
	return append([]uintptr(nil), enumOut...)
}

// windowsOf returns the windows of the program with this file name.
func windowsOf(exe string) []uintptr {
	var out []uintptr
	names := map[uint32]string{} // one process lookup per process, not per window
	for _, w := range appWindows() {
		pid, _ := windowPID(w)
		name, ok := names[pid]
		if !ok {
			name = baseLower(processPath(pid))
			names[pid] = name
		}
		if name == exe {
			out = append(out, w)
		}
	}
	return out
}

// activate brings a window to the front and makes sure it got there.
//
// Windows lets only the program that received the last input change the
// foreground window. A shortcut arrives through our keyboard hook, which
// does not count, so first send a null input of our own, which does. If the
// window still is not in front, join the foreground thread's input queue,
// and try again a couple of times: the window we are leaving may be busy.
func activate(hwnd uintptr) bool {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if min, _, _ := pIsIconic.Call(hwnd); min != 0 {
		pShowWindow.Call(hwnd, swRestore)
	}
	isFront := func() bool {
		fg, _, _ := pGetForegroundWindow.Call()
		return fg == hwnd
	}
	self, _, _ := pGetCurrentThreadId.Call()
	for attempt := 0; attempt < 4; attempt++ {
		if isFront() {
			return true
		}
		var null mouseInput // a mouse event that moves and presses nothing
		null.Type, null.Extra = inputMouse, injectMagic
		pSendInput.Call(1, uintptr(unsafe.Pointer(&null)), inputSize)
		pSetForegroundWindow.Call(hwnd)
		if isFront() {
			return true
		}
		fg, _, _ := pGetForegroundWindow.Call()
		if _, fgThread := windowPID(fg); fgThread != 0 && uintptr(fgThread) != self {
			pAttachThreadInput.Call(self, uintptr(fgThread), 1)
			pBringWindowToTop.Call(hwnd)
			pSetForegroundWindow.Call(hwnd)
			pAttachThreadInput.Call(self, uintptr(fgThread), 0)
		}
		if isFront() {
			return true
		}
		// An unassigned virtual key: counts as input from us, does nothing else.
		sendStrokes([]engine.Stroke{{VK: 0xE8, Down: true}, {VK: 0xE8, Down: false}})
		pSetForegroundWindow.Call(hwnd)
		time.Sleep(time.Duration(8*(attempt+1)) * time.Millisecond)
	}
	return isFront()
}

// waitKeysQuiet returns once no key has been pressed or released for a
// little while (at most a few seconds).
func (a *App) waitKeysQuiet() {
	for i := 0; i < 100; i++ {
		if time.Now().UnixMilli()-a.lastKey.Load() > 700 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
