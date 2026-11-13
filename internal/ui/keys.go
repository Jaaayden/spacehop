package ui

import (
	"math"
	"sort"
	"strings"

	"spacehop/internal/config"
	"spacehop/internal/i18n"
	"spacehop/internal/raster"
)

type mapKey struct {
	k string
	w float64
}

func keysOf(names string, wide map[string]float64) []mapKey {
	var out []mapKey
	for _, k := range strings.Fields(names) {
		w := wide[k]
		if w == 0 {
			w = 1
		}
		out = append(out, mapKey{k, w})
	}
	return out
}

// The keyboard drawn on the shortcuts page.
var keyRows = [][]mapKey{
	keysOf("Esc F1 F2 F3 F4 F5 F6 F7 F8 F9 F10 F11 F12 Delete", map[string]float64{"Esc": 1.3, "Delete": 1.3}),
	keysOf("` 1 2 3 4 5 6 7 8 9 0 - = Backspace", map[string]float64{"Backspace": 1.9}),
	keysOf("Tab Q W E R T Y U I O P [ ] \\", map[string]float64{"Tab": 1.45, "\\": 1.45}),
	keysOf("CapsLock A S D F G H J K L ; ' Enter", map[string]float64{"CapsLock": 1.75, "Enter": 2.2}),
	keysOf("LShift Z X C V B N M , . / RShift", map[string]float64{"LShift": 2.25, "RShift": 2.75}),
	keysOf("LCtrl Win LAlt Space RAlt Menu RCtrl", map[string]float64{"LCtrl": 1.4, "Win": 1.4, "LAlt": 1.4, "Space": 6.6, "RAlt": 1.4, "Menu": 1.4, "RCtrl": 1.4}),
}

var keyCapName = map[string]string{"Esc": "esc", "Delete": "del", "Backspace": "bksp", "Tab": "tab", "CapsLock": "caps", "Enter": "enter",
	"LShift": "shift", "RShift": "shift", "LCtrl": "ctrl", "RCtrl": "ctrl", "LAlt": "alt", "RAlt": "alt", "Win": "win", "Menu": "menu", "Space": ""}

type level struct {
	n    int
	leaf *config.Binding
	on   bool
}

// levelOf lists what follows path under a leader: per key, how many
// shortcuts, and the one that ends exactly there.
func (a *App) levelOf(leader string, path []string) map[string]*level {
	out := map[string]*level{}
	for i := range a.cfg.Bindings {
		b := &a.cfg.Bindings[i]
		if a.leaderOf(b).ID != leader || len(b.Keys) <= len(path) || !hasPrefix(b.Keys, path) {
			continue
		}
		k := b.Keys[len(path)]
		e := out[k]
		if e == nil {
			e = &level{}
			out[k] = e
		}
		e.n++
		e.on = e.on || b.Enabled
		if len(b.Keys) == len(path)+1 && (e.leaf == nil || b.Enabled) {
			e.leaf = b
		}
	}
	return out
}

func hasPrefix(keys, path []string) bool {
	if len(keys) < len(path) {
		return false
	}
	for i, k := range path {
		if keys[i] != k {
			return false
		}
	}
	return true
}

// groupLabel names a key that leads to several shortcuts.
func (a *App) groupLabel(leader string, path []string) string {
	var names []string
	for i := range a.cfg.Bindings {
		b := &a.cfg.Bindings[i]
		if a.leaderOf(b).ID == leader && hasPrefix(b.Keys, path) {
			names = append(names, label(b))
			if len(names) == 3 {
				break
			}
		}
	}
	return strings.Join(names, T("、"))
}

func (a *App) visibleBindings() []*config.Binding {
	l := a.curLeader()
	var out []*config.Binding
	for i := range a.cfg.Bindings {
		b := &a.cfg.Bindings[i]
		if a.q != "" {
			if matches(b, a.q) {
				out = append(out, b)
			}
		} else if a.leaderOf(b).ID == l.ID && hasPrefix(b.Keys, a.path) {
			out = append(out, b)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.Join(out[i].Keys, " ") < strings.Join(out[j].Keys, " ") })
	return out
}

