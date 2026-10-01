// deckcap-win: the Windows capture helper. Protocol: docs/helpers.md.
//
// Lists top-level windows, grabs one with PrintWindow(PW_RENDERFULLCONTENT), which asks DWM for the
// window's own composed picture and so works while it is covered, adds that app's sound through
// WASAPI process loopback (audio_windows.go) and injects mouse input with SendInput. stdout carries
// only framed messages; logs go to stderr.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/asmsaifs/techo5-streamdeck/internal/helperkit"
)

// gwlExStyle is GWL_EXSTYLE (-20); a variable so the conversion to uintptr sign-extends.
var gwlExStyle = int32(-20)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pEnumWindows       = user32.NewProc("EnumWindows")
	pIsWindowVisible   = user32.NewProc("IsWindowVisible")
	pIsWindow          = user32.NewProc("IsWindow")
	pIsIconic          = user32.NewProc("IsIconic")
	pGetWindow         = user32.NewProc("GetWindow")
	pGetWindowLongW    = user32.NewProc("GetWindowLongW")
	pGetWindowTextW    = user32.NewProc("GetWindowTextW")
	pGetWindowRect     = user32.NewProc("GetWindowRect")
	pGetWindowThreadID = user32.NewProc("GetWindowThreadProcessId")
	pSetForeground     = user32.NewProc("SetForegroundWindow")
	pShowWindow        = user32.NewProc("ShowWindow")
	pSetCursorPos      = user32.NewProc("SetCursorPos")
	pSendInput         = user32.NewProc("SendInput")
	pWindowFromPoint   = user32.NewProc("WindowFromPoint")
	pGetAncestor       = user32.NewProc("GetAncestor")
	pKeybdEvent        = user32.NewProc("keybd_event")
	pPrintWindow       = user32.NewProc("PrintWindow")
	pGetDC             = user32.NewProc("GetDC")
	pReleaseDC         = user32.NewProc("ReleaseDC")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")

	pDwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")
)

const (
	pwRenderFullContent = 2
	dwmwaCloaked        = 14
	gwOwner             = 4
	gaRoot              = 2
	wsExToolWindow      = 0x80
	swRestore           = 9
	vkMenu              = 0x12

	inputMouse          = 0
	mouseeventfLeftDown = 0x0002
	mouseeventfLeftUp   = 0x0004
	mouseeventfWheel    = 0x0800
	keyeventfKeyUp      = 0x0002
	maxDimension        = 16384
)

type rect struct{ left, top, right, bottom int32 }

type bitmapInfoHeader struct {
	size          uint32
	width, height int32
	planes, bits  uint16
	compression   uint32
	sizeImage     uint32
	xppm, yppm    int32
	clrUsed, clrI uint32
}

type mouseInput struct {
	dx, dy    int32
	mouseData uint32
	flags     uint32
	time      uint32
	extra     uintptr
}

// input is INPUT with the MOUSEINPUT arm of its union; Go aligns the union as C does.
type input struct {
	typ uint32
	mi  mouseInput
}

func hwndOf(id string) (uintptr, bool) {
	n, err := strconv.ParseUint(id, 10, 64)
	return uintptr(n), err == nil && n != 0
}

func windowRect(h uintptr) (rect, bool) {
	var r rect
	ok, _, _ := pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
	return r, ok != 0
}

func windowTitle(h uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := pGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

func windowPID(h uintptr) uint32 {
	var pid uint32
	pGetWindowThreadID.Call(h, uintptr(unsafe.Pointer(&pid)))
	return pid
}

func exeName(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH*2)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(windows.UTF16ToString(buf[:n])), ".exe")
}

