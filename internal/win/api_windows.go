// Package win is the Windows layer: keyboard hook, hint window, tray icon
// and the actions themselves. It talks to Win32 directly so the program is
// one small executable with no runtime to install.
package win

import (
	"strings"
	"syscall"
	"unsafe"

	"spacehop/internal/i18n"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")
	shcore   = syscall.NewLazyDLL("shcore.dll")
	powrprof = syscall.NewLazyDLL("powrprof.dll")

	pSetWindowsHookEx    = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx      = user32.NewProc("CallNextHookEx")
	pGetMessage          = user32.NewProc("GetMessageW")
	pTranslateMessage    = user32.NewProc("TranslateMessage")
	pDispatchMessage     = user32.NewProc("DispatchMessageW")
	pPostMessage         = user32.NewProc("PostMessageW")
	pPostQuitMessage     = user32.NewProc("PostQuitMessage")
	pRegisterClassEx     = user32.NewProc("RegisterClassExW")
	pCreateWindowEx      = user32.NewProc("CreateWindowExW")
	pDefWindowProc       = user32.NewProc("DefWindowProcW")
	pDestroyWindow       = user32.NewProc("DestroyWindow")
	pShowWindow          = user32.NewProc("ShowWindow")
	pSetWindowPos        = user32.NewProc("SetWindowPos")
	pInvalidateRect      = user32.NewProc("InvalidateRect")
	pBeginPaint          = user32.NewProc("BeginPaint")
	pEndPaint            = user32.NewProc("EndPaint")
	pFillRect            = user32.NewProc("FillRect")
	pDrawText            = user32.NewProc("DrawTextW")
	pSetTimer            = user32.NewProc("SetTimer")
	pKillTimer           = user32.NewProc("KillTimer")
	pSendInput           = user32.NewProc("SendInput")
	pMapVirtualKey       = user32.NewProc("MapVirtualKeyW")
	pGetAsyncKeyState    = user32.NewProc("GetAsyncKeyState")
	pGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pBringWindowToTop    = user32.NewProc("BringWindowToTop")
	pAttachThreadInput   = user32.NewProc("AttachThreadInput")
	pGetWindowThreadPID  = user32.NewProc("GetWindowThreadProcessId")
	pEnumWindows         = user32.NewProc("EnumWindows")
	pIsWindowVisible     = user32.NewProc("IsWindowVisible")
	pIsIconic            = user32.NewProc("IsIconic")
	pGetWindow           = user32.NewProc("GetWindow")
	pGetWindowLongPtr    = user32.NewProc("GetWindowLongPtrW")
	pGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	pMonitorFromWindow   = user32.NewProc("MonitorFromWindow")
	pGetMonitorInfo      = user32.NewProc("GetMonitorInfoW")
	pSetLayeredAttrs     = user32.NewProc("SetLayeredWindowAttributes")
	pSetWindowRgn        = user32.NewProc("SetWindowRgn")
	pCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	pAppendMenu          = user32.NewProc("AppendMenuW")
	pTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	pDestroyMenu         = user32.NewProc("DestroyMenu")
	pGetCursorPos        = user32.NewProc("GetCursorPos")
	pCreateIconIndirect  = user32.NewProc("CreateIconIndirect")
	pDestroyIcon         = user32.NewProc("DestroyIcon")
	pRegisterWindowMsg   = user32.NewProc("RegisterWindowMessageW")
	pFindWindow          = user32.NewProc("FindWindowW")
	pLockWorkStation     = user32.NewProc("LockWorkStation")
	pSetDpiAwarenessCtx  = user32.NewProc("SetProcessDpiAwarenessContext")
	pMessageBox          = user32.NewProc("MessageBoxW")

	pUpdateLayered      = user32.NewProc("UpdateLayeredWindow")
	pGetSystemMetrics   = user32.NewProc("GetSystemMetrics")
	pSendMessageTimeout = user32.NewProc("SendMessageTimeoutW")
	pGetMenu            = user32.NewProc("GetMenu")
	pGetSubMenu         = user32.NewProc("GetSubMenu")
	pGetMenuItemCount   = user32.NewProc("GetMenuItemCount")
	pGetMenuString      = user32.NewProc("GetMenuStringW")
	pGetMenuItemID      = user32.NewProc("GetMenuItemID")
	pGetMenuState       = user32.NewProc("GetMenuState")
	pGetWindowRect      = user32.NewProc("GetWindowRect")
	pIsZoomed           = user32.NewProc("IsZoomed")
	pEnumMonitors       = user32.NewProc("EnumDisplayMonitors")
	pMonitorFromPoint   = user32.NewProc("MonitorFromPoint")
	pGetKbdLayoutList   = user32.NewProc("GetKeyboardLayoutList")
	pOpenClipboard      = user32.NewProc("OpenClipboard")
	pCloseClipboard     = user32.NewProc("CloseClipboard")
	pEmptyClipboard     = user32.NewProc("EmptyClipboard")
	pGetClipboardData   = user32.NewProc("GetClipboardData")
	pSetClipboardData   = user32.NewProc("SetClipboardData")
	pGetClassName       = user32.NewProc("GetClassNameW")
	pGetWindowText      = user32.NewProc("GetWindowTextW")
	pFindWindowEx       = user32.NewProc("FindWindowExW")
	pGetDC              = user32.NewProc("GetDC")
	pReleaseDC          = user32.NewProc("ReleaseDC")

	pGlobalAlloc       = kernel32.NewProc("GlobalAlloc")
	pGlobalLock        = kernel32.NewProc("GlobalLock")
	pGlobalUnlock      = kernel32.NewProc("GlobalUnlock")
	pGetLocaleInfo     = kernel32.NewProc("GetLocaleInfoW")
	pFindFirstChange   = kernel32.NewProc("FindFirstChangeNotificationW")
	pFindNextChange    = kernel32.NewProc("FindNextChangeNotification")
	pWaitForSingleObj  = kernel32.NewProc("WaitForSingleObject")
	pImmGetDescription = syscall.NewLazyDLL("imm32.dll").NewProc("ImmGetDescriptionW")
	pRegGetValue       = advapi32.NewProc("RegGetValueW")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pGetTextExtent      = gdi32.NewProc("GetTextExtentPoint32W")
	pGdiFlush           = gdi32.NewProc("GdiFlush")
	pBitBlt             = gdi32.NewProc("BitBlt")

	pGetModuleHandle    = kernel32.NewProc("GetModuleHandleW")
	pGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
	pOpenProcess        = kernel32.NewProc("OpenProcess")
	pCloseHandle        = kernel32.NewProc("CloseHandle")
	pQueryImageName     = kernel32.NewProc("QueryFullProcessImageNameW")
	pCreateMutex        = kernel32.NewProc("CreateMutexW")

	pShellExecute       = shell32.NewProc("ShellExecuteW")
	pShellNotifyIcon    = shell32.NewProc("Shell_NotifyIconW")
	pSHQueryNotifyState = shell32.NewProc("SHQueryUserNotificationState")

	pCreateSolidBrush   = gdi32.NewProc("CreateSolidBrush")
	pCreateFont         = gdi32.NewProc("CreateFontW")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pSetTextColor       = gdi32.NewProc("SetTextColor")
	pSetBkMode          = gdi32.NewProc("SetBkMode")
	pRoundRect          = gdi32.NewProc("RoundRect")
	pGetStockObject     = gdi32.NewProc("GetStockObject")
	pCreateRoundRectRgn = gdi32.NewProc("CreateRoundRectRgn")
	pCreateBitmap       = gdi32.NewProc("CreateBitmap")

	pRegCreateKeyEx  = advapi32.NewProc("RegCreateKeyExW")
	pRegSetValueEx   = advapi32.NewProc("RegSetValueExW")
	pRegQueryValueEx = advapi32.NewProc("RegQueryValueExW")
	pRegDeleteValue  = advapi32.NewProc("RegDeleteValueW")
	pRegCloseKey     = advapi32.NewProc("RegCloseKey")

	pDwmSetWindowAttr = dwmapi.NewProc("DwmSetWindowAttribute")
	pDwmGetWindowAttr = dwmapi.NewProc("DwmGetWindowAttribute")
	pGetDpiForMonitor = shcore.NewProc("GetDpiForMonitor")
	pSetSuspendState  = powrprof.NewProc("SetSuspendState")
)

