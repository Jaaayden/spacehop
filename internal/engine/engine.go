// Package engine decides, key event by key event, whether a leader key is
// being typed or used to run a shortcut. It is pure logic: the platform
// layer feeds it events and carries out its effects.
//
// Every key event costs O(1) in the number of bindings: a few map lookups.
package engine

import (
	"sort"
	"strings"

	"spacehop/internal/config"
)

// Stroke is a synthesised key or mouse-button event.
type Stroke struct {
	VK   uint32
	Down bool
}

// HintItem is one row of the hint window.
type HintItem struct {
	Key   string   // the key to press (level view)
	Keys  []string // the whole shortcut (search view)
	Label string
	Type  string // action type, "" for a pure group
	ID    string // binding id, "" for a pure group
	Group bool   // more keys follow
	Count int    // shortcuts below this key
}

// HintView is what the hint window shows.
type HintView struct {
	Path   []string
	Items  []HintItem
	Search bool
	Query  string
	Sel    int
	// SearchKey is the key that opens search from this view, or "".
	SearchKey string
}

// Effects is what the engine asks the platform to do.
type Effects interface {
	// Inject sends events to the system in order.
	Inject(s []Stroke)
	// Run performs a binding's action without blocking.
	Run(b *config.Binding)
	// Hint shows or refreshes the hint window after delayMs. The platform
	// calls View when it actually draws, so an ordinary tap of the leader
	// never pays for building the list.
	Hint(delayMs int)
	HideHint()
	// Timeout arranges a call to OnTimeout after ms; 0 cancels it.
	Timeout(ms int)
	// WatchMouse turns mouse-button reporting on while a leader is undecided.
	WatchMouse(on bool)
	// Context returns the foreground program (lower-case file name) and
	// whether a full-screen game is running.
	Context() (exe string, fullscreen bool)
	// Mods returns the modifier keys physically held (config.Mod* bits),
	// not counting the event being handled.
	Mods() uint8
}

type node struct {
	key      uint32
	parent   *node
	children map[uint32]*node
	bindings []*config.Binding
	count    int // bindings in this subtree
}

type leader struct {
	cfg  *config.Leader
	hold bool
	vk   uint32 // hold key, or main key of a tap combination; 0 for a lone modifier
	root *node
}

type state int

const (
	idle        state = iota
	held              // hold leader down, swallowed; waiting for triggers or release
	seq               // tap leader pressed; waiting for triggers
	search            // typing a search in the hint window
	passthrough       // leader was delivered to the system; wait for its release
	bypass            // disabled for this press (excluded app, modifiers, game)
	dead              // cancelled; swallow everything until the leader is released
)

const (
	maskKey      = 0xE8 // unassigned key: stops a lone Alt/Win tap opening a menu
	maxQuery     = 32
	maxSearchHit = 200
)

type searchEntry struct {
	b    *config.Binding
	keys []string
	text string
}

type Engine struct {
	fx  Effects
	cfg *config.Config
	// Trace, when set, receives a line for every decision (activity log).
	Trace func(string)

	holdBy   map[uint32]*leader
	chordBy  map[uint32]*leader // mods<<16 | vk
	chordVK  map[uint32]bool
	modBy    map[uint32]*leader
	disabled map[string]bool
	searchVK uint32
	index    []searchEntry // built on first search

	st        state
	ld        *leader
	ldVK      uint32 // physical hold-leader key currently down
	t0        uint32 // time of leader down
	typing    bool   // leader followed other typing closely
	used      bool   // a trigger key was accepted during this hold
	cur       *node
	exe       string
	lastType  uint32
	haveType  bool
	swallowed map[uint32]bool // keys whose down we swallowed; swallow the up too
	repeatVK  uint32
	repeatB   *config.Binding

	pend   *leader // single-modifier leader that is down and may become a tap
	pendVK uint32
	pendT  uint32

	query   []byte
	results []int // indexes into index
	sel     int
}

func New(fx Effects, cfg *config.Config) *Engine {
	e := &Engine{fx: fx, swallowed: map[uint32]bool{}}
	e.SetConfig(cfg)
	return e
}

func (e *Engine) trace(parts ...string) {
	if e.Trace != nil {
		e.Trace(strings.Join(parts, " "))
	}
}

