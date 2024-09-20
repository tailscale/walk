// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows
// +build windows

package walk

import (
	"unsafe"

	"github.com/tailscale/win"
)

// Monitor is a reference to an individual monitor attached to the current machine.
type Monitor win.HMONITOR

// WorkArea returns the rectangle representing the bounds of the monitor in
// virtual screen coordinates, excluding taskbars and application bars.
func (m Monitor) WorkArea() Rectangle {
	mi := m.getInfo()
	return rectangleFromRECT(mi.RcWork)
}

// Rectangle returns the rectangle representing the bounds of the monitor in
// virtual screen coordinates.
func (m Monitor) Rectangle() Rectangle {
	mi := m.getInfo()
	return rectangleFromRECT(mi.RcMonitor)
}

// IsPrimary returns whether m is classified as the primary monitor.
func (m Monitor) IsPrimary() bool {
	if !m.IsValid() {
		return false
	}
	mi := m.getInfo()
	return mi.DwFlags&win.MONITORINFOF_PRIMARY != 0
}

// DPI returns m's DPI. If m does not refer to a valid monitor, then the legacy
// screen DPI is returned.
func (m Monitor) DPI() (int, error) {
	if !m.IsValid() {
		return screenDPI(), nil
	}

	var dpiX, dpiY uint32
	if hr := win.GetDpiForMonitor(win.HMONITOR(m), win.MDT_EFFECTIVE_DPI, &dpiX, &dpiY); win.FAILED(hr) {
		return 0, errorFromHRESULT("GetDpiForMonitor", hr)
	}

	// X and Y are always identical, so we only need to return one of them.
	return int(dpiX), nil
}

// IsValid returns whether m refers to a valid monitor.
func (m Monitor) IsValid() bool {
	return m != 0
}

func (m Monitor) getInfo() (mi win.MONITORINFO) {
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	win.GetMonitorInfo(win.HMONITOR(m), &mi)
	return mi
}

// PrimaryMonitor obtains the Monitor associated with the primary monitor.
func PrimaryMonitor() Monitor {
	return Monitor(win.MonitorFromWindow(0, win.MONITOR_DEFAULTTOPRIMARY))
}
