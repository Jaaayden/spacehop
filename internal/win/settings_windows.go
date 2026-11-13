package win

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"spacehop/internal/config"
	"spacehop/internal/i18n"
	"spacehop/internal/raster"
	"spacehop/internal/ui"
)

// The settings window is drawn by the program itself (package ui). It runs
// on its own thread with its own message loop, so nothing it does can
// delay the keyboard hook. Closing the window ends the thread and frees
// everything it used.

const (
	settingsClass = "SpaceHopSettings"
	msgSetAsync   = wmApp + 20
	msgSetRecKey  = wmApp + 21
	msgSetShow    = wmApp + 22
	msgSetRefresh = wmApp + 23
	msgSetTest    = wmApp + 24
	timerSetAnim  = 1
)

var (
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")

	pGetOpenFileName  = comdlg32.NewProc("GetOpenFileNameW")
	pGetSaveFileName  = comdlg32.NewProc("GetSaveFileNameW")
	pSetCapture       = user32.NewProc("SetCapture")
	pReleaseCapture   = user32.NewProc("ReleaseCapture")
	pTrackMouseEvent  = user32.NewProc("TrackMouseEvent")
	pLoadCursor       = user32.NewProc("LoadCursorW")
	pSetCursor        = user32.NewProc("SetCursor")
	pLoadIcon         = user32.NewProc("LoadIconW")
	pGetClientRect    = user32.NewProc("GetClientRect")
	pSetFocus         = user32.NewProc("SetFocus")
	pGetFocus         = user32.NewProc("GetFocus")
	pSetWindowText    = user32.NewProc("SetWindowTextW")
	pSendMessage      = user32.NewProc("SendMessageW")
	pMoveWindow       = user32.NewProc("MoveWindow")
	pSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	pCallWindowProc   = user32.NewProc("CallWindowProcW")
	pGetDpiForWindow  = user32.NewProc("GetDpiForWindow")
	pIsWindow         = user32.NewProc("IsWindow")
	pUpdateWindow     = user32.NewProc("UpdateWindow")
	pSetBkColor       = gdi32.NewProc("SetBkColor")
	pSaveDC           = gdi32.NewProc("SaveDC")
	pRestoreDC        = gdi32.NewProc("RestoreDC")
	pIntersectClip    = gdi32.NewProc("IntersectClipRect")
	pGetUILanguage    = kernel32.NewProc("GetUserDefaultUILanguage")
	pExtTextOut       = gdi32.NewProc("ExtTextOutW")
	pGetTextMetrics   = gdi32.NewProc("GetTextMetricsW")
)

type settings struct {
	a    *App
	hwnd uintptr
	app  *ui.App
	ctx  *ui.Ctx

	memDC, dib, oldBmp uintptr
	canvas             raster.Canvas
	scale              float64
	fonts              [ui.FontCount]uintptr
	fontDPI            uint32
	fontLang           string

	edit        uintptr
	editID      string
	editSpec    *ui.Edit
	editProc    uintptr
	editBrush   uintptr
	editBrushC  uint32
	editFont    ui.Font
	inFrame     bool
	animOn      bool
	last        time.Time
	tracking    bool
	hand, arrow uintptr

	widths  map[string]int32
	fontH   [ui.FontCount]int32
	textNs  int64 // time spent drawing text, for the self-test
	textOps int

	mu     sync.Mutex
	done   []func()
	timers map[uintptr]func()
	nextID uintptr
	tests  []func(*ui.App, *ui.Ctx)
}

var (
	theSettings    *settings // owned by the settings thread
	settingsMu     sync.Mutex
	settingsHwnd   uintptr // 0 when the window is closed
	settingsProcCB uintptr
	editProcCB     uintptr
)

func init() {
	settingsProcCB = syscall.NewCallback(settingsProc)
	editProcCB = syscall.NewCallback(editProc)
}

// SystemLanguage returns "zh" when Windows is shown in Chinese, else "en".
func SystemLanguage() string {
	id, _, _ := pGetUILanguage.Call()
	if id&0x3FF == 0x04 {
		return "zh"
	}
	return "en"
}