// SetConfig swaps the configuration. Call it from the same thread as OnKey.
func (e *Engine) SetConfig(cfg *config.Config) {
	if e.st == held || e.st == seq || e.st == search {
		e.end()
	}
	e.clearPend()
	e.st = idle
	e.cfg = cfg
	e.index = nil
	e.searchVK, _ = config.VK(cfg.Hint.SearchKey)
	e.disabled = map[string]bool{}
	for _, a := range cfg.DisabledApps {
		e.disabled[a] = true
	}
	e.holdBy, e.chordBy, e.chordVK, e.modBy = map[uint32]*leader{}, map[uint32]*leader{}, map[uint32]bool{}, map[uint32]*leader{}
	byID := map[string]*leader{}
	var first *leader
	for i := range cfg.Leaders {
		lc := &cfg.Leaders[i]
		mods, vk, err := config.ParseChord(lc.Key)
		if err != nil {
			continue
		}
		l := &leader{cfg: lc, hold: lc.Mode == config.Hold, root: &node{children: map[uint32]*node{}}}
		switch {
		case l.hold:
			l.vk = vk
			e.holdBy[vk] = l
		case len(mods) == 0:
			for _, side := range config.Sides(vk) {
				e.modBy[side] = l
			}
		default:
			var m uint32
			for _, mk := range mods {
				m |= uint32(config.ModBit(mk))
			}
			l.vk = vk
			e.chordBy[m<<16|vk] = l
			e.chordVK[vk] = true
		}
		byID[lc.ID] = l
		if first == nil {
			first = l
		}
	}
	if first == nil {
		return
	}
	for i := range cfg.Bindings {
		b := &cfg.Bindings[i]
		if !b.Enabled || b.Action.Incomplete() {
			continue
		}
		l := byID[b.Leader]
		if l == nil {
			l = first
		}
		n := l.root
		ok := true
		for _, k := range b.Keys {
			vk, found := config.VK(k)
			if !found {
				ok = false
				break
			}
			c := n.children[vk]
			if c == nil {
				c = &node{key: vk, parent: n, children: map[uint32]*node{}}
				n.children[vk] = c
			}
			n = c
		}
		if ok && (n != l.root || !l.hold) {
			n.bindings = append(n.bindings, b)
		}
	}
	for _, l := range byID {
		prune(l.root)
	}
}

// prune removes branches that lead to no binding and counts the rest.
func prune(n *node) int {
	n.count = len(n.bindings)
	for k, c := range n.children {
		if got := prune(c); got == 0 {
			delete(n.children, k)
		} else {
			n.count += got
		}
	}
	return n.count
}

func (n *node) match(exe string) *config.Binding {
	for _, b := range n.bindings {
		if b.Scope.Matches(exe) {
			return b
		}
	}
	return nil
}

func (n *node) visible(exe string) bool {
	if n.match(exe) != nil {
		return true
	}
	for _, c := range n.children {
		if c.visible(exe) {
			return true
		}
	}
	return false
}

func (n *node) label(exe string) string {
	b := n.match(exe)
	if b != nil && len(n.children) == 0 {
		return b.Label()
	}
	var names []string
	if b != nil {
		names = append(names, b.Label())
	}
	for _, c := range sortedChildren(n) {
		if c.visible(exe) {
			names = append(names, c.label(exe))
		}
		if len(names) >= 4 {
			break
		}
	}
	return strings.Join(names, "、")
}

func sortedChildren(n *node) []*node {
	out := make([]*node, 0, len(n.children))
	for _, c := range n.children {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return keyRank(out[i].key) < keyRank(out[j].key) })
	return out
}

// keyRank orders letters first, then digits, then everything else.
func keyRank(vk uint32) uint32 {
	switch {
	case vk >= 'A' && vk <= 'Z':
		return vk
	case vk >= '0' && vk <= '9':
		return 0x100 + vk
	}
	return 0x200 + vk
}

func (e *Engine) path() []string {
	var p []string
	for n := e.cur; n != nil && n.parent != nil; n = n.parent {
		p = append([]string{config.KeyName(n.key)}, p...)
	}
	return append([]string{e.ld.cfg.Key}, p...)
}

func (e *Engine) showHint(delay int) {
	if e.cfg.Hint.Show || e.st == search {
		e.fx.Hint(delay)
	}
}

// View returns what the hint window should show right now; ok is false
// when no leader is active.
func (e *Engine) View() (v HintView, ok bool) {
	switch e.st {
	case search:
		v.Search, v.Query, v.Sel = true, string(e.query), e.sel
		for _, i := range e.results {
			en := e.index[i]
			v.Items = append(v.Items, HintItem{Keys: en.keys, Label: en.b.Label(), Type: en.b.Action.Type, ID: en.b.ID})
		}
		return v, true
	case held, seq:
		v.Path = e.path()
		for _, c := range sortedChildren(e.cur) {
			if !c.visible(e.exe) {
				continue
			}
			it := HintItem{Key: config.KeyName(c.key), Label: c.label(e.exe), Group: len(c.children) > 0, Count: c.count}
			if b := c.match(e.exe); b != nil {
				it.Type, it.ID = b.Action.Type, b.ID
			}
			v.Items = append(v.Items, it)
		}
		if e.searchVK != 0 && e.cur.children[e.searchVK] == nil {
			v.SearchKey = e.cfg.Hint.SearchKey
		}
		return v, true
	}
	return v, false
}

