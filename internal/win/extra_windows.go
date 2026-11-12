package win

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"spacehop/internal/config"
	"spacehop/internal/engine"
)

// ---- clipboard ----

func openClipboard() bool {
	for i := 0; i < 10; i++ {
		if r, _, _ := pOpenClipboard.Call(0); r != 0 {
			return true
		}
		time.Sleep(20 * time.Millisecond) // another program has it open
	}
	return false
}

const cfUnicode = 13

func readClipboard() (string, bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !openClipboard() {
		return "", false
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipboardData.Call(cfUnicode)
	if h == 0 {
		return "", false
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return "", false
	}
	defer pGlobalUnlock.Call(h)
	const max = 1 << 20
	u := unsafe.Slice((*uint16)(ptr(p)), max)
	n := 0
	for n < max && u[n] != 0 {
		n++
	}
	return syscall.UTF16ToString(u[:n]), true
}

func writeClipboard(text string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	u := w16(text)
	h, _, _ := pGlobalAlloc.Call(0x2, uintptr(len(u)*2)) // GMEM_MOVEABLE
	if h == 0 {
		return actionError("内存不足")
	}
	p, _, _ := pGlobalLock.Call(h)
	copy(unsafe.Slice((*uint16)(ptr(p)), len(u)), u)
	pGlobalUnlock.Call(h)
	if !openClipboard() {
		return actionError("剪贴板正被其他程序占用")
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	if r, _, _ := pSetClipboardData.Call(cfUnicode, h); r == 0 {
		return actionError("无法写入剪贴板")
	}
	return nil
}

// ---- insert text ----

// expandText fills the placeholders and returns how many characters follow
// the {{cursor}} mark.
func expandText(text string) (string, int) {
	now := time.Now()
	if strings.Contains(text, "{{clipboard}}") {
		clip, _ := readClipboard()
		text = strings.ReplaceAll(text, "{{clipboard}}", clip)
	}
	text = strings.NewReplacer("{{date}}", now.Format("2006-01-02"), "{{time}}", now.Format("15:04"),
		"{{datetime}}", now.Format("2006-01-02 15:04"), "\r", "").Replace(text)
	back := 0
	if i := strings.Index(text, "{{cursor}}"); i >= 0 {
		rest := text[i+len("{{cursor}}"):]
		back = len([]rune(rest))
		text = text[:i] + rest
	}
	return strings.ReplaceAll(text, "{{cursor}}", ""), back
}

func insertText(act config.Action) error {
	text, back := expandText(act.Text)
	if act.Paste {
		old, had := readClipboard()
		if err := writeClipboard(text); err != nil {
			return err
		}
		if err := sendChord("Ctrl+V"); err != nil {
			return err
		}
		time.Sleep(300 * time.Millisecond) // let the program read it first
		if had {
			_ = writeClipboard(old)
		}
	} else {
		sendText(text)
	}
	if back > 0 {
		if back > 2000 {
			back = 2000
		}
		time.Sleep(40 * time.Millisecond)
		s := make([]engine.Stroke, 0, back*2)
		for i := 0; i < back; i++ {
			s = append(s, engine.Stroke{VK: 0x25, Down: true}, engine.Stroke{VK: 0x25, Down: false})
		}
		sendStrokes(s)
	}
	return nil
}

// ---- menu command ----

func cleanMenu(s string) string {
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	// "保存(&S)" style access keys, then plain "&".
	if i := strings.Index(s, "(&"); i >= 0 && strings.HasSuffix(strings.TrimRight(s, ". …"), ")") {
		s = s[:i] + s[strings.Index(s[i:], ")")+i+1:]
	}
	s = strings.ReplaceAll(strings.ReplaceAll(s, "&", ""), "…", "...")
	return strings.ToLower(strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s), ".")))
}

