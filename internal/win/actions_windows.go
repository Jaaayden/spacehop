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
	"unsafe"

	"spacehop/internal/config"
	"spacehop/internal/engine"
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

// RunAction performs an action. It is called off the UI thread.
func (a *App) RunAction(act config.Action) error {
	if act.Incomplete() {
		return actionError("这个动作还没有填完整")
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
			pSetSuspendState.Call(0, 0, 0)
		case "display-off":
			pPostMessage.Call(a.hwnd, wmSysCmd, 0xF170, 2) // SC_MONITORPOWER, off
		case "show-desktop":
			return sendChord("Win+D")
		case "settings":
			a.openSettings()
		case "toggle":
			a.SetEnabled(!a.Config().Enabled)
		default:
			return actionError("未知的系统动作")
		}
		return nil
	}
	return actionError("未知的动作类型")
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
		return actionError("命令无法启动：" + err.Error())
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
			activate(wins[0])
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
	return shellExecute(target, args, dir, swShowNormal)
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

// activate brings a window to the front. Windows only lets the foreground
// program do that freely, so fall back to joining the foreground thread's
// input queue, and finally to a neutral key press.
func activate(hwnd uintptr) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if min, _, _ := pIsIconic.Call(hwnd); min != 0 {
		pShowWindow.Call(hwnd, swRestore)
	}
	isFront := func() bool {
		fg, _, _ := pGetForegroundWindow.Call()
		return fg == hwnd
	}
	pSetForegroundWindow.Call(hwnd)
	if isFront() {
		return
	}
	fg, _, _ := pGetForegroundWindow.Call()
	_, fgThread := windowPID(fg)
	self, _, _ := pGetCurrentThreadId.Call()
	if fgThread != 0 && uintptr(fgThread) != self {
		pAttachThreadInput.Call(self, uintptr(fgThread), 1)
		pBringWindowToTop.Call(hwnd)
		pSetForegroundWindow.Call(hwnd)
		pAttachThreadInput.Call(self, uintptr(fgThread), 0)
		if isFront() {
			return
		}
	}
	// An unassigned virtual key: counts as input from us, does nothing else.
	sendStrokes([]engine.Stroke{{VK: 0xE8, Down: true}, {VK: 0xE8, Down: false}})
	pSetForegroundWindow.Call(hwnd)
}
