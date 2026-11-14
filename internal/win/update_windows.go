package win

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"spacehop/internal/config"
	"spacehop/internal/i18n"
	"spacehop/internal/ui"
	"spacehop/internal/update"
)

// aboutLink is the "address" of a notice that opens the About page.
const aboutLink = "spacehop:about"

const (
	updateEvery   = 6 * time.Hour  // how often the timer looks
	updateMinGap  = 20 * time.Hour // but it asks the server at most about daily
	updateTimeout = 20 * time.Second
)

// startPage is the settings page (plus one) the next window opens on.
var startPage atomic.Int32

type updater struct {
	mu    sync.Mutex
	info  config.UpdateInfo
	rel   *update.Release
	busy  bool
	timed bool // the first timer has fired
	off   bool // self-test: never touch the network
}

// UpdateInfo is what the settings window shows.
func (a *App) UpdateInfo() config.UpdateInfo {
	a.upd.mu.Lock()
	defer a.upd.mu.Unlock()
	if a.upd.info.State == "" {
		return config.UpdateInfo{State: "idle", Percent: -1}
	}
	return a.upd.info
}

func (a *App) updSet(i config.UpdateInfo) {
	a.upd.mu.Lock()
	a.upd.info = i
	a.upd.mu.Unlock()
	settingsMu.Lock()
	if settingsHwnd > 1 {
		pPostMessage.Call(settingsHwnd, msgSetRefresh, 0, 0)
	}
	settingsMu.Unlock()
}

// openAbout shows the settings window on its About page.
func (a *App) openAbout() {
	startPage.Store(int32(ui.PageAbout) + 1)
	if !a.settingsDo(func(app *ui.App, _ *ui.Ctx) { app.Goto(ui.PageAbout) }) {
		a.ShowSettings()
	}
}

// updateTick runs from a timer: shortly after start, then every few hours.
func (a *App) updateTick() {
	if !a.upd.timed {
		a.upd.timed = true
		pSetTimer.Call(a.hwnd, timerUpdate, uintptr(updateEvery/time.Millisecond), 0)
		if exe, err := os.Executable(); err == nil {
			go update.Cleanup(exe) // the new program has run for a while: the old one can go
		}
	}
	if a.upd.off || a.Config().NoUpdateCheck || !update.Known(a.Version) {
		return
	}
	st := update.LoadState(config.Dir())
	if time.Since(time.Unix(st.LastCheck, 0)) < updateMinGap && !update.Newer(a.Version, st.Latest) {
		return
	}
	a.startCheck(false, false)
}

// CheckUpdate is the "Check now" button.
func (a *App) CheckUpdate() { a.startCheck(true, false) }

// UpdateNow is `--update`: check, and install a newer version if there is one.
func (a *App) UpdateNow() { a.startCheck(true, true) }

func (a *App) client() *update.Client {
	return &update.Client{Arch: runtime.GOARCH, Get: winGet}
}

