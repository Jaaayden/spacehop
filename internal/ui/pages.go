package ui

import (
	"math"
	"sort"
	"strings"

	"spacehop/internal/config"
	"spacehop/internal/i18n"
	"spacehop/internal/raster"
)

// row draws one setting: title, description, and returns the rectangle of
// the control area on the right plus the row height.
func (a *App) row(c *Ctx, x, y, w float64, title, desc string, ctlW float64, first bool) (ctl Rect, h float64) {
	p := c.P
	tw := w - ctlW - 16
	dh := 0.0
	if desc != "" {
		dh = c.TextH(FSmall, desc, tw)
	}
	h = math.Max(46, 22+dh+22)
	if !first {
		c.Fill(Rect{x, y, w, 1}, 0, raster.RGB(p.Line))
	}
	c.Text(title, FBold, Rect{x, y + 11, tw, 20}, p.Text, Left)
	if desc != "" {
		c.Para(desc, FSmall, x, y+32, tw, p.Muted)
	}
	return Rect{x + w - ctlW, y + (h-34)/2, ctlW, 34}, h
}

// page starts a scrolling page and returns its content rectangle.
func (a *App) scrollPage(c *Ctx, id string, view Rect, top float64) (x, y, w float64) {
	h := c.anim[id+"/h"]
	off := c.Scroll(id, Rect{view.X, top, view.W, view.H - top}, h)
	return view.X + 32, top - off, math.Min(view.W-64, 980)
}

func (a *App) endPage(c *Ctx, id string, y, top float64) {
	c.SetAnim(id+"/h", y+c.scroll[id]-top+40)
	c.EndScroll()
}

// ---- leaders ----

func (a *App) pageLeader(c *Ctx, view Rect) {
	p := c.P
	top := a.header(c, view, T("引导键"), T("引导键是开始一个快捷键的那个键。每条快捷键可以用不同的引导键，空格和 CapsLock 可以并存。"))
	full := len(a.cfg.Leaders) >= config.MaxLeaders
	if ok, _ := c.Button("ld/add", view.X+view.W-32-c.BtnW(IconPlus, T("添加引导键")), view.Y+30, IconPlus, T("添加引导键"), BtnPrimary, full); ok {
		used := map[string]bool{}
		for _, l := range a.cfg.Leaders {
			used[l.Key] = true
		}
		for _, k := range []string{"CapsLock", "F12", "F11", "F10", "F9", "F8", "F7", "F6"} {
			if !used[k] {
				a.cfg.Leaders = append(a.cfg.Leaders, config.Leader{Mode: config.Hold, Key: k})
				a.save(true)
				break
			}
		}
	}
	x, y, w := a.scrollPage(c, "leader", view, top)
	count := func(id string) int {
		n := 0
		for i := range a.cfg.Bindings {
			if a.leaderOf(&a.cfg.Bindings[i]).ID == id {
				n++
			}
		}
		return n
	}
	for i := range a.cfg.Leaders {
		l := &a.cfg.Leaders[i]
		pre := "ld/" + itoa(i)
		h := 200.0
		if l.Mode == config.Hold {
			h = 268
		}
		card := Rect{x, y, w, h}
		c.Card(card)
		cx, cy := x+18, y+18
		cur := 0
		if l.Mode == config.Tap {
			cur = 1
		}
		pick, sw := c.Seg(pre+"/mode", cx, cy, []string{T("按住"), T("轻按")}, cur)
		if pick >= 0 {
			// Each mode keeps its own key: switching back and forth must
			// not lose Space.
			was, had := a.leaderWas[l.ID]
			a.leaderWas[l.ID] = *l
			switch {
			case had && was.Mode != l.Mode:
				l.Mode, l.Key, l.KeepTap = was.Mode, was.Key, was.KeepTap
			case pick == 0:
				// Space with its quick tap kept, unless another leader has it.
				l.Mode, l.Key, l.KeepTap = config.Hold, "Space", true
				for j := range a.cfg.Leaders {
					if j != i && a.cfg.Leaders[j].Key == "Space" {
						l.Key, l.KeepTap = "F12", false
					}
				}
			default:
				k := "S"
				if len(l.Key) == 1 {
					k = l.Key
				}
				l.Mode, l.KeepTap, l.Key = config.Tap, false, "Alt+"+k
			}
			a.save(true)
			c.EndScroll()
			return
		}
		tx := cx + sw + 12
		if i == 0 {
			tx += c.Chip(T("默认"), tx, cy+6, ChipAccent) + 6
		} else {
			ok, bw := c.Button(pre+"/default", tx, cy, "", T("设为默认"), BtnGhost, false)
			tx += bw + 6
			if ok {
				old := a.cfg.Leaders[0].ID
				nl := *l
				for j := range a.cfg.Bindings {
					b := &a.cfg.Bindings[j]
					if b.Leader == "" {
						b.Leader = old
					} else if b.Leader == nl.ID {
						b.Leader = ""
					}
				}
				rest := append(append([]config.Leader{}, a.cfg.Leaders[:i]...), a.cfg.Leaders[i+1:]...)
				a.cfg.Leaders = append([]config.Leader{nl}, rest...)
				a.save(false)
				c.EndScroll()
				return
			}
		}
		n := count(l.ID)
		c.Chip(i18n.F("%d 条快捷键", n), tx, cy+6, ChipPlain)
		if len(a.cfg.Leaders) > 1 {
			if ok, _ := c.Button(pre+"/del", x+w-18-c.BtnW(IconTrash, T("移除")), cy, IconTrash, T("移除"), BtnDanger, false); ok {
				idx, key, id := i, l.Key, l.ID
				a.defer_(func() {
					if n > 0 && !a.H.Confirm(i18n.F("移除引导键 %s，同时删除它的 %d 条快捷键？", key, n)) {
						return
					}
					var keep []config.Binding
					for _, b := range a.cfg.Bindings {
						if a.leaderOf(&b).ID != id {
							keep = append(keep, b)
						}
					}
					a.cfg.Bindings = keep
					a.cfg.Leaders = append(a.cfg.Leaders[:idx], a.cfg.Leaders[idx+1:]...)
					for j := range a.cfg.Bindings {
						if a.cfg.Bindings[j].Leader == a.cfg.Leaders[0].ID {
							a.cfg.Bindings[j].Leader = ""
						}
					}
					a.leader, a.path = "", nil
					a.save(false)
				})
			}
		}
		cy += 48
		key := l.Key
		a.recorder(c, "lead:"+itoa(i), Rect{cx, cy, w - 36, 46}, "", false, func(kx, ky float64) float64 {
			return c.Chord(key, kx, ky, true)
		})
		cy += 58
		if l.Mode == config.Hold {
			desc := T("轻点一下照常输入这个键，按住才是引导键。关掉后这个键专门用作引导键，按下立即就绪。")
			if !l.KeepTap && typingKey(l.Key) {
				desc = i18n.F("现在按 %s 不会再输入任何字符。平时要用它打字的话，请把这一项打开。", l.Key)
			}
			ctl, rh := a.row(c, cx, cy, w-36, T("保留轻点"), desc, 38, true)
			if c.Toggle(pre+"/keep", ctl.X, ctl.Y+6, l.KeepTap) {
				l.KeepTap = !l.KeepTap
				a.save(true)
			}
			cy += rh + 4
		}
		// A small timeline of how it is used.
		lane := Rect{cx, cy, w - 36, 40}
		c.Fill(lane, 9, raster.RGB(p.Raised))
		lx := lane.X + 8
		verb, note := T("按住"), T("立即就绪，这个键原来的功能不再有")
		if l.Mode == config.Tap {
			verb, note = T("按一下"), T("单独轻按一个修饰键")
			if strings.Contains(l.Key, "+") {
				note = T("组合键")
			}
		} else if l.KeepTap {
			note = T("轻点一下仍然输入这个键")
		}
		bw := c.ChordW(l.Key) + c.TextW(FSmall, verb) + 22
		c.Fill(Rect{lx, cy + 5, bw, 30}, 8, raster.RGB(p.AccentSoft))
		kw := c.Chord(l.Key, lx+5, cy+7, true)
		c.Text(verb, FSmall, Rect{lx + kw + 11, cy + 5, 100, 30}, p.AccentText, Left)
		lx += bw + 8
		if l.Mode == config.Tap {
			s := T("松开，然后")
			c.Text(s, FSmall, Rect{lx, cy, 200, 40}, p.Muted, Left)
			lx += c.TextW(FSmall, s) + 8
		}
		lx += c.Cap("F", lx, cy+7, false) + 4
		lx += c.Cap("D", lx, cy+7, false) + 8
		c.Text(note, FSmall, Rect{lx, cy, lane.X + lane.W - lx - 12, 40}, p.Muted, Right)
		y += h + 14
	}
	// The four ways, explained.
	rows := [][2]string{
		{T("按住 · 保留轻点"), T("空格，甚至字母键。轻点照常打字，按住才触发；快速打字时不会误触。")},
		{T("按住 · 专用键"), T("F12、CapsLock 这类平时不用的键。按下就绪，没有任何等待。")},
		{T("轻按 · 组合键"), T("Alt+S 这类组合。按一下松开，再依次按触发键。")},
		{T("轻按 · 单个修饰键"), T("单独按一下右 Ctrl 之类的修饰键，再按触发键；也可以不带触发键直接运行一个动作。")},
	}
	ch := 56.0 + float64(len(rows))*30
	c.Card(Rect{x, y, w, ch})
	c.Text(T("四种用法"), FBold, Rect{x + 18, y + 16, 300, 20}, p.Text, Left)
	lw := 0.0
	for _, r := range rows {
		lw = math.Max(lw, c.TextW(FBold, r[0]))
	}
	for i, r := range rows {
		ry := y + 46 + float64(i)*30
		c.Text(r[0], FBold, Rect{x + 18, ry, lw + 4, 24}, p.Text, Left)
		c.Text(r[1], FText, Rect{x + 18 + lw + 18, ry, w - lw - 54, 24}, p.Muted, Left)
	}
	y += ch
	a.endPage(c, "leader", y, top)
}