// menuCommand runs an item of the front window's menu bar, given as
// "File->Save As...".
func menuCommand(path string) error {
	fg, _, _ := pGetForegroundWindow.Call()
	menu, _, _ := pGetMenu.Call(fg)
	if fg == 0 || menu == 0 {
		return actionError("当前窗口没有标准菜单栏，可以改用“模拟按键”")
	}
	var res uintptr
	send := func(m, w, l uintptr) {
		pSendMessageTimeout.Call(fg, m, w, l, 0x2, 500, uintptr(unsafe.Pointer(&res)))
	}
	send(0x0116, menu, 0) // WM_INITMENU: let the program update its items
	parts := strings.Split(path, "->")
	for depth, part := range parts {
		want := cleanMenu(part)
		n, _, _ := pGetMenuItemCount.Call(menu)
		found := -1
		for i := 0; i < int(int32(n)); i++ {
			var buf [256]uint16
			pGetMenuString.Call(menu, uintptr(i), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0x400) // MF_BYPOSITION
			if cleanMenu(syscall.UTF16ToString(buf[:])) == want {
				found = i
				break
			}
		}
		if found < 0 {
			return actionError(fmt.Sprintf("找不到菜单项 “%s”（第 %d 级）", strings.TrimSpace(part), depth+1))
		}
		if depth < len(parts)-1 {
			sub, _, _ := pGetSubMenu.Call(menu, uintptr(found))
			if sub == 0 {
				return actionError("“" + strings.TrimSpace(part) + "” 下面没有子菜单")
			}
			send(0x0117, sub, uintptr(found)) // WM_INITMENUPOPUP
			menu = sub
			continue
		}
		if st, _, _ := pGetMenuState.Call(menu, uintptr(found), 0x400); st&0x3 != 0 { // MF_GRAYED | MF_DISABLED
			return actionError("菜单项 “" + strings.TrimSpace(part) + "” 当前不可用")
		}
		id, _, _ := pGetMenuItemID.Call(menu, uintptr(found))
		if int32(id) == -1 {
			return actionError("“" + strings.TrimSpace(part) + "” 是一个子菜单，请写到具体的菜单项")
		}
		pPostMessage.Call(fg, wmCommand, id&0xFFFF, 0)
	}
	return nil
}

// ---- windows ----

// Layout fractions in sixths of the work area: left, top, width, height.
var layouts = map[string][4]int32{
	"left": {0, 0, 3, 6}, "right": {3, 0, 3, 6}, "top": {0, 0, 6, 3}, "bottom": {0, 3, 6, 3},
	"left-third": {0, 0, 2, 6}, "center-third": {2, 0, 2, 6}, "right-third": {4, 0, 2, 6},
	"left-two-thirds": {0, 0, 4, 6}, "right-two-thirds": {2, 0, 4, 6},
	"top-left": {0, 0, 3, 3}, "top-right": {3, 0, 3, 3}, "bottom-left": {0, 3, 3, 3}, "bottom-right": {3, 3, 3, 3},
}

// targetWindow is the window a window action applies to.
func targetWindow() uintptr {
	fg, _, _ := pGetForegroundWindow.Call()
	switch className(fg) {
	case "", "Progman", "WorkerW", "Shell_TrayWnd", mainClass, overlayClass:
		return 0
	}
	return fg
}

// frame returns a window's outer rectangle and the widths of the invisible
// resize borders around what the user sees.
func frame(h uintptr) (outer rect, l, t, r, b int32) {
	pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&outer)))
	var vis rect
	if hr, _, _ := pDwmGetWindowAttr.Call(h, 9, uintptr(unsafe.Pointer(&vis)), 16); hr == 0 { // DWMWA_EXTENDED_FRAME_BOUNDS
		return outer, vis.Left - outer.Left, vis.Top - outer.Top, outer.Right - vis.Right, outer.Bottom - vis.Bottom
	}
	return outer, 0, 0, 0, 0
}

// place moves a window so that its visible frame fills the rectangle.
func place(h uintptr, x, y, w, ht int32) {
	if z, _, _ := pIsZoomed.Call(h); z != 0 {
		pShowWindow.Call(h, swRestore)
	} else if m, _, _ := pIsIconic.Call(h); m != 0 {
		pShowWindow.Call(h, swRestore)
	}
	_, l, t, r, b := frame(h)
	// SWP_NOZORDER | SWP_NOACTIVATE
	pSetWindowPos.Call(h, 0, uintptr(x-l), uintptr(y-t), uintptr(w+l+r), uintptr(ht+t+b), 0x4|0x10)
}

var (
	monMu  sync.Mutex
	monOut []uintptr
	monCB  = syscall.NewCallback(func(mon, _, _, _ uintptr) uintptr {
		monOut = append(monOut, mon)
		return 1
	})
)

func monitors() []uintptr {
	monMu.Lock()
	defer monMu.Unlock()
	monOut = monOut[:0]
	pEnumMonitors.Call(0, 0, monCB, 0)
	return append([]uintptr(nil), monOut...)
}

