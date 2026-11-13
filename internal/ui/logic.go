package ui

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"spacehop/internal/config"
	"spacehop/internal/i18n"
)

var T = i18n.T

// TypeNames lists the action types in display order.
var TypeNames = [][2]string{
	{config.OpenApp, i18n.N("打开程序")}, {config.OpenURL, i18n.N("打开网址")}, {config.OpenFile, i18n.N("文件 / 文件夹")},
	{config.ExecuteFile, i18n.N("运行文件")}, {config.RunCommand, i18n.N("运行命令")}, {config.Keystroke, i18n.N("模拟按键")},
	{config.InsertText, i18n.N("插入文本")}, {config.MenuCommand, i18n.N("菜单命令")}, {config.Window, i18n.N("窗口")},
	{config.InputMethod, i18n.N("输入法")}, {config.Notify, i18n.N("通知")}, {config.System, i18n.N("系统")},
}

func typeName(t string) string {
	for _, r := range TypeNames {
		if r[0] == t {
			return T(r[1])
		}
	}
	return t
}

// frontTypes act on whatever window is in front, so a test run waits for
// the user to switch to it.
var frontTypes = map[string]bool{config.Keystroke: true, config.InsertText: true, config.MenuCommand: true, config.Window: true, config.InputMethod: true}

func clone(c *config.Config) *config.Config {
	data, _ := json.Marshal(c)
	out := &config.Config{}
	_ = json.Unmarshal(data, out)
	return out
}

func cloneBinding(b *config.Binding) *config.Binding {
	data, _ := json.Marshal(b)
	out := &config.Binding{}
	_ = json.Unmarshal(data, out)
	return out
}