// ApplyLanguage switches the interface language from the configuration.
func ApplyLanguage(c *config.Config) {
	lang := c.Language
	if lang != "zh" && lang != "en" {
		lang = SystemLanguage()
	}
	i18n.Set(lang)
}

// ShowSettings opens the settings window, or brings it to the front.
func (a *App) ShowSettings() {
	settingsMu.Lock()
	h := settingsHwnd
	if h == 0 {
		settingsHwnd = 1 // being created
	}
	settingsMu.Unlock()
	switch h {
	case 0:
		go a.runSettings()
	case 1:
	default:
		pPostMessage.Call(h, msgSetShow, 0, 0)
	}
}

// settingsDo runs f against the open settings window (self-test only).
func (a *App) settingsDo(f func(*ui.App, *ui.Ctx)) bool {
	settingsMu.Lock()
	h := settingsHwnd
	settingsMu.Unlock()
	if h <= 1 || theSettings == nil {
		return false
	}
	s := theSettings
	s.mu.Lock()
	s.tests = append(s.tests, f)
	s.mu.Unlock()
	pPostMessage.Call(h, msgSetTest, 0, 0)
	return true
}

func (a *App) runSettings() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s := &settings{a: a, timers: map[uintptr]func(){}, nextID: 100}
	theSettings = s
	defer func() {
		settingsMu.Lock()
		settingsHwnd = 0
		settingsMu.Unlock()
		a.recHwnd.Store(0)
		a.Activity(false)
		theSettings = nil
	}()
	inst, _, _ := pGetModuleHandle.Call(0)
	s.arrow, _, _ = pLoadCursor.Call(0, 32512)
	s.hand, _, _ = pLoadCursor.Call(0, 32649)
	icon, _, _ := pLoadIcon.Call(inst, 1)
	wc := wndClassEx{WndProc: settingsProcCB, Instance: inst, ClassName: u16(settingsClass), Icon: icon, IconSm: icon, Cursor: s.arrow}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))) // fails harmlessly the second time

	// Size for the screen the pointer is on.
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	mon, _, _ := pMonitorFromPoint.Call(packPoint(pt), 1)
	mi, dpi := monitorOf(mon)
	k := float64(dpi) / 96
	w, h := int32(1140*k), int32(800*k)
	workW, workH := mi.Work.Right-mi.Work.Left, mi.Work.Bottom-mi.Work.Top
	if w > workW-40 {
		w = workW - 40
	}
	if h > workH-40 {
		h = workH - 40
	}
	s.app = ui.New(s)
	s.ctx = ui.NewCtx(s)
	s.memDC, _, _ = pCreateCompatibleDC.Call(0)
	// WS_OVERLAPPEDWINDOW | WS_CLIPCHILDREN
	s.hwnd, _, _ = pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(u16(settingsClass))), uintptr(unsafe.Pointer(u16("SpaceHop"))),
		0x00CF0000|0x02000000, uintptr(mi.Work.Left+(workW-w)/2), uintptr(mi.Work.Top+(workH-h)/2), uintptr(w), uintptr(h), 0, 0, inst, 0)
	if s.hwnd == 0 {
		a.Notify(i18n.T("无法打开设置窗口"))
		return
	}
	settingsMu.Lock()
	settingsHwnd = s.hwnd
	settingsMu.Unlock()
	s.chrome()
	pShowWindow.Call(s.hwnd, swShowNormal)
	pSetForegroundWindow.Call(s.hwnd)

	var m msg
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	for _, f := range s.fonts {
		if f != 0 {
			pDeleteObject.Call(f)
		}
	}
	if s.dib != 0 {
		pSelectObject.Call(s.memDC, s.oldBmp)
		pDeleteObject.Call(s.dib)
	}
	if s.editBrush != 0 {
		pDeleteObject.Call(s.editBrush)
	}
}

func bgr(rgb uint32) uintptr {
	return uintptr(rgb&0xFF)<<16 | uintptr(rgb&0xFF00) | uintptr(rgb>>16&0xFF)
}