func (a *App) pageKeys(c *Ctx, view Rect) {
	p := c.P
	top := a.header(c, view, T("快捷键"), T("按住引导键，再按下面的键。点键盘上的键来查看、进入下一层或新建。"))
	// Header buttons, right to left.
	x := view.X + view.W - 32
	x -= c.BtnW(IconPlus, T("新建"))
	if ok, _ := c.Button("new", x, view.Y+30, IconPlus, T("新建"), BtnPrimary, false); ok {
		a.openEditor(c, a.newBinding(nil), true)
	}
	x -= c.BtnW(IconExport, T("导出")) + 8
	if ok, _ := c.Button("export", x, view.Y+30, IconExport, T("导出"), BtnPlain, false); ok {
		list := a.visibleBindings()
		if a.q == "" {
			list = nil
			for i := range a.cfg.Bindings {
				list = append(list, &a.cfg.Bindings[i])
			}
		}
		data := makeBundle(a.cfg, list)
		a.defer_(func() { a.H.SaveFile("spacehop-bindings.json", data) })
	}
	x -= c.BtnW(IconImport, T("导入")) + 8
	if ok, _ := c.Button("import", x, view.Y+30, IconImport, T("导入"), BtnPlain, false); ok {
		a.dialog = "import"
	}

	list := a.visibleBindings()
	shown := list
	if len(shown) > a.shown {
		shown = shown[:a.shown]
	}
	inner := Rect{view.X + 32, top, view.W - 64, view.H - top}
	mapH := 46.0 + 6*52 + 8
	contentH := mapH + 16 + 46 + float64(len(shown))*40 + 80
	if len(shown) == 0 {
		contentH += 110
	}
	off := c.Scroll("keys", Rect{view.X, top, view.W, view.H - top}, contentH)
	y := top - off
	a.keyMap(c, Rect{inner.X, y, inner.W, mapH})
	y += mapH + 16

	// Search.
	sr := Rect{inner.X, y, math.Min(420, inner.W-140), 36}
	before := a.q
	a.fieldIcon(c, "q", sr, &a.q, T("搜索全部快捷键：名称、按键、程序，或 type:url  is:off"), IconSearch)
	if a.q != before {
		a.shown = 200
	}
	cnt := i18n.F("这一层 %d 条", len(list))
	if a.q != "" {
		cnt = i18n.F("找到 %d 条", len(list))
	}
	c.Text(cnt, FSmall, Rect{inner.X, y, inner.W, 36}, p.Muted, Right)
	y += 46

	if len(list) == 0 {
		r := Rect{inner.X, y, inner.W, 110}
		c.Card(r)
		t1, t2 := T("还没有快捷键"), T("点上面键盘上的任意一个键，或者按“新建”")
		if a.q != "" {
			t1, t2 = T("没有匹配的快捷键"), T("换个关键词，或者试试 type:url、is:off、app:chrome")
		} else if len(a.path) > 0 {
			t1 = T("这一层还没有快捷键")
		}
		c.Text(t1, FBold, Rect{r.X, r.Y + 30, r.W, 22}, p.Text, Center)
		c.Text(t2, FText, Rect{r.X, r.Y + 56, r.W, 20}, p.Muted, Center)
	} else {
		lh := float64(len(shown)) * 40
		more := len(list) > len(shown)
		if more {
			lh += 40
		}
		c.Card(Rect{inner.X, y, inner.W, lh})
		for i, b := range shown {
			a.bindingRow(c, b, Rect{inner.X, y + float64(i)*40, inner.W, 40}, i > 0)
		}
		if more {
			r := Rect{inner.X, y + float64(len(shown))*40, inner.W, 40}
			if c.Click("more", r) {
				a.shown += 200
			}
			c.Text(i18n.F("再显示 200 条（共 %d 条）", len(list)), FText, r, p.AccentText, Center)
		}
	}
	c.EndScroll()
	a.bulkBar(c, view)
}

// fieldIcon is a text field with a glyph on its left.
func (a *App) fieldIcon(c *Ctx, id string, r Rect, v *string, ph, glyph string) {
	c.FieldPad = 22
	c.Field(id, r, v, ph, false, false, false)
	c.FieldPad = 0
	c.Text(glyph, FIcon, Rect{r.X + 6, r.Y, 26, r.H}, c.P.Faint, Center)
}