// ---- hint window ----

func (a *App) pageHint(c *Ctx, view Rect) {
	p := c.P
	h := &a.cfg.Hint
	top := a.header(c, view, T("提示窗"), T("忘了按什么的时候，按住引导键等一下，它会列出这一步能按的键。"))
	if c.Toggle("hint/show", view.X+view.W-32-38, view.Y+36, h.Show) {
		h.Show = !h.Show
		a.save(true)
	}
	x, y, w := a.scrollPage(c, "hint", view, top)
	gap := 16.0
	lw := math.Floor((w - gap) * 0.52)
	rw := w - gap - lw
	stacked := w < 760
	if stacked {
		lw, rw = w, w
	}
	// Preview: a small screen with the hint on it, draggable.
	stage := Rect{x, y, lw, math.Floor(lw * 9 / 16)}
	hintLight := h.Theme == "light" || (h.Theme == "auto" && !a.dark)
	bg := raster.RGB(0x2A3442)
	if hintLight {
		bg = raster.RGB(0xDDE4EC)
	}
	c.Box(stage, 14, bg, raster.RGB(p.Line))
	barC := raster.RGB(0x141820).Alpha(0.6)
	if hintLight {
		barC = raster.RGB(0xFFFFFF).Alpha(0.7)
	}
	c.PushClip(stage)
	c.Fill(Rect{stage.X + 1, stage.Y + stage.H*0.945, stage.W - 2, stage.H * 0.055}, 0, barC)
	a.hintMock(c, stage, hintLight)
	c.PopClip()
	c.Para(T("拖动小窗来决定它出现的位置；按屏幕比例记住，换显示器也在同一处。"), FSmall, x, stage.Y+stage.H+10, lw-140, p.Muted)
	if ok, _ := c.Button("hint/reset", x+lw-c.BtnW("", T("恢复默认位置")), stage.Y+stage.H+4, "", T("恢复默认位置"), BtnGhost, false); ok {
		h.X, h.Y = 50, 8
		a.save(true)
	}
	leftBottom := stage.Y + stage.H + 48

	cx, cy := x+lw+gap, y
	if stacked {
		cx, cy = x, leftBottom+8
	}
	cardTop := cy
	inner := rw - 36
	iy := cy + 6
	ix := cx + 18
	seg := func(id, title, desc string, vals, labels []string, cur *string, first bool) {
		idx := 0
		for i, v := range vals {
			if v == *cur {
				idx = i
			}
		}
		sw := 6.0
		for _, l := range labels {
			sw += c.TextW(FText, l) + 24
		}
		ctl, rh := a.row(c, ix, iy, inner, title, desc, sw, first)
		if pick, _ := c.Seg(id, ctl.X, ctl.Y, labels, idx); pick >= 0 {
			*cur = vals[pick]
			a.save(true)
		}
		iy += rh
	}
	// The card is drawn after measuring: remember where it starts.
	cardH := c.anim["hint/card"]
	c.Card(Rect{cx, cardTop, rw, cardH})
	seg("hint/layout", T("样式"), T("现代样式带图标和子项数量；经典样式更紧凑。"), []string{"modern", "classic"}, []string{T("现代"), T("经典")}, &h.Layout, true)
	seg("hint/theme", T("颜色"), "", []string{"auto", "dark", "light"}, []string{T("跟随系统"), T("深色"), T("浅色")}, &h.Theme, false)
	slider := func(id, title, desc string, v *int, min, max, step float64, unit string) {
		ctl, rh := a.row(c, ix, iy, inner, title, desc, 186, false)
		nv := int(c.Slider(id, Rect{ctl.X + 6, ctl.Y, 110, 34}, float64(*v), min, max, step))
		c.Text(itoa(nv)+unit, FMono, Rect{ctl.X + 122, ctl.Y, 64, 34}, p.Text, Right)
		if nv != *v {
			*v = nv
		}
		if c.Released && c.active == id {
			a.save(true)
		}
		iy += rh
	}
	slider("hint/delay", T("出现前等待"), T("只影响提示窗何时出现，不影响快捷键何时生效。"), &a.cfg.Timing.HintDelayMs, 0, 2000, 50, " ms")
	slider("hint/width", T("每列宽度"), "", &h.Width, 160, 480, 10, "")
	seg("hint/monitor", T("显示在哪块屏幕"), T("只在多显示器时有区别。"), []string{"focus", "pointer"}, []string{T("当前窗口所在"), T("鼠标所在")}, &h.Monitor, false)
	ctl, rh := a.row(c, ix, iy, inner, T("搜索键"), T("提示窗出现时按它，输入文字搜索全部快捷键；方向键选择，Enter 运行。"), 190, false)
	key := h.SearchKey
	if a.recorder(c, "search", Rect{ctl.X, ctl.Y - 6, 190, 46}, T("未设置"), true, func(kx, ky float64) float64 {
		if key == "" {
			return 0
		}
		return c.Cap(key, kx, ky, false)
	}) {
		h.SearchKey = ""
		a.save(true)
	}
	iy += rh
	ctl, rh = a.row(c, ix, iy, inner, T("淡化常用的"), T("已经用熟的快捷键（运行 20 次以上）显示得淡一些，突出还不熟的。"), 38, false)
	if c.Toggle("hint/dim", ctl.X, ctl.Y+6, h.Dim) {
		h.Dim = !h.Dim
		a.save(true)
	}
	iy += rh + 6
	c.SetAnim("hint/card", iy-cardTop)
	a.endPage(c, "hint", math.Max(iy, leftBottom), top)
}