const (
	whKeyboardLL = 13
	whMouseLL    = 14

	wmDestroy        = 0x0002
	wmPaint          = 0x000F
	wmNCHitTest      = 0x0084
	wmTimer          = 0x0113
	wmKeyDown        = 0x0100
	wmSysKeyDown     = 0x0104
	wmLButtonDown    = 0x0201
	wmLButtonUp      = 0x0202
	wmRButtonDown    = 0x0204
	wmRButtonUp      = 0x0205
	wmMButtonDown    = 0x0207
	wmPowerBroadcast = 0x0218
	wmApp            = 0x8000

	llkhfInjected = 0x10
	llmhfInjected = 0x01

	inputMouse    = 0
	inputKeyboard = 1
	keyExtended   = 0x1
	keyUp         = 0x2
	keyUnicode    = 0x4

	wmClose    = 0x0010
	wmCopyData = 0x004A
	wmCommand  = 0x0111
	wmSysCmd   = 0x0112

	swHide       = 0
	swShowNormal = 1
	swMinimize   = 6
	swRestore    = 9

	// Marks events we synthesise so the hook lets them through.
	injectMagic = 0x53504850 // "SPHP"
)

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type kbdllHook struct {
	VK, Scan, Flags, Time uint32
	Extra                 uintptr
}