// chrome makes the title bar follow the window's colours.
func (s *settings) chrome() {
	s.app.SetDark(!isLight("auto"))
	dark := uint32(0)
	p := &ui.Light
	if s.app.Dark() {
		dark, p = 1, &ui.Dark
	}
	pDwmSetWindowAttr.Call(s.hwnd, 20, uintptr(unsafe.Pointer(&dark)), 4) // DWMWA_USE_IMMERSIVE_DARK_MODE
	col := uint32(bgr(p.Bg))
	pDwmSetWindowAttr.Call(s.hwnd, 35, uintptr(unsafe.Pointer(&col)), 4) // DWMWA_CAPTION_COLOR (Windows 11)
}

func (s *settings) surface() bool {
	var rc rect
	pGetClientRect.Call(s.hwnd, uintptr(unsafe.Pointer(&rc)))
	w, h := rc.Right, rc.Bottom
	if w <= 0 || h <= 0 {
		return false
	}
	if s.dib != 0 && int32(s.canvas.W) == w && int32(s.canvas.H) == h {
		return true
	}
	var bmi struct {
		Size          uint32
		Width, Height int32
		Planes, Bits  uint16
		Compression   uint32
		_             [20]byte
	}
	bmi.Size, bmi.Width, bmi.Height, bmi.Planes, bmi.Bits = 40, w, -h, 1, 32
	var bits uintptr
	dib, _, _ := pCreateDIBSection.Call(s.memDC, uintptr(unsafe.Pointer(&bmi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if dib == 0 || bits == 0 {
		return false
	}
	old, _, _ := pSelectObject.Call(s.memDC, dib)
	if s.dib != 0 {
		pDeleteObject.Call(s.dib)
	} else {
		s.oldBmp = old
	}
	s.dib = dib
	s.canvas = raster.Canvas{Pix: unsafe.Slice((*uint32)(ptr(bits)), int(w)*int(h)), W: int(w), H: int(h)}
	return true
}

func (s *settings) setFonts() {
	dpi, _, _ := pGetDpiForWindow.Call(s.hwnd)
	if dpi < 48 {
		dpi = 96
	}
	s.scale = float64(dpi) / 96
	lang := i18n.Lang()
	if uint32(dpi) == s.fontDPI && lang == s.fontLang {
		return
	}
	s.fontDPI, s.fontLang = uint32(dpi), lang
	face := "Segoe UI"
	if lang == "zh" {
		face = "Microsoft YaHei UI"
	}
	spec := [ui.FontCount]struct {
		face   string
		weight uintptr
	}{{face, 400}, {face, 400}, {face, 600}, {face, 700}, {"Segoe UI", 600}, {"Segoe MDL2 Assets", 400}, {face, 700}, {"Consolas", 400}}
	for i, sp := range spec {
		if s.fonts[i] != 0 {
			pDeleteObject.Call(s.fonts[i])
		}
		h := -int32(ui.FontSize[i]*s.scale + 0.5)
		s.fonts[i], _, _ = pCreateFont.Call(uintptr(h), 0, 0, 0, sp.weight, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(u16(sp.face))))
		var tm [16]int32 // TEXTMETRICW starts with tmHeight
		old, _, _ := pSelectObject.Call(s.memDC, s.fonts[i])
		pGetTextMetrics.Call(s.memDC, uintptr(unsafe.Pointer(&tm)))
		pSelectObject.Call(s.memDC, old)
		s.fontH[i] = tm[0]
	}
	s.widths = map[string]int32{}
	s.ctx.ClearMemo()
}

// frame draws the window once. dry only finds what is under the pointer
// and reports whether that changed.
func (s *settings) frame(dry bool) bool {
	if s.inFrame || s.hwnd == 0 || !s.surface() {
		return false
	}
	s.inFrame = true
	s.setFonts()
	now := time.Now()
	dt := 0.0
	if s.animOn && !dry {
		dt = now.Sub(s.last).Seconds()
		if dt > 0.05 {
			dt = 0.05
		}
	}
	s.ctx.Begin(&s.canvas, s.scale, dt, dry)
	s.app.Frame(s.ctx)
	changed := s.ctx.End()
	if !dry {
		s.last = now
		pGdiFlush.Call()
		s.syncEdit(s.ctx.Edit)
		dc, _, _ := pGetDC.Call(s.hwnd)
		pBitBlt.Call(dc, 0, 0, uintptr(s.canvas.W), uintptr(s.canvas.H), s.memDC, 0, 0, 0x00CC0020)
		pReleaseDC.Call(s.hwnd, dc)
		if s.ctx.Animating != s.animOn {
			s.animOn = s.ctx.Animating
			if s.animOn {
				pSetTimer.Call(s.hwnd, timerSetAnim, 15, 0)
			} else {
				pKillTimer.Call(s.hwnd, timerSetAnim)
			}
		}
	}
	s.inFrame = false
	if !dry {
		if after := s.app.TakeAfter(); len(after) > 0 {
			for _, f := range after {
				f()
			}
			s.chrome()
			s.frame(false)
		}
	}
	return changed
}

// input draws after an event; the second pass shows the result of what the
// first one changed.
func (s *settings) input() {
	s.frame(false)
	s.frame(false)
}

// ---- ui.TextBackend ----

func (s *settings) Width(f ui.Font, str string) float64 {
	u := w16(str)
	if len(u) <= 1 {
		return 0
	}
	old, _, _ := pSelectObject.Call(s.memDC, s.fonts[f])
	var size struct{ W, H int32 }
	pGetTextExtent.Call(s.memDC, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&size)))
	pSelectObject.Call(s.memDC, old)
	return float64(size.W)
}