// hintMock draws a miniature of the hint window and lets it be dragged.
func (a *App) hintMock(c *Ctx, stage Rect, light bool) {
	h := &a.cfg.Hint
	l := &a.cfg.Leaders[0]
	lv := a.levelOf(l.ID, nil)
	var keys []string
	for k, e := range lv {
		if e.on {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) > 12 {
		keys = keys[:12]
	}
	classic := h.Layout == "classic"
	rows := 12
	if classic {
		rows = 14
	}
	n := len(keys)
	if n == 0 {
		n = 1
	}
	cols := (n + rows - 1) / rows
	per := (n + cols - 1) / cols
	k := stage.W / 1280 * 1.35 // miniature scale
	colW, rowH := float64(h.Width)*k, 25*k
	if classic {
		rowH = 22 * k
	}
	w := 24*k + float64(cols)*colW
	ht := (12+34)*k + float64(per)*rowH + 6*k
	if !classic {
		ht += 26 * k
	}
	bx := stage.X + stage.W*float64(h.X)/100 - w/2
	by := stage.Y + stage.H*(1-float64(h.Y)/100) - ht - stage.H*0.055
	bx = math.Max(stage.X+2, math.Min(stage.X+stage.W-w-2, bx))
	by = math.Max(stage.Y+2, by)
	box := Rect{bx, by, w, ht}
	// Drag.
	if c.Hover("hint/mock", box) {
		c.Hand = true
		if c.Pressed {
			c.active = "hint/mock"
			c.dragStartX, c.dragY0 = c.MX-(bx+w/2), (by+ht)-c.MY
		}
	}
	if c.Held("hint/mock") {
		cxp := (c.MX - c.dragStartX - stage.X) / stage.W * 100
		gapp := (stage.Y + stage.H*0.945 - (c.MY + c.dragY0)) / stage.H * 100
		h.X = int(math.Max(0, math.Min(100, cxp)) + 0.5)
		h.Y = int(math.Max(0, math.Min(90, gapp)) + 0.5)
	}
	if c.Released && c.active == "hint/mock" {
		a.save(true)
	}
	bg, line, text, capC, capE, acc := uint32(0x16181D), uint32(0x2E323C), uint32(0xE9EBEF), uint32(0x282C36), uint32(0x3D4350), uint32(0xC8F53C)
	if light {
		bg, line, text, capC, capE, acc = 0xFBFBF9, 0xD9DBDF, 0x1B1D22, 0xFFFFFF, 0xC9CCD3, 0x4A7A00
	}
	c.Shadow(Rect{box.X, box.Y + 5*k, box.W, box.H}, 10*k, 16*k, raster.RGB(0).Alpha(0.35))
	c.Box(box, 10*k, raster.RGB(bg), raster.RGB(line))
	capAt := func(x, y, w float64) {
		c.Fill(Rect{x, y, w, 18 * k}, 4*k, raster.RGB(capC))
		c.Stroke(Rect{x, y, w, 18 * k}, 4*k, 1, raster.RGB(capE))
	}
	capAt(box.X+12*k, box.Y+10*k, 46*k)
	c.Fill(Rect{box.X + 1, box.Y + 38*k, box.W - 2, 1}, 0, raster.RGB(line))
	for i, key := range keys {
		e := lv[key]
		x := box.X + 12*k + float64(i/per)*colW
		y := box.Y + 46*k + float64(i%per)*rowH
		capAt(x, y+(rowH-18*k)/2, 20*k)
		// Text stands in as a bar: this is a miniature.
		bw := colW * 0.42
		col := raster.RGB(text).Alpha(0.75)
		if e.n > 1 || e.leaf == nil {
			col = raster.RGB(text).Alpha(0.4)
			if !classic {
				c.Fill(Rect{x + colW - 30*k, y + rowH/2 - 2*k, 12 * k, 4 * k}, 2*k, raster.RGB(acc))
			} else {
				col = raster.RGB(acc)
			}
		} else if h.Dim && a.usage[e.leaf.ID] >= 20 {
			col = raster.RGB(text).Alpha(0.35)
		}
		c.Fill(Rect{x + 30*k, y + rowH/2 - 2.5*k, bw + float64(len(key)%3)*10*k, 5 * k}, 2.5*k, col)
	}
	if !classic {
		c.Fill(Rect{box.X + 14*k, box.Y + box.H - 16*k, 90 * k, 4 * k}, 2*k, raster.RGB(text).Alpha(0.3))
	}
}

// ---- general ----

func (a *App) num(key string, v int) *string {
	if s, ok := a.numS[key]; ok {
		return s
	}
	s := itoa(v)
	a.numS[key] = &s
	return &s
}

func (a *App) pageGeneral(c *Ctx, view Rect) {
	p := c.P
	top := a.header(c, view, T("通用"), T("语言、启动、排除的程序、按键时序和备份。"))
	if !a.backupsOK {
		a.backupsOK = true
		a.H.Async(func() any { return a.H.Backups() }, func(v any) { a.backups, _ = v.([]config.Backup) })
	}
	x, y, w := a.scrollPage(c, "general", view, top)
	inner, ix := w-36, x+18
	card := func(name string, body func(iy float64) float64) {
		h := c.anim["general/"+name]
		c.Card(Rect{x, y, w, h})
		end := body(y + 6)
		c.SetAnim("general/"+name, end-y+6)
		y = end + 6 + 14
	}
	card("basic", func(iy float64) float64 {
		// Language.
		langs := []string{"auto", "zh", "en"}
		cur := 0
		for i, l := range langs {
			if a.cfg.Language == l {
				cur = i
			}
		}
		labels := []string{T("跟随系统"), "中文", "English"}
		sw := 6.0
		for _, l := range labels {
			sw += c.TextW(FText, l) + 24
		}
		ctl, rh := a.row(c, ix, iy, inner, T("语言"), "", sw, true)
		if pick, _ := c.Seg("gen/lang", ctl.X, ctl.Y, labels, cur); pick >= 0 {
			a.cfg.Language = langs[pick]
			a.save(true)
			c.ClearMemo()
		}
		iy += rh
		themes := []string{"auto", "dark", "light"}
		cur = 0
		for i, t := range themes {
			if a.cfg.Theme == t {
				cur = i
			}
		}
		labels = []string{T("跟随系统"), T("深色"), T("浅色")}
		sw = 6.0
		for _, l := range labels {
			sw += c.TextW(FText, l) + 24
		}
		ctl, rh = a.row(c, ix, iy, inner, T("设置窗口的颜色"), "", sw, false)
		if pick, _ := c.Seg("gen/theme", ctl.X, ctl.Y, labels, cur); pick >= 0 {
			a.cfg.Theme = themes[pick]
			a.save(true)
		}
		iy += rh
		ctl, rh = a.row(c, ix, iy, inner, T("开机自动启动"), T("登录 Windows 后在后台运行，只在托盘显示一个图标。"), 38, false)
		if c.Toggle("gen/auto", ctl.X, ctl.Y+6, a.autostart) {
			if err := a.H.SetAutostart(!a.autostart); err != nil {
				a.say(err.Error(), true)
			}
			a.autostart = a.H.Autostart()
		}
		iy += rh
		ctl, rh = a.row(c, ix, iy, inner, T("全屏游戏中停用"), T("检测到独占全屏的游戏时不拦截任何按键。"), 38, false)
		if c.Toggle("gen/fs", ctl.X, ctl.Y+6, a.cfg.DisableInFullscreen) {
			a.cfg.DisableInFullscreen = !a.cfg.DisableInFullscreen
			a.save(true)
		}
		iy += rh
		_, rh = a.row(c, ix, iy, inner, T("在这些程序里完全停用"), T("在这些程序里引导键就是普通按键。"), 0, false)
		iy += rh - 4
		cx := ix
		for i, app := range a.cfg.DisabledApps {
			cw := c.ChipW(app) + 18
			if cx+cw > ix+inner {
				cx = ix
				iy += 32
			}
			r := Rect{cx, iy, cw, 26}
			c.Box(r, 13, raster.RGB(p.Raised), raster.RGB(p.Line))
			c.Text(app, FSmall, Rect{r.X + 10, r.Y, cw - 30, 26}, p.Muted, Left)
			id := "gen/undis/" + itoa(i)
			xr := Rect{r.X + cw - 24, r.Y, 22, 26}
			c.Text(IconClose, FSmall, xr, p.Faint, Center)
			if c.Click(id, xr) {
				a.cfg.DisabledApps = append(a.cfg.DisabledApps[:i], a.cfg.DisabledApps[i+1:]...)
				a.save(true)
				break
			}
			cx += cw + 6
		}
		if ok, _ := c.Button("gen/adddis", cx, iy-4, IconPlus, T("添加程序"), BtnPlain, false); ok {
			a.openPicker("disabled")
		}
		return iy + 40
	})
	card("timing", func(iy float64) float64 {
		c.Text(T("按键时序"), FBold, Rect{ix, iy + 10, inner, 20}, p.Text, Left)
		iy += 34 + c.Para(T("刚开始用建议保留默认值。前三项只对“按住 · 保留轻点”的引导键起作用。"), FText, ix, iy+34, inner, p.Muted) + 8
		t := &a.cfg.Timing
		for i, r0 := range []struct {
			key, title, desc string
		}{
			{"activationMs", T("就绪等待"), T("按下引导键后，这段时间内按的键算作普通打字。觉得快捷键反应慢就调小，打字时误触就调大。")},
			{"typingSuppressMs", T("打字后的保护时间"), T("刚打完字紧接着按引导键时，改用这个更长的等待。快速打字时空格偶尔被吞就调大。")},
			{"tapReplayMs", T("轻点仍输入本键的时限"), T("按住不超过这个时间并且没有触发快捷键，松开时照常输入这个键；超过则什么都不输入。")},
			{"tapTimeoutMs", T("轻按型引导键等待触发键"), T("轻按引导键后，这段时间内没有按触发键就自动退出。")},
			{"modTapMs", T("单个修饰键的轻按时限"), T("修饰键按下到松开不超过这个时间才算一次轻按。")},
		} {
			r := struct {
				key string
				v   *int
			}{r0.key, map[string]*int{"activationMs": &t.ActivationMs, "typingSuppressMs": &t.TypingSuppressMs,
				"tapReplayMs": &t.TapReplayMs, "tapTimeoutMs": &t.TapTimeoutMs, "modTapMs": &t.ModTapMs}[r0.key]}
			ctl, rh := a.row(c, ix, iy, inner, r0.title, r0.desc, 150, i == 0)
			s := a.num(r.key, *r.v)
			id := "gen/" + r.key
			c.Field(id, Rect{ctl.X, ctl.Y - 1, 96, 36}, s, "", false, false, true)
			c.Text(T("毫秒"), FSmall, Rect{ctl.X + 104, ctl.Y, 46, 34}, p.Muted, Left)
			// Save as the number is typed, once per distinct text. Leaving
			// the field shows the value as it was accepted.
			if text := *s; text != a.numSeen[r.key] {
				a.numSeen[r.key] = text
				if v := atoi(text); text != "" && v != *r.v {
					*r.v = v
					if c2, err := a.H.Save(a.cfg); err != nil {
						a.say(err.Error(), true)
						a.cfg = clone(a.H.Config())
					} else {
						a.cfg = clone(c2)
					}
					t = &a.cfg.Timing
				}
			} else if c.Focused() != id && text != itoa(*r.v) {
				delete(a.numS, r.key)
				delete(a.numSeen, r.key)
			}
			iy += rh
		}
		return iy
	})
	card("backup", func(iy float64) float64 {
		c.Text(T("备份与恢复"), FBold, Rect{ix, iy + 10, inner, 20}, p.Text, Left)
		iy += 38
		iy += c.Para(T("每次保存前的版本都会留一份；每十分钟最多另存一份带时间的备份，保留最近 30 份。配置文件可以直接编辑，保存后自动生效："), FText, ix, iy, inner, p.Muted) + 4
		c.Text(a.H.ConfigPath(), FMono, Rect{ix, iy, inner, 22}, p.Text, Left)
		iy += 32
		ok, bw := c.Button("gen/exportAll", ix, iy, IconExport, T("导出完整配置"), BtnPlain, false)
		if ok {
			data := configJSON(a.cfg)
			a.defer_(func() { a.H.SaveFile("spacehop-config.json", data) })
		}
		if ok, _ := c.Button("gen/import", ix+bw+8, iy, IconImport, T("从文件恢复或导入"), BtnPlain, false); ok {
			a.dialog = "import"
		}
		iy += 46
		if len(a.backups) == 0 {
			c.Text(T("还没有自动备份。"), FSmall, Rect{ix, iy, inner, 24}, p.Muted, Left)
			return iy + 30
		}
		for i, b := range a.backups {
			if i >= 12 {
				break
			}
			name := b.Time
			if name == "" {
				name = b.Name
			}
			ctl, rh := a.row(c, ix, iy, inner, name, i18n.F("%d 条快捷键", b.Bindings), c.BtnW("", T("恢复到这一份")), false)
			if ok, _ := c.Button("gen/restore/"+itoa(i), ctl.X, ctl.Y, "", T("恢复到这一份"), BtnPlain, false); ok {
				file := b.Name
				a.defer_(func() {
					if !a.H.Confirm(T("用这份备份替换当前的全部设置？当前配置会先自动备份。")) {
						return
					}
					cfg, err := a.H.Restore(file)
					if err != nil {
						a.say(err.Error(), true)
						return
					}
					a.cfg = clone(cfg)
					a.issues = analyse(a.cfg)
					a.numS = map[string]*string{}
					a.say(T("已恢复"), false)
				})
			}
			iy += rh
		}
		return iy
	})
	a.endPage(c, "general", y-14, top)
}

// ---- activity ----

var logWords = map[string][2]string{
	"tap":     {i18n.N("输入"), i18n.N("轻点了引导键，照常输入")},
	"typing":  {i18n.N("打字"), i18n.N("按得很快，判定为打字，两个键都照常输入")},
	"run":     {i18n.N("运行"), ""},
	"bypass":  {i18n.N("放行"), i18n.N("当前程序或全屏游戏里已停用")},
	"cancel":  {i18n.N("取消"), ""},
	"timeout": {i18n.N("超时"), i18n.N("没有等到触发键")},
	"leader":  {i18n.N("引导"), i18n.N("轻按型引导键已就绪")},
	"refused": {i18n.N("被拒"), i18n.N("Windows 没有允许把这个窗口切到最前面")},
}

func (a *App) pollLog(seq int) {
	if seq != a.logSeq || !a.logOn || a.page != pageActivity {
		return
	}
	a.logEntries = a.H.Activity(true)
	a.H.Invalidate()
	a.H.Later(1000, func() { a.pollLog(seq) })
}

func (a *App) pageActivity(c *Ctx, view Rect) {
	p := c.P
	top := a.header(c, view, T("活动"), T("记录每一次判定，用来弄清某次按键为什么触发或没有触发。不记输入内容，只在内存里保留最近 300 条。"))
	lab, st := T("开始记录"), BtnPrimary
	if a.logOn {
		lab, st = T("停止并清空"), BtnPlain
	}
	if ok, _ := c.Button("log/toggle", view.X+view.W-32-c.BtnW("", lab), view.Y+30, "", lab, st, false); ok {
		a.logOn = !a.logOn
		a.logEntries = a.H.Activity(a.logOn)
		a.logSeq++
		if a.logOn {
			seq := a.logSeq
			a.H.Later(1000, func() { a.pollLog(seq) })
		}
	}
	if a.logOn && c.anim["log/poll"] != float64(a.logSeq) { // returning to the page restarts polling
		c.SetAnim("log/poll", float64(a.logSeq))
		seq := a.logSeq
		a.H.Later(1000, func() { a.pollLog(seq) })
	}
	x, y, w := a.scrollPage(c, "activity", view, top)
	if !a.logOn || len(a.logEntries) == 0 {
		r := Rect{x, y, w, 120}
		c.Card(r)
		t1, t2 := T("记录已关闭"), T("点“开始记录”，然后去别的窗口按几次引导键，再回来看。")
		if a.logOn {
			t1, t2 = T("正在等待按键…"), T("去别的窗口按住引导键试试。")
		}
		c.Text(t1, FBold, Rect{r.X, r.Y + 36, r.W, 22}, p.Text, Center)
		c.Text(t2, FText, Rect{r.X, r.Y + 62, r.W, 20}, p.Muted, Center)
		a.endPage(c, "activity", y+120, top)
		return
	}
	n := len(a.logEntries)
	c.Card(Rect{x, y, w, float64(n) * 36})
	for i := 0; i < n; i++ {
		e := a.logEntries[n-1-i]
		r := Rect{x, y + float64(i)*36, w, 36}
		if !c.visible(r) {
			continue
		}
		if i > 0 {
			c.Fill(Rect{r.X + 1, r.Y, r.W - 2, 1}, 0, raster.RGB(p.Line))
		}
		kind, rest, _ := strings.Cut(e.Text, " ")
		word := logWords[kind]
		c.Text(e.Time, FMono, Rect{r.X + 16, r.Y, 100, 36}, p.Muted, Left)
		st := ChipPlain
		if kind == "run" {
			st = ChipAccent
		}
		tag := T(word[0])
		if word[0] == "" {
			tag = kind
		}
		cw := c.Chip(tag, r.X+122, r.Y+7, st)
		tx := r.X + 122 + cw + 12
		rw := c.TextW(FBold, rest)
		c.Text(rest, FBold, Rect{tx, r.Y, rw + 4, 36}, p.Text, Left)
		c.Text(T(word[1]), FSmall, Rect{tx + rw + 12, r.Y, r.X + r.W - tx - rw - 160, 36}, p.Muted, Left)
		c.Text(e.App, FMono, Rect{r.X + r.W - 180, r.Y, 164, 36}, p.Muted, Right)
	}
	a.endPage(c, "activity", y+float64(n)*36, top)
}

// ---- about ----

func (a *App) pageAbout(c *Ctx, view Rect) {
	p := c.P
	top := a.header(c, view, T("关于"), "SpaceHop "+a.H.Version())
	x, y, w := a.scrollPage(c, "about", view, top)
	gap := 16.0
	lw := math.Floor((w - gap) / 2)
	if w < 760 {
		lw = w
	}
	// Usage.
	type used struct {
		b *config.Binding
		n int
	}
	var tops []used
	for i := range a.cfg.Bindings {
		if n := a.usage[a.cfg.Bindings[i].ID]; n > 0 {
			tops = append(tops, used{&a.cfg.Bindings[i], n})
		}
	}
	sort.SliceStable(tops, func(i, j int) bool { return tops[i].n > tops[j].n })
	if len(tops) > 5 {
		tops = tops[:5]
	}
	uh := 110.0 + float64(len(tops))*40
	if len(tops) == 0 {
		uh += 30
	}
	cmds := [][2]string{{"--settings", T("打开这个设置窗口")}, {"--toggle", T("暂停 / 恢复（还有 --enable、--disable）")}, {`--run "name"`, T("运行一条快捷键的动作，按名称或 id")}, {"--reload", T("重新读取配置文件")}, {"--quit", T("退出")}}
	chH := 96.0 + float64(len(cmds))*32
	rowH := math.Max(uh, chH)
	if lw == w {
		rowH = uh
	}
	c.Card(Rect{x, y, lw, rowH})
	c.Text(T("已经替你运行"), FSmall, Rect{x + 18, y + 16, lw, 16}, p.Muted, Left)
	num := itoa(a.usageTotal)
	c.Text(num, FBig, Rect{x + 18, y + 36, lw, 44}, p.Text, Left)
	c.Text(T("次快捷键"), FText, Rect{x + 26 + c.TextW(FBig, num), y + 50, 200, 24}, p.Muted, Left)
	uy := y + 96
	for i, t := range tops {
		if i > 0 {
			c.Fill(Rect{x + 18, uy, lw - 36, 1}, 0, raster.RGB(p.Line))
		}
		kx := x + 18
		kx += c.Chord(a.leaderOf(t.b).Key, kx, uy+7, true) + 4
		for _, k := range t.b.Keys {
			kx += c.Cap(k, kx, uy+7, false) + 4
		}
		c.Text(label(t.b), FBold, Rect{kx + 8, uy, x + lw - kx - 110, 40}, p.Text, Left)
		c.Text(i18n.F("%d 次", t.n), FSmall, Rect{x + lw - 100, uy, 82, 40}, p.Muted, Right)
		uy += 40
	}
	if len(tops) == 0 {
		c.Text(T("用起来以后，这里会列出你最常用的几个。"), FSmall, Rect{x + 18, uy, lw - 36, 20}, p.Muted, Left)
	}
	// Command line.
	cx, cy := x+lw+gap, y
	if lw == w {
		cx, cy = x, y+rowH+14
		rowH += chH + 14
	}
	c.Card(Rect{cx, cy, lw, math.Max(chH, uh)})
	c.Text(T("命令行"), FBold, Rect{cx + 18, cy + 16, lw, 20}, p.Text, Left)
	c.Para(T("SpaceHop 运行时，再次运行它并带上参数，可以从脚本或其他工具里控制它。"), FSmall, cx+18, cy+42, lw-36, p.Muted)
	for i, cm := range cmds {
		ry := cy + 84 + float64(i)*32
		cw := c.TextW(FMono, cm[0]) + 16
		c.Box(Rect{cx + 18, ry, cw, 24}, 6, raster.RGB(p.Raised), raster.RGB(p.Line))
		c.Text(cm[0], FMono, Rect{cx + 18, ry, cw, 24}, p.Text, Center)
		c.Text(cm[1], FSmall, Rect{cx + 18 + 140, ry, lw - 176, 24}, p.Muted, Left)
	}
	y += rowH + 14
	priv := T("SpaceHop 不联网，不记录你输入的内容。配置保存在你的用户目录里，是可以直接阅读和编辑的 JSON。")
	ph := c.TextH(FText, priv, w-36) + 62
	c.Card(Rect{x, y, w, ph})
	c.Text(T("隐私"), FBold, Rect{x + 18, y + 16, w, 20}, p.Text, Left)
	c.Para(priv, FText, x+18, y+44, w-36, p.Muted)
	a.endPage(c, "about", y+ph, top)
}

// ---- dialogs ----

func (a *App) openPicker(what string) {
	a.dialog, a.pickFor, a.pickQ = "apps", what, ""
	if a.apps == nil && !a.appsBusy {
		a.appsBusy = true
		a.H.Async(func() any { return a.H.Apps() }, func(v any) {
			a.apps, _ = v.([]config.AppInfo)
			if a.apps == nil {
				a.apps = []config.AppInfo{}
			}
			a.appsBusy = false
		})
	}
}

func (a *App) picked(app config.AppInfo) {
	a.dialog = ""
	exe := app.Exe
	if exe == "" && strings.HasSuffix(strings.ToLower(app.Path), ".exe") {
		exe = strings.ToLower(app.Path[strings.LastIndexAny(app.Path, `\/`)+1:])
	}
	d := a.draft
	switch a.pickFor {
	case "path":
		d.Action.Path, d.Action.Exe = app.Path, app.Exe
	case "with":
		d.Action.With = app.Path
	case "cycle":
		if len(d.Action.Cycle) < config.MaxCycle {
			d.Action.Cycle = append(d.Action.Cycle, config.AppRef{Name: app.Name, Path: app.Path, Exe: app.Exe})
		}
	case "scope", "disabled":
		if exe == "" {
			a.say(T("这个程序没有可识别的 exe 名称，不能用来限定范围"), true)
			return
		}
		list := &a.cfg.DisabledApps
		if a.pickFor == "scope" {
			list = &d.Scope.Apps
		}
		for _, x := range *list {
			if x == exe {
				return
			}
		}
		*list = append(*list, exe)
		if a.pickFor == "disabled" {
			a.save(true)
		}
	}
}

func (a *App) dialogs(c *Ctx) {
	t := c.Anim("dialog", b2f(a.dialog != ""))
	if a.dialog == "" {
		return
	}
	p := c.P
	c.Fill(Rect{0, 0, c.W, c.H}, 0, raster.RGB(0x0A0C08).Alpha(0.34*t))
	w := math.Min(560, c.W-60)
	h := 230.0
	if a.dialog == "apps" {
		h = math.Min(560, c.H-80)
	}
	r := Rect{(c.W - w) / 2, (c.H-h)/2 + (1-t)*14, w, h}
	if c.Click("dlg/scrim", Rect{0, 0, c.W, c.H}) && !r.In(c.MX, c.MY) {
		a.dialog = ""
		return
	}
	c.Shadow(Rect{r.X, r.Y + 10, r.W, r.H}, 16, 40, raster.RGB(0).Alpha(0.3))
	c.Box(r, 16, raster.RGB(p.Surface), raster.RGB(p.Line))
	x, y := r.X+20, r.Y+18
	title := func(s string) {
		c.Text(s, FBold, Rect{x, y, w - 40, 22}, p.Text, Left)
		y += 34
	}
	footer := func(btns ...func(x, y float64) float64) {
		c.Fill(Rect{r.X + 1, r.Y + r.H - 60, r.W - 2, 1}, 0, raster.RGB(p.Line))
		bx := r.X + r.W - 20
		for _, b := range btns {
			bx -= b(bx, r.Y+r.H-47) + 8
		}
	}
	cancel := func(bx, by float64) float64 {
		bw := c.BtnW("", T("取消"))
		if ok, _ := c.Button("dlg/cancel", bx-bw, by, "", T("取消"), BtnPlain, false); ok {
			a.dialog = ""
		}
		return bw
	}
	btn := func(id, label string, st BtnStyle, do func()) func(bx, by float64) float64 {
		return func(bx, by float64) float64 {
			bw := c.BtnW("", label)
			if ok, _ := c.Button("dlg/"+id, bx-bw, by, "", label, st, false); ok {
				do()
			}
			return bw
		}
	}
	switch a.dialog {
	case "apps":
		title(T("选择程序"))
		a.fieldIcon(c, "pick/q", Rect{x, y, w - 40, 36}, &a.pickQ, T("搜索已安装和正在运行的程序"), IconSearch)
		y += 46
		view := Rect{r.X + 8, y, w - 16, r.Y + r.H - 60 - y}
		if a.apps == nil {
			c.Text(T("正在读取程序列表…"), FText, view, p.Muted, Center)
			footer(cancel)
			break
		}
		q := strings.ToLower(a.pickQ)
		var list []config.AppInfo
		for _, ap := range a.apps {
			if q == "" || strings.Contains(strings.ToLower(ap.Name+" "+ap.Exe+" "+ap.Path), q) {
				list = append(list, ap)
				if len(list) == 200 {
					break
				}
			}
		}
		if len(list) == 0 {
			c.Text(T("没有找到。可以回到上一步直接填写路径。"), FText, view, p.Muted, Center)
		}
		off := c.Scroll("pick", view, float64(len(list))*48)
		src := map[string]string{"running": T("正在运行"), "start": T("开始菜单"), "store": T("应用商店")}
		for i, ap := range list {
			ir := Rect{view.X + 4, view.Y + float64(i)*48 - off, view.W - 14, 46}
			if !c.visible(ir) {
				continue
			}
			id := "pick/" + itoa(i)
			if c.Hover(id, ir) {
				c.Fill(ir, 9, raster.RGB(p.Raised))
			}
			ic := Rect{ir.X + 8, ir.Y + 10, 26, 26}
			c.Fill(ic, 7, raster.RGB(p.Raised))
			c.Text(TypeIcon[config.OpenApp], FIcon, ic, p.Muted, Center)
			cw := c.ChipW(src[ap.Source])
			c.Chip(src[ap.Source], ir.X+ir.W-cw-8, ir.Y+12, ChipPlain)
			c.Text(ap.Name, FBold, Rect{ir.X + 44, ir.Y + 5, ir.W - cw - 64, 20}, p.Text, Left)
			c.Text(ap.Path, FSmall, Rect{ir.X + 44, ir.Y + 24, ir.W - cw - 64, 16}, p.Muted, Left)
			if c.Click(id, ir) {
				a.picked(ap)
				c.EndScroll()
				return
			}
		}
		c.EndScroll()
		footer(cancel)
	case "import":
		title(T("导入"))
		y += c.Para(T("可以导入之前导出的快捷键文件、完整配置，或者刚才用“复制”放进剪贴板的快捷键。"), FText, x, y, w-40, p.Muted) + 16
		ok, bw := c.Button("dlg/file", x, y, IconImport, T("选择文件…"), BtnPrimary, false)
		if ok {
			a.defer_(func() {
				if data, ok := a.H.OpenFile(); ok {
					a.gotImport(data)
				}
			})
		}
		if ok, _ := c.Button("dlg/clip", x+bw+8, y, IconCopy, T("从剪贴板粘贴"), BtnPlain, false); ok {
			a.defer_(func() { a.gotImport([]byte(a.H.Clipboard())) })
		}
		footer(cancel)
	case "merge":
		n, dup := len(a.incoming.Bindings), 0
		for i := range a.incoming.Bindings {
			if duplicateOf(a.cfg, &a.incoming.Bindings[i]) >= 0 {
				dup++
			}
		}
		title(i18n.F("导入 %d 条快捷键", n))
		c.Para(i18n.F("其中 %d 条是新的，%d 条与现有快捷键的按键和范围相同。用到的引导键如果还没有，会一并添加。", n-dup, dup), FText, x, y, w-40, p.Muted)
		do := func(mode string) func() {
			return func() {
				added, replaced, skipped := merge(a.cfg, a.incoming, mode)
				a.dialog = ""
				if a.save(true) {
					a.say(i18n.F("新增 %d 条，替换 %d 条，跳过 %d 条", added, replaced, skipped), false)
				}
			}
		}
		skip := T("合并")
		if dup > 0 {
			skip = T("合并，跳过重复")
		}
		btns := []func(x, y float64) float64{btn("skip", skip, BtnPrimary, do("skip"))}
		if dup > 0 {
			btns = append(btns, btn("replace", T("合并，重复的用新的"), BtnPlain, do("replace")))
		}
		btns = append(btns, btn("all", T("全部替换现有"), BtnDanger, do("replaceAll")), cancel)
		footer(btns...)
	case "full":
		title(T("恢复完整配置"))
		c.Para(i18n.F("这个文件是一份完整的配置（%d 条快捷键，以及引导键、提示窗等全部设置）。恢复会替换当前的全部设置；当前配置会先自动备份。", len(a.full.Bindings)), FText, x, y, w-40, p.Muted)
		footer(btn("ok", T("替换当前配置"), BtnPrimary, func() {
			a.cfg = a.full
			a.dialog = ""
			a.numS = map[string]*string{}
			if a.save(true) {
				a.say(T("已恢复"), false)
			}
		}), cancel)
	}
	c.Flush()
}

func (a *App) gotImport(data []byte) {
	b, full := parseImport(data)
	switch {
	case b != nil:
		a.incoming, a.dialog = b, "merge"
	case full != nil:
		a.full, a.dialog = full, "full"
	default:
		a.say(T("这不是 SpaceHop 的快捷键文件"), true)
	}
}