func (a *App) startCheck(manual, install bool) {
	if a.upd.off {
		return
	}
	a.upd.mu.Lock()
	if a.upd.busy {
		a.upd.mu.Unlock()
		return
	}
	a.upd.busy = true
	before := a.upd.info
	a.upd.mu.Unlock()
	if manual {
		a.updSet(config.UpdateInfo{State: "checking", Percent: -1})
	}
	go func() {
		defer func() {
			a.upd.mu.Lock()
			a.upd.busy = false
			a.upd.mu.Unlock()
			if install && a.UpdateInfo().State == "available" {
				a.InstallUpdate()
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
		defer cancel()
		rel, err := a.client().Latest(ctx)
		if err != nil {
			if manual {
				a.updSet(config.UpdateInfo{State: "error", Err: err.Error(), Page: "https://github.com/" + update.Repo + "/releases/latest", Percent: -1})
			} else {
				a.updSet(before) // a failed look in the background stays quiet
			}
			return
		}
		st := update.LoadState(config.Dir())
		st.LastCheck, st.Latest = time.Now().Unix(), rel.Version
		if !update.Newer(a.Version, rel.Version) {
			update.SaveState(config.Dir(), st)
			a.updSet(config.UpdateInfo{State: "uptodate", Percent: -1})
			return
		}
		a.upd.mu.Lock()
		a.upd.rel = rel
		a.upd.mu.Unlock()
		a.updSet(config.UpdateInfo{State: "available", Latest: rel.Version, Page: rel.Page, Percent: -1})
		if !manual && st.Told != rel.Version {
			st.Told = rel.Version
			a.NotifyURL("SpaceHop", i18n.F("有新版本 %s。点击查看并更新。", rel.Version), aboutLink)
		}
		update.SaveState(config.Dir(), st)
	}()
}

// InstallUpdate downloads, checks and installs the version found, then
// restarts. Nothing is replaced until the download has been verified and has
// shown that it starts.
func (a *App) InstallUpdate() {
	a.upd.mu.Lock()
	rel := a.upd.rel
	if a.upd.busy || rel == nil {
		a.upd.mu.Unlock()
		return
	}
	a.upd.busy = true
	a.upd.mu.Unlock()
	fail := func(msg string) {
		a.updSet(config.UpdateInfo{State: "error", Latest: rel.Version, Err: msg, Page: rel.Page, Percent: -1})
		a.upd.mu.Lock()
		a.upd.busy = false
		a.upd.mu.Unlock()
	}
	a.updSet(config.UpdateInfo{State: "downloading", Latest: rel.Version, Page: rel.Page, Percent: -1})
	go func() {
		exe, err := os.Executable()
		if err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err != nil {
			fail(err.Error())
			return
		}
		fresh := update.NewPath(exe)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err = a.client().Download(ctx, rel, fresh, func(done, total int64) {
			p := -1
			if total > 0 {
				p = int(done * 100 / total)
			}
			a.updSet(config.UpdateInfo{State: "downloading", Latest: rel.Version, Page: rel.Page, Percent: p})
		})
		if err != nil {
			if os.IsPermission(err) {
				fail(i18n.T("无法写入程序所在的文件夹。请手动下载新版本，或把 SpaceHop 放到你有权限的位置。"))
			} else {
				fail(err.Error())
			}
			return
		}
		// The new program must run on this computer before it replaces this one.
		pctx, pcancel := context.WithTimeout(context.Background(), 15*time.Second)
		perr := exec.CommandContext(pctx, fresh, "--probe").Run()
		pcancel()
		if perr != nil {
			_ = os.Remove(fresh)
			fail(i18n.T("下载的新版本在这台电脑上无法运行，已放弃更新。"))
			return
		}
		if _, err := update.Swap(exe, fresh); err != nil {
			_ = os.Remove(fresh)
			if os.IsPermission(err) {
				fail(i18n.T("无法写入程序所在的文件夹。请手动下载新版本，或把 SpaceHop 放到你有权限的位置。"))
			} else {
				fail(err.Error())
			}
			return
		}
		a.onUI(func() {
			cmd := exec.Command(exe, "--after-update", strconv.Itoa(os.Getpid()))
			if err := cmd.Start(); err != nil {
				_, _ = update.Rollback(exe)
				fail(err.Error())
				return
			}
			a.Quit()
		})
	}()
}

// WaitProcess waits until process pid has ended (at most max).
func WaitProcess(pid int, max time.Duration) {
	h, _, _ := pOpenProcess.Call(0x00100000, 0, uintptr(pid)) // SYNCHRONIZE
	if h == 0 {
		return // already gone
	}
	defer pCloseHandle.Call(h)
	pWaitForSingleObject.Call(h, uintptr(max/time.Millisecond))
}

// RollbackSelf is for a freshly updated program that cannot start: it puts
// the previous one back and runs it.
func RollbackSelf() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if exe, err = update.Rollback(exe); err == nil {
		_ = exec.Command(exe).Start()
	}
}

// Probe is `--probe`: it proves that the program starts; with hold it stays
// for a while so the self-test can replace the file under it.
func Probe(hold bool) {
	if hold {
		time.Sleep(20 * time.Second)
	}
}