const (
	dtWordBreak = 0x10
	dtCalcRect  = 0x400
	dtEditCtl   = 0x2000
)

func (s *settings) Height(f ui.Font, str string, width float64) float64 {
	u := w16(str)
	if len(u) <= 1 {
		return 0
	}
	old, _, _ := pSelectObject.Call(s.memDC, s.fonts[f])
	r := rect{0, 0, int32(width), 0}
	pDrawText.Call(s.memDC, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&r)), dtWordBreak|dtCalcRect|dtNoPrefix|dtEditCtl)
	pSelectObject.Call(s.memDC, old)
	return float64(r.Bottom)
}

func (s *settings) Draw(ops []ui.TextOp) {
	start := time.Now()
	defer func() { s.textNs += int64(time.Since(start)); s.textOps += len(ops) }()
	pSetBkMode.Call(s.memDC, 1)
	old, _, _ := pSelectObject.Call(s.memDC, s.fonts[ui.FText])
	for i := range ops {
		op := &ops[i]
		u := w16(op.S)
		if len(u) <= 1 {
			continue
		}
		pSelectObject.Call(s.memDC, s.fonts[op.Font])
		pSetTextColor.Call(s.memDC, bgr(op.Color))
		r := rect{int32(op.X0), int32(op.Y0), int32(op.X1), int32(op.Y1)}
		flags := uintptr(dtNoPrefix)
		if op.Wrap {
			flags |= dtWordBreak | dtEditCtl
		} else {
			flags |= dtSingle | dtVCenter | dtEllipsis
		}
		switch op.Align {
		case ui.Center:
			flags |= dtCenter
		case ui.Right:
			flags |= dtRight
		}
		clipped := op.Clip[2] > op.Clip[0]
		if !op.Wrap {
			// Text that fits needs no ellipsis: place it directly, which is
			// several times cheaper than DrawText.
			key := string(rune('0'+op.Font)) + op.S
			w, ok := s.widths[key]
			if !ok {
				if len(s.widths) > 6000 {
					s.widths = map[string]int32{}
				}
				var size struct{ W, H int32 }
				pGetTextExtent.Call(s.memDC, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&size)))
				w = size.W
				s.widths[key] = w
			}
			if w <= r.Right-r.Left {
				x := r.Left
				switch op.Align {
				case ui.Center:
					x += (r.Right - r.Left - w) / 2
				case ui.Right:
					x = r.Right - w
				}
				y := r.Top + (r.Bottom-r.Top-s.fontH[op.Font])/2
				var opt uintptr
				cr := r
				if clipped {
					opt = 0x4 // ETO_CLIPPED
					cr = rect{int32(op.Clip[0]), int32(op.Clip[1]), int32(op.Clip[2]), int32(op.Clip[3])}
				}
				pExtTextOut.Call(s.memDC, uintptr(x), uintptr(y), opt, uintptr(unsafe.Pointer(&cr)), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), 0)
				continue
			}
		}
		if clipped {
			pSaveDC.Call(s.memDC)
			pIntersectClip.Call(s.memDC, uintptr(op.Clip[0]), uintptr(op.Clip[1]), uintptr(op.Clip[2]), uintptr(op.Clip[3]))
		}
		pDrawText.Call(s.memDC, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&r)), flags)
		if clipped {
			pRestoreDC.Call(s.memDC, ^uintptr(0))
		}
	}
	pSelectObject.Call(s.memDC, old)
	pGdiFlush.Call()
}