func (a *App) keyMap(c *Ctx, card Rect) {
	p := c.P
	c.Box(card, 16, raster.RGB(p.Surface), raster.RGB(p.Line))
	l := a.curLeader()
	lv := a.levelOf(l.ID, a.path)
	// Head: leaders, then the path.
	x, y := card.X+16, card.Y+14
	if len(a.cfg.Leaders) > 1 {
		for i := range a.cfg.Leaders {
			ld := &a.cfg.Leaders[i]
			mode := T("按住")
			if ld.Mode == config.Tap {
				mode = T("轻按")
			}
			w := c.ChordW(ld.Key) + c.TextW(FSmall, mode) + 22
			r := Rect{x, y - 2, w, 30}
			cur := ld.ID == l.ID
			line := p.Line
			if cur {
				line = p.AccentText
			}
			c.Box(r, 15, raster.RGB(p.Raised), raster.RGB(line))
			cw := c.Chord(ld.Key, x+4, y, cur)
			col := p.Muted
			if cur {
				col = p.Text
			}
			c.Text(mode, FSmall, Rect{x + cw + 10, r.Y, 80, 30}, col, Left)
			if c.Click("ld/"+itoa(i), r) && !cur {
				a.leader, a.path = ld.ID, nil
			}
			x += w + 6
		}
		x += 8
	}
	if c.Click("crumb/0", Rect{x, y, c.ChordW(l.Key), 26}) {
		a.path = nil
	}
	x += c.Chord(l.Key, x, y, true)
	for i, k := range a.path {
		c.Text("›", FText, Rect{x, y, 18, 26}, p.Faint, Center)
		x += 18
		if c.Click("crumb/"+itoa(i+1), Rect{x, y, c.CapW(k), 26}) {
			a.path = a.path[:i+1]
		}
		x += c.Cap(k, x, y, false)
	}
	c.Text(T("点亮的键可以查看或进入下一层，空白键直接新建"), FSmall, Rect{x + 12, y, card.X + card.W - x - 28, 26}, p.Muted, Right)

	// Keys.
	leadKeys := map[string]bool{}
	for _, part := range strings.Split(l.Key, "+") {
		switch part {
		case "Ctrl", "Shift", "Alt":
			part = "L" + part
		}
		leadKeys[part] = true
	}
	kbW := math.Min(card.W-32, 880)
	kx0 := card.X + (card.W-kbW)/2
	ky := card.Y + 52
	const gap = 5.0
	for ri, row := range keyRows {
		units := 0.0
		for _, k := range row {
			units += k.w
		}
		u := (kbW - gap*float64(len(row)-1)) / units
		h := 47.0
		kx := kx0
		for _, mk := range row {
			w := mk.w * u
			r := Rect{kx, ky, w, h}
			kx += w + gap
			k := mk.k
			e := lv[k]
			lead := leadKeys[k]
			dead := !lead && (isMod(k) || k == "Menu" || k == "Esc" || (k == "Backspace" && len(a.path) > 0))
			id := "k/" + k
			capName, ok := keyCapName[k]
			if !ok {
				capName = k
			}
			text := ""
			switch {
			case lead:
				text = T("按住")
				if l.Mode == config.Tap {
					text = T("轻按")
				}
				c.Fill(Rect{r.X, r.Y + 2.5, r.W, r.H}, 9, raster.RGB(mixRGB(p.Accent, 0, 0.35)))
				c.Fill(r, 9, raster.RGB(p.Accent))
				c.Text(capName, FKey, Rect{r.X + 7, r.Y + 5, r.W - 10, 14}, p.AccentInk, Left)
				c.Text(text, FSmall, Rect{r.X + 7, r.Y + r.H - 21, r.W - 10, 16}, p.AccentInk, Left)
				continue
			case dead:
				c.Stroke(r, 9, 1, raster.RGB(p.Line))
				c.Text(capName, FKey, Rect{r.X + 7, r.Y + 5, r.W - 10, 14}, mixRGB(p.Faint, p.Surface, 0.45), Left)
				continue
			}
			hover := c.Hover(id, r)
			if hover {
				c.Hand = true
				if c.Pressed {
					c.active = id
				}
			}
			press := c.Anim(id+"/p", b2f(hover && c.Held(id)))
			lift := c.Anim(id+"/h", b2f(hover)) - press*2.5
			kr := Rect{r.X, r.Y - lift, r.W, r.H}
			fill, edge, capCol := p.Key, p.KeyEdge, p.Faint
			if e != nil {
				fill, edge, capCol = p.AccentSoft, mixRGB(p.AccentText, p.KeyEdge, 0.55), p.AccentText
			}
			if hover {
				edge = p.AccentText
			}
			c.Fill(Rect{kr.X, kr.Y + 2.5 - press*1.5, kr.W, kr.H}, 9, raster.RGB(edge))
			c.Fill(kr, 9, raster.RGB(fill))
			c.Stroke(kr, 9, 1, raster.RGB(edge))
			c.Text(capName, FKey, Rect{kr.X + 7, kr.Y + 5, kr.W - 10, 14}, capCol, Left)
			if e != nil {
				group := e.n > 1 || e.leaf == nil
				if group {
					text = a.groupLabel(l.ID, append(append([]string{}, a.path...), k))
					n := itoa(e.n)
					bw := math.Max(16, c.TextW(FSmall, n)+8)
					br := Rect{kr.X + kr.W - bw - 4, kr.Y + 4, bw, 15}
					c.Fill(br, 7.5, raster.RGB(p.Accent))
					c.Text(n, FSmall, br, p.AccentInk, Center)
				} else {
					text = label(e.leaf)
				}
				col := p.Text
				if !e.on {
					col = p.Faint
				}
				if ri > 0 {
					c.Text(text, FSmall, Rect{kr.X + 7, kr.Y + kr.H - 21, kr.W - 10, 16}, col, Left)
				}
				c.SetTip(id, r, text)
			}
			if hover && c.Released && c.active == id {
				switch {
				case e == nil:
					a.openEditor(c, a.newBinding(append(append([]string{}, a.path...), k)), true)
				case e.n > 1 || e.leaf == nil:
					a.path = append(append([]string{}, a.path...), k)
					a.q = ""
					c.ResetScroll("keys")
				default:
					a.openEditor(c, e.leaf, false)
				}
			}
		}
		ky += h + gap
	}
}

func (a *App) bindingRow(c *Ctx, b *config.Binding, r Rect, sep bool) {
	if !c.visible(r) {
		return
	}
	p := c.P
	id := "b/" + b.ID
	rowHot := c.Hover(id, r)
	cur := a.draft != nil && a.draft.ID == b.ID && b.ID != ""
	if cur {
		c.Fill(r.Inset(1), 10, raster.RGB(p.AccentSoft))
	} else if rowHot || c.hot == id+"/ck" || c.hot == id+"/sw" {
		c.Fill(r.Inset(1), 10, raster.RGB(p.Raised))
	}
	if sep {
		c.Fill(Rect{r.X + 1, r.Y, r.W - 2, 1}, 0, raster.RGB(p.Line))
	}
	dim := func(col uint32) uint32 {
		if !b.Enabled {
			return mixRGB(col, p.Surface, 0.5)
		}
		return col
	}
	// Check box: shown on hover or while a selection exists.
	ck := Rect{r.X + 14, r.Y + 12, 16, 16}
	picked := a.sel[b.ID]
	if picked || rowHot || len(a.sel) > 0 || c.hot == id+"/ck" {
		if picked {
			c.Fill(ck, 5, raster.RGB(p.Accent))
			c.Text(IconCheck, FSmall, ck, p.AccentInk, Center)
		} else {
			c.Stroke(ck, 5, 1.5, raster.RGB(p.Line2))
		}
	}
	if c.Click(id+"/ck", Rect{ck.X - 8, r.Y, 32, r.H}) {
		if picked {
			delete(a.sel, b.ID)
		} else {
			a.sel[b.ID] = true
		}
	}
	// Enable switch on the right.
	if c.Toggle(id+"/sw", r.X+r.W-52, r.Y+9, b.Enabled) {
		b.Enabled = !b.Enabled
		a.save(true)
		return
	}
	// Keys.
	x := r.X + 44
	l := a.leaderOf(b)
	x += c.Chord(l.Key, x, r.Y+7, true)
	for _, k := range b.Keys {
		c.Text("›", FSmall, Rect{x, r.Y, 16, r.H}, p.Faint, Center)
		x += 16
		x += c.Cap(k, x, r.Y+7, false)
	}
	x = math.Max(x+14, r.X+44+170)
	// Chips, right to left.
	right := r.X + r.W - 66
	if n := a.usage[b.ID]; n > 0 {
		s := i18n.F("%d 次", n)
		c.Text(s, FSmall, Rect{right - 60, r.Y, 60, r.H}, p.Faint, Right)
	}
	right -= 68
	chip := func(text string, st ChipStyle) {
		w := c.ChipW(text)
		if right-w < x+120 {
			return
		}
		right -= w
		c.Chip(text, right, r.Y+9, st)
		right -= 6
	}
	is := a.issues[b.ID]
	switch b.Scope.Mode {
	case "only":
		chip(T("仅")+" "+strings.Join(b.Scope.Apps, T("、")), ChipAccent)
	case "except":
		chip(T("排除")+" "+strings.Join(b.Scope.Apps, T("、")), ChipPlain)
	}
	if b.After == "stay" {
		chip(T("连按"), ChipPlain)
	}
	if b.Action.Incomplete() {
		chip(T("未填完"), ChipWarn)
	} else if is.prefix > 0 {
		chip(i18n.F("前缀 · %d", is.prefix), ChipPlain)
	}
	if is.conflict {
		chip(T("冲突"), ChipWarn)
	}
	// Icon, name, detail.
	ic := Rect{x, r.Y + 7, 26, 26}
	c.Fill(ic, 7, raster.RGB(p.Raised))
	c.Text(TypeIcon[b.Action.Type], FIcon, ic, dim(p.Muted), Center)
	x += 36
	name := label(b)
	nw := math.Min(c.TextW(FBold, name)+4, math.Max(60, right-x-8))
	c.Text(name, FBold, Rect{x, r.Y, nw, r.H}, dim(p.Text), Left)
	if d := detail(b); d != "" && d != name {
		c.Text(d, FSmall, Rect{x + nw + 8, r.Y + 1, right - x - nw - 16, r.H}, dim(p.Muted), Left)
	}
	if c.Click(id, r) {
		a.openEditor(c, b, false)
	}
}