func baseName(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/!`); i >= 0 {
		p = p[i+1:]
	}
	low := strings.ToLower(p)
	if strings.HasSuffix(low, ".exe") || strings.HasSuffix(low, ".lnk") {
		p = p[:len(p)-4]
	}
	return p
}

// label is the name shown for a binding, with a hint when it is unfinished.
func label(b *config.Binding) string {
	if b.Name != "" {
		return b.Name
	}
	if b.Action.Incomplete() {
		return T("（未填写）")
	}
	return b.Label()
}

// detail is the secondary line of a binding in the list.
func detail(b *config.Binding) string {
	a := b.Action
	switch a.Type {
	case config.OpenApp:
		if len(a.Cycle) > 0 {
			names := []string{baseName(a.Path)}
			for _, c := range a.Cycle {
				names = append(names, baseName(c.Path))
			}
			return T("轮换") + " " + strings.Join(names, " → ")
		}
		return a.Path
	case config.OpenURL:
		if b.Name == "" {
			if u, err := url.Parse(a.URL); err == nil && u.Host != "" {
				return a.URL
			}
		}
		return a.URL
	case config.OpenFile, config.ExecuteFile:
		return a.Path
	case config.RunCommand:
		sh := "cmd"
		if a.Shell == "powershell" {
			sh = "PowerShell"
		}
		return sh + " · " + a.Command
	case config.InsertText:
		mode := T("键入")
		if a.Paste {
			mode = T("粘贴")
		}
		return mode + " · " + strings.Join(strings.Fields(a.Text), " ")
	case config.MenuCommand:
		return a.Menu
	case config.Notify:
		return a.Text
	case config.Keystroke:
		return a.Chord
	}
	return typeName(a.Type)
}

func scopesOverlap(a, b *config.Binding) bool {
	am, bm := a.Scope.Mode, b.Scope.Mode
	if am != "only" && am != "except" || bm != "only" && bm != "except" {
		return true
	}
	in := func(list []string, x string) bool {
		for _, v := range list {
			if v == x {
				return true
			}
		}
		return false
	}
	anyIn := func(xs, ys []string, want bool) bool {
		for _, x := range xs {
			if in(ys, x) == want {
				return true
			}
		}
		return false
	}
	switch {
	case am == "only" && bm == "only":
		return anyIn(a.Scope.Apps, b.Scope.Apps, true)
	case am == "only":
		return anyIn(a.Scope.Apps, b.Scope.Apps, false)
	case bm == "only":
		return anyIn(b.Scope.Apps, a.Scope.Apps, false)
	}
	return true
}

type issue struct {
	conflict bool
	prefix   int // longer shortcuts that continue from this one
}

func seqKey(c *config.Config, b *config.Binding) string {
	return c.LeaderOf(b).ID + "|" + strings.Join(b.Keys, " ")
}

// analyse finds shortcuts that collide and ones that are a prefix of others.
func analyse(c *config.Config) map[string]issue {
	groups := map[string][]*config.Binding{}
	for i := range c.Bindings {
		b := &c.Bindings[i]
		k := seqKey(c, b)
		groups[k] = append(groups[k], b)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]issue{}
	for i, k := range keys {
		// Sorted, so everything this sequence is a prefix of follows it.
		n := 0
		for _, k2 := range keys[i+1:] {
			if !strings.HasPrefix(k2, k+" ") {
				if !strings.HasPrefix(k2, k) {
					break
				}
				continue
			}
			n += len(groups[k2])
		}
		g := groups[k]
		for _, b := range g {
			is := issue{}
			if len(b.Keys) > 0 {
				is.prefix = n
			}
			for _, x := range g {
				if x != b && x.Enabled && b.Enabled && scopesOverlap(x, b) {
					is.conflict = true
				}
			}
			if is.conflict || is.prefix > 0 {
				out[b.ID] = is
			}
		}
	}
	return out
}

func matches(b *config.Binding, q string) bool {
	hay := strings.ToLower(label(b) + " " + detail(b) + " " + strings.Join(b.Keys, "") + " " + strings.Join(b.Keys, " ") + " " + typeName(b.Action.Type) + " " + b.Action.Type + " " + strings.Join(b.Scope.Apps, " "))
	for _, w := range strings.Fields(strings.ToLower(q)) {
		switch {
		case w == "is:on":
			if !b.Enabled {
				return false
			}
		case w == "is:off":
			if b.Enabled {
				return false
			}
		case strings.HasPrefix(w, "type:"):
			if !strings.Contains(strings.ToLower(b.Action.Type+" "+typeName(b.Action.Type)), w[5:]) {
				return false
			}
		case strings.HasPrefix(w, "app:"):
			if !strings.Contains(strings.Join(b.Scope.Apps, " "), w[4:]) {
				return false
			}
		default:
			if !strings.Contains(hay, w) {
				return false
			}
		}
	}
	return true
}

// ---- import / export ----

type exported struct {
	config.Binding
	LeaderKey     string `json:"leaderKey"`
	LeaderMode    string `json:"leaderMode"`
	LeaderKeepTap bool   `json:"leaderKeepTap,omitempty"`
}

type bundle struct {
	SpaceHop string     `json:"spacehop"`
	Version  int        `json:"version"`
	Bindings []exported `json:"bindings"`
}

func makeBundle(c *config.Config, list []*config.Binding) []byte {
	out := bundle{SpaceHop: "bindings", Version: 2}
	for _, b := range list {
		l := c.LeaderOf(b)
		e := exported{Binding: *b, LeaderKey: l.Key, LeaderMode: l.Mode, LeaderKeepTap: l.KeepTap}
		e.Leader = ""
		out.Bindings = append(out.Bindings, e)
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	return data
}

// parseImport recognises a bindings bundle or a whole configuration.
func parseImport(data []byte) (*bundle, *config.Config) {
	var b bundle
	if json.Unmarshal(data, &b) == nil && b.SpaceHop == "bindings" && len(b.Bindings) > 0 {
		return &b, nil
	}
	var probe struct {
		Leaders  []json.RawMessage `json:"leaders"`
		Bindings []json.RawMessage `json:"bindings"`
	}
	if json.Unmarshal(data, &probe) == nil && len(probe.Leaders) > 0 && probe.Bindings != nil {
		if c, err := config.Parse(data); err == nil {
			return nil, c
		}
	}
	return nil, nil
}

func sameScope(a, b config.Scope) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y) || (a.Mode != "only" && a.Mode != "except" && b.Mode != "only" && b.Mode != "except")
}

// duplicateOf returns the index of the binding with the same leader, keys
// and scope, or -1.
func duplicateOf(c *config.Config, e *exported) int {
	for i := range c.Bindings {
		x := &c.Bindings[i]
		if c.LeaderOf(x).Key == e.LeaderKey && strings.Join(x.Keys, " ") == strings.Join(e.Keys, " ") && sameScope(x.Scope, e.Scope) {
			return i
		}
	}
	return -1
}

// merge adds a bundle to c. mode is "skip", "replace" or "replaceAll".
func merge(c *config.Config, b *bundle, mode string) (added, replaced, skipped int) {
	if mode == "replaceAll" {
		c.Bindings = nil
	}
	for i := range b.Bindings {
		e := b.Bindings[i]
		if e.LeaderKey == "" {
			e.LeaderKey = c.Leaders[0].Key
		}
		var l *config.Leader
		for j := range c.Leaders {
			if c.Leaders[j].Key == e.LeaderKey {
				l = &c.Leaders[j]
			}
		}
		if l == nil {
			if len(c.Leaders) >= config.MaxLeaders {
				skipped++
				continue
			}
			c.Leaders = append(c.Leaders, config.Leader{ID: config.NewID(), Mode: e.LeaderMode, Key: e.LeaderKey, KeepTap: e.LeaderKeepTap})
			l = &c.Leaders[len(c.Leaders)-1]
		}
		nb := e.Binding
		nb.ID = ""
		nb.Leader = l.ID
		if l == &c.Leaders[0] {
			nb.Leader = ""
		}
		if at := duplicateOf(c, &e); at >= 0 {
			if mode == "replace" {
				c.Bindings[at] = nb
				replaced++
			} else {
				skipped++
			}
			continue
		}
		c.Bindings = append(c.Bindings, nb)
		added++
	}
	return
}

func configJSON(c *config.Config) []byte {
	data, _ := json.MarshalIndent(c, "", "  ")
	return data
}