// processElevated says whether pid runs elevated while we do not. Windows drops synthetic input
// aimed at a higher-integrity window (UIPI) without telling the sender, so this is the only way to
// explain why touches do nothing. A process we cannot even open a token for is taken as elevated.
func processElevated(pid uint32) bool {
	if windows.GetCurrentProcessToken().IsElevated() {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer windows.CloseHandle(h)
	var t windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &t); err != nil {
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer t.Close()
	return t.IsElevated()
}

// enumCallback is created once: the runtime only has a limited number of callback slots.
var (
	enumMu      sync.Mutex
	enumHandles []uintptr
	enumCB      = windows.NewCallback(func(h, _ uintptr) uintptr {
		enumHandles = append(enumHandles, h)
		return 1
	})
)

type backend struct {
	mu      sync.Mutex
	hwnd    uintptr
	pid     uint32
	fw, fh  int // size of the last frame sent
	dragged bool
	warned  bool
	stop    chan struct{}
	done    chan struct{}
	audio   *audioCapture
	out     *helperkit.Out
}

func (b *backend) List() ([]helperkit.Window, error) {
	enumMu.Lock()
	enumHandles = enumHandles[:0]
	pEnumWindows.Call(enumCB, 0)
	handles := append([]uintptr(nil), enumHandles...)
	enumMu.Unlock()

	self := uint32(os.Getpid())
	var list []helperkit.Window
	for _, h := range handles {
		if vis, _, _ := pIsWindowVisible.Call(h); vis == 0 {
			continue
		}
		if ic, _, _ := pIsIconic.Call(h); ic != 0 {
			continue
		}
		if owner, _, _ := pGetWindow.Call(h, gwOwner); owner != 0 {
			continue
		}
		ex, _, _ := pGetWindowLongW.Call(h, uintptr(gwlExStyle))
		if ex&wsExToolWindow != 0 {
			continue
		}
		var cloaked uint32 // windows on other virtual desktops and suspended store apps
		pDwmGetWindowAttribute.Call(h, dwmwaCloaked, uintptr(unsafe.Pointer(&cloaked)), 4)
		if cloaked != 0 {
			continue
		}
		title := windowTitle(h)
		r, ok := windowRect(h)
		w, ht := int(r.right-r.left), int(r.bottom-r.top)
		if !ok || title == "" || title == "Program Manager" || w <= 100 || ht <= 80 {
			continue
		}
		pid := windowPID(h)
		if pid == self {
			continue
		}
		list = append(list, helperkit.Window{ID: strconv.FormatUint(uint64(h), 10), Title: title, App: exeName(pid), W: w, H: ht})
	}
	return list, nil
}

func (b *backend) Start(out *helperkit.Out, a helperkit.StartArgs) error {
	h, ok := hwndOf(a.Window)
	if ok {
		isWin, _, _ := pIsWindow.Call(h)
		ok = isWin != 0
	}
	if !ok {
		return &helperkit.Error{Code: "no_window", Msg: "window " + a.Window + " not found"}
	}
	b.mu.Lock()
	b.hwnd, b.pid, b.fw, b.fh, b.dragged, b.warned = h, windowPID(h), 0, 0, false, false
	b.stop, b.done, b.out = make(chan struct{}), make(chan struct{}), out
	stop, done := b.stop, b.done
	b.mu.Unlock()
	go b.captureLoop(out, h, a, stop, done)
	if a.Audio {
		ac, err := startAudio(out, b.pid)
		if err != nil {
			// The picture still works; say why there is no sound.
			out.Event("error", "unsupported", "no sound: "+err.Error())
		} else {
			b.mu.Lock()
			b.audio = ac
			b.mu.Unlock()
		}
	}
	return nil
}

func (b *backend) Stop() {
	b.mu.Lock()
	stop, done, ac := b.stop, b.done, b.audio
	b.stop, b.done, b.audio = nil, nil, nil
	b.dragged = false
	b.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
	if ac != nil {
		ac.Stop()
	}
}

// grabber owns the memory device context and DIB section a window is drawn into; they are
// recreated when the window changes size.
type grabber struct {
	dc, bmp, old uintptr
	bits         unsafe.Pointer
	w, h         int
}

func (g *grabber) free() {
	if g.dc != 0 {
		pSelectObject.Call(g.dc, g.old)
		pDeleteObject.Call(g.bmp)
		pDeleteDC.Call(g.dc)
	}
	*g = grabber{}
}

func (g *grabber) resize(w, h int) bool {
	g.free()
	if w <= 0 || h <= 0 || w > maxDimension || h > maxDimension {
		return false
	}
	screen, _, _ := pGetDC.Call(0)
	defer pReleaseDC.Call(0, screen)
	dc, _, _ := pCreateCompatibleDC.Call(screen)
	if dc == 0 {
		return false
	}
	bi := bitmapInfoHeader{size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), width: int32(w), height: -int32(h), planes: 1, bits: 32}
	var bits unsafe.Pointer
	bmp, _, _ := pCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		pDeleteDC.Call(dc)
		return false
	}
	old, _, _ := pSelectObject.Call(dc, bmp)
	*g = grabber{dc: dc, bmp: bmp, old: old, bits: bits, w: w, h: h}
	return true
}