func windowAction(name string, custom []int) error {
	if name == "switch-previous" {
		fg, _, _ := pGetForegroundWindow.Call()
		for _, w := range appWindows() {
			if w != fg {
				activate(w)
				return nil
			}
		}
		return nil
	}
	h := targetWindow()
	if h == 0 {
		return actionError("当前没有可以操作的窗口")
	}
	mon, _, _ := pMonitorFromWindow.Call(h, 2)
	mi, _ := monitorOf(mon)
	wa := mi.Work
	ww, wh := wa.Right-wa.Left, wa.Bottom-wa.Top
	switch name {
	case "maximize":
		pShowWindow.Call(h, 3)
	case "minimize":
		pShowWindow.Call(h, swMinimize)
	case "restore":
		pShowWindow.Call(h, swRestore)
	case "close":
		pPostMessage.Call(h, wmClose, 0, 0)
	case "switch-same-app":
		pid, _ := windowPID(h)
		if wins := windowsOf(baseLower(processPath(pid))); len(wins) > 1 {
			activate(wins[len(wins)-1])
		}
	case "center":
		o, l, t, r, b := frame(h)
		w, ht := o.Right-o.Left-l-r, o.Bottom-o.Top-t-b
		place(h, wa.Left+(ww-w)/2, wa.Top+(wh-ht)/2, w, ht)
	case "next-monitor":
		mons := monitors()
		if len(mons) < 2 {
			return nil
		}
		next := mons[0]
		for i, m := range mons {
			if m == mon {
				next = mons[(i+1)%len(mons)]
			}
		}
		zoomed, _, _ := pIsZoomed.Call(h)
		o, l, t, r, b := frame(h)
		ni, _ := monitorOf(next)
		nw, nh := ni.Work.Right-ni.Work.Left, ni.Work.Bottom-ni.Work.Top
		// Keep the same relative place and size on the other screen.
		x := ni.Work.Left + (o.Left+l-wa.Left)*nw/ww
		y := ni.Work.Top + (o.Top+t-wa.Top)*nh/wh
		place(h, x, y, (o.Right-o.Left-l-r)*nw/ww, (o.Bottom-o.Top-t-b)*nh/wh)
		if zoomed != 0 {
			pShowWindow.Call(h, 3)
		}
	case "custom":
		if len(custom) != 4 {
			return actionError("自定义位置不完整")
		}
		c := [4]int32{int32(custom[0]), int32(custom[1]), int32(custom[2]), int32(custom[3])}
		place(h, wa.Left+ww*c[0]/100, wa.Top+wh*c[1]/100, ww*c[2]/100, wh*c[3]/100)
	default:
		f, ok := layouts[name]
		if !ok {
			return actionError("未知的窗口动作")
		}
		x0, y0 := wa.Left+ww*f[0]/6, wa.Top+wh*f[1]/6
		place(h, x0, y0, wa.Left+ww*(f[0]+f[2])/6-x0, wa.Top+wh*(f[1]+f[3])/6-y0)
	}
	return nil
}

// ---- keyboard layouts / input methods ----

// Layouts lists the installed keyboard layouts and input methods.
func (a *App) Layouts() []config.LayoutInfo {
	var hkls [64]uintptr
	n, _, _ := pGetKbdLayoutList.Call(uintptr(len(hkls)), uintptr(unsafe.Pointer(&hkls[0])))
	out := []config.LayoutInfo{}
	for _, h := range hkls[:n] {
		var buf [128]uint16
		name := ""
		if r, _, _ := pGetLocaleInfo.Call(h&0xFFFF, 0x2 /* LOCALE_SLOCALIZEDDISPLAYNAME */, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); r > 0 {
			name = syscall.UTF16ToString(buf[:])
		}
		if r, _, _ := pImmGetDescription.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); r > 0 {
			name += " · " + syscall.UTF16ToString(buf[:r])
		}
		id := fmt.Sprintf("%08X", uint32(h))
		if name == "" {
			name = id
		}
		out = append(out, config.LayoutInfo{ID: id, Name: name})
	}
	return out
}

func switchLayout(id string) error {
	fg, _, _ := pGetForegroundWindow.Call()
	if fg == 0 {
		return nil
	}
	const wmInputLangChangeRequest = 0x0050
	if id == "next" {
		pPostMessage.Call(fg, wmInputLangChangeRequest, 0x2, 1) // INPUTLANGCHANGE_FORWARD, HKL_NEXT
		return nil
	}
	v, err := strconv.ParseUint(id, 16, 32)
	if err != nil {
		return actionError("输入法编号不对：" + id)
	}
	pPostMessage.Call(fg, wmInputLangChangeRequest, 0, uintptr(int32(uint32(v)))) // handles are sign-extended
	return nil
}
