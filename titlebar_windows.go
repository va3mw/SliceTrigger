package main

import (
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// Title bar colouring via DWM. DWMWA_CAPTION_COLOR and DWMWA_TEXT_COLOR exist
// from Windows 11 (build 22000); older Windows ignores them and keeps the
// normal title bar.

var procDwmSetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")

const (
	dwmwaCaptionColor = 35
	dwmwaTextColor    = 36
)

func setDwmColor(hwnd win.HWND, attr uint32, c walk.Color) bool {
	if procDwmSetWindowAttribute.Find() != nil {
		return false
	}
	v := uint32(c) // COLORREF 0x00BBGGRR, same layout as walk.Color
	hr, _, _ := procDwmSetWindowAttribute.Call(uintptr(hwnd), uintptr(attr), uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v))
	return hr == 0
}

// setTitleBarColor paints the window's title bar c with white title text.
// It reports false where Windows does not support it.
func setTitleBarColor(hwnd win.HWND, c walk.Color) bool {
	if !setDwmColor(hwnd, dwmwaCaptionColor, c) {
		return false
	}
	setDwmColor(hwnd, dwmwaTextColor, walk.RGB(0xFF, 0xFF, 0xFF))
	return true
}
