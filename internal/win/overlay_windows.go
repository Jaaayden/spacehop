package win

import (
	"errors"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"spacehop/internal/config"
	"spacehop/internal/engine"
	"spacehop/internal/raster"
)

const overlayClass = "SpaceHopHint"

const (
	dtCenter   = 0x1
	dtRight    = 0x2
	dtVCenter  = 0x4
	dtSingle   = 0x20
	dtNoPrefix = 0x800
	dtEllipsis = 0x8000

	maxCols    = 4
	searchRows = 8
	dimAfter   = 20 // runs after which a shortcut is shown less prominently
)

// Layout metrics in pixels at 100% scaling.
const (
	mShadow = 26
	mPad    = 12
	mHead   = 34
	mFoot   = 26
	mCapH   = 24
	mRadius = 14
)

type palette struct {
	bg, border, cap, capEdge, sel raster.Color
	text, dim, accent, capText    uint32 // RGB
}

var (
	darkPal = palette{
		bg: raster.RGB(0x16181D), border: raster.RGB(0x2E323C), cap: raster.RGB(0x282C36), capEdge: raster.RGB(0x3D4350),
		sel: raster.RGB(0x242832), text: 0xE9EBEF, dim: 0x868D9A, accent: 0xC8F53C, capText: 0xF4F6F8,
	}
	lightPal = palette{
		bg: raster.RGB(0xFBFBF9), border: raster.RGB(0xD9DBDF), cap: raster.RGB(0xFFFFFF), capEdge: raster.RGB(0xC9CCD3),
		sel: raster.RGB(0xEEF1E8), text: 0x1B1D22, dim: 0x7C828E, accent: 0x4A7A00, capText: 0x1B1D22,
	}
)

// Glyphs from the Segoe MDL2 Assets font, one per action type.
var glyphs = map[string]string{
	config.OpenApp: "", config.OpenURL: "", config.OpenFile: "", config.ExecuteFile: "",
	config.RunCommand: "", config.Keystroke: "", config.InsertText: "", config.MenuCommand: "",
	config.Window: "", config.InputMethod: "", config.Notify: "", config.System: "",
}

type textOp struct {
	s     string
	r     rect
	font  uintptr
	color uint32
	flags uintptr
}

// overlay is the hint window: a topmost, click-through panel with per-pixel
// transparency that never takes focus. It is drawn only when its content
// changes; hidden, it costs nothing.
type overlay struct {
	hwnd    uintptr
	memDC   uintptr
	dib     uintptr
	oldBmp  uintptr
	canvas  raster.Canvas
	scale   float64
	visible bool

	x, y, w, h int32
	step       int // animation frame; animFrames means settled

	fontDPI                             uint32
	fText, fKey, fSmall, fIcon, fSearch uintptr
	ops                                 []textOp
}

const animFrames = 7

var overlayProcCB = syscall.NewCallback(overlayProc)

func overlayProc(hwnd, message, wParam, lParam uintptr) uintptr {
	if uint32(message) == wmNCHitTest {
		return ^uintptr(0) // HTTRANSPARENT
	}
	r, _, _ := pDefWindowProc.Call(hwnd, message, wParam, lParam)
	return r
}

func (o *overlay) create(inst uintptr) error {
	wc := wndClassEx{WndProc: overlayProcCB, Instance: inst, ClassName: u16(overlayClass)}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, e := pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return e
	}
	// layered | transparent | topmost | toolwindow | noactivate
	const ex = 0x00080000 | 0x00000020 | 0x00000008 | 0x00000080 | 0x08000000
	o.hwnd, _, _ = pCreateWindowEx.Call(ex, uintptr(unsafe.Pointer(u16(overlayClass))), 0,
		0x80000000 /* WS_POPUP */, 0, 0, 10, 10, 0, 0, inst, 0)
	if o.hwnd == 0 {
		return errors.New("无法创建提示窗")
	}
	o.memDC, _, _ = pCreateCompatibleDC.Call(0)
	if o.memDC == 0 {
		return errors.New("无法创建提示窗画布")
	}
	return nil
}

func (o *overlay) hide() {
	if o.visible {
		o.visible = false
		pKillTimer.Call(theApp.hwnd, timerAnim)
		pShowWindow.Call(o.hwnd, swHide)
	}
}