// ---- the text field that has the keyboard ----

func (s *settings) syncEdit(e *ui.Edit) {
	if e == nil {
		if s.edit != 0 {
			h := s.edit
			s.edit, s.editID, s.editSpec = 0, "", nil
			pDestroyWindow.Call(h)
			pSetFocus.Call(s.hwnd)
		}
		return
	}
	if s.edit != 0 && s.editID == e.ID {
		if o := s.editSpec; o.X != e.X || o.Y != e.Y || o.W != e.W || o.H != e.H {
			pMoveWindow.Call(s.edit, uintptr(e.X), uintptr(e.Y), uintptr(e.W), uintptr(e.H), 1)
		}
		s.editSpec = e
		return
	}
	if s.edit != 0 {
		h := s.edit
		s.edit = 0
		pDestroyWindow.Call(h)
	}
	style := uintptr(0x40000000 | 0x10000000 | 0x0080) // WS_CHILD | WS_VISIBLE | ES_AUTOHSCROLL
	if e.Multi {
		style = 0x40000000 | 0x10000000 | 0x0004 | 0x0040 | 0x1000 // ES_MULTILINE | ES_AUTOVSCROLL | ES_WANTRETURN
	}
	if e.Number {
		style |= 0x2000
	}
	inst, _, _ := pGetModuleHandle.Call(0)
	s.editID, s.editSpec = e.ID, e
	s.editFont = ui.FText
	if e.Mono {
		s.editFont = ui.FMono
	}
	text := strings.ReplaceAll(strings.ReplaceAll(*e.Text, "\r", ""), "\n", "\r\n")
	h, _, _ := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(u16("EDIT"))), uintptr(unsafe.Pointer(&w16(text)[0])), style,
		uintptr(e.X), uintptr(e.Y), uintptr(e.W), uintptr(e.H), s.hwnd, 0, inst, 0)
	if h == 0 {
		return
	}
	s.editProc, _, _ = pSetWindowLongPtr.Call(h, ^uintptr(3), editProcCB) // GWLP_WNDPROC
	pSendMessage.Call(h, 0x0030, s.fonts[s.editFont], 1)                  // WM_SETFONT
	pSendMessage.Call(h, 0x00D3, 0x3, 0)                                  // EM_SETMARGINS: none
	s.edit = h
	pReleaseCapture.Call()
	pSetFocus.Call(h)
	if e.ClickX > e.W*4 {
		n := uintptr(len(w16(text)))
		pSendMessage.Call(h, 0x00B1, n, n) // EM_SETSEL: caret at the end
	} else {
		// Put the caret where the user clicked.
		at := uintptr(uint16(max(0, e.ClickX))) | uintptr(uint16(max(0, e.ClickY)))<<16
		pSendMessage.Call(h, wmLButtonDown, 1, at)
		pSendMessage.Call(h, wmLButtonUp, 0, at)
	}
}

