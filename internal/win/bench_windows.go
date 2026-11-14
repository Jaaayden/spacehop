package win

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"spacehop/internal/config"
	"spacehop/internal/lnk"
)

// Bench measures how long opening a program takes, from the trigger to its
// window being on screen and in front: started directly, and through a
// SpaceHop shortcut pressed for real. It exists to keep "open app" as fast
// as a click on the program's own icon.
func Bench(out, version string) int {
	_ = os.MkdirAll(out, 0o755)
	dir, _ := os.MkdirTemp("", "spacehop-bench")
	os.Setenv("SPACEHOP_DIR", dir)

	a := &App{Version: version}
	a.upd.off = true
	var link, target string
	for _, ap := range a.Apps() {
		if ap.Exe == "msedge.exe" && ap.Source == "start" {
			link = ap.Path
			target, _ = lnk.Target(ap.Path)
			break
		}
	}
	var lines []string
	logf := func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
		_ = os.WriteFile(filepath.Join(out, "bench.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}
	if link == "" || target == "" {
		logf("Microsoft Edge was not found in the Start menu")
		return 1
	}
	if len(windowsOf("msedge.exe")) > 0 {
		logf("Edge already has windows open; close them first so they are not disturbed")
		return 1
	}
	logf("shortcut: %s\ntarget:   %s", link, target)

	cfg := config.Default()
	cfg.Language = "en"
	cfg.Hint.Show = false
	all := config.Scope{Mode: "all"}
	cfg.Bindings = []config.Binding{
		{ID: "l", Enabled: true, Keys: []string{"L"}, Action: config.Action{Type: config.OpenApp, Path: link, Exe: "msedge.exe"}, Scope: all},
		{ID: "x", Enabled: true, Keys: []string{"X"}, Action: config.Action{Type: config.OpenApp, Path: target, Exe: "msedge.exe"}, Scope: all},
		{ID: "f", Enabled: true, Keys: []string{"F"}, Action: config.Action{Type: config.OpenApp, Path: "explorer.exe", Exe: "explorer.exe"}, Scope: all},
		{ID: "n", Enabled: true, Keys: []string{"N"}, Action: config.Action{Type: config.OpenApp, Path: "notepad.exe", Exe: "notepad.exe"}, Scope: all},
	}
	if err := cfg.Normalize(); err != nil {
		logf("config: %v", err)
		return 1
	}
	t := &selfTest{a: a, out: out}
	theTest = t
	a.beforeHook = func() { t.makeSink() }
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; a.kbHook == 0; i++ {
			if wait(20); i > 500 {
				os.Exit(2)
			}
		}
		wait(800)

		// waitWindow returns when an Edge window exists and when one is in front.
		waitWindow := func(start time.Time) (shown, front time.Duration) {
			shown, front = -1, -1
			for time.Since(start) < 15*time.Second {
				wins := windowsOf("msedge.exe")
				if len(wins) > 0 && shown < 0 {
					shown = time.Since(start)
				}
				fg, _, _ := pGetForegroundWindow.Call()
				for _, w := range wins {
					if w == fg {
						return shown, time.Since(start)
					}
				}
				wait(5)
			}
			return shown, front
		}
		closeEdge := func() {
			for i := 0; i < 100; i++ {
				wins := windowsOf("msedge.exe")
				if len(wins) == 0 {
					break
				}
				for _, w := range wins {
					pPostMessage.Call(w, wmClose, 0, 0)
				}
				wait(100)
			}
			wait(1500) // let it settle, as it would between two uses
		}
		run := func(name string, trigger func() time.Time) {
			for i := 0; i < 3; i++ {
				if !t.focus() {
					logf("%-28s could not take focus", name)
					return
				}
				start := trigger()
				shown, front := waitWindow(start)
				logf("%-28s window after %6d ms, in front after %6d ms", name, shown.Milliseconds(), front.Milliseconds())
				closeEdge()
			}
		}
		direct := func(path string) func() time.Time {
			return func() time.Time {
				start := time.Now()
				err := shellExecute(path, "", "", swShowNormal)
				logf("    ShellExecute returned after %d ms (%v)", time.Since(start).Milliseconds(), err)
				return start
			}
		}
		hotkey := func(key uint32) func() time.Time {
			return func() time.Time {
				press(config.VKSpace, true)
				wait(150)
				start := time.Now()
				press(key, true)
				wait(30)
				press(key, false)
				press(config.VKSpace, false)
				return start
			}
		}
		// A first launch warms the disk cache so the comparison is fair.
		_ = shellExecute(link, "", "", swShowNormal)
		waitWindow(time.Now())
		closeEdge()
		alternate := func(key2 uint32, exe2 string) {
			before := map[uintptr]bool{}
			for _, w := range windowsOf(exe2) {
				before[w] = true
			}
			// Hold Space and go back and forth between two programs, as fast
			// as someone would: every press must bring its program forward.
			for _, k := range []uint32{'L', key2} {
				press(config.VKSpace, true)
				wait(150)
				press(k, true)
				wait(30)
				press(k, false)
				press(config.VKSpace, false)
				wait(2500)
			}
			if len(windowsOf("msedge.exe")) == 0 || len(windowsOf(exe2)) == 0 {
				logf("alternate: the two programs did not both open")
				return
			}
			var times []int64
			fails := 0
			press(config.VKSpace, true)
			wait(200)
			for i := 0; i < 40; i++ {
				key, want := uint32('L'), "msedge.exe"
				if i%2 == 1 {
					key, want = key2, exe2
				}
				start := time.Now()
				press(key, true)
				wait(25)
				press(key, false)
				ok := false
				for time.Since(start) < 700*time.Millisecond {
					if foregroundExe() == want {
						ok = true
						break
					}
					wait(4)
				}
				if ok {
					times = append(times, time.Since(start).Milliseconds())
				} else {
					fails++
				}
				wait(120)
			}
			// Two presses almost on top of each other: the second one decides.
			burstFails := 0
			for i := 0; i < 30; i++ {
				k1, k2, want := uint32('L'), key2, exe2
				if i%2 == 1 {
					k1, k2, want = key2, 'L', "msedge.exe"
				}
				press(k1, true)
				wait(12)
				press(k2, true)
				wait(12)
				press(k1, false)
				press(k2, false)
				wait(450)
				if foregroundExe() != want {
					burstFails++
				}
			}
			press(config.VKSpace, false)
			logf("Edge <-> %-18s %d of 30 double presses ended on the wrong window", exe2+":", burstFails)
			var sum, max int64
			for _, v := range times {
				sum += v
				if v > max {
					max = v
				}
			}
			avg := int64(0)
			if len(times) > 0 {
				avg = sum / int64(len(times))
			}
			logf("Edge <-> %-18s %d of 40 switches failed; the others took %d ms on average, %d ms at most", exe2+":", fails, avg, max)
			closeEdge()
			for _, w := range windowsOf(exe2) { // only the windows this test opened
				if !before[w] {
					pPostMessage.Call(w, wmClose, 0, 0)
				}
			}
			wait(800)
		}
		run("direct, shortcut file", direct(link))
		run("direct, exe", direct(target))
		run("direct, exe, own process", func() time.Time {
			start := time.Now()
			err := startProgram(target, "", filepath.Dir(target))
			logf("    process created after %d ms (%v)", time.Since(start).Milliseconds(), err)
			return start
		})
		run("SpaceHop key, shortcut file", hotkey('L'))
		run("SpaceHop key, exe", hotkey('X'))
		alternate('N', "notepad.exe")
		alternate('F', "explorer.exe")
		_ = exec.Command("taskkill", "/F", "/IM", "notepad.exe").Run()
		a.onUI(a.Quit)
	}()
	if err := a.Main(cfg, nil); err != nil {
		logf("start: %v", err)
	}
	<-done
	return 0
}
