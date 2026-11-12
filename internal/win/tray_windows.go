package win

import (
	"unsafe"

	"spacehop/internal/config"
	"spacehop/internal/icon"
)

type notifyIconData struct {
	Size            uint32
	Hwnd            uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GuidItem        [16]byte
	BalloonIcon     uintptr
}

const (
	nimAdd    = 0
	nimModify = 1
	nimDelete = 2
	nifMsg    = 0x1
	nifIcon   = 0x2
	nifTip    = 0x4
	nifInfo   = 0x10
)

type tray struct {
	hwnd            uintptr
	iconOn, iconOff uintptr
	ok              bool // the icon was accepted by the shell
}

func (t *tray) data(enabled bool) *notifyIconData {
	d := &notifyIconData{Hwnd: t.hwnd, ID: 1}
	d.Size = uint32(unsafe.Sizeof(*d))
	d.Icon = t.iconOn
	tip := "SpaceHop"
	if !enabled {
		d.Icon = t.iconOff
		tip = "SpaceHop（已暂停）"
	}
	copy(d.Tip[:len(d.Tip)-1], w16(tip))
	return d
}

func (t *tray) add(hwnd uintptr, enabled bool) {
	t.hwnd = hwnd
	if t.iconOn == 0 {
		t.iconOn = makeIcon(false)
		t.iconOff = makeIcon(true)
	}
	d := t.data(enabled)
	d.Flags = nifMsg | nifIcon | nifTip
	d.CallbackMessage = msgTray
	r, _, _ := pShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(d)))
	t.ok = r != 0
}

func (t *tray) update(enabled bool) {
	d := t.data(enabled)
	d.Flags = nifIcon | nifTip
	pShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(d)))
}

func (t *tray) balloon(title, text string) {
	d := &notifyIconData{Hwnd: t.hwnd, ID: 1, Flags: nifInfo}
	d.Size = uint32(unsafe.Sizeof(*d))
	if title == "" {
		title = "SpaceHop"
	}
	if text == "" {
		text = " "
	}
	copy(d.Info[:len(d.Info)-1], w16(text))
	copy(d.InfoTitle[:len(d.InfoTitle)-1], w16(title))
	pShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(d)))
}

func (t *tray) remove() {
	d := &notifyIconData{Hwnd: t.hwnd, ID: 1}
	d.Size = uint32(unsafe.Sizeof(*d))
	pShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(d)))
	pDestroyIcon.Call(t.iconOn)
	pDestroyIcon.Call(t.iconOff)
}

// makeIcon turns the drawn icon into a Windows icon of the size the
// notification area uses on this display.
func makeIcon(off bool) uintptr {
	n, _, _ := pGetSystemMetrics.Call(49) // SM_CXSMICON
	if n < 16 || n > 64 {
		n = 16
	}
	img := icon.Render(int(n), off)
	px := make([]uint32, n*n)
	for i := range px {
		r, g, b, a := uint32(img.Pix[i*4]), uint32(img.Pix[i*4+1]), uint32(img.Pix[i*4+2]), uint32(img.Pix[i*4+3])
		px[i] = a<<24 | (r*a/255)<<16 | (g*a/255)<<8 | b*a/255 // premultiplied BGRA
	}
	color, _, _ := pCreateBitmap.Call(n, n, 1, 32, uintptr(unsafe.Pointer(&px[0])))
	maskBits := make([]byte, ((n+15)/16*2)*n)
	mask, _, _ := pCreateBitmap.Call(n, n, 1, 1, uintptr(unsafe.Pointer(&maskBits[0])))
	info := struct {
		IsIcon     int32
		XHot, YHot uint32
		Mask       uintptr
		Color      uintptr
	}{IsIcon: 1, Mask: mask, Color: color}
	h, _, _ := pCreateIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	pDeleteObject.Call(color)
	pDeleteObject.Call(mask)
	return h
}

const (
	cmdToggle = iota + 1
	cmdSettings
	cmdAutostart
	cmdFolder
	cmdReload
	cmdQuit
)

func (a *App) trayMenu() {
	menu, _, _ := pCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer pDestroyMenu.Call(menu)
	add := func(id uintptr, text string, checked bool) {
		var flags uintptr
		if checked {
			flags = 0x8 // MF_CHECKED
		}
		pAppendMenu.Call(menu, flags, id, uintptr(unsafe.Pointer(u16(text))))
	}
	sep := func() { pAppendMenu.Call(menu, 0x800, 0, 0) }
	cfg := a.Config()
	add(cmdSettings, "快捷键与设置…", false)
	add(cmdToggle, "启用 SpaceHop", cfg.Enabled)
	add(cmdAutostart, "开机自动启动", a.Autostart())
	sep()
	add(cmdFolder, "打开配置文件夹", false)
	add(cmdReload, "重新载入配置文件", false)
	pAppendMenu.Call(menu, 0x1 /* MF_GRAYED */, 0, uintptr(unsafe.Pointer(u16("版本 "+a.Version))))
	sep()
	add(cmdQuit, "退出", false)

	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// Required so the menu closes when the user clicks elsewhere.
	pSetForegroundWindow.Call(a.hwnd)
	// TPM_RIGHTBUTTON | TPM_RETURNCMD | TPM_NONOTIFY
	cmd, _, _ := pTrackPopupMenu.Call(menu, 0x0002|0x0100|0x0080, uintptr(pt.X), uintptr(pt.Y), 0, a.hwnd, 0)
	switch cmd {
	case cmdToggle:
		a.SetEnabled(!cfg.Enabled)
	case cmdSettings:
		a.openSettings()
	case cmdAutostart:
		if err := a.SetAutostart(!a.Autostart()); err != nil {
			a.Notify("无法修改开机启动：" + err.Error())
		}
	case cmdFolder:
		_ = shellExecute(config.Dir(), "", "", swShowNormal)
	case cmdReload:
		a.reload(true)
	case cmdQuit:
		a.Quit()
	}
}