func (o *overlay) px(v int) int32       { return int32(float64(v)*o.scale + 0.5) }
func (o *overlay) fx(v float64) float64 { return v * o.scale }

// surface makes sure the drawing buffer has the given size.
func (o *overlay) surface(w, h int32) bool {
	if o.dib != 0 && int32(o.canvas.W) == w && int32(o.canvas.H) == h {
		return true
	}
	var bmi struct {
		Size          uint32
		Width, Height int32
		Planes, Bits  uint16
		Compression   uint32
		_             [20]byte
	}
	bmi.Size, bmi.Width, bmi.Height, bmi.Planes, bmi.Bits = 40, w, -h, 1, 32 // negative height: top-down
	var bits uintptr
	dib, _, _ := pCreateDIBSection.Call(o.memDC, uintptr(unsafe.Pointer(&bmi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if dib == 0 || bits == 0 {
		return false
	}
	old, _, _ := pSelectObject.Call(o.memDC, dib)
	if o.dib != 0 {
		pDeleteObject.Call(o.dib)
	} else {
		o.oldBmp = old
	}
	o.dib = dib
	o.canvas = raster.Canvas{Pix: unsafe.Slice((*uint32)(ptr(bits)), int(w)*int(h)), W: int(w), H: int(h)}
	return true
}

func (o *overlay) fonts(dpi uint32) {
	if o.fontDPI == dpi && o.fText != 0 {
		return
	}
	for _, f := range []uintptr{o.fText, o.fKey, o.fSmall, o.fIcon, o.fSearch} {
		if f != 0 {
			pDeleteObject.Call(f)
		}
	}
	mk := func(face string, size int, weight uintptr) uintptr {
		h := -int32(float64(size)*float64(dpi)/96 + 0.5)
		// DEFAULT_CHARSET, CLEARTYPE_QUALITY
		f, _, _ := pCreateFont.Call(uintptr(h), 0, 0, 0, weight, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(u16(face))))
		return f
	}
	o.fText = mk("Microsoft YaHei UI", 14, 400)
	o.fKey = mk("Segoe UI", 13, 600)
	o.fSmall = mk("Microsoft YaHei UI", 12, 400)
	o.fSearch = mk("Microsoft YaHei UI", 16, 400)
	o.fIcon = mk("Segoe MDL2 Assets", 13, 400)
	o.fontDPI = dpi
}

func (o *overlay) textWidth(font uintptr, s string) int32 {
	u, _ := syscall.UTF16FromString(strings.ReplaceAll(s, "\x00", ""))
	if len(u) <= 1 {
		return 0
	}
	old, _, _ := pSelectObject.Call(o.memDC, font)
	var size struct{ W, H int32 }
	pGetTextExtent.Call(o.memDC, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&size)))
	pSelectObject.Call(o.memDC, old)
	return size.W
}

func (o *overlay) text(s string, r rect, font uintptr, color uint32, flags uintptr) {
	if s != "" && r.Right > r.Left {
		o.ops = append(o.ops, textOp{s, r, font, color, flags})
	}
}

// keyLabel shortens key names for the caps.
func keyLabel(k string) string {
	switch k {
	case "Backspace":
		return "Bksp"
	case "Left":
		return "←"
	case "Right":
		return "→"
	case "Up":
		return "↑"
	case "Down":
		return "↓"
	}
	return k
}

// capWidth returns the width of a key cap, in real pixels.
func (o *overlay) capWidth(key string) int32 {
	w := o.textWidth(o.fKey, keyLabel(key)) + o.px(14)
	if min := o.px(mCapH + 2); w < min {
		w = min
	}
	return w
}

// cap draws one key cap and returns its width.
func (o *overlay) cap(p *palette, key string, x, y int32, accent bool) int32 {
	w, h := o.capWidth(key), o.px(mCapH)
	fx0, fy0, fx1, fy1 := float64(x), float64(y), float64(x+w), float64(y+h)
	r := o.fx(6.5)
	// A slightly darker lower edge gives the cap some depth.
	o.canvas.Fill(fx0, fy0+o.fx(1.5), fx1, fy1+o.fx(1.5), r, p.capEdge)
	o.canvas.Fill(fx0, fy0, fx1, fy1, r, p.cap)
	o.canvas.Stroke(fx0, fy0, fx1, fy1, r, o.fx(1), p.capEdge)
	color := p.capText
	if accent {
		color = p.accent
	}
	o.text(keyLabel(key), rect{x, y, x + w, y + h}, o.fKey, color, dtCenter)
	return w
}