func (s *settings) editText() string {
	n, _, _ := pGetWindowTextLength.Call(s.edit)
	buf := make([]uint16, n+1)
	pGetWindowText.Call(s.edit, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return strings.ReplaceAll(syscall.UTF16ToString(buf), "\r\n", "\n")
}

func editProc(hwnd, message, wParam, lParam uintptr) uintptr {
	s := theSettings
	if s == nil {
		return 0
	}
	switch uint32(message) {
	case wmKeyDown, 0x0102: // WM_CHAR
		single := s.editSpec != nil && !s.editSpec.Multi
		if wParam == config.VKEsc || wParam == config.VKTab || (single && wParam == config.VKEnter) {
			if uint32(message) == wmKeyDown {
				s.ctx.Blur()
				s.input()
			}
			return 0
		}
	}
	r, _, _ := pCallWindowProc.Call(s.editProc, hwnd, message, wParam, lParam)
	return r
}

// ---- window procedure ----

func hiword(v uintptr) int16 { return int16(v >> 16) }

func settingsProc(hwnd, message, wParam, lParam uintptr) uintptr {
	s := theSettings
	if s == nil || (s.hwnd != 0 && hwnd != s.hwnd) {
		r, _, _ := pDefWindowProc.Call(hwnd, message, wParam, lParam)
		return r
	}
	mouse := func() {
		s.ctx.MX = float64(int16(lParam)) / s.scale
		s.ctx.MY = float64(hiword(lParam)) / s.scale
	}
	switch uint32(message) {
	case 0x0014: // WM_ERASEBKGND
		return 1
	case wmPaint:
		var ps [72]byte
		dc, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if s.hwnd != 0 {
			if s.dib == 0 || !s.sameSize() {
				s.frame(false)
			}
			pBitBlt.Call(dc, 0, 0, uintptr(s.canvas.W), uintptr(s.canvas.H), s.memDC, 0, 0, 0x00CC0020)
		}
		pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case 0x0005: // WM_SIZE
		if s.hwnd != 0 {
			s.frame(false)
		}
		return 0
	case 0x0200: // WM_MOUSEMOVE
		mouse()
		if !s.tracking {
			s.tracking = true
			tme := struct {
				Size, Flags uint32
				Hwnd        uintptr
				Hover       uint32
			}{24, 0x2, hwnd, 0}
			pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
		}
		if s.ctx.Down || s.frame(true) {
			s.frame(false)
		}
		return 0
	case 0x02A3: // WM_MOUSELEAVE
		s.tracking = false
		if !s.ctx.Down {
			s.ctx.MX, s.ctx.MY = -1, -1
			s.input()
		}
		return 0
	case wmLButtonDown, 0x0203: // and double click
		mouse()
		pSetCapture.Call(hwnd)
		s.frame(true) // know what is under the pointer before the press
		s.ctx.Pressed, s.ctx.Down = true, true
		s.input()
		return 0
	case wmLButtonUp:
		mouse()
		pReleaseCapture.Call()
		s.ctx.Released, s.ctx.Down = true, false
		s.input()
		return 0
	case 0x020A: // WM_MOUSEWHEEL
		s.ctx.Wheel = float64(hiword(wParam)) / 120
		s.input()
		return 0
	case wmKeyDown:
		switch wParam {
		case config.VKEsc:
			s.ctx.Key = "Esc"
		case config.VKEnter:
			s.ctx.Key = "Enter"
		default:
			return 0
		}
		s.input()
		return 0
	case 0x0020: // WM_SETCURSOR
		if uint32(lParam)&0xFFFF == 1 { // in the client area
			c := s.arrow
			if s.ctx.Hand {
				c = s.hand
			}
			pSetCursor.Call(c)
			return 1
		}
	case wmCommand:
		if lParam == s.edit && s.edit != 0 && s.editSpec != nil {
			switch wParam >> 16 & 0xFFFF {
			case 0x0300: // EN_CHANGE
				*s.editSpec.Text = s.editText()
				s.input()
			case 0x0200: // EN_KILLFOCUS
				if f, _, _ := pGetFocus.Call(); f != s.hwnd && s.edit != 0 {
					s.ctx.Blur() // the user went to another window
					s.frame(false)
				}
			}
		}
		return 0
	case 0x0133: // WM_CTLCOLOREDIT
		p := &ui.Light
		if s.app.Dark() {
			p = &ui.Dark
		}
		if s.editBrush == 0 || s.editBrushC != p.Surface {
			if s.editBrush != 0 {
				pDeleteObject.Call(s.editBrush)
			}
			s.editBrush, _, _ = pCreateSolidBrush.Call(bgr(p.Surface))
			s.editBrushC = p.Surface
		}
		pSetTextColor.Call(wParam, bgr(p.Text))
		pSetBkColor.Call(wParam, bgr(p.Surface))
		return s.editBrush
	case wmTimer:
		if wParam == timerSetAnim {
			s.frame(false)
			return 0
		}
		pKillTimer.Call(hwnd, wParam)
		if f := s.timers[wParam]; f != nil {
			delete(s.timers, wParam)
			f()
			s.input()
		}
		return 0
	case msgSetAsync:
		s.mu.Lock()
		done := s.done
		s.done = nil
		s.mu.Unlock()
		for _, f := range done {
			f()
		}
		s.input()
		return 0
	case msgSetTest:
		s.mu.Lock()
		tests := s.tests
		s.tests = nil
		s.mu.Unlock()
		for _, f := range tests {
			f(s.app, s.ctx)
		}
		s.chrome()
		s.input()
		return 0
	case msgSetRecKey:
		s.app.OnRecordKey(config.KeyName(uint32(wParam)), lParam != 0)
		s.input()
		return 0
	case msgSetShow:
		if min, _, _ := pIsIconic.Call(hwnd); min != 0 {
			pShowWindow.Call(hwnd, swRestore)
		}
		activate(hwnd)
		return 0
	case msgSetRefresh:
		s.app.Refresh()
		s.chrome()
		s.input()
		return 0
	case 0x0006: // WM_ACTIVATE
		if wParam&0xFFFF == 0 {
			s.a.recHwnd.Store(0) // never keep the keyboard while in the background
			s.app.OnDeactivate()
			s.frame(false)
		}
	case 0x001A: // WM_SETTINGCHANGE: light / dark may have changed
		s.chrome()
		s.frame(false)
	case 0x02E0: // WM_DPICHANGED
		r := (*rect)(ptr(lParam))
		pSetWindowPos.Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), 0x4|0x10)
		return 0
	case 0x0024: // WM_GETMINMAXINFO
		mm := (*[10]int32)(ptr(lParam))
		k := s.scale
		if k == 0 {
			k = 1
		}
		mm[6], mm[7] = int32(900*k), int32(620*k)
		return 0
	case wmDestroy:
		s.hwnd = 0
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, message, wParam, lParam)
	return r
}