// end leaves any active leader state and tidies the platform side.
func (e *Engine) end() {
	e.fx.HideHint()
	e.fx.WatchMouse(false)
	e.fx.Timeout(0)
	e.repeatB = nil
	e.query, e.results = nil, nil
}

func (e *Engine) clearPend() {
	if e.pend != nil {
		e.pend = nil
		e.fx.WatchMouse(false)
	}
}

// Active reports whether a leader is active (held, in sequence or search).
func (e *Engine) Active() bool { return e.st == held || e.st == seq || e.st == search }

// usable reports whether shortcuts may start in the foreground program.
func (e *Engine) usable() (exe string, ok bool) {
	exe, fullscreen := e.fx.Context()
	return exe, !e.disabled[exe] && !(fullscreen && e.cfg.DisableInFullscreen)
}

// OnKey handles one physical key event. t is a millisecond tick count that
// may wrap. It returns true when the event must not reach other programs.
func (e *Engine) OnKey(vk uint32, down bool, t uint32) bool {
	if !e.cfg.Enabled {
		return false
	}
	if !down && vk != e.ldVK && e.swallowed[vk] {
		delete(e.swallowed, vk)
		if vk == e.repeatVK {
			e.repeatB = nil
		}
		return true
	}
	switch e.st {
	case idle:
		return e.onIdle(vk, down, t)
	case bypass, passthrough:
		if vk == e.ldVK && !down {
			e.st, e.ldVK = idle, 0
		} else if down {
			e.noteTyping(vk, t)
		}
		return false
	case dead:
		if vk == e.ldVK {
			if !down {
				e.st, e.ldVK = idle, 0
			}
			return true
		}
		if !down || config.IsModifier(vk) {
			return false
		}
		e.swallowed[vk] = true
		return true
	case held:
		return e.onHeld(vk, down, t)
	case seq:
		return e.onSeq(vk, down)
	default:
		return e.onSearch(vk, down)
	}
}

func (e *Engine) onIdle(vk uint32, down bool, t uint32) bool {
	if !down {
		p := e.pend
		if p == nil || vk != e.pendVK {
			return false
		}
		e.clearPend()
		if t-e.pendT > uint32(e.cfg.Timing.ModTapMs) {
			return false
		}
		exe, ok := e.usable()
		if !ok {
			return false
		}
		// Swallow the release and send it after a neutral key, so a lone
		// Alt or Win tap does not open a menu.
		e.fx.Inject([]Stroke{{maskKey, true}, {maskKey, false}, {vk, false}})
		e.startSeq(p, exe)
		return true
	}
	if config.IsModifier(vk) {
		if l := e.modBy[vk]; l != nil && e.pend == nil && e.fx.Mods() == 0 {
			e.pend, e.pendVK, e.pendT = l, vk, t
			e.fx.WatchMouse(true)
		} else if e.pend != nil && vk != e.pendVK {
			e.clearPend()
		}
		return false
	}
	e.clearPend()
	hl := e.holdBy[vk]
	if hl == nil && !e.chordVK[vk] {
		e.noteTyping(vk, t)
		return false
	}
	mods := e.fx.Mods()
	if mods != 0 {
		if l := e.chordBy[uint32(mods)<<16|vk]; l != nil {
			if exe, ok := e.usable(); ok {
				e.swallowed[vk] = true
				e.startSeq(l, exe)
				return true
			}
		}
		if hl != nil {
			e.st, e.ldVK = bypass, vk
		}
		return false
	}
	if hl == nil {
		e.noteTyping(vk, t)
		return false
	}
	exe, ok := e.usable()
	if !ok {
		e.st, e.ldVK = bypass, vk
		e.trace("bypass", hl.cfg.Key, exe)
		return false
	}
	e.st, e.ld, e.ldVK, e.t0, e.exe, e.cur, e.used = held, hl, vk, t, exe, hl.root, false
	e.typing = hl.cfg.KeepTap && e.haveType && t-e.lastType < uint32(e.cfg.Timing.TypingSuppressMs)
	e.fx.WatchMouse(true)
	e.showHint(e.cfg.Timing.HintDelayMs)
	return true
}