type msllHook struct {
	Pt                     point
	MouseData, Flags, Time uint32
	Extra                  uintptr
}

// keybdInput and mouseInput are the two layouts of the 40-byte INPUT
// structure on 64-bit Windows.
type keybdInput struct {
	Type  uint32
	_     uint32
	VK    uint16
	Scan  uint16
	Flags uint32
	Time  uint32
	_     uint32
	Extra uintptr
	_     [8]byte
}

type mouseInput struct {
	Type      uint32
	_         uint32
	Dx, Dy    int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	_         uint32
	Extra     uintptr
}

func u16(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(strings.ReplaceAll(s, "\x00", ""))
	if err != nil {
		p, _ = syscall.UTF16PtrFromString("")
	}
	return p
}

// w16 converts a string for Windows; unlike the syscall helpers it accepts
// any input (NUL characters are dropped).
func w16(s string) []uint16 {
	u, _ := syscall.UTF16FromString(strings.ReplaceAll(s, "\x00", ""))
	return u
}

// ptr converts an address handed to us by Windows into a pointer.
func ptr(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

func loword(v uintptr) uint32 { return uint32(v) & 0xFFFF }

// processName returns the full image path of a process, or "".
func processPath(pid uint32) string {
	h, _, _ := pOpenProcess.Call(0x1000, 0, uintptr(pid)) // PROCESS_QUERY_LIMITED_INFORMATION
	if h == 0 {
		return ""
	}
	defer pCloseHandle.Call(h)
	var buf [520]uint16
	n := uint32(len(buf))
	r, _, _ := pQueryImageName.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

func baseLower(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		p = p[i+1:]
	}
	return strings.ToLower(p)
}

func windowPID(hwnd uintptr) (pid uint32, tid uint32) {
	t, _, _ := pGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid, uint32(t)
}

func shellExecute(file, args, dir string, show int) error {
	var a, d *uint16
	if args != "" {
		a = u16(args)
	}
	if dir != "" {
		d = u16(dir)
	}
	r, _, _ := pShellExecute.Call(0, uintptr(unsafe.Pointer(u16("open"))), uintptr(unsafe.Pointer(u16(file))),
		uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(d)), uintptr(show))
	if r <= 32 {
		return shellError(r, file)
	}
	return nil
}

type actionError string

func (e actionError) Error() string { return string(e) }

func shellError(code uintptr, file string) error {
	switch code {
	case 2, 3:
		return actionError(i18n.T("找不到：") + file)
	case 5:
		return actionError(i18n.T("没有权限打开：") + file)
	case 31:
		return actionError(i18n.T("没有程序可以打开：") + file)
	}
	return actionError(i18n.T("无法打开：") + file)
}

// Open starts a program, document or URL the way Explorer would.
func Open(file, args string) error { return shellExecute(file, args, "", swShowNormal) }

// packPoint passes a POINT by value, as the 64-bit calling conventions do.
func packPoint(p point) uintptr { return uintptr(uint32(p.X)) | uintptr(uint32(p.Y))<<32 }

type monitorInfo struct {
	Size          uint32
	Monitor, Work rect
	Flags         uint32
}

func monitorOf(mon uintptr) (mi monitorInfo, dpi uint32) {
	mi.Size = uint32(unsafe.Sizeof(mi))
	pGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi)))
	var dx, dy uint32 = 96, 96
	if pGetDpiForMonitor.Find() == nil {
		pGetDpiForMonitor.Call(mon, 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy)))
	}
	if dx < 48 {
		dx = 96
	}
	return mi, dx
}

func className(hwnd uintptr) string {
	var buf [128]uint16
	n, _, _ := pGetClassName.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

// regDword reads a DWORD under HKEY_CURRENT_USER.
func regDword(key, name string) (uint32, bool) {
	var v, size uint32 = 0, 4
	r, _, _ := pRegGetValue.Call(hkcu, uintptr(unsafe.Pointer(u16(key))), uintptr(unsafe.Pointer(u16(name))),
		0x10 /* RRF_RT_REG_DWORD */, 0, uintptr(unsafe.Pointer(&v)), uintptr(unsafe.Pointer(&size)))
	return v, r == 0
}
