package main

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/asmsaifs/techo5-streamdeck/internal/helperkit"
)

// WASAPI process loopback (Windows 10 2004+): an audio client that hears only one process tree,
// obtained through ActivateAudioInterfaceAsync with the virtual device "VAD\Process_Loopback". There
// is no COM library in the dependencies, so the few vtable calls are made by hand; every COM
// pointer is an unsafe.Pointer so the vet "misuse of unsafe.Pointer" check stays quiet.

var (
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	mmdevapi = windows.NewLazySystemDLL("mmdevapi.dll")

	pCoInitializeEx              = ole32.NewProc("CoInitializeEx")
	pCoUninitialize              = ole32.NewProc("CoUninitialize")
	pActivateAudioInterfaceAsync = mmdevapi.NewProc("ActivateAudioInterfaceAsync")

	iidIUnknown      = windows.GUID{Data1: 0x00000000, Data2: 0, Data3: 0, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidAgile         = windows.GUID{Data1: 0x94EA2B94, Data2: 0xE9CC, Data3: 0x49E0, Data4: [8]byte{0xC0, 0xFF, 0xEE, 0x64, 0xCA, 0x8F, 0x5B, 0x90}}
	iidCompletion    = windows.GUID{Data1: 0x41D949AB, Data2: 0x9862, Data3: 0x444A, Data4: [8]byte{0x80, 0xF6, 0xC2, 0x61, 0x33, 0x4D, 0xA5, 0xEB}}
	iidAudioClient   = windows.GUID{Data1: 0x1CB9AD4C, Data2: 0xDBFA, Data3: 0x4C32, Data4: [8]byte{0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2}}
	iidCaptureClient = windows.GUID{Data1: 0xC8ADBD64, Data2: 0xE71E, Data3: 0x48A0, Data4: [8]byte{0xA4, 0xDE, 0x18, 0x5C, 0x39, 0x5C, 0xD3, 0x17}}
)

const (
	vtBlob                = 65
	audclntShared         = 0
	flagLoopback          = 0x00020000
	flagEventCallback     = 0x00040000
	flagAutoConvertPCM    = 0x80000000
	flagSrcDefaultQuality = 0x08000000
	bufferFlagSilent      = 0x2
	eNoInterface          = 0x80004002
	sampleRate            = 48000
	channels              = 2
)

// ptr turns a pointer-sized value handed to a callback by COM into a pointer. The value points at
// memory COM keeps alive for the call (or at our own handler, kept alive by its owner), so the usual
// uintptr-to-Pointer warning does not apply; going through the address of the variable says so to vet.
func ptr(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

// comCall calls method idx of a COM object.
func comCall(obj unsafe.Pointer, idx int, args ...uintptr) uintptr {
	vtbl := (*[32]uintptr)(*(*unsafe.Pointer)(obj))
	r, _, _ := syscall.SyscallN(vtbl[idx], append([]uintptr{uintptr(obj)}, args...)...)
	return r
}

func comRelease(obj unsafe.Pointer) {
	if obj != nil {
		comCall(obj, 2)
	}
}

// handler is the IActivateAudioInterfaceCompletionHandler the activation calls back. It also answers
// IAgileObject: without it ActivateAudioInterfaceAsync refuses a handler that is not free-threaded.
type handler struct {
	vtbl   *handlerVtbl
	done   windows.Handle
	result unsafe.Pointer // the IAudioClient, set by the callback
	hr     uintptr
}

type handlerVtbl struct{ qi, addRef, release, completed uintptr }

var (
	handlerQI = syscall.NewCallback(func(this, riid, ppv uintptr) uintptr {
		id := *(*windows.GUID)(ptr(riid))
		out := (*uintptr)(ptr(ppv))
		if id == iidIUnknown || id == iidCompletion || id == iidAgile {
			*out = this
			return 0
		}
		*out = 0
		return eNoInterface
	})
	handlerRef  = syscall.NewCallback(func(this uintptr) uintptr { return 1 })
	handlerDone = syscall.NewCallback(func(this, op uintptr) uintptr {
		h := (*handler)(ptr(this))
		var hr int32
		var unk unsafe.Pointer
		// IActivateAudioInterfaceAsyncOperation::GetActivateResult is method 3.
		comCall(ptr(op), 3, uintptr(unsafe.Pointer(&hr)), uintptr(unsafe.Pointer(&unk)))
		h.hr = uintptr(uint32(hr))
		if hr >= 0 {
			h.result = unk
		}
		windows.SetEvent(h.done)
		return 0
	})
	handlerTable = &handlerVtbl{handlerQI, handlerRef, handlerRef, handlerDone}
)

type propVariantBlob struct {
	vt   uint16
	_    [3]uint16
	cb   uint32
	data unsafe.Pointer
}

type activationParams struct {
	typ, pid, mode uint32 // AUDIOCLIENT_ACTIVATION_TYPE_PROCESS_LOOPBACK = 1, include the process tree = 0
}

type waveFormat struct {
	tag                    uint16
	channels               uint16
	samplesPerSec          uint32
	avgBytesPerSec         uint32
	blockAlign, bitsPerSmp uint16
	cbSize                 uint16
}

type audioCapture struct {
	stop chan struct{}
	done chan struct{}
}

func (a *audioCapture) Stop() {
	close(a.stop)
	<-a.done
}

// startAudio starts a goroutine that sends the process tree's sound until Stop. It returns once the
// loopback client is running, or the reason it could not be.
func startAudio(out *helperkit.Out, pid uint32) (*audioCapture, error) {
	a := &audioCapture{stop: make(chan struct{}), done: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		defer close(a.done)
		// COM objects live on the thread that made them; keep this goroutine on one.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		pCoInitializeEx.Call(0, 0) // COINIT_MULTITHREADED
		defer pCoUninitialize.Call()
		a.run(out, pid, ready)
	}()
	if err := <-ready; err != nil {
		<-a.done
		return nil, err
	}
	return a, nil
}

func (a *audioCapture) run(out *helperkit.Out, pid uint32, ready chan<- error) {
	fail := func(format string, args ...any) { ready <- fmt.Errorf(format, args...) }

	ev, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		fail("event: %v", err)
		return
	}
	defer windows.CloseHandle(ev)
	h := &handler{vtbl: handlerTable, done: ev}

	params := activationParams{typ: 1, pid: pid, mode: 0}
	pv := propVariantBlob{vt: vtBlob, cb: uint32(unsafe.Sizeof(params)), data: unsafe.Pointer(&params)}
	path, _ := windows.UTF16PtrFromString("VAD\\Process_Loopback")
	var op unsafe.Pointer
	hr, _, _ := pActivateAudioInterfaceAsync.Call(uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(&iidAudioClient)),
		uintptr(unsafe.Pointer(&pv)), uintptr(unsafe.Pointer(h)), uintptr(unsafe.Pointer(&op)))
	if hr != 0 {
		fail("ActivateAudioInterfaceAsync 0x%08x (needs Windows 10 2004 or later)", uint32(hr))
		return
	}
	defer comRelease(op)
	if r, _ := windows.WaitForSingleObject(ev, 5000); r != windows.WAIT_OBJECT_0 {
		fail("activating the loopback client timed out")
		return
	}
	if h.hr != 0 || h.result == nil {
		fail("loopback client 0x%08x", uint32(h.hr))
		return
	}
	client := h.result
	defer comRelease(client)

	wf := waveFormat{tag: 1, channels: channels, samplesPerSec: sampleRate, avgBytesPerSec: sampleRate * channels * 2, blockAlign: channels * 2, bitsPerSmp: 16}
	// 200 ms buffer, in 100 ns units. AUTOCONVERTPCM lets the engine give us exactly this format.
	if r := comCall(client, 3, audclntShared, flagLoopback|flagEventCallback|flagAutoConvertPCM|flagSrcDefaultQuality,
		2000000, 0, uintptr(unsafe.Pointer(&wf)), 0); r != 0 {
		fail("IAudioClient.Initialize 0x%08x", uint32(r))
		return
	}
	wake, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		fail("event: %v", err)
		return
	}
	defer windows.CloseHandle(wake)
	if r := comCall(client, 13, uintptr(wake)); r != 0 { // SetEventHandle
		fail("SetEventHandle 0x%08x", uint32(r))
		return
	}
	var capture unsafe.Pointer
	if r := comCall(client, 14, uintptr(unsafe.Pointer(&iidCaptureClient)), uintptr(unsafe.Pointer(&capture))); r != 0 || capture == nil { // GetService
		fail("IAudioCaptureClient 0x%08x", uint32(r))
		return
	}
	defer comRelease(capture)
	if r := comCall(client, 10); r != 0 { // Start
		fail("IAudioClient.Start 0x%08x", uint32(r))
		return
	}
	defer comCall(client, 11) // Stop
	ready <- nil

	for {
		select {
		case <-a.stop:
			return
		default:
		}
		// The event fires per period, but a silent app may never raise it: the timeout keeps the
		// loop answering to Stop.
		windows.WaitForSingleObject(wake, 100)
		if err := drain(out, capture); err != nil {
			out.Event("error", "internal", "sound: "+err.Error())
			return
		}
	}
}

// drain sends every packet the capture client has.
func drain(out *helperkit.Out, capture unsafe.Pointer) error {
	for {
		var n uint32
		if r := comCall(capture, 5, uintptr(unsafe.Pointer(&n))); r != 0 { // GetNextPacketSize
			return fmt.Errorf("GetNextPacketSize 0x%08x", uint32(r))
		}
		if n == 0 {
			return nil
		}
		var data unsafe.Pointer
		var frames, flags uint32
		if r := comCall(capture, 3, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&frames)), uintptr(unsafe.Pointer(&flags)), 0, 0); r != 0 { // GetBuffer
			return fmt.Errorf("GetBuffer 0x%08x", uint32(r))
		}
		pcm := make([]byte, int(frames)*channels*2)
		if flags&bufferFlagSilent == 0 && data != nil {
			copy(pcm, unsafe.Slice((*byte)(data), len(pcm)))
		}
		comCall(capture, 4, uintptr(frames)) // ReleaseBuffer
		out.Audio(sampleRate, channels, pcm)
	}
}