func (a *App) bulkBar(c *Ctx, view Rect) {
	t := c.Anim("bulk", b2f(len(a.sel) > 0))
	if t < 0.02 {
		return
	}
	c.Flush()
	p := c.P
	type btn struct{ id, icon, label string }
	btns := []btn{{"on", "", T("启用")}, {"off", "", T("停用")}, {"copy", IconCopy, T("复制")}, {"export", IconExport, T("导出")}, {"del", IconTrash, T("删除")}, {"none", IconClose, ""}}
	label := i18n.F("已选 %d 条", len(a.sel))
	w := c.TextW(FBold, label) + 40
	for _, b := range btns {
		w += c.BtnW(b.icon, b.label) + 4
	}
	r := Rect{view.X + (view.W-w)/2, view.Y + view.H - 22 - 48*t, w, 48}
	c.Shadow(Rect{r.X, r.Y + 6, r.W, r.H}, 14, 18, raster.RGB(0).Alpha(0.28))
	c.Box(r, 14, raster.RGB(p.Surface), raster.RGB(p.Line2))
	c.Text(label, FBold, Rect{r.X + 16, r.Y, 200, r.H}, p.Text, Left)
	x := r.X + c.TextW(FBold, label) + 32
	var picked []*config.Binding
	for i := range a.cfg.Bindings {
		if a.sel[a.cfg.Bindings[i].ID] {
			picked = append(picked, &a.cfg.Bindings[i])
		}
	}
	for _, b := range btns {
		st := BtnGhost
		if b.id == "del" {
			st = BtnDanger
		}
		ok, bw := c.Button("bulk/"+b.id, x, r.Y+7, b.icon, b.label, st, false)
		x += bw + 4
		if !ok {
			continue
		}
		switch b.id {
		case "on", "off":
			for _, x := range picked {
				x.Enabled = b.id == "on"
			}
			a.sel = map[string]bool{}
			a.save(false)
		case "copy":
			data := string(makeBundle(a.cfg, picked))
			n := len(picked)
			a.sel = map[string]bool{}
			a.defer_(func() {
				a.H.SetClipboard(data)
				a.say(i18n.F("已复制 %d 条，可以在“导入”里粘贴", n), false)
			})
		case "export":
			data := makeBundle(a.cfg, picked)
			a.defer_(func() { a.H.SaveFile("spacehop-bindings.json", data) })
		case "del":
			n := len(picked)
			a.defer_(func() {
				if !a.H.Confirm(i18n.F("删除选中的 %d 条快捷键？", n)) {
					return
				}
				var keep []config.Binding
				for _, b := range a.cfg.Bindings {
					if !a.sel[b.ID] {
						keep = append(keep, b)
					}
				}
				a.cfg.Bindings = keep
				a.sel = map[string]bool{}
				a.save(false)
			})
		case "none":
			a.sel = map[string]bool{}
		}
	}
	c.Flush()
}

// ---- editor ----

func (a *App) newBinding(keys []string) *config.Binding {
	if keys == nil {
		keys = append([]string{}, a.path...)
	}
	b := &config.Binding{Enabled: true, Keys: keys, Action: config.Action{Type: config.OpenApp}, Scope: config.Scope{Mode: "all"}}
	if l := a.curLeader(); l.ID != a.cfg.Leaders[0].ID {
		b.Leader = l.ID
	}
	return b
}

func (a *App) openEditor(c *Ctx, b *config.Binding, isNew bool) {
	a.draft, a.draftNew = cloneBinding(b), isNew
	a.setRectS()
	c.Blur()
	c.ResetScroll("editor")
	c.SetAnim("drawer", 0)
	if isNew && len(a.draft.Keys) == 0 && a.leaderOf(a.draft).Mode == config.Hold {
		a.startRec("keys")
	}
}

