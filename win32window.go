// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows
// +build windows

package walk

import (
	"errors"
	"unsafe"

	"github.com/dblohm7/wingoes"
	"github.com/tailscale/win"
)

// ErrUnsupportedOnThisWindowsVersion indicates that an API call is not supported
// on the currently running version of Windows. If you are receiving this error
// even though you are running on the correct version of Windows, ensure that
// the executable's application manifest indicates the necessary OS compatibilities.
var ErrUnsupportedOnThisWindowsVersion = errors.New("unsupported on this version of Windows")

// Win32Window is an interface that provides some primitive operations
// supported by any Win32-based window.
type Win32Window interface {
	// BoundsPixels returns the outer bounding box rectangle of the Win32Window,
	// including decorations.
	//
	// For a Form, like *MainWindow or *Dialog, the rectangle is in screen
	// coordinates, for a child Win32Window the coordinates are relative to its
	// parent.
	BoundsPixels() Rectangle

	// ClearFrameInset clears any frame inset previously set by [SetFrameInset],
	// resetting the DWM-drawn non-client window frame back to its defaults.
	ClearFrameInset() error

	// ClientBoundsPixels returns the bounding box rectangle of the Win32Window's
	// client area (excluding decorations). The coordinates are relative to the
	// upper-left corner of the client area.
	ClientBoundsPixels() Rectangle

	// DPI returns the current DPI value of the Win32Window.
	DPI() int

	// EnableHostBackdropBrush sets whether this Win32Window may use host
	// backdrop brushes. It returns [ErrUnsupportedOnThisWindowsVersion] if not
	// running on at least Windows 11.
	EnableHostBackdropBrush(enable bool) error

	// Handle returns the window handle of the Win32Window.
	Handle() win.HWND

	// IsCloaked returns whether the Win32Window is currently cloaked.
	IsCloaked() bool

	// Monitor returns the Monitor upon which the Win32Window resides.
	Monitor() Monitor

	// RemoveDWMBorder removes the non-client border drawn by DWM from the
	// Win32Window. It returns [ErrUnsupportedOnThisWindowsVersion] if not running
	// on at least Windows 11.
	RemoveDWMBorder() error

	// ResetDWMBorderColor resets the color used by DWM for drawing the
	// Win32Window's non-client border to the system default. It returns
	// [ErrUnsupportedOnThisWindowsVersion] if not running on at least Windows 11.
	ResetDWMBorderColor() error

	// SetCloaked either cloaks or de-cloaks the Win32Window depending on the
	// value of cloak.
	SetCloaked(cloak bool) error

	// SetDWMBorderColor sets the color used by DWM for drawing the Win32Window's
	// non-client border. It returns [ErrUnsupportedOnThisWindowsVersion] if not
	// running on at least Windows 11.
	SetDWMBorderColor(color win.COLORREF) error

	// SetFrameInset extends the DWM-drawn non-client window frame into
	// the client area of the Win32Window by inset. Insets must be specified
	// in native pixels.
	SetFrameInset(inset win.MARGINS) error

	// SetRoundedCornerPreference tells DWM how to render the corners of the
	// Win32Window. It returns [ErrUnsupportedOnThisWindowsVersion] if not running
	// on at least Windows 11.
	SetRoundedCornerPreference(pref win.DWM_WINDOW_CORNER_PREFERENCE) error

	// SetSolidSurface extends the DWM-drawn non-client window frame to fill the
	// entire client area of the Win32Window.
	SetSolidSurface() error

	// SetSupportsDarkMode indicates to Windows that the Win32Window is capable
	// of supporting dark mode. It returns [ErrUnsupportedOnThisWindowsVersion] if
	// not running on at least Windows 11.
	SetSupportsDarkMode() error

	// SetSystemBackdrop specifies the backdrop material of the Win32Window.
	// It returns [ErrUnsupportedOnThisWindowsVersion] if not running on at least
	// Windows 11 Build 22H2.
	SetSystemBackdrop(sbdType win.DWM_SYSTEMBACKDROP_TYPE) error

	// SystemBackdrop returns the current backdrop material set for the Win32Window.
	// It returns [ErrUnsupportedOnThisWindowsVersion] if not running on at least
	// Windows 11 Build 22H2.
	SystemBackdrop() (win.DWM_SYSTEMBACKDROP_TYPE, error)

	// Visible returns whether the Win32Window is visible.
	Visible() bool
}

// Win32WindowImpl implements some primitive operations common to all Win32 windows.
type Win32WindowImpl struct {
	hWnd          win.HWND
	defWindowProc func(win.HWND, uint32, uintptr, uintptr) uintptr
}

func (ww *Win32WindowImpl) BoundsPixels() (rect Rectangle) {
	var r win.RECT
	if win.GetWindowRect(ww.hWnd, &r) {
		return RectangleFromRECT(r)
	}
	return rect
}

func (ww *Win32WindowImpl) ClientBoundsPixels() (rect Rectangle) {
	var r win.RECT
	if win.GetClientRect(ww.hWnd, &r) {
		return RectangleFromRECT(r)
	}
	return rect
}

func (ww *Win32WindowImpl) DPI() int {
	return int(win.GetDpiForWindow(ww.hWnd))
}

func (ww *Win32WindowImpl) Handle() win.HWND {
	return ww.hWnd
}

func (ww *Win32WindowImpl) Monitor() Monitor {
	return Monitor(win.MonitorFromWindow(ww.hWnd, win.MONITOR_DEFAULTTONEAREST))
}

