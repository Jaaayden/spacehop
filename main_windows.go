package main

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"spacehop/internal/config"
	"spacehop/internal/i18n"
	"spacehop/internal/win"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) >= 1 && (args[0] == "--probe" || args[0] == "--probe-hold") {
		win.Probe(args[0] == "--probe-hold") // an update checks that the new program starts
		return
	}
	updated := false
	if len(args) == 2 && args[0] == "--after-update" {
		// Started by the program being replaced: wait for it to end.
		if pid, err := strconv.Atoi(args[1]); err == nil {
			win.WaitProcess(pid, 15*time.Second)
		}
		updated, args = true, nil
	}
	if len(args) >= 1 && args[0] == "--selftest" {
		out := "selftest"
		if len(args) > 1 {
			out = args[1]
		}
		os.Exit(win.SelfTest(out, version, len(args) > 2 && args[2] == "ui"))
	}
	if len(args) == 2 && args[0] == "--bench" {
		os.Exit(win.Bench(args[1], version))
	}
	if len(args) == 2 && args[0] == "--screenshot" {
		win.Screenshot(args[1])
		return
	}
	if win.AlreadyRunning(args) {
		return // the running copy carried out the command
	}
	if len(args) > 0 && args[0] != "--settings" {
		return // a command for a copy that is not running
	}
	win.SetupLog()
	i18n.Set(win.SystemLanguage()) // the sample shortcuts of a first run are named in it
	cfg, loadErr := config.Load()
	if loadErr != nil {
		log.Printf("config: %v", loadErr)
	}
	win.ApplyLanguage(cfg)
	app := &win.App{Version: version}
	// First run: show the settings so the user sees what the program does.
	welcomed := filepath.Join(config.Dir(), "welcomed")
	if _, err := os.Stat(welcomed); err != nil || len(args) > 0 {
		_ = os.WriteFile(welcomed, nil, 0o644)
		app.ShowOnStart = true
	}
	if err := app.Main(cfg, loadErr); err != nil {
		log.Printf("fatal: %v", err)
		if updated {
			win.RollbackSelf() // the new version cannot start: go back to the old one
			os.Exit(1)
		}
		win.Fatal(i18n.T("SpaceHop 无法启动：") + err.Error())
		os.Exit(1)
	}
}