func (a *App) setRectS() {
	r := a.draft.Action.Rect
	if len(r) != 4 {
		r = []int{10, 10, 80, 80}
	}
	for i := range a.rectS {
		a.rectS[i] = itoa(r[i])
	}
}

func (a *App) closeEditor(c *Ctx) {
	a.stopRec()
	a.draft = nil
	c.Blur()
}

func atoi(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			break
		}
		if n = n*10 + int(ch-'0'); n > 100000 {
			break
		}
	}
	return n
}

// Layout thumbnails: left, top, width, height in sixths.
var layoutTiles = []struct {
	name       string
	x, y, w, h float64
}{
	{"left", 0, 0, 3, 6}, {"right", 3, 0, 3, 6}, {"top", 0, 0, 6, 3}, {"bottom", 0, 3, 6, 3},
	{"left-third", 0, 0, 2, 6}, {"center-third", 2, 0, 2, 6}, {"right-third", 4, 0, 2, 6},
	{"left-two-thirds", 0, 0, 4, 6}, {"right-two-thirds", 2, 0, 4, 6},
	{"top-left", 0, 0, 3, 3}, {"top-right", 3, 0, 3, 3}, {"bottom-left", 0, 3, 3, 3}, {"bottom-right", 3, 3, 3, 3},
	{"maximize", 0, 0, 6, 6}, {"center", 1, 1, 4, 4},
}

