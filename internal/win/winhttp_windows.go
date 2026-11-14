package win

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"spacehop/internal/update"
)

// A small GET client on the system's WinHTTP. Using it instead of Go's
// net/http keeps about 5 MB out of the executable, and the system's
// certificate store and proxy settings apply as they do for other programs.

var (
	winhttp = syscall.NewLazyDLL("winhttp.dll")

	pWinHttpOpen          = winhttp.NewProc("WinHttpOpen")
	pWinHttpConnect       = winhttp.NewProc("WinHttpConnect")
	pWinHttpOpenRequest   = winhttp.NewProc("WinHttpOpenRequest")
	pWinHttpSetTimeouts   = winhttp.NewProc("WinHttpSetTimeouts")
	pWinHttpSetOption     = winhttp.NewProc("WinHttpSetOption")
	pWinHttpAddHeaders    = winhttp.NewProc("WinHttpAddRequestHeaders")
	pWinHttpSendRequest   = winhttp.NewProc("WinHttpSendRequest")
	pWinHttpReceiveResp   = winhttp.NewProc("WinHttpReceiveResponse")
	pWinHttpQueryHeaders  = winhttp.NewProc("WinHttpQueryHeaders")
	pWinHttpReadData      = winhttp.NewProc("WinHttpReadData")
	pWinHttpCloseHandle   = winhttp.NewProc("WinHttpCloseHandle")
	pWinHttpIEProxy       = winhttp.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
	pWinHttpProxyForURL   = winhttp.NewProc("WinHttpGetProxyForUrl")
	kernelGlobalFree      = kernel32.NewProc("GlobalFree")
	winhttpSession        uintptr
	winhttpSessionOnce    sync.Once
	errWinHTTPUnavailable = errors.New("WinHTTP is not available")
)

const (
	whNoProxy         = 1
	whNamedProxy      = 3
	whFlagSecure      = 0x00800000
	whOptProxy        = 38
	whOptDisableFeat  = 63
	whDisableRedirect = 0x00000080
	whQueryStatus     = 19
	whQueryLength     = 5
	whQueryLocation   = 33
	whQueryNumber     = 0x20000000
	whAddReplace      = 0x20000000
	whAutoDetect      = 0x1
	whAutoConfigURL   = 0x2
	whAutoDetectAll   = 0x3 // DHCP | DNS
)

type whIEProxy struct {
	AutoDetect    int32
	AutoConfigURL *uint16
	Proxy         *uint16
	Bypass        *uint16
}

type whAutoOpts struct {
	Flags         uint32
	DetectFlags   uint32
	ConfigURL     *uint16
	Reserved      uintptr
	Reserved2     uint32
	AutoLogonPass int32
}

type whProxyInfo struct {
	AccessType uint32
	Proxy      *uint16
	Bypass     *uint16
}

func whSession() uintptr {
	winhttpSessionOnce.Do(func() {
		if winhttp.Load() != nil {
			return
		}
		h, _, _ := pWinHttpOpen.Call(uintptr(unsafe.Pointer(u16("SpaceHop-updater"))), whNoProxy, 0, 0, 0)
		if h != 0 {
			pWinHttpSetTimeouts.Call(h, 10000, 10000, 30000, 30000)
		}
		winhttpSession = h
	})
	return winhttpSession
}

func ptrString(p *uint16) string {
	if p == nil {
		return ""
	}
	return syscall.UTF16ToString((*[1 << 16]uint16)(unsafe.Pointer(p))[:])
}

