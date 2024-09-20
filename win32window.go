// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows
// +build windows

package walk

import (
	"github.com/tailscale/win"
)

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

	// ClientBoundsPixels returns the bounding box rectangle of the Win32Window's
	// client area (excluding decorations). The coordinates are relative to the
	// upper-left corner of the client area.
	ClientBoundsPixels() Rectangle

	// DPI returns the current DPI value of the Window.
	DPI() int

	// Handle returns the window handle of the Window.
	Handle() win.HWND

	// Monitor returns the Monitor upon which the WindowWrapper resides.
	Monitor() Monitor

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
