// Package ui is SpaceHop's settings window, drawn by the program itself.
//
// It is an immediate-mode interface: every frame the whole window is
// described again from the current state, and the same pass both draws and
// reacts to the mouse. Nothing here depends on Windows; the platform layer
// supplies a pixel buffer, a way to draw text, and input events. That keeps
// the logic testable anywhere.
package ui

import (
	"math"
	"strings"

	"spacehop/internal/raster"
)

type Font int

const (
	FText  Font = iota // 14 regular
	FSmall             // 12 regular
	FBold              // 14 semibold
	FTitle             // 22 bold
	FKey               // 12 semibold, for key caps
	FIcon              // 15, Segoe MDL2 Assets
	FBig               // 32 bold
	FMono              // 13 monospace
	FontCount
)

// FontSize is the pixel size of each font at 100% scaling.
var FontSize = [FontCount]float64{14, 12, 14, 22, 12, 15, 32, 13}

type Align int

const (
	Left Align = iota
	Center
	Right
)

// TextOp is one piece of text to draw, in device pixels.
type TextOp struct {
	S              string
	Font           Font
	X0, Y0, X1, Y1 int
	Color          uint32 // 0xRRGGBB
	Align          Align
	Wrap           bool   // break lines, top-aligned; otherwise one centred line
	Clip           [4]int // x0, y0, x1, y1 when x1 > x0
}

// TextBackend measures and draws text. Widths and heights are in device
// pixels for the given scale.
type TextBackend interface {
	Width(f Font, s string) float64
	// Height of s wrapped into the given width.
	Height(f Font, s string, width float64) float64
	Draw(ops []TextOp)
}

type Rect struct{ X, Y, W, H float64 }

func (r Rect) In(x, y float64) bool { return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H }
func (r Rect) Inset(d float64) Rect { return Rect{r.X + d, r.Y + d, r.W - 2*d, r.H - 2*d} }

// Edit describes the one text field that has the keyboard. The platform
// places a native edit control over it.
type Edit struct {
	ID             string
	X, Y, W, H     int // device pixels
	Text           *string
	Multi, Mono    bool
	Number         bool
	ClickX, ClickY int  // where the field was clicked, relative to it
	Fresh          bool // focus arrived this frame
}

// Ctx is one frame.
type Ctx struct {
	C    *raster.Canvas
	S    float64 // device pixels per unit
	TB   TextBackend
	P    *Palette
	Dry  bool    // react to input but draw nothing
	W, H float64 // window size in units
	Dt   float64 // seconds since the previous frame

	// Input for this frame.
	MX, MY             float64
	Pressed, Released  bool
	Down               bool
	Wheel              float64
	Key                string // "Esc", "Enter", "Up", "Down" pressed this frame
	hot, nextHot       string
	active             string
	inputOff           int // >0 while drawing under a modal layer
	clip               []Rect
	ops                []TextOp
	Hand               bool // the pointer is over something clickable
	focus              string
	focusFresh         bool
	clickX, clickY     float64
	Edit               *Edit
	Animating          bool
	anim               map[string]float64
	scroll             map[string]float64
	Tip                string
	tipRect            Rect
	memo               map[string]float64
	memoLang           string
	dragStartX, dragY0 float64
	pressOnField       bool
	// FieldPad is extra room on the left of the next text field (an icon).
	FieldPad float64
}

func (c *Ctx) px(v float64) float64 { return v * c.S }

// ---- drawing ----

func (c *Ctx) Fill(r Rect, radius float64, col raster.Color) {
	if !c.Dry {
		c.C.Fill(c.px(r.X), c.px(r.Y), c.px(r.X+r.W), c.px(r.Y+r.H), c.px(radius), col)
	}
}

func (c *Ctx) Stroke(r Rect, radius, w float64, col raster.Color) {
	if !c.Dry {
		c.C.Stroke(c.px(r.X), c.px(r.Y), c.px(r.X+r.W), c.px(r.Y+r.H), c.px(radius), math.Max(1, math.Round(c.px(w))), col)
	}
}