func (e *Engine) startSeq(l *leader, exe string) {
	e.st, e.ld, e.exe, e.cur, e.used = seq, l, exe, l.root, false
	e.trace("leader", l.cfg.Key)
	if b := l.root.match(exe); b != nil {
		e.fx.Run(b) // leader-only shortcut
		e.trace("run", b.Label())
	}
	if len(l.root.children) == 0 {
		e.st = idle
		return
	}
	e.fx.WatchMouse(true)
	e.fx.Timeout(e.cfg.Timing.TapTimeoutMs)
	e.showHint(e.cfg.Timing.HintDelayMs)
}

func (e *Engine) onHeld(vk uint32, down bool, t uint32) bool {
	keepTap := e.ld.cfg.KeepTap
	if vk == e.ldVK {
		if down {
			return true // auto-repeat while holding
		}
		e.end()
		e.st, e.ldVK = idle, 0
		if !e.used && keepTap && t-e.t0 <= uint32(e.cfg.Timing.TapReplayMs) {
			e.fx.Inject([]Stroke{{vk, true}, {vk, false}})
			e.lastType, e.haveType = t, true
			e.trace("tap", e.ld.cfg.Key)
		}
		return true
	}
	if !down || config.IsModifier(vk) {
		return false
	}
	if e.swallowed[vk] {
		// Auto-repeat of a trigger key: only simulated keystrokes repeat.
		if vk == e.repeatVK && e.repeatB != nil {
			e.fx.Run(e.repeatB)
		}
		return true
	}
	if !e.used && keepTap {
		thr := uint32(e.cfg.Timing.ActivationMs)
		if e.typing && uint32(e.cfg.Timing.TypingSuppressMs) > thr {
			thr = uint32(e.cfg.Timing.TypingSuppressMs)
		}
		if t-e.t0 < thr {
			return e.rollover(vk, t)
		}
	}
	if e.child(vk) == nil && !e.used && e.cur == e.ld.root && keepTap && vk != config.VKEsc && vk != e.searchVK {
		// Not a shortcut: the user was typing, deliver both keys.
		return e.rollover(vk, t)
	}
	e.swallowed[vk] = true
	e.trigger(vk)
	return true
}

func (e *Engine) onSeq(vk uint32, down bool) bool {
	if !down || config.IsModifier(vk) {
		return false
	}
	e.swallowed[vk] = true
	e.trigger(vk)
	return true
}

// stop ends the active leader. While a hold leader is still down the rest
// of the hold is ignored.
func (e *Engine) stop() {
	e.end()
	if e.ldVK != 0 {
		e.st = dead
	} else {
		e.st = idle
	}
}

// child returns the next level for a key, if any shortcut there applies to
// the foreground program.
func (e *Engine) child(vk uint32) *node {
	c := e.cur.children[vk]
	if c != nil && !c.visible(e.exe) {
		return nil
	}
	return c
}

// trigger handles a key pressed while a leader is active.
func (e *Engine) trigger(vk uint32) {
	holding := e.st == held
	child := e.child(vk)
	switch {
	case child != nil:
	case vk == config.VKEsc:
		e.trace("cancel")
		e.stop()
		return
	case vk == config.VKBack && e.cur.parent != nil:
		e.cur = e.cur.parent
		e.again()
		return
	case vk == e.searchVK && vk != 0:
		e.startSearch()
		return
	default:
		if !holding {
			e.trace("cancel", config.KeyName(vk))
			e.stop() // a key that is not part of any shortcut ends a tap sequence
		}
		return
	}
	e.used = true
	e.repeatB = nil
	b := child.match(e.exe)
	if b != nil {
		e.fx.Run(b)
		e.trace("run", b.Label())
		if b.Action.Type == config.Keystroke {
			e.repeatVK, e.repeatB = vk, b
		}
	}
	after := ""
	if b != nil {
		after = b.After
	}
	switch {
	case len(child.children) > 0 && after != "stop" && after != "root":
		e.cur = child // a prefix action runs, then the longer shortcuts stay reachable
		e.again()
	case after == "stay":
		e.again()
	case after == "stop" || (after == "" && !holding):
		e.stop()
	default:
		e.cur = e.ld.root
		if holding && after == "" {
			e.fx.HideHint()
		} else {
			e.again()
		}
	}
}

// again refreshes the hint and restarts the tap-leader timeout.
func (e *Engine) again() {
	if e.st == seq {
		e.fx.Timeout(e.cfg.Timing.TapTimeoutMs)
	}
	e.showHint(0)
}