func (s *settings) sameSize() bool {
	var rc rect
	pGetClientRect.Call(s.hwnd, uintptr(unsafe.Pointer(&rc)))
	return int32(s.canvas.W) == rc.Right && int32(s.canvas.H) == rc.Bottom
}

// ---- ui.Host ----

func (s *settings) Config() *config.Config             { return s.a.Config() }
func (s *settings) Apps() []config.AppInfo             { return s.a.Apps() }
func (s *settings) Layouts() []config.LayoutInfo       { return s.a.Layouts() }
func (s *settings) Autostart() bool                    { return s.a.Autostart() }
func (s *settings) SetAutostart(on bool) error         { return s.a.SetAutostart(on) }
func (s *settings) RunAction(c config.Action) error    { return s.a.RunAction(c) }
func (s *settings) Usage() (int, map[string]int)       { return s.a.Usage() }
func (s *settings) Version() string                    { return s.a.Version }
func (s *settings) ConfigPath() string                 { return config.Path() }
func (s *settings) Backups() []config.Backup           { return config.Backups() }
func (s *settings) Invalidate()                        { pPostMessage.Call(s.hwnd, msgSetAsync, 0, 0) }
func (s *settings) Clipboard() string                  { t, _ := readClipboard(); return t }
func (s *settings) SetClipboard(t string)              { _ = writeClipboard(t) }
func (s *settings) Activity(on bool) []config.LogEntry { return s.a.Activity(on) }

func (s *settings) Save(c *config.Config) (*config.Config, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	n, err := config.Parse(data)
	if err != nil {
		return nil, err
	}
	if err := config.Save(n); err != nil {
		return nil, err
	}
	_ = s.a.Apply(n)
	return n, nil
}

