package main

import (
	"bytes"
	"image/color"
	"image/png"
	"syscall"
	"unsafe"

	"slicetrigger/internal/sticon"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// Taskbar status badge via ITaskbarList3::SetOverlayIcon. Unlike the window
// icon, an overlay also shows when SliceTrigger is pinned to the taskbar
// (a pinned button always uses the shortcut's icon).

var (
	ole32                        = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstance         = ole32.NewProc("CoCreateInstance")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")

	clsidTaskbarList = windows.GUID{Data1: 0x56FDF344, Data2: 0xFD6D, Data3: 0x11D0, Data4: [8]byte{0x95, 0x8A, 0x00, 0x60, 0x97, 0xC9, 0xA0, 0x90}}
	iidITaskbarList3 = windows.GUID{Data1: 0xEA1AFB91, Data2: 0x9E28, Data3: 0x4B86, Data4: [8]byte{0x90, 0xE9, 0x9E, 0x9F, 0x8A, 0x5E, 0xEF, 0xAF}}
)

const (
	clsctxInprocServer = 1
	smCxSmIcon         = 49
	vtblHrInit         = 3
	vtblSetOverlayIcon = 18
	taskbarVtblEntries = 21
)

type taskbarList3 struct {
	vtbl *[taskbarVtblEntries]uintptr
}

type taskbarBadge struct {
	tb    *taskbarList3
	icons map[string]uintptr // state -> HICON
	descs map[string]*uint16
}

// newTaskbarBadge must be called on the GUI thread.
func newTaskbarBadge(colors map[string]walk.Color, descs map[string]string) *taskbarBadge {
	win.OleInitialize() // already initialised is fine
	b := &taskbarBadge{icons: map[string]uintptr{}, descs: map[string]*uint16{}}
	var tb *taskbarList3
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidTaskbarList)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidITaskbarList3)), uintptr(unsafe.Pointer(&tb)))
	if hr != 0 || tb == nil {
		return b
	}
	if hr, _, _ := syscall.SyscallN(tb.vtbl[vtblHrInit], uintptr(unsafe.Pointer(tb))); hr != 0 {
		return b
	}
	b.tb = tb

	size, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	if size == 0 {
		size = 16
	}
	for state, c := range colors {
		rgb := color.NRGBA{uint8(c), uint8(c >> 8), uint8(c >> 16), 0xFF}
		var buf bytes.Buffer
		if png.Encode(&buf, sticon.Dot(64, rgb)) != nil {
			continue
		}
		data := buf.Bytes()
		h, _, _ := procCreateIconFromResourceEx.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 1, 0x00030000, size, size, 0)
		if h != 0 {
			b.icons[state] = h
		}
		b.descs[state], _ = syscall.UTF16PtrFromString(descs[state])
	}
	return b
}

// set shows the badge for state on hwnd's taskbar button.
func (b *taskbarBadge) set(hwnd win.HWND, state string) {
	if b == nil || b.tb == nil || b.icons[state] == 0 {
		return
	}
	syscall.SyscallN(b.tb.vtbl[vtblSetOverlayIcon], uintptr(unsafe.Pointer(b.tb)),
		uintptr(hwnd), b.icons[state], uintptr(unsafe.Pointer(b.descs[state])))
}
