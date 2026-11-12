package win

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"spacehop/internal/config"
	"spacehop/internal/lnk"
)

const maxApps = 3000

var (
	storeMu    sync.Mutex
	storeApps  []config.AppInfo
	storeStamp time.Time
)

// Apps lists programs the user can bind: Start-menu shortcuts, Store apps
// and programs with a window open right now. Called only when the settings
// page asks for it.
func (a *App) Apps() []config.AppInfo {
	var out []config.AppInfo
	seen := map[string]bool{}
	add := func(i config.AppInfo) {
		k := strings.ToLower(i.Path)
		if !seen[k] && len(out) < maxApps {
			seen[k] = true
			out = append(out, i)
		}
	}
	// Running programs first: they are the ones the user most likely wants.
	names := map[uint32]bool{}
	for _, w := range appWindows() {
		pid, _ := windowPID(w)
		if names[pid] {
			continue
		}
		names[pid] = true
		p := processPath(pid)
		exe := baseLower(p)
		if p == "" || exe == "applicationframehost.exe" || exe == "spacehop.exe" {
			continue
		}
		add(config.AppInfo{Name: strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)), Path: p, Exe: exe, Source: "running"})
	}
	var start []config.AppInfo
	for _, root := range []string{
		filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs`),
		filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`),
	} {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".lnk") {
				return nil
			}
			name := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
			low := strings.ToLower(name)
			if strings.Contains(low, "uninstall") || strings.Contains(name, "卸载") {
				return nil
			}
			info := config.AppInfo{Name: name, Path: p, Source: "start"}
			if t, err := lnk.Target(p); err == nil && strings.HasSuffix(strings.ToLower(t), ".exe") {
				info.Exe = baseLower(t)
			}
			start = append(start, info)
			return nil
		})
	}
	start = append(start, storeList()...)
	sort.SliceStable(start, func(i, j int) bool { return strings.ToLower(start[i].Name) < strings.ToLower(start[j].Name) })
	for _, i := range start {
		add(i)
	}
	return out
}

// storeList asks PowerShell for Store (packaged) apps, which have no
// shortcut file. The answer is kept for ten minutes.
func storeList() []config.AppInfo {
	storeMu.Lock()
	defer storeMu.Unlock()
	if storeApps != nil && time.Since(storeStamp) < 10*time.Minute {
		return storeApps
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"[Console]::OutputEncoding=[Text.Encoding]::UTF8; Get-StartApps | Where-Object { $_.AppID -like '*!*' } | Select-Object Name,AppID | ConvertTo-Json -Compress")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	raw, err := cmd.Output()
	list := []config.AppInfo{}
	if err == nil {
		var many []struct{ Name, AppID string }
		if json.Unmarshal(raw, &many) != nil {
			var one struct{ Name, AppID string }
			if json.Unmarshal(raw, &one) == nil && one.AppID != "" {
				many = append(many, one)
			}
		}
		for _, m := range many {
			if m.Name != "" && isAppID(m.AppID) {
				list = append(list, config.AppInfo{Name: m.Name, Path: m.AppID, Source: "store"})
			}
		}
	}
	storeApps, storeStamp = list, time.Now()
	return list
}

// ---- start at login (per-user Run key) ----

const (
	hkcu    = 0x80000001
	runKey  = `Software\Microsoft\Windows\CurrentVersion\Run`
	runName = "SpaceHop"
)

func openRunKey() (uintptr, error) {
	var h uintptr
	// KEY_QUERY_VALUE | KEY_SET_VALUE
	r, _, _ := pRegCreateKeyEx.Call(hkcu, uintptr(unsafe.Pointer(u16(runKey))), 0, 0, 0, 0x1|0x2, 0, uintptr(unsafe.Pointer(&h)), 0)
	if r != 0 {
		return 0, syscall.Errno(r)
	}
	return h, nil
}

func (a *App) Autostart() bool {
	h, err := openRunKey()
	if err != nil {
		return false
	}
	defer pRegCloseKey.Call(h)
	r, _, _ := pRegQueryValueEx.Call(h, uintptr(unsafe.Pointer(u16(runName))), 0, 0, 0, 0)
	return r == 0
}

func (a *App) SetAutostart(on bool) error {
	h, err := openRunKey()
	if err != nil {
		return err
	}
	defer pRegCloseKey.Call(h)
	if !on {
		r, _, _ := pRegDeleteValue.Call(h, uintptr(unsafe.Pointer(u16(runName))))
		if r != 0 && r != 2 {
			return syscall.Errno(r)
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	val := syscall.StringToUTF16(`"` + exe + `"`)
	r, _, _ := pRegSetValueEx.Call(h, uintptr(unsafe.Pointer(u16(runName))), 0, 1, // REG_SZ
		uintptr(unsafe.Pointer(&val[0])), uintptr(len(val)*2))
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}
