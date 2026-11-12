//go:build !windows

// On other systems the program only serves the settings page against a
// stand-in host, which is how the page is developed and checked off Windows.
package main

import (
	"fmt"
	"log"
	"os"
	"sync/atomic"

	"spacehop/internal/config"
	"spacehop/internal/server"
)

var version = "dev"

type devHost struct {
	cfg       atomic.Pointer[config.Config]
	autostart atomic.Bool
}

func (d *devHost) Config() *config.Config       { return d.cfg.Load() }
func (d *devHost) Apply(c *config.Config) error { d.cfg.Store(c); return nil }
func (d *devHost) Autostart() bool              { return d.autostart.Load() }
func (d *devHost) SetAutostart(on bool) error   { d.autostart.Store(on); return nil }
func (d *devHost) RunAction(a config.Action) error {
	log.Printf("test action: %+v", a)
	return nil
}
func (d *devHost) Layouts() []config.LayoutInfo {
	return []config.LayoutInfo{{ID: "04090409", Name: "英语(美国)"}, {ID: "08040804", Name: "中文(简体，中国) · 微软拼音"}}
}
func (d *devHost) Usage() (int, map[string]int) {
	counts := map[string]int{}
	for i, b := range d.cfg.Load().Bindings {
		counts[b.ID] = (i * 37) % 60
	}
	return 1284, counts
}
func (d *devHost) Activity(on bool) []config.LogEntry {
	if !on {
		return []config.LogEntry{}
	}
	return []config.LogEntry{
		{Time: "10:21:07.120", Text: "tap Space", App: "code.exe"},
		{Time: "10:21:09.482", Text: "run 记事本", App: "code.exe"},
		{Time: "10:21:12.033", Text: "typing Space T", App: "notepad.exe"},
		{Time: "10:21:15.910", Text: "leader Alt+S", App: "chrome.exe"},
		{Time: "10:21:16.402", Text: "cancel Q", App: "chrome.exe"},
	}
}
func (d *devHost) Apps() []config.AppInfo {
	return []config.AppInfo{
		{Name: "chrome", Path: `C:\Program Files\Google\Chrome\Application\chrome.exe`, Exe: "chrome.exe", Source: "running"},
		{Name: "Code", Path: `C:\Users\me\AppData\Local\Programs\Microsoft VS Code\Code.exe`, Exe: "code.exe", Source: "running"},
		{Name: "Google Chrome", Path: `C:\ProgramData\Microsoft\Windows\Start Menu\Programs\Google Chrome.lnk`, Exe: "chrome.exe", Source: "start"},
		{Name: "Visual Studio Code", Path: `C:\Users\me\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Visual Studio Code.lnk`, Exe: "code.exe", Source: "start"},
		{Name: "Word", Path: `C:\ProgramData\Microsoft\Windows\Start Menu\Programs\Word.lnk`, Exe: "winword.exe", Source: "start"},
		{Name: "微信", Path: `C:\ProgramData\Microsoft\Windows\Start Menu\Programs\微信\微信.lnk`, Exe: "wechat.exe", Source: "start"},
		{Name: "计算器", Path: "Microsoft.WindowsCalculator_8wekyb3d8bbwe!App", Source: "store"},
	}
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("config: %v", err)
	}
	h := &devHost{}
	h.cfg.Store(cfg)
	addr := "127.0.0.1:0"
	if p := os.Getenv("PORT"); p != "" {
		addr = "127.0.0.1:" + p
	}
	s, err := server.StartOn(addr, h, version)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(s.URL())
	select {}
}