func (a *App) drawer(c *Ctx) {
	t := c.Anim("drawer", b2f(a.draft != nil))
	if a.draft == nil {
		return
	}
	p := c.P
	d := a.draft
	act := &d.Action
	const W = 480.0
	if a.dialog != "" {
		c.inputOff++
		defer func() { c.inputOff-- }()
	}
	c.Fill(Rect{0, 0, c.W, c.H}, 0, raster.RGB(0x0A0C08).Alpha(0.34*t))
	if c.Click("scrim", Rect{0, 0, c.W - W, c.H}) {
		a.closeEditor(c)
		return
	}
	x0 := c.W - W*t
	panel := Rect{x0, 0, W, c.H}
	c.Shadow(panel, 0, 30, raster.RGB(0).Alpha(0.22))
	c.Fill(panel, 0, raster.RGB(p.Surface))
	c.Fill(Rect{x0, 0, 1, c.H}, 0, raster.RGB(p.Line))
	title := T("编辑快捷键")
	if a.draftNew {
		title = T("新建快捷键")
	}
	c.Text(title, FBold, Rect{x0 + 20, 0, 300, 56}, p.Text, Left)
	if ok, _ := c.Button("ed/close", x0+W-48, 11, IconClose, "", BtnGhost, false); ok {
		a.closeEditor(c)
		return
	}
	c.Fill(Rect{x0, 56, W, 1}, 0, raster.RGB(p.Line))
	c.Fill(Rect{x0, c.H - 63, W, 1}, 0, raster.RGB(p.Line))

	l := a.leaderOf(d)
	view := Rect{x0, 57, W, c.H - 57 - 63}
	contentH := c.anim["editor/h"]
	off := c.Scroll("editor", view, contentH)
	x, w := x0+20, W-40
	y := view.Y + 18 - off
	field := func(caption string) { y = c.Label(caption, x, y) }
	hint := func(s string) { y += c.Para(s, FSmall, x, y, w, p.Muted) + 14 }
	textField := func(id string, v *string, ph string, mono bool) {
		c.Field("ed/"+id, Rect{x, y, w, 36}, v, ph, false, mono, false)
		y += 36
	}

	// Notes about conflicts.
	note := func(s string, warn bool) {
		h := c.TextH(FSmall, s, w-24) + 18
		fill, col := p.Raised, p.Muted
		if warn {
			fill, col = p.WarnSoft, p.Warn
		}
		c.Fill(Rect{x, y, w, h}, 10, raster.RGB(fill))
		c.Para(s, FSmall, x+12, y+9, w-24, col)
		y += h + 12
	}
	if len(d.Keys) > 0 || l.Mode == config.Tap {
		longer := 0
		for i := range a.cfg.Bindings {
			b := &a.cfg.Bindings[i]
			if b.ID == d.ID && d.ID != "" || a.leaderOf(b).ID != l.ID {
				continue
			}
			same := strings.Join(b.Keys, " ") == strings.Join(d.Keys, " ")
			if same && b.Enabled && scopesOverlap(b, d) {
				note(i18n.F("“%s” 已经在用这个快捷键，范围也有重叠。保存后只有排在前面的那条会运行。", label(b)), true)
			}
			if len(b.Keys) > len(d.Keys) && hasPrefix(b.Keys, d.Keys) {
				longer++
			}
			if len(b.Keys) > 0 && len(b.Keys) < len(d.Keys) && hasPrefix(d.Keys, b.Keys) && (b.After == "stop" || b.After == "root") {
				note(i18n.F("较短的 “%s” 运行后会结束按键序列，这条按不到。把它的“运行之后”改回默认即可。", label(b)), true)
			}
		}
		if longer > 0 && len(d.Keys) > 0 {
			note(i18n.F("这是一个前缀动作：它先运行，然后继续等待下面 %d 条更长的快捷键。", longer), false)
		}
	}

	if len(a.cfg.Leaders) > 1 {
		field(T("引导键"))
		var names []string
		cur := 0
		for i := range a.cfg.Leaders {
			names = append(names, a.cfg.Leaders[i].Key)
			if a.cfg.Leaders[i].ID == l.ID {
				cur = i
			}
		}
		if pick, _ := c.Seg("ed/leader", x, y, names, cur); pick >= 0 {
			d.Leader = a.cfg.Leaders[pick].ID
			if pick == 0 {
				d.Leader = ""
			}
		}
		y += 34 + 16
	}

	field(T("触发键"))
	if a.recorder(c, "keys", Rect{x, y, w, 46}, "", true, func(kx, ky float64) float64 {
		w0 := kx
		kx += c.Chord(l.Key, kx, ky, true)
		for _, k := range d.Keys {
			c.Text("›", FSmall, Rect{kx, ky, 16, 26}, p.Faint, Center)
			kx += 16
			kx += c.Cap(k, kx, ky, false)
		}
		if len(d.Keys) == 0 {
			return 0.1
		}
		return kx - w0
	}) {
		d.Keys = []string{}
	}
	y += 46 + 6
	if l.Mode == config.Hold {
		hint(i18n.F("按住 %s 后依次按这些键，最多 %d 个。", l.Key, config.MaxKeys))
	} else {
		hint(i18n.F("轻按 %s 后依次按这些键；不录触发键就是轻按引导键直接运行。", l.Key))
	}

	field(T("动作"))
	const cols = 4
	tw := (w - 6*(cols-1)) / cols
	for i, tn := range TypeNames {
		r := Rect{x + float64(i%cols)*(tw+6), y + float64(i/cols)*62, tw, 56}
		cur := act.Type == tn[0]
		id := "ed/type/" + tn[0]
		fill, line, col := p.Surface, p.Line, p.Muted
		if cur {
			fill, line, col = p.AccentSoft, p.AccentText, p.Text
		} else if c.hot == id {
			col = p.Text
		}
		c.Box(r, 10, raster.RGB(fill), raster.RGB(line))
		c.Text(TypeIcon[tn[0]], FIcon, Rect{r.X, r.Y + 8, r.W, 20}, col, Center)
		f := FSmall
		c.Text(T(tn[1]), f, Rect{r.X + 2, r.Y + 30, r.W - 4, 18}, col, Center)
		if c.Click(id, r) && !cur {
			d.Action = config.Action{Type: tn[0]}
			act = &d.Action
			a.stopRec()
			c.Blur()
			if tn[0] == config.InputMethod && a.layouts == nil {
				a.layouts = a.H.Layouts()
			}
		}
	}
	y += float64((len(TypeNames)+cols-1)/cols)*62 + 10

	pickBtn := func(id, what string) {
		if ok, _ := c.Button("ed/pick/"+id, x+w-c.BtnW("", T("选择…")), y, "", T("选择…"), BtnPlain, false); ok {
			a.openPicker(what)
		}
	}
	withPicker := func(id string, v *string, ph, what string) {
		bw := c.BtnW("", T("选择…")) + 8
		c.Field("ed/"+id, Rect{x, y, w - bw, 36}, v, ph, false, true, false)
		pickBtn(id, what)
		y += 36 + 14
	}
	sel := func(id string, opts [][2]string, cur *string, placeholder string) {
		lab, ph := placeholder, true
		for _, o := range opts {
			if o[0] == *cur {
				lab, ph = o[1], false
			}
		}
		if c.Select("ed/"+id, Rect{x, y, w, 36}, lab, ph) {
			a.defer_(func() {
				var items []string
				chk := -1
				for i, o := range opts {
					items = append(items, o[1])
					if o[0] == *cur {
						chk = i
					}
				}
				if i := a.H.Menu(items, chk); i >= 0 {
					*cur = opts[i][0]
					if id == "window" {
						a.setRectS()
					}
				}
			})
		}
		y += 36 + 14
	}
	chipsRow := func(items []string, remove func(i int), addID, what string) {
		cx := x
		for i, it := range items {
			cw := c.ChipW(it) + 18
			if cx+cw > x+w {
				cx = x
				y += 30
			}
			r := Rect{cx, y + 4, cw, 26}
			c.Box(r, 13, raster.RGB(p.Raised), raster.RGB(p.Line))
			c.Text(it, FSmall, Rect{r.X + 10, r.Y, cw - 30, 26}, p.Muted, Left)
			xr := Rect{r.X + cw - 24, r.Y, 22, 26}
			id := "ed/chip/" + addID + "/" + itoa(i)
			col := p.Faint
			if c.hot == id {
				col = p.Text
			}
			c.Text(IconClose, FSmall, xr, col, Center)
			if c.Click(id, xr) {
				remove(i)
				return
			}
			cx += cw + 6
		}
		bw := c.BtnW(IconPlus, T("添加"))
		if cx+bw > x+w {
			cx = x
			y += 34
		}
		if ok, _ := c.Button("ed/add/"+addID, cx, y, IconPlus, T("添加"), BtnPlain, false); ok {
			a.openPicker(what)
		}
		y += 34 + 6
	}

	switch act.Type {
	case config.OpenApp:
		field(T("程序"))
		withPicker("path", &act.Path, T("选择程序，或填 exe / 快捷方式的路径"), "path")
		field(T("启动参数"))
		textField("args", &act.Args, T("可不填"), true)
		y += 14
		field(T("已经在最前面时"))
		sel("ifRunning", [][2]string{{"", T("切到它的下一个窗口")}, {"hide", T("最小化")}, {"previous", T("切回上一个程序")}, {"new", T("再开一个")}}, &act.IfRunning, "")
		field(T("轮换程序"))
		var names []string
		for _, cy := range act.Cycle {
			n := cy.Name
			if n == "" {
				n = baseName(cy.Path)
			}
			names = append(names, n)
		}
		chipsRow(names, func(i int) { act.Cycle = append(act.Cycle[:i], act.Cycle[i+1:]...) }, "cycle", "cycle")
		hint(T("加了以后，同一个快捷键会在这些程序之间依次切换。"))
	case config.OpenURL:
		field(T("网址"))
		textField("url", &act.URL, "https://example.com", true)
		y += 14
		field(T("用哪个浏览器打开"))
		withPicker("with", &act.With, T("默认浏览器"), "with")
	case config.OpenFile:
		field(T("文件或文件夹"))
		textField("path", &act.Path, `%USERPROFILE%\Downloads`, true)
		y += 6
		hint(T("可以用 %USERPROFILE% 这类环境变量，或以 ~ 开头。"))
		field(T("用哪个程序打开"))
		withPicker("with", &act.With, T("默认程序"), "with")
	case config.ExecuteFile:
		field(T("要运行的文件"))
		textField("path", &act.Path, `D:\scripts\sync.bat`, true)
		y += 14
		field(T("参数"))
		textField("args", &act.Args, T("可不填"), true)
		y += 14
	case config.RunCommand:
		field(T("命令"))
		c.Field("ed/command", Rect{x, y, w, 96}, &act.Command, `code %USERPROFILE%\notes`, true, true, false)
		y += 96 + 14
		field(T("用什么运行"))
		cur := 0
		if act.Shell == "powershell" {
			cur = 1
		}
		if pick, _ := c.Seg("ed/shell", x, y, []string{"cmd", "PowerShell"}, cur); pick >= 0 {
			act.Shell = []string{"", "powershell"}[pick]
		}
		if c.Toggle("ed/hidden", x+w-38, y+6, act.Hidden) {
			act.Hidden = !act.Hidden
		}
		c.Text(T("不显示命令行窗口"), FText, Rect{x, y, w - 48, 34}, p.Text, Right)
		y += 34 + 14
	case config.Keystroke:
		field(T("要模拟的按键"))
		if a.recorder(c, "chord", Rect{x, y, w, 46}, T("点这里，然后按下组合键，例如 Ctrl+Shift+T"), true, func(kx, ky float64) float64 {
			if act.Chord == "" {
				return 0
			}
			return c.Chord(act.Chord, kx, ky, false)
		}) {
			act.Chord = ""
		}
		y += 46 + 6
		hint(T("按住触发键不放会连续重复，适合缩放、音量这类操作。"))
	case config.InsertText:
		field(T("文本"))
		c.Field("ed/text", Rect{x, y, w, 110}, &act.Text, T("要插入的内容"), true, false, false)
		y += 110 + 8
		bx := x
		for _, ph := range [][2]string{{"{{cursor}}", T("光标停在这里")}, {"{{clipboard}}", T("剪贴板内容")}, {"{{date}}", T("日期")}, {"{{time}}", T("时间")}} {
			id := "ed/ph/" + ph[0]
			ok, bw := c.Button(id, bx, y, "", ph[0], BtnPlain, false)
			c.SetTip(id, Rect{bx, y, bw, 34}, ph[1])
			if ok {
				c.Blur()
				act.Text += ph[0]
			}
			bx += bw + 6
		}
		y += 34 + 14
		field(T("方式"))
		if pick, _ := c.Seg("ed/paste", x, y, []string{T("逐字键入"), T("通过剪贴板粘贴")}, int(b2f(act.Paste))); pick >= 0 {
			act.Paste = pick == 1
		}
		y += 34 + 6
		if act.Paste {
			hint(T("长文本更快，粘贴后会把剪贴板恢复原样。"))
		} else {
			hint(T("不动剪贴板，支持中文和表情。带换行的文本在聊天软件里可能会直接发送。"))
		}
	case config.MenuCommand:
		field(T("菜单路径"))
		textField("menu", &act.Menu, T("文件->另存为..."), true)
		y += 6
		hint(T("按程序菜单栏上显示的文字填写，各级之间用 -> 隔开。只支持带标准菜单栏的程序；新式界面的程序请改用“模拟按键”。"))
	case config.Window:
		field(T("窗口动作"))
		const tc = 5
		tw := (w - 6*(tc-1)) / tc
		th := tw * 0.62
		for i, lt := range layoutTiles {
			r := Rect{x + float64(i%tc)*(tw+6), y + float64(i/tc)*(th+6), tw, th}
			id := "ed/tile/" + lt.name
			cur := act.Window == lt.name
			line, fill := p.Line2, p.Line2
			if cur {
				line, fill = p.AccentText, p.Accent
			} else if c.hot == id {
				fill = p.Accent
			}
			c.Box(r, 7, raster.RGB(p.Raised), raster.RGB(line))
			in := r.Inset(5)
			c.Fill(Rect{in.X + in.W*lt.x/6, in.Y + in.H*lt.y/6, in.W*lt.w/6 - 0.5, in.H*lt.h/6 - 0.5}, 3, raster.RGB(fill))
			c.SetTip(id, r, config.LabelOf(config.WindowLayouts, lt.name))
			if c.Click(id, r) {
				act.Window = lt.name
			}
		}
		y += float64((len(layoutTiles)+tc-1)/tc)*(th+6) + 10
		var opts [][2]string
		for _, wl := range config.WindowLayouts {
			opts = append(opts, [2]string{wl[0], T(wl[1])})
		}
		sel("window", opts, &act.Window, T("选择…"))
		if act.Window == "custom" {
			caps := []string{T("左边距 %"), T("上边距 %"), T("宽度 %"), T("高度 %")}
			cw := (w - 30) / 4
			for i := range caps {
				c.Text(caps[i], FSmall, Rect{x + float64(i)*(cw+10), y, cw, 16}, p.Muted, Left)
				c.Field("ed/rect/"+itoa(i), Rect{x + float64(i)*(cw+10), y + 22, cw, 36}, &a.rectS[i], "", false, false, true)
			}
			act.Rect = []int{atoi(a.rectS[0]), atoi(a.rectS[1]), atoi(a.rectS[2]), atoi(a.rectS[3])}
			y += 58 + 14
		}
	case config.InputMethod:
		field(T("切换到"))
		opts := [][2]string{{"next", T("下一个输入法")}}
		for _, ly := range a.layouts {
			opts = append(opts, [2]string{ly.ID, ly.Name})
		}
		sel("layout", opts, &act.Layout, T("选择…"))
		hint(T("按语言切换。同一种语言下装了多个输入法时，Windows 不提供直接指定其中一个的方法。"))
	case config.Notify:
		field(T("标题"))
		textField("title", &act.Title, T("提醒"), false)
		y += 14
		field(T("内容"))
		textField("ntext", &act.Text, T("可不填"), false)
		y += 14
		field(T("点击通知后打开的网址"))
		textField("nurl", &act.URL, T("可不填"), true)
		y += 14
	case config.System:
		field(T("动作"))
		var opts [][2]string
		for _, sa := range config.SystemActions {
			opts = append(opts, [2]string{sa[0], T(sa[1])})
		}
		sel("system", opts, &act.System, T("选择…"))
	}

	field(T("名称"))
	def := (&config.Binding{Action: *act}).Label()
	if act.Incomplete() {
		def = ""
	}
	textField("name", &d.Name, def, false)
	y += 6
	hint(T("显示在提示窗里，不填就用默认名。"))

	field(T("运行之后"))
	afters := [][2]string{{"", T("回到顶层")}, {"stay", T("停在这一层")}, {"stop", T("结束序列")}}
	if l.Mode == config.Tap {
		afters = [][2]string{{"", T("结束")}, {"stay", T("停在这一层")}, {"root", T("回到顶层")}}
	}
	cur := 0
	var names []string
	for i, af := range afters {
		names = append(names, af[1])
		if af[0] == d.After {
			cur = i
		}
	}
	if pick, _ := c.Seg("ed/after", x, y, names, cur); pick >= 0 {
		d.After = afters[pick][0]
	}
	y += 34 + 6
	if d.After == "stay" {
		hint(T("适合要连按的动作：不松开引导键，可以反复按同一层的键。"))
	} else {
		y += 10
	}

	field(T("在哪些程序里生效"))
	modes := []string{"all", "only", "except"}
	cur = 0
	for i, m := range modes {
		if d.Scope.Mode == m {
			cur = i
		}
	}
	if pick, _ := c.Seg("ed/scope", x, y, []string{T("所有程序"), T("仅这些程序"), T("除了这些程序")}, cur); pick >= 0 {
		d.Scope.Mode = modes[pick]
	}
	y += 34 + 8
	if d.Scope.Mode != "all" {
		chipsRow(d.Scope.Apps, func(i int) { d.Scope.Apps = append(d.Scope.Apps[:i], d.Scope.Apps[i+1:]...) }, "scope", "scope")
	}
	c.SetAnim("editor/h", y+off-view.Y+24)
	c.EndScroll()

	// Footer.
	fy := c.H - 49
	fx := x0 + 20
	if !a.draftNew {
		ok, bw := c.Button("ed/delete", fx, fy, IconTrash, T("删除"), BtnDanger, false)
		fx += bw + 4
		if ok {
			name := label(d)
			a.defer_(func() {
				if !a.H.Confirm(i18n.F("删除 “%s”？", name)) {
					return
				}
				var keep []config.Binding
				for _, b := range a.cfg.Bindings {
					if b.ID != d.ID {
						keep = append(keep, b)
					}
				}
				a.cfg.Bindings = keep
				if a.save(false) {
					a.draft = nil
				}
			})
		}
		if ok, _ := c.Button("ed/dup", fx, fy, IconCopy, T("复制一条"), BtnGhost, false); ok {
			d.ID, d.Keys, a.draftNew = "", []string{}, true
			a.say(T("已复制，录一个新的触发键后保存"), false)
			a.startRec("keys")
		}
	}
	rx := x0 + W - 20 - c.BtnW("", T("保存"))
	if ok, _ := c.Button("ed/save", rx, fy, "", T("保存"), BtnPrimary, false); ok {
		a.saveDraft(c)
	}
	rx -= c.BtnW(IconPlay, T("试一下")) + 8
	if ok, _ := c.Button("ed/test", rx, fy, IconPlay, T("试一下"), BtnPlain, act.Incomplete()); ok {
		a.testAction(*act)
	}
	c.Flush()
}