func isLight(theme string) bool {
	switch theme {
	case "light":
		return true
	case "dark":
		return false
	}
	v, ok := regDword(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, "AppsUseLightTheme")
	return ok && v != 0
}

// show draws the view and puts the window on screen.
func (o *overlay) show(v engine.HintView, cfg *config.Config, uses func(string) int) {
	hc := cfg.Hint
	var mon uintptr
	if hc.Monitor == "pointer" {
		var pt point
		pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
		mon, _, _ = pMonitorFromPoint.Call(packPoint(pt), 2) // MONITOR_DEFAULTTONEAREST
	} else {
		fg, _, _ := pGetForegroundWindow.Call()
		mon, _, _ = pMonitorFromWindow.Call(fg, 1) // MONITOR_DEFAULTTOPRIMARY
	}
	mi, dpi := monitorOf(mon)
	o.scale = float64(dpi) / 96
	o.fonts(dpi)
	o.ops = o.ops[:0]
	pal := &darkPal
	if isLight(hc.Theme) {
		pal = &lightPal
	}
	classic := hc.Layout == "classic" && !v.Search
	rowH, maxRows := 34, 8
	if classic {
		rowH, maxRows = 28, 10
	}

	// ---- measure ----
	items, more := v.Items, 0
	first := 0
	if v.Search {
		// Keep the selection inside a window of rows.
		if v.Sel >= searchRows {
			first = v.Sel - searchRows + 1
		}
		if len(items) > first+searchRows {
			items = items[:first+searchRows]
		}
		items = items[first:]
	} else if limit := maxRows * maxCols; len(items) > limit {
		more = len(items) - (limit - 1)
		items = items[:limit-1]
	}
	n := len(items)
	if more > 0 {
		n++
	}
	if n == 0 {
		n = 1
	}
	rows, cols, colW := n, 1, hc.Width
	if v.Search {
		colW = 460
	} else if n > maxRows {
		cols = (n + maxRows - 1) / maxRows
		rows = (n + cols - 1) / cols
	}
	foot := mFoot
	if classic {
		foot = 0
	}
	inW := o.px(mPad*2 + cols*colW)
	inH := o.px(mPad + mHead + rows*rowH + foot + mPad/2)
	workW, workH := mi.Work.Right-mi.Work.Left, mi.Work.Bottom-mi.Work.Top
	sh := o.px(mShadow)
	if inW > workW {
		inW = workW
	}
	w, h := inW+2*sh, inH+2*sh
	if !o.surface(w, h) {
		return
	}

	// ---- shapes ----
	c := &o.canvas
	c.Clear()
	x0, y0, x1, y1 := float64(sh), float64(sh), float64(sh+inW), float64(sh+inH)
	rad := o.fx(mRadius)
	c.Shadow(x0, y0+o.fx(8), x1, y1+o.fx(8), rad, float64(sh)*0.9, raster.RGB(0).Alpha(0.30))
	c.Fill(x0, y0, x1, y1, rad, pal.bg)
	c.Stroke(x0, y0, x1, y1, rad, o.fx(1), pal.border)

	left, top := sh+o.px(mPad), sh+o.px(mPad)
	right := sh + inW - o.px(mPad)
	headBottom := top + o.px(mHead-8)
	if v.Search {
		// The search line: a magnifier, the query and a caret.
		o.text("", rect{left, top, left + o.px(24), headBottom}, o.fIcon, pal.dim, dtCenter)
		q := v.Query
		tx := left + o.px(30)
		if q == "" {
			o.text("搜索快捷键…", rect{tx, top, right, headBottom}, o.fSearch, pal.dim, 0)
		} else {
			o.text(q, rect{tx, top, right, headBottom}, o.fSearch, pal.text, 0)
		}
		cx := float64(tx + o.textWidth(o.fSearch, strings.ReplaceAll(q, " ", " ")) + o.px(2))
		c.Fill(cx, float64(top)+o.fx(4), cx+o.fx(2), float64(headBottom)-o.fx(4), o.fx(1), raster.RGB(pal.accent))
	} else {
		x := left
		for i, k := range v.Path {
			if i > 0 {
				o.text("›", rect{x, top, x + o.px(18), top + o.px(mCapH)}, o.fText, pal.dim, dtCenter)
				x += o.px(18)
			}
			x += o.cap(pal, k, x, top, i == len(v.Path)-1)
		}
		if v.SearchKey != "" && classic {
			o.text(v.SearchKey+" 搜索", rect{x + o.px(8), top, right, top + o.px(mCapH)}, o.fSmall, pal.dim, dtRight)
		}
	}
	// Hairline under the header.
	c.Fill(x0+o.fx(1), float64(headBottom)+o.fx(3), x1-o.fx(1), float64(headBottom)+o.fx(4), 0, pal.border)

	gridTop := sh + o.px(mPad+mHead)
	cell := func(i int) (int32, int32) {
		return left + o.px((i/rows)*colW), gridTop + o.px((i%rows)*rowH)
	}
	capY := func(y int32) int32 { return y + (o.px(rowH)-o.px(mCapH))/2 }
	switch {
	case len(items) == 0 && v.Search:
		o.text("没有匹配的快捷键", rect{left + o.px(6), gridTop, right, gridTop + o.px(rowH)}, o.fText, pal.dim, 0)
	case len(items) == 0:
		o.text("这里还没有快捷键", rect{left + o.px(6), gridTop, right, gridTop + o.px(rowH)}, o.fText, pal.dim, 0)
	}
	for i, it := range items {
		x, y := cell(i)
		cellRight := x + o.px(colW) - o.px(6)
		if v.Search {
			if first+i == v.Sel {
				c.Fill(float64(x-o.px(4)), float64(y)+o.fx(1), float64(right+o.px(4)), float64(y+o.px(rowH))-o.fx(1), o.fx(8), pal.sel)
				c.Fill(float64(x-o.px(4)), float64(y)+o.fx(9), float64(x-o.px(4))+o.fx(3), float64(y+o.px(rowH))-o.fx(9), o.fx(1.5), raster.RGB(pal.accent))
			}
			// Shortcut on the right, name on the left.
			kx := right
			for j := len(it.Keys) - 1; j >= 0; j-- {
				kx -= o.capWidth(it.Keys[j])
				o.cap(pal, it.Keys[j], kx, capY(y), false)
				kx -= o.px(4)
			}
			o.text(glyphs[it.Type], rect{x + o.px(4), y, x + o.px(26), y + o.px(rowH)}, o.fIcon, pal.dim, dtCenter)
			o.text(it.Label, rect{x + o.px(32), y, kx - o.px(8), y + o.px(rowH)}, o.fText, pal.text, 0)
			continue
		}
		tx := x + o.px(2) + o.cap(pal, it.Key, x+o.px(2), capY(y), false) + o.px(10)
		color := pal.text
		if hc.Dim && it.ID != "" && uses(it.ID) >= dimAfter {
			color = pal.dim
		}
		if classic {
			label := it.Label
			if it.Group {
				label, color = label+" …", pal.accent
			}
			o.text(label, rect{tx, y, cellRight, y + o.px(rowH)}, o.fText, color, 0)
			continue
		}
		if it.Group {
			// A folder of shortcuts: how many, and a chevron.
			count := strconv.Itoa(it.Count)
			cw := o.textWidth(o.fSmall, count) + o.px(18)
			o.text(count+" ›", rect{cellRight - cw, y, cellRight, y + o.px(rowH)}, o.fSmall, pal.accent, dtRight)
			o.text(it.Label, rect{tx, y, cellRight - cw - o.px(6), y + o.px(rowH)}, o.fText, pal.dim, 0)
			continue
		}
		if g := glyphs[it.Type]; g != "" {
			o.text(g, rect{tx - o.px(2), y, tx + o.px(18), y + o.px(rowH)}, o.fIcon, pal.dim, dtCenter)
			tx += o.px(24)
		}
		o.text(it.Label, rect{tx, y, cellRight, y + o.px(rowH)}, o.fText, color, 0)
	}
	if more > 0 {
		x, y := cell(len(items))
		o.text("还有 "+strconv.Itoa(more)+" 项", rect{x + o.px(8), y, x + o.px(colW), y + o.px(rowH)}, o.fText, pal.dim, 0)
	}
	if foot > 0 {
		help := "Esc 取消"
		switch {
		case v.Search:
			help = "方向键 选择    Enter 运行    Esc 关闭"
		case len(v.Path) > 1:
			help = "Backspace 返回上一级    Esc 取消"
		}
		if v.SearchKey != "" {
			help += "    " + v.SearchKey + " 搜索"
		}
		fy := sh + inH - o.px(foot+2)
		o.text(help, rect{left + o.px(4), fy, right, fy + o.px(foot-4)}, o.fSmall, pal.dim, 0)
	}

	// ---- text ----
	pSetBkMode.Call(o.memDC, 1) // TRANSPARENT
	oldFont, _, _ := pSelectObject.Call(o.memDC, o.fText)
	for i := range o.ops {
		op := &o.ops[i]
		pSelectObject.Call(o.memDC, op.font)
		// COLORREF is 0x00BBGGRR.
		pSetTextColor.Call(o.memDC, uintptr(op.color&0xFF)<<16|uintptr(op.color&0xFF00)|uintptr(op.color>>16&0xFF))
		u, _ := syscall.UTF16FromString(strings.ReplaceAll(op.s, "\x00", ""))
		pDrawText.Call(o.memDC, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&op.r)),
			op.flags|dtSingle|dtVCenter|dtNoPrefix|dtEllipsis)
	}
	pSelectObject.Call(o.memDC, oldFont)
	pGdiFlush.Call()
	for i := range o.ops {
		r := o.ops[i].r
		c.Opaque(int(r.Left), int(r.Top), int(r.Right), int(r.Bottom))
	}

	// ---- place ----
	o.w, o.h = w, h
	o.x = mi.Work.Left + workW*int32(hc.X)/100 - w/2
	o.y = mi.Work.Bottom - workH*int32(hc.Y)/100 - inH - sh
	if o.x+sh < mi.Work.Left {
		o.x = mi.Work.Left - sh
	}
	if o.x+w-sh > mi.Work.Right {
		o.x = mi.Work.Right - w + sh
	}
	if o.y+sh < mi.Work.Top {
		o.y = mi.Work.Top - sh
	}
	if !o.visible {
		o.visible = true
		o.step = 0
		o.present()
		// SW_SHOWNOACTIVATE, then HWND_TOPMOST with NOMOVE | NOSIZE | NOACTIVATE.
		pShowWindow.Call(o.hwnd, 4)
		pSetWindowPos.Call(o.hwnd, ^uintptr(0), 0, 0, 0, 0, 0x0001|0x0002|0x0010)
		pSetTimer.Call(theApp.hwnd, timerAnim, 15, 0)
		return
	}
	o.present()
}

// present pushes the drawn buffer to the screen at the current animation
// step: a short fade and rise when the window first appears.
func (o *overlay) present() {
	alpha, rise := 255.0, 0.0
	if o.step < animFrames {
		t := float64(o.step+1) / float64(animFrames+1)
		e := 1 - (1-t)*(1-t)*(1-t) // ease-out
		alpha, rise = 255*e, (1-e)*o.fx(10)
	}
	pos := point{o.x, o.y + int32(rise)}
	size := point{o.w, o.h}
	var src point
	blend := [4]byte{0, 0, byte(alpha), 1} // AC_SRC_OVER, constant alpha, AC_SRC_ALPHA
	pUpdateLayered.Call(o.hwnd, 0, uintptr(unsafe.Pointer(&pos)), uintptr(unsafe.Pointer(&size)), o.memDC,
		uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), 2 /* ULW_ALPHA */)
}

// tick advances the appear animation; it stops its own timer when done.
func (o *overlay) tick() {
	if !o.visible || o.step >= animFrames {
		pKillTimer.Call(theApp.hwnd, timerAnim)
		return
	}
	o.step++
	o.present()
}