func (ww *Win32WindowImpl) Visible() bool {
	return win.IsWindowVisible(ww.hWnd)
}

func (ww *Win32WindowImpl) ClearFrameInset() error {
	return ww.SetFrameInset(win.MARGINS{})
}

func (ww *Win32WindowImpl) SetSolidSurface() error {
	return ww.SetFrameInset(win.MARGINS{-1, -1, -1, -1})
}

func (ww *Win32WindowImpl) SetFrameInset(inset win.MARGINS) error {
	if hr := win.DwmExtendFrameIntoClientArea(ww.hWnd, &inset); win.FAILED(hr) {
		return errorFromHRESULT("DwmExtendFrameIntoClientArea", hr)
	}
	return nil
}

func (ww *Win32WindowImpl) dwmAttribute(attr win.DWMWINDOWATTRIBUTE, val unsafe.Pointer, valLen uint32) error {
	if hr := win.DwmGetWindowAttribute(ww.hWnd, attr, val, valLen); win.FAILED(hr) {
		return errorFromHRESULT("DwmGetWindowAttribute", hr)
	}
	return nil
}

func (ww *Win32WindowImpl) setDWMAttribute(attr win.DWMWINDOWATTRIBUTE, val unsafe.Pointer, valLen uint32) error {
	if hr := win.DwmSetWindowAttribute(ww.hWnd, attr, val, valLen); win.FAILED(hr) {
		return errorFromHRESULT("DwmSetWindowAttribute", hr)
	}
	return nil
}

func (ww *Win32WindowImpl) IsCloaked() bool {
	why, err := ww.whyCloaked()
	return err == nil && why != 0
}

func (ww *Win32WindowImpl) whyCloaked() (why uint32, err error) {
	err = ww.dwmAttribute(win.DWMWA_CLOAKED, unsafe.Pointer(&why), uint32(unsafe.Sizeof(why)))
	return why, err
}

func (ww *Win32WindowImpl) SetRoundedCornerPreference(pref win.DWM_WINDOW_CORNER_PREFERENCE) error {
	if !wingoes.IsWin11OrGreater() {
		return ErrUnsupportedOnThisWindowsVersion
	}

	return ww.setDWMAttribute(win.DWMWA_WINDOW_CORNER_PREFERENCE, unsafe.Pointer(&pref), uint32(unsafe.Sizeof(pref)))
}

func (ww *Win32WindowImpl) SetSupportsDarkMode() error {
	if !wingoes.IsWin11OrGreater() {
		return ErrUnsupportedOnThisWindowsVersion
	}

	val := int32(1) // Win32 BOOL
	return ww.setDWMAttribute(win.DWMWA_USE_IMMERSIVE_DARK_MODE, unsafe.Pointer(&val), uint32(unsafe.Sizeof(val)))
}

func (ww *Win32WindowImpl) SetSystemBackdrop(sbdType win.DWM_SYSTEMBACKDROP_TYPE) error {
	if !wingoes.IsWin11BuildOrGreater(wingoes.Win11Build22H2) {
		return ErrUnsupportedOnThisWindowsVersion
	}

	return ww.setDWMAttribute(win.DWMWA_SYSTEMBACKDROP_TYPE, unsafe.Pointer(&sbdType), uint32(unsafe.Sizeof(sbdType)))
}

func (ww *Win32WindowImpl) SystemBackdrop() (sbdType win.DWM_SYSTEMBACKDROP_TYPE, err error) {
	if !wingoes.IsWin11BuildOrGreater(wingoes.Win11Build22H2) {
		return win.DWMSBT_AUTO, ErrUnsupportedOnThisWindowsVersion
	}

	err = ww.dwmAttribute(win.DWMWA_SYSTEMBACKDROP_TYPE, unsafe.Pointer(&sbdType), uint32(unsafe.Sizeof(sbdType)))
	return sbdType, err
}

func (ww *Win32WindowImpl) SetCloaked(cloak bool) error {
	var val int32 // Win32 BOOL
	if cloak {
		val = 1
	}

	return ww.setDWMAttribute(win.DWMWA_CLOAK, unsafe.Pointer(&val), uint32(unsafe.Sizeof(val)))
}

func (ww *Win32WindowImpl) RemoveDWMBorder() error {
	return ww.SetDWMBorderColor(win.DWMWA_COLOR_NONE)
}

func (ww *Win32WindowImpl) ResetDWMBorderColor() error {
	return ww.SetDWMBorderColor(win.DWMWA_COLOR_DEFAULT)
}

func (ww *Win32WindowImpl) SetDWMBorderColor(color win.COLORREF) error {
	if !wingoes.IsWin11OrGreater() {
		return ErrUnsupportedOnThisWindowsVersion
	}

	return ww.setDWMAttribute(win.DWMWA_BORDER_COLOR, unsafe.Pointer(&color), uint32(unsafe.Sizeof(color)))
}

func (ww *Win32WindowImpl) EnableHostBackdropBrush(enable bool) error {
	if !wingoes.IsWin11OrGreater() {
		return ErrUnsupportedOnThisWindowsVersion
	}

	var val int32 // Win32 BOOL
	if enable {
		val = 1
	}

	return ww.setDWMAttribute(win.DWMWA_USE_HOSTBACKDROPBRUSH, unsafe.Pointer(&val), uint32(unsafe.Sizeof(val)))
}