func (s *settings) Restore(name string) (*config.Config, error) {
	c, err := config.ReadBackup(name)
	if err != nil {
		return nil, err
	}
	return s.Save(c)
}

func (s *settings) Async(work func() any, done func(any)) {
	h := s.hwnd
	go func() {
		v := work()
		s.mu.Lock()
		s.done = append(s.done, func() { done(v) })
		s.mu.Unlock()
		pPostMessage.Call(h, msgSetAsync, 0, 0)
	}()
}

func (s *settings) Later(ms int, f func()) {
	if ms <= 0 {
		ms = 1
	}
	s.nextID++
	s.timers[s.nextID] = f
	pSetTimer.Call(s.hwnd, s.nextID, uintptr(ms), 0)
}

func (s *settings) Menu(items []string, checked int) int {
	menu, _, _ := pCreatePopupMenu.Call()
	if menu == 0 {
		return -1
	}
	defer pDestroyMenu.Call(menu)
	for i, it := range items {
		var flags uintptr
		if i == checked {
			flags = 0x8
		}
		pAppendMenu.Call(menu, flags, uintptr(i+1), uintptr(unsafe.Pointer(u16(it))))
	}
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	cmd, _, _ := pTrackPopupMenu.Call(menu, 0x0100|0x0080, uintptr(pt.X), uintptr(pt.Y), 0, s.hwnd, 0)
	return int(cmd) - 1
}

func (s *settings) Confirm(text string) bool {
	// MB_OKCANCEL | MB_ICONQUESTION
	r, _, _ := pMessageBox.Call(s.hwnd, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16("SpaceHop"))), 0x1|0x20)
	return r == 1
}

type openFileName struct {
	Size                    uint32
	Owner, Instance         uintptr
	Filter, CustomFilter    *uint16
	MaxCustFilter, FilterIx uint32
	File                    *uint16
	MaxFile                 uint32
	FileTitle               *uint16
	MaxFileTitle            uint32
	InitialDir, Title       *uint16
	Flags                   uint32
	FileOffset, FileExt     uint16
	DefExt                  *uint16
	CustData, Hook          uintptr
	TemplateName            *uint16
	Reserved                uintptr
	DwReserved, FlagsEx     uint32
}

func (s *settings) fileDialog(save bool, name string) (string, bool) {
	filter := append(w16("JSON (*.json)"), w16("*.json")...)
	filter = append(filter, 0)
	buf := make([]uint16, 1024)
	copy(buf, w16(name))
	ofn := openFileName{Owner: s.hwnd, Filter: &filter[0], File: &buf[0], MaxFile: uint32(len(buf)), DefExt: u16("json")}
	ofn.Size = uint32(unsafe.Sizeof(ofn))
	var r uintptr
	if save {
		ofn.Flags = 0x2 | 0x800 // OFN_OVERWRITEPROMPT | OFN_PATHMUSTEXIST
		r, _, _ = pGetSaveFileName.Call(uintptr(unsafe.Pointer(&ofn)))
	} else {
		ofn.Flags = 0x1000 | 0x800 // OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST
		r, _, _ = pGetOpenFileName.Call(uintptr(unsafe.Pointer(&ofn)))
	}
	return syscall.UTF16ToString(buf), r != 0
}

func (s *settings) OpenFile() ([]byte, bool) {
	p, ok := s.fileDialog(false, "")
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil || len(data) > 8<<20 {
		return nil, false
	}
	return data, true
}

func (s *settings) SaveFile(name string, data []byte) bool {
	p, ok := s.fileDialog(true, name)
	return ok && os.WriteFile(p, data, 0o644) == nil
}

// Record turns key capture on: the keyboard hook hands every key to this
// window instead of the engine, so even Win and Alt combinations can be
// recorded.
func (s *settings) Record(on bool) {
	if on {
		s.a.recTick.Store(uint32(time.Now().UnixMilli()))
		s.a.recHwnd.Store(s.hwnd)
	} else {
		s.a.recHwnd.Store(0)
	}
}
