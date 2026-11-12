package main

import (
	"log"
	"os"
	"path/filepath"
	"sync"

	"spacehop/internal/config"
	"spacehop/internal/server"
	"spacehop/internal/win"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) >= 1 && args[0] == "--selftest" {
		out := "selftest"
		if len(args) > 1 {
			out = args[1]
		}
		os.Exit(win.SelfTest(out, version))
	}
	if win.AlreadyRunning(args) {
		return // the running copy carried out the command
	}
	if len(args) > 0 && args[0] != "--settings" {
		return // a command for a copy that is not running
	}
	win.SetupLog()
	cfg, loadErr := config.Load()
	if loadErr != nil {
		log.Printf("config: %v", loadErr)
	}
	app := &win.App{Version: version}

	// The settings server starts the first time the page is opened, so an
	// idle program holds no listening socket.
	var once sync.Once
	var srv *server.Server
	app.OpenSettings = func() {
		once.Do(func() {
			s, err := server.Start(app, version)
			if err != nil {
				log.Printf("settings server: %v", err)
				app.Notify("无法打开设置：" + err.Error())
				return
			}
			srv = s
		})
		if srv != nil {
			openPage(srv.URL())
		}
	}
	// First run: show the settings so the user sees what the program does.
	if len(args) > 0 {
		go app.OpenSettings()
	} else if _, err := os.Stat(filepath.Join(config.Dir(), "welcomed")); err != nil {
		_ = os.WriteFile(filepath.Join(config.Dir(), "welcomed"), nil, 0o644)
		go app.OpenSettings()
	}
	if err := app.Main(cfg, loadErr); err != nil {
		log.Printf("fatal: %v", err)
		win.Fatal("SpaceHop 无法启动：" + err.Error())
		os.Exit(1)
	}
}

// openPage shows the settings in a plain app-style window when Edge is
// present (it ships with Windows), otherwise in the default browser.
func openPage(url string) {
	for _, base := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles")} {
		if base == "" {
			continue
		}
		edge := filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`)
		if _, err := os.Stat(edge); err == nil {
			if win.Open(edge, "--app="+url+" --window-size=1180,820") == nil {
				return
			}
		}
	}
	if err := win.Open(url, ""); err != nil {
		log.Printf("open settings: %v", err)
	}
}