// rollover handles fast typing where the next key goes down before the
// leader comes up: both are delivered in the order they were pressed.
func (e *Engine) rollover(vk uint32, t uint32) bool {
	e.end()
	e.st = passthrough
	e.fx.Inject([]Stroke{{e.ldVK, true}, {vk, true}})
	e.lastType, e.haveType = t, true
	e.trace("typing", e.ld.cfg.Key, config.KeyName(vk))
	return true
}

func (e *Engine) noteTyping(vk uint32, t uint32) {
	if !config.IsModifier(vk) {
		e.lastType, e.haveType = t, true
	}
}

// OnTimeout is called by the platform when the Timeout it was asked for
// expires: a tap leader got no trigger key in time.
func (e *Engine) OnTimeout() {
	if e.st == seq {
		e.trace("timeout")
		e.stop()
	}
}

// OnMouseDown is called for a physical mouse-button press while a leader is
// undecided. It returns true when the click was swallowed and re-sent after
// the leader (space-drag in drawing programs).
func (e *Engine) OnMouseDown(button uint32) bool {
	e.clearPend()
	switch e.st {
	case seq, search:
		e.stop()
	case held:
		if e.used || !e.ld.cfg.KeepTap {
			return false
		}
		e.end()
		e.st = passthrough
		e.fx.Inject([]Stroke{{e.ldVK, true}, {button, true}})
		return true
	}
	return false
}

// ---- search ----

func (e *Engine) buildIndex() {
	leaders := map[string]*config.Leader{}
	for i := range e.cfg.Leaders {
		leaders[e.cfg.Leaders[i].ID] = &e.cfg.Leaders[i]
	}
	for i := range e.cfg.Bindings {
		b := &e.cfg.Bindings[i]
		if !b.Enabled || b.Action.Incomplete() {
			continue
		}
		l := leaders[b.Leader]
		if l == nil {
			l = &e.cfg.Leaders[0]
		}
		keys := append([]string{l.Key}, b.Keys...)
		a := b.Action
		text := strings.ToLower(strings.Join([]string{b.Label(), strings.Join(b.Keys, ""), a.Path, a.Exe, a.URL, a.Command, a.Chord, a.Menu, a.Window, a.System}, " "))
		e.index = append(e.index, searchEntry{b: b, keys: keys, text: text})
	}
}

func (e *Engine) startSearch() {
	if e.index == nil {
		e.buildIndex()
	}
	e.st = search
	e.query = e.query[:0]
	e.fx.Timeout(0)
	e.fx.WatchMouse(true)
	e.filter()
}

func (e *Engine) filter() {
	words := strings.Fields(string(e.query))
	e.results = e.results[:0]
	e.sel = 0
	for i := range e.index {
		en := &e.index[i]
		if !en.b.Scope.Matches(e.exe) {
			continue
		}
		ok := true
		for _, w := range words {
			if !strings.Contains(en.text, w) {
				ok = false
				break
			}
		}
		if ok {
			e.results = append(e.results, i)
			if len(e.results) == maxSearchHit {
				break
			}
		}
	}
	e.fx.Hint(0)
}

// queryChar maps a key to the character it adds to the search text.
func queryChar(vk uint32) byte {
	switch {
	case vk >= 'A' && vk <= 'Z':
		return byte(vk) + 32
	case vk >= '0' && vk <= '9':
		return byte(vk)
	case vk >= 0x60 && vk <= 0x69:
		return byte(vk-0x60) + '0'
	case vk == config.VKSpace:
		return ' '
	}
	if n := config.KeyName(vk); len(n) == 1 {
		return n[0]
	}
	return 0
}

func (e *Engine) onSearch(vk uint32, down bool) bool {
	if vk == e.ldVK {
		if !down {
			e.ldVK = 0 // the search stays open after the leader is released
		}
		return true
	}
	if !down || config.IsModifier(vk) {
		return false
	}
	e.swallowed[vk] = true
	switch vk {
	case config.VKEsc:
		e.stop()
	case config.VKEnter:
		if e.sel < len(e.results) {
			b := e.index[e.results[e.sel]].b
			e.fx.Run(b)
			e.trace("run", b.Label())
		}
		e.stop()
	case 0x26: // Up
		if e.sel > 0 {
			e.sel--
			e.fx.Hint(0)
		}
	case 0x28, config.VKTab: // Down
		if e.sel < len(e.results)-1 {
			e.sel++
			e.fx.Hint(0)
		}
	case config.VKBack:
		if len(e.query) == 0 {
			e.stop()
			return true
		}
		e.query = e.query[:len(e.query)-1]
		e.filter()
	default:
		if c := queryChar(vk); c != 0 && len(e.query) < maxQuery {
			e.query = append(e.query, c)
			e.filter()
		}
	}
	return true
}