func (c *Ctx) Shadow(r Rect, radius, blur float64, col raster.Color) {
	if !c.Dry {
		c.C.Shadow(c.px(r.X), c.px(r.Y), c.px(r.X+r.W), c.px(r.Y+r.H), c.px(radius), c.px(blur), col)
	}
}

// Box is a filled, outlined rounded rectangle.
func (c *Ctx) Box(r Rect, radius float64, fill, line raster.Color) {
	c.Fill(r, radius, fill)
	c.Stroke(r, radius, 1, line)
}

// TextW is the width of a single line, in units.
func (c *Ctx) TextW(f Font, s string) float64 {
	if s == "" {
		return 0
	}
	key := string(rune('0'+f)) + s
	if w, ok := c.memo[key]; ok {
		return w
	}
	if len(c.memo) > 4000 {
		c.memo = map[string]float64{}
	}
	w := c.TB.Width(f, s) / c.S
	c.memo[key] = w
	return w
}

// TextH is the height of text wrapped to a width, in units.
func (c *Ctx) TextH(f Font, s string, width float64) float64 {
	key := string(rune('0'+f)) + "\x00" + s + "\x00" + string(rune(int(width)))
	if h, ok := c.memo[key]; ok {
		return h
	}
	h := c.TB.Height(f, s, c.px(width)) / c.S
	c.memo[key] = h
	return h
}

func (c *Ctx) text(s string, f Font, r Rect, col uint32, a Align, wrap bool) {
	if c.Dry || s == "" || r.W <= 0 {
		return
	}
	op := TextOp{S: s, Font: f, X0: int(c.px(r.X)), Y0: int(c.px(r.Y)), X1: int(c.px(r.X + r.W)), Y1: int(c.px(r.Y + r.H)), Color: col, Align: a, Wrap: wrap}
	if n := len(c.clip); n > 0 {
		k := c.clip[n-1]
		op.Clip = [4]int{int(c.px(k.X)), int(c.px(k.Y)), int(c.px(k.X + k.W)), int(c.px(k.Y + k.H))}
		if op.Y1 <= op.Clip[1] || op.Y0 >= op.Clip[3] {
			return
		}
	}
	c.ops = append(c.ops, op)
}

// Text draws one line, vertically centred in r, cut with an ellipsis.
func (c *Ctx) Text(s string, f Font, r Rect, col uint32, a Align) { c.text(s, f, r, col, a, false) }

// Para draws wrapped text from the top of r and returns its height.
func (c *Ctx) Para(s string, f Font, x, y, w float64, col uint32) float64 {
	h := c.TextH(f, s, w)
	c.text(s, f, Rect{x, y, w, h + 2}, col, Left, true)
	return h
}

// Flush draws the queued text. Call it before drawing a layer on top.
func (c *Ctx) Flush() {
	if len(c.ops) > 0 && !c.Dry {
		c.TB.Draw(c.ops)
	}
	c.ops = c.ops[:0]
}

func (c *Ctx) PushClip(r Rect) {
	c.Flush()
	if n := len(c.clip); n > 0 { // intersect with the enclosing clip
		k := c.clip[n-1]
		x0, y0 := math.Max(r.X, k.X), math.Max(r.Y, k.Y)
		x1, y1 := math.Min(r.X+r.W, k.X+k.W), math.Min(r.Y+r.H, k.Y+k.H)
		r = Rect{x0, y0, math.Max(0, x1-x0), math.Max(0, y1-y0)}
	}
	c.clip = append(c.clip, r)
	c.setClip()
}

func (c *Ctx) PopClip() {
	c.Flush()
	c.clip = c.clip[:len(c.clip)-1]
	c.setClip()
}

func (c *Ctx) setClip() {
	if c.Dry {
		return
	}
	if n := len(c.clip); n > 0 {
		k := c.clip[n-1]
		c.C.Clip = [4]int{int(c.px(k.X)), int(c.px(k.Y)), int(c.px(k.X+k.W)) + 1, int(c.px(k.Y+k.H)) + 1}
		if c.C.Clip[2] <= c.C.Clip[0] {
			c.C.Clip[2] = c.C.Clip[0] + 1
			c.C.Clip[3] = c.C.Clip[1]
		}
	} else {
		c.C.Clip = [4]int{}
	}
}