func (b *backend) captureLoop(out *helperkit.Out, h uintptr, a helperkit.StartArgs, stop, done chan struct{}) {
	defer close(done)
	var g grabber
	defer g.free()
	tick := time.NewTicker(time.Second / time.Duration(a.FPS))
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		if isWin, _, _ := pIsWindow.Call(h); isWin == 0 {
			out.Event("error", "no_window", "the window was closed")
			return
		}
		if ic, _, _ := pIsIconic.Call(h); ic != 0 {
			continue // a minimized window draws nothing; keep the last picture
		}
		r, ok := windowRect(h)
		w, ht := int(r.right-r.left), int(r.bottom-r.top)
		if !ok || w <= 0 || ht <= 0 {
			continue
		}
		if g.w != w || g.h != ht {
			if !g.resize(w, ht) {
				continue
			}
		}
		if ok, _, _ := pPrintWindow.Call(h, g.dc, pwRenderFullContent); ok == 0 {
			continue
		}
		src := unsafe.Slice((*byte)(g.bits), w*ht*4)
		pix, fw, fh := helperkit.Scale(src, w, ht, w*4, a.MaxW)
		if pix == nil {
			continue
		}
		b.mu.Lock()
		b.fw, b.fh = fw, fh
		b.mu.Unlock()
		out.Frame(fw, fh, pix)
	}
}

func (b *backend) Input(kind string, x, y, dy int) {
	b.mu.Lock()
	h, pid, fw, fh, out := b.hwnd, b.pid, b.fw, b.fh, b.out
	warned := b.warned
	b.mu.Unlock()
	if h == 0 || fw == 0 || out == nil {
		return
	}
	if processElevated(pid) {
		if !warned {
			out.Event("error", "permission", "the window belongs to an elevated app; run the deck app as administrator to control it")
			b.mu.Lock()
			b.warned = true
			b.mu.Unlock()
		}
		return
	}
	r, ok := windowRect(h)
	if !ok {
		return
	}
	// Clipped to the window: nothing outside it can be clicked.
	mx, my := helperkit.MapPoint(x, y, fw, fh, int(r.right-r.left), int(r.bottom-r.top))
	px, py := int(r.left)+mx, int(r.top)+my

	press := kind == "down" || kind == "tap"
	if press && !b.bringToFront(h, px, py, out) {
		return
	}
	pSetCursorPos.Call(uintptr(px), uintptr(py))
	switch kind {
	case "tap":
		sendMouse(mouseeventfLeftDown, 0)
		sendMouse(mouseeventfLeftUp, 0)
	case "down":
		sendMouse(mouseeventfLeftDown, 0)
		b.mu.Lock()
		b.dragged = true
		b.mu.Unlock()
	case "move":
		// SetCursorPos above is the move; a held button turns it into a drag.
	case "up":
		sendMouse(mouseeventfLeftUp, 0)
		b.mu.Lock()
		b.dragged = false
		b.mu.Unlock()
	case "wheel":
		// Positive dy scrolls down; the wheel's positive direction is up. A notch is 120 units
		// and a pixel is about a third of a line, so dy pixels is dy*3 units.
		sendMouse(mouseeventfWheel, uint32(int32(-dy*3)))
	default:
		out.Event("error", "unsupported", "input kind "+kind)
	}
}

// bringToFront raises the window and checks that it is what is under the pointer: synthetic
// clicks go to whatever is on top, and a click on the wrong window would be worse than none.
func (b *backend) bringToFront(h uintptr, px, py int, out *helperkit.Out) bool {
	if ic, _, _ := pIsIconic.Call(h); ic != 0 {
		pShowWindow.Call(h, swRestore)
	}
	// Windows only lets the foreground process change the foreground; a tap on Alt counts as input.
	pKeybdEvent.Call(vkMenu, 0, 0, 0)
	pKeybdEvent.Call(vkMenu, 0, keyeventfKeyUp, 0)
	pSetForeground.Call(h)
	for range 5 {
		at, _, _ := pWindowFromPoint.Call(uintptr(uint32(int32(px))) | uintptr(uint64(uint32(int32(py)))<<32))
		if root, _, _ := pGetAncestor.Call(at, gaRoot); root == h {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	out.Event("error", "internal", "the window is covered by another one; the touch was dropped")
	return false
}

func sendMouse(flags, data uint32) {
	in := input{typ: inputMouse, mi: mouseInput{flags: flags, mouseData: data}}
	pSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

func main() {
	fmt.Fprintln(os.Stderr, "deckcap-win starting")
	helperkit.Run(&backend{})
}
