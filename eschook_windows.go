package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A low-level keyboard hook that watches for ESC in every application
// (N1MM included) without swallowing the key: N1MM still gets its ESC.

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
)

const (
	whKeyboardLL = 13
	wmKeyDown    = 0x0100
	wmSysKeyDown = 0x0104
	vkEscape     = 0x1B
)

type kbdLLHookStruct struct {
	VkCode    uint32
	ScanCode  uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

var (
	escHandler func()
	escHook    uintptr
	escProc    = syscall.NewCallback(escHookProc)
)

func escHookProc(nCode, wParam uintptr, k *kbdLLHookStruct) uintptr {
	if int32(nCode) >= 0 && (wParam == wmKeyDown || wParam == wmSysKeyDown) {
		if k.VkCode == vkEscape && escHandler != nil {
			// Windows drops hooks that are slow to return: do the work elsewhere.
			go escHandler()
		}
	}
	r, _, _ := procCallNextHookEx.Call(0, nCode, wParam, uintptr(unsafe.Pointer(k)))
	return r
}

// installEscHook must be called on the GUI thread (it needs a message loop).
func installEscHook(fn func()) error {
	escHandler = fn
	if escHook != 0 {
		return nil
	}
	mod, _, _ := procGetModuleHandleW.Call(0)
	h, _, err := procSetWindowsHookExW.Call(whKeyboardLL, escProc, mod, 0)
	if h == 0 {
		return err
	}
	escHook = h
	return nil
}

func removeEscHook() {
	if escHook != 0 {
		procUnhookWindowsHookEx.Call(escHook)
		escHook = 0
	}
}