func (c *Ctx) visible(r Rect) bool {
	if n := len(c.clip); n > 0 {
		k := c.clip[n-1]
		return r.Y+r.H > k.Y && r.Y < k.Y+k.H
	}
	return true
}

// ---- interaction ----

// Hover reports whether the pointer is over r for the widget id.
func (c *Ctx) Hover(id string, r Rect) bool {
	if c.inputOff > 0 || !r.In(c.MX, c.MY) {
		return false
	}
	if n := len(c.clip); n > 0 && !c.clip[n-1].In(c.MX, c.MY) {
		return false
	}
	c.nextHot = id
	return c.hot == id
}

// Click reports a completed click on r.
func (c *Ctx) Click(id string, r Rect) bool {
	h := c.Hover(id, r)
	if h {
		c.Hand = true
		if c.Pressed {
			c.active = id
		}
	}
	return h && c.Released && c.active == id
}

// Held reports whether the mouse button went down on id and is still down.
func (c *Ctx) Held(id string) bool { return c.active == id && c.Down }

// Anim moves a value towards target and returns it; while it is on the way
// the platform keeps drawing frames.
func (c *Ctx) Anim(id string, target float64) float64 {
	v, ok := c.anim[id]
	if !ok {
		c.anim[id] = target
		return target
	}
	if math.Abs(v-target) < 0.002 {
		c.anim[id] = target
		return target
	}
	if !c.Dry {
		v += (target - v) * math.Min(1, c.Dt*18) // quick ease-out
		c.anim[id] = v
	}
	c.Animating = true
	return v
}

// SetAnim jumps a value, e.g. to start an entrance from 0.
func (c *Ctx) SetAnim(id string, v float64) { c.anim[id] = v }

func mixRGB(a, b uint32, t float64) uint32 {
	ch := func(sh uint) uint32 {
		x, y := float64(a>>sh&0xFF), float64(b>>sh&0xFF)
		return uint32(x+(y-x)*t+0.5) << sh
	}
	return ch(16) | ch(8) | ch(0)
}

// Scroll makes view a scrolling area for content of the given height and
// returns the offset to subtract from y positions. Call EndScroll after.
func (c *Ctx) Scroll(id string, view Rect, contentH float64) float64 {
	max := math.Max(0, contentH-view.H)
	off := c.scroll[id]
	if view.In(c.MX, c.MY) && c.Wheel != 0 && c.inputOff == 0 {
		off -= c.Wheel * 60
		c.Wheel = 0
	}
	off = math.Max(0, math.Min(max, off))
	c.scroll[id] = off
	c.PushClip(view)
	if max > 0 && !c.Dry { // thumb
		th := math.Max(28, view.H*view.H/contentH)
		ty := view.Y + (view.H-th)*off/max
		c.Fill(Rect{view.X + view.W - 6, ty, 3, th}, 1.5, raster.RGB(c.P.Faint).Alpha(0.6))
	}
	return off
}

func (c *Ctx) EndScroll() { c.PopClip() }

// ResetScroll puts a scrolling area back at the top.
func (c *Ctx) ResetScroll(id string) { delete(c.scroll, id) }

// ---- widgets ----

type BtnStyle int

const (
	BtnPlain BtnStyle = iota
	BtnPrimary
	BtnGhost
	BtnDanger
)

// BtnW is the width a button needs for its label.
func (c *Ctx) BtnW(icon, label string) float64 {
	w := c.TextW(FText, label) + 26
	if icon != "" {
		w += 22
		if label == "" {
			w = 34
		}
	}
	return w
}