func (a *App) saveDraft(c *Ctx) {
	d := a.draft
	c.Blur()
	a.stopRec()
	if len(d.Keys) == 0 && a.leaderOf(d).Mode == config.Hold {
		a.say(T("先录制至少一个触发键"), true)
		return
	}
	saved := *cloneBinding(d)
	at := -1
	for i := range a.cfg.Bindings {
		if a.cfg.Bindings[i].ID == d.ID && d.ID != "" {
			at = i
		}
	}
	if at >= 0 {
		a.cfg.Bindings[at] = saved
	} else {
		a.cfg.Bindings = append(a.cfg.Bindings, saved)
	}
	if a.save(false) {
		if !hasPrefix(saved.Keys, a.path) || len(saved.Keys) == len(a.path) {
			a.path = nil
		}
		a.draft = nil
	} else {
		a.draft = d // keep the editor open on the unsaved draft
	}
}

func (a *App) testAction(act config.Action) {
	delay := 0
	if frontTypes[act.Type] {
		delay = 2500
		a.say(T("2.5 秒后执行，请切到要操作的窗口"), false)
	}
	a.H.Later(delay, func() {
		a.H.Async(func() any { return a.H.RunAction(act) }, func(v any) {
			if err, ok := v.(error); ok && err != nil {
				a.say(err.Error(), true)
			} else if delay == 0 {
				a.say(T("已执行"), false)
			}
		})
	})
}