// setProxy chooses a proxy for rawURL the way the system is configured:
// HTTPS_PROXY in the environment, else the user's Internet Settings (a fixed
// proxy, an automatic configuration script or auto-detection), else direct.
func setProxy(session, req uintptr, rawURL string) {
	info := whProxyInfo{AccessType: whNamedProxy}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		if v := os.Getenv(k); v != "" {
			if u, err := url.Parse(v); err == nil && u.Host != "" {
				info.Proxy = u16(u.Host)
				pWinHttpSetOption.Call(req, whOptProxy, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
				return
			}
		}
	}
	var ie whIEProxy
	if r, _, _ := pWinHttpIEProxy.Call(uintptr(unsafe.Pointer(&ie))); r == 0 {
		return
	}
	defer func() {
		for _, p := range []*uint16{ie.AutoConfigURL, ie.Proxy, ie.Bypass} {
			if p != nil {
				kernelGlobalFree.Call(uintptr(unsafe.Pointer(p)))
			}
		}
	}()
	if ie.Proxy != nil && ie.AutoDetect == 0 && ie.AutoConfigURL == nil {
		info.Proxy, info.Bypass = ie.Proxy, ie.Bypass
		pWinHttpSetOption.Call(req, whOptProxy, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
		return
	}
	if ie.AutoDetect != 0 || ie.AutoConfigURL != nil {
		opts := whAutoOpts{AutoLogonPass: 1}
		if ie.AutoConfigURL != nil {
			opts.Flags, opts.ConfigURL = whAutoConfigURL, ie.AutoConfigURL
		} else {
			opts.Flags, opts.DetectFlags = whAutoDetect, whAutoDetectAll
		}
		var found whProxyInfo
		if r, _, _ := pWinHttpProxyForURL.Call(session, uintptr(unsafe.Pointer(u16(rawURL))), uintptr(unsafe.Pointer(&opts)), uintptr(unsafe.Pointer(&found))); r != 0 {
			if found.AccessType == whNamedProxy {
				pWinHttpSetOption.Call(req, whOptProxy, uintptr(unsafe.Pointer(&found)), unsafe.Sizeof(found))
			}
			for _, p := range []*uint16{found.Proxy, found.Bypass} {
				if p != nil {
					kernelGlobalFree.Call(uintptr(unsafe.Pointer(p)))
				}
			}
			return
		}
	}
	if ie.Proxy != nil { // auto-detection failed: fall back to the fixed proxy, if any
		info.Proxy, info.Bypass = ie.Proxy, ie.Bypass
		pWinHttpSetOption.Call(req, whOptProxy, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	}
}

type whBody struct {
	req, conn uintptr
	once      sync.Once
	stop      func() bool
}

func (b *whBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	var n uint32
	r, _, e := pWinHttpReadData.Call(b.req, uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return 0, e
	}
	if n == 0 {
		return 0, io.EOF
	}
	return int(n), nil
}

func (b *whBody) Close() error {
	b.once.Do(func() {
		if b.stop != nil {
			b.stop()
		}
		pWinHttpCloseHandle.Call(b.req)
		pWinHttpCloseHandle.Call(b.conn)
	})
	return nil
}

// winGet is an update.Getter: one request, redirects reported not followed.
func winGet(ctx context.Context, rawURL, accept string) (*update.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" {
		return nil, errors.New("only https addresses are allowed")
	}
	session := whSession()
	if session == 0 {
		return nil, errWinHTTPUnavailable
	}
	port := uintptr(443)
	if p := u.Port(); p != "" {
		n, _ := strconv.Atoi(p)
		port = uintptr(n)
	}
	conn, _, e := pWinHttpConnect.Call(session, uintptr(unsafe.Pointer(u16(u.Hostname()))), port, 0)
	if conn == 0 {
		return nil, e
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	req, _, e := pWinHttpOpenRequest.Call(conn, uintptr(unsafe.Pointer(u16("GET"))), uintptr(unsafe.Pointer(u16(path))), 0, 0, 0, whFlagSecure)
	if req == 0 {
		pWinHttpCloseHandle.Call(conn)
		return nil, e
	}
	body := &whBody{req: req, conn: conn}
	body.stop = context.AfterFunc(ctx, func() { body.Close() }) // cancelling aborts a blocked call
	fail := func(err error) (*update.Response, error) {
		body.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	feature := uint32(whDisableRedirect)
	pWinHttpSetOption.Call(req, whOptDisableFeat, uintptr(unsafe.Pointer(&feature)), 4)
	setProxy(session, req, rawURL)
	if accept != "" {
		h := u16("Accept: " + accept)
		pWinHttpAddHeaders.Call(req, uintptr(unsafe.Pointer(h)), ^uintptr(0), whAddReplace)
	}
	if r, _, e := pWinHttpSendRequest.Call(req, 0, 0, 0, 0, 0, 0); r == 0 {
		return fail(e)
	}
	if r, _, e := pWinHttpReceiveResp.Call(req, 0); r == 0 {
		return fail(e)
	}
	var status uint32
	size := uint32(4)
	pWinHttpQueryHeaders.Call(req, whQueryStatus|whQueryNumber, 0, uintptr(unsafe.Pointer(&status)), uintptr(unsafe.Pointer(&size)), 0)
	resp := &update.Response{Status: int(status), Length: -1, Body: body}
	var length uint32
	size = 4
	if r, _, _ := pWinHttpQueryHeaders.Call(req, whQueryLength|whQueryNumber, 0, uintptr(unsafe.Pointer(&length)), uintptr(unsafe.Pointer(&size)), 0); r != 0 {
		resp.Length = int64(length)
	}
	if status >= 300 && status < 400 {
		buf := make([]uint16, 2048)
		size = uint32(len(buf) * 2)
		if r, _, _ := pWinHttpQueryHeaders.Call(req, whQueryLocation, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0); r != 0 {
			resp.Location = syscall.UTF16ToString(buf)
		}
	}
	return resp, nil
}