// Button draws a button at x, y (height 34) and reports a click.
func (c *Ctx) Button(id string, x, y float64, icon, label string, style BtnStyle, disabled bool) (bool, float64) {
	w := c.BtnW(icon, label)
	r := Rect{x, y, w, 34}
	if !c.visible(r) {
		return false, w
	}
	clicked := false
	hover := false
	if !disabled {
		clicked = c.Click(id, r)
		hover = c.hot == id
	}
	press := c.Anim(id+"/p", b2f(hover && c.Held(id)))
	r = r.Inset(press * 1)
	p := c.P
	fill, line, fg := raster.RGB(p.Surface), raster.RGB(p.Line2), p.Text
	switch style {
	case BtnPrimary:
		fill, line, fg = raster.RGB(p.Accent), raster.RGB(p.Accent), p.AccentInk
		if hover {
			fill = raster.RGB(mixRGB(p.Accent, 0xFFFFFF, 0.18))
		}
	case BtnGhost, BtnDanger:
		fill, line, fg = 0, 0, p.Muted
		if style == BtnDanger {
			fg = p.Danger
		}
		if hover {
			fill = raster.RGB(p.Raised)
			if style == BtnGhost {
				fg = p.Text
			}
		}
	default:
		if hover {
			fill = raster.RGB(p.Raised)
		}
	}
	if disabled {
		fg = p.Faint
		if style == BtnPrimary {
			fill, line = raster.RGB(p.Raised), raster.RGB(p.Line)
		}
	}
	if fill != 0 {
		c.Fill(r, 9, fill)
	}
	if line != 0 && style != BtnPrimary {
		c.Stroke(r, 9, 1, line)
	}
	tx := r.X + 13
	if icon != "" {
		if label == "" {
			c.Text(icon, FIcon, r, fg, Center)
			return clicked, w
		}
		c.Text(icon, FIcon, Rect{r.X + 10, r.Y, 20, r.H}, fg, Center)
		tx = r.X + 35
	}
	f := FText
	if style == BtnPrimary {
		f = FBold
	}
	c.Text(label, f, Rect{tx, r.Y, r.W, r.H}, fg, Left)
	return clicked, w
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// Toggle draws a switch (38x22) and reports a click.
func (c *Ctx) Toggle(id string, x, y float64, on bool) bool {
	r := Rect{x, y, 38, 22}
	if !c.visible(r) {
		return false
	}
	clicked := c.Click(id, r)
	t := c.Anim(id+"/t", b2f(on))
	p := c.P
	c.Fill(r, 11, raster.RGB(mixRGB(p.Line2, p.Accent, t)))
	c.Fill(Rect{x + 3 + 16*t, y + 3, 16, 16}, 8, raster.RGB(mixRGB(0xFFFFFF, p.AccentInk, t)))
	return clicked
}

// Seg draws a segmented control at x, y (height 34) and returns the chosen
// index, or -1, and its width.
func (c *Ctx) Seg(id string, x, y float64, labels []string, cur int) (int, float64) {
	w := 6.0
	ws := make([]float64, len(labels))
	for i, l := range labels {
		ws[i] = c.TextW(FText, l) + 24
		w += ws[i]
	}
	r := Rect{x, y, w, 34}
	picked := -1
	if !c.visible(r) {
		return picked, w
	}
	p := c.P
	c.Box(r, 10, raster.RGB(p.Raised), raster.RGB(p.Line))
	// The selected pill slides between positions.
	cx := x + 3
	var selX, selW float64
	for i := range labels {
		if i == cur {
			selX, selW = cx, ws[i]
		}
		cx += ws[i]
	}
	if cur >= 0 && cur < len(labels) {
		ax := c.Anim(id+"/x", selX)
		aw := c.Anim(id+"/w", selW)
		pill := Rect{ax, y + 3, aw, 28}
		c.Shadow(Rect{pill.X, pill.Y + 1, pill.W, pill.H}, 7, 3, raster.RGB(0).Alpha(0.12))
		c.Fill(pill, 7, raster.RGB(p.Surface))
	}
	cx = x + 3
	for i, l := range labels {
		ir := Rect{cx, y + 3, ws[i], 28}
		if c.Click(id+"/"+itoa(i), ir) && i != cur {
			picked = i
		}
		col, f := p.Muted, FText
		if i == cur {
			col, f = p.Text, FBold
		}
		c.Text(l, f, ir, col, Center)
		cx += ws[i]
	}
	return picked, w
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// KeyShow is the label printed on a key cap.
func KeyShow(k string) string {
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

// CapW is the width of a key cap.
func (c *Ctx) CapW(k string) float64 { return math.Max(26, c.TextW(FKey, KeyShow(k))+14) }

// Cap draws a key cap (height 26) and returns its width.
func (c *Ctx) Cap(k string, x, y float64, lead bool) float64 {
	w := c.CapW(k)
	r := Rect{x, y, w, 26}
	p := c.P
	fill, edge, fg := raster.RGB(p.Key), raster.RGB(p.KeyEdge), p.Text
	if lead {
		fill, edge, fg = raster.RGB(p.Accent), raster.RGB(mixRGB(p.Accent, 0, 0.35)), p.AccentInk
	}
	c.Fill(Rect{x, y + 1.5, w, 26}, 7, edge)
	c.Fill(r, 7, fill)
	c.Stroke(r, 7, 1, edge)
	c.Text(KeyShow(k), FKey, r, fg, Center)
	return w
}

// Chord draws the caps of "Ctrl+Alt+J" and returns the total width.
func (c *Ctx) Chord(chord string, x, y float64, lead bool) float64 {
	x0 := x
	for _, k := range strings.Split(chord, "+") {
		x += c.Cap(k, x, y, lead) + 4
	}
	return x - x0 - 4
}

func (c *Ctx) ChordW(chord string) float64 {
	w := -4.0
	for _, k := range strings.Split(chord, "+") {
		w += c.CapW(k) + 4
	}
	return w
}

type ChipStyle int

const (
	ChipPlain ChipStyle = iota
	ChipAccent
	ChipWarn
)

func (c *Ctx) ChipW(label string) float64 { return c.TextW(FSmall, label) + 18 }

// Chip draws a small label (height 22) and returns its width.
func (c *Ctx) Chip(label string, x, y float64, style ChipStyle) float64 {
	w := c.ChipW(label)
	r := Rect{x, y, w, 22}
	p := c.P
	switch style {
	case ChipAccent:
		c.Fill(r, 11, raster.RGB(p.AccentSoft))
		c.Text(label, FSmall, r, p.AccentText, Center)
	case ChipWarn:
		c.Fill(r, 11, raster.RGB(p.WarnSoft))
		c.Text(label, FSmall, r, p.Warn, Center)
	default:
		c.Box(r, 11, raster.RGB(p.Raised), raster.RGB(p.Line))
		c.Text(label, FSmall, r, p.Muted, Center)
	}
	return w
}

// Field draws a text field. While it has the keyboard, the platform shows
// a native edit control in its place.
func (c *Ctx) Field(id string, r Rect, value *string, placeholder string, multi, mono, number bool) {
	if !c.visible(r) {
		if c.focus == id {
			c.focus = ""
		}
		return
	}
	p := c.P
	focused := c.focus == id && c.inputOff == 0
	hover := c.Hover(id, r)
	if hover && c.Pressed {
		c.pressOnField = true
	}
	if hover && c.Pressed && !focused {
		c.focus, c.focusFresh = id, true
		c.clickX, c.clickY = c.MX-r.X, c.MY-r.Y
		focused = true
	}
	line := raster.RGB(p.Line2)
	if focused {
		c.Fill(Rect{r.X - 3, r.Y - 3, r.W + 6, r.H + 6}, 12, raster.RGB(p.Accent).Alpha(0.35))
		line = raster.RGB(p.AccentText)
	}
	c.Box(r, 9, raster.RGB(p.Surface), line)
	inner := Rect{r.X + 11 + c.FieldPad, r.Y + 1, r.W - 22 - c.FieldPad, r.H - 2}
	if multi {
		inner = Rect{r.X + 11, r.Y + 8, r.W - 22, r.H - 16}
	}
	if focused {
		e := &Edit{ID: id, X: int(c.px(inner.X)), W: int(c.px(inner.W)), Text: value, Multi: multi, Mono: mono, Number: number,
			ClickX: int(c.px(c.clickX - 11 - c.FieldPad)), ClickY: int(c.px(c.clickY - 8)), Fresh: c.focusFresh}
		// A single-line control is as tall as its text, centred in the box.
		h := c.px(FontSize[FText] * 1.45)
		e.Y, e.H = int(c.px(r.Y)+(c.px(r.H)-h)/2), int(h)
		if multi {
			e.Y, e.H = int(c.px(inner.Y)), int(c.px(inner.H))
		}
		c.Edit = e
		return
	}
	f := FText
	if mono {
		f = FMono
	}
	if *value == "" {
		if multi {
			c.Para(placeholder, FText, inner.X, inner.Y, inner.W, p.Faint)
		} else {
			c.Text(placeholder, FText, inner, p.Faint, Left)
		}
		return
	}
	if multi {
		c.PushClip(inner)
		c.Para(*value, f, inner.X, inner.Y, inner.W, p.Text)
		c.PopClip()
	} else {
		c.Text(*value, f, inner, p.Text, Left)
	}
}

// Focused reports which text field has the keyboard, or "".
func (c *Ctx) Focused() string { return c.focus }

// Hot reports the widget under the pointer.
func (c *Ctx) Hot() string { return c.hot }

// Blur takes the keyboard away from the text field.
func (c *Ctx) Blur() { c.focus = "" }

// Focus gives a text field the keyboard.
func (c *Ctx) Focus(id string) { c.focus, c.focusFresh, c.clickX = id, true, 1e6 }

// Slider draws a horizontal slider and returns the new value.
func (c *Ctx) Slider(id string, r Rect, v, min, max, step float64) float64 {
	p := c.P
	track := Rect{r.X, r.Y + r.H/2 - 2, r.W, 4}
	if c.Hover(id, Rect{r.X - 8, r.Y, r.W + 16, r.H}) {
		c.Hand = true
		if c.Pressed {
			c.active = id
		}
	}
	if c.Held(id) {
		t := math.Max(0, math.Min(1, (c.MX-r.X)/r.W))
		v = math.Round((min+t*(max-min))/step) * step
	}
	t := (v - min) / (max - min)
	c.Fill(track, 2, raster.RGB(p.Line2))
	c.Fill(Rect{track.X, track.Y, track.W * t, 4}, 2, raster.RGB(p.AccentText))
	k := Rect{r.X + r.W*t - 8, r.Y + r.H/2 - 8, 16, 16}
	c.Shadow(Rect{k.X, k.Y + 1, 16, 16}, 8, 4, raster.RGB(0).Alpha(0.2))
	c.Fill(k, 8, raster.RGB(0xFFFFFF))
	c.Stroke(k, 8, 1, raster.RGB(p.Line2))
	return v
}

// Select draws a drop-down button showing the current choice and reports a
// click; the caller then asks the platform for a menu.
func (c *Ctx) Select(id string, r Rect, label string, placeholder bool) bool {
	if !c.visible(r) {
		return false
	}
	p := c.P
	clicked := c.Click(id, r)
	fill := raster.RGB(p.Surface)
	if c.hot == id {
		fill = raster.RGB(p.Raised)
	}
	c.Box(r, 9, fill, raster.RGB(p.Line2))
	col := p.Text
	if placeholder {
		col = p.Faint
	}
	c.Text(label, FText, Rect{r.X + 11, r.Y, r.W - 40, r.H}, col, Left)
	c.Text(IconChevron, FIcon, Rect{r.X + r.W - 30, r.Y, 22, r.H}, p.Muted, Center)
	return clicked
}

// Card draws the background of a group of settings.
func (c *Ctx) Card(r Rect) { c.Box(r, 12, raster.RGB(c.P.Surface), raster.RGB(c.P.Line)) }

// Label draws a small field caption and returns the y below it.
func (c *Ctx) Label(s string, x, y float64) float64 {
	c.Text(s, FSmall, Rect{x, y, 400, 16}, c.P.Muted, Left)
	return y + 22
}

// SetTip shows a tooltip for the hovered widget.
func (c *Ctx) SetTip(id string, r Rect, tip string) {
	if c.hot == id && tip != "" {
		c.Tip, c.tipRect = tip, r
	}
}

// Glyphs of the Segoe MDL2 Assets font.
const (
	IconKeys     = ""
	IconLeader   = ""
	IconHint     = ""
	IconGeneral  = ""
	IconActivity = ""
	IconAbout    = ""
	IconPlus     = ""
	IconClose    = ""
	IconCheck    = ""
	IconSearch   = ""
	IconPlay     = ""
	IconTrash    = ""
	IconCopy     = ""
	IconImport   = ""
	IconExport   = ""
	IconWarn     = ""
	IconChevron  = ""
	IconInfo     = ""
	IconFolder   = ""
	IconBack     = ""
)

// TypeIcon is the glyph of each action type.
var TypeIcon = map[string]string{
	"openApp": "", "openURL": "", "openFile": "", "executeFile": "",
	"runCommand": "", "keystroke": "", "insertText": "", "menu": "",
	"window": "", "inputMethod": "", "notify": "", "system": "",
}

// NewCtx returns the state that lives across frames.
func NewCtx(tb TextBackend) *Ctx {
	return &Ctx{TB: tb, anim: map[string]float64{}, scroll: map[string]float64{}, memo: map[string]float64{}}
}

// Begin starts a frame on canvas cv at the given scale.
func (c *Ctx) Begin(cv *raster.Canvas, scale float64, dt float64, dry bool) {
	if scale != c.S {
		c.memo = map[string]float64{} // measurements depend on the scale
	}
	c.C, c.S, c.Dt, c.Dry = cv, scale, dt, dry
	c.W, c.H = float64(cv.W)/scale, float64(cv.H)/scale
	c.nextHot, c.Hand, c.Animating, c.Edit, c.Tip = "", false, false, nil, ""
	c.clip, c.ops, c.inputOff = c.clip[:0], c.ops[:0], 0
	cv.Clip = [4]int{}
}

// End finishes a frame. It reports whether the widget under the pointer
// changed, which is how the platform knows a pointer move needs a redraw.
func (c *Ctx) End() (hotChanged bool) {
	c.Flush()
	hotChanged = c.nextHot != c.hot
	c.hot = c.nextHot
	if c.Dry {
		return hotChanged
	}
	if c.Released || !c.Down {
		c.active = ""
	}
	if c.Pressed && !c.pressOnField {
		c.focus = "" // a click outside any text field takes the keyboard away
	}
	c.pressOnField = false
	c.Pressed, c.Released, c.Wheel, c.Key, c.focusFresh = false, false, 0, "", false
	return hotChanged
}

// ClearMemo forgets text measurements (after a language change).
func (c *Ctx) ClearMemo() { c.memo = map[string]float64{} }

// Image draws straight-alpha RGBA pixels (w wide) at a position in units.
func (c *Ctx) Image(pix []uint8, w, h int, x, y float64) {
	if c.Dry {
		return
	}
	x0, y0 := int(c.px(x)), int(c.px(y))
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			px, py := x0+i, y0+j
			if px < 0 || py < 0 || px >= c.C.W || py >= c.C.H {
				continue
			}
			s := pix[(j*w+i)*4:]
			a := uint32(s[3])
			if a == 0 {
				continue
			}
			d := c.C.Pix[py*c.C.W+px]
			k := 255 - a
			r := (uint32(s[0])*a + (d>>16&0xFF)*k) / 255
			g := (uint32(s[1])*a + (d>>8&0xFF)*k) / 255
			b := (uint32(s[2])*a + (d&0xFF)*k) / 255
			c.C.Pix[py*c.C.W+px] = 0xFF000000 | r<<16 | g<<8 | b
		}
	}
}
