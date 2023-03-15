// Copyright 2023 Tailscale Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package walk

import (
	"unsafe"

	"github.com/tailscale/win"
)

func init() {
	AppendToWalkInit(func() {
		// Initializes a buffer pool on the UI thread.
		win.BufferedPaintInit()
	})
}

// BufferedPaint encapsulates a double-buffered paint operation.
type BufferedPaint struct {
	h  win.HPAINTBUFFER
	dc win.HDC
}

// BeginBufferedPaint obtains a back buffer from the OS according to format and
// maps it to canvas using bounds. The buffer will be initially erased.
func BeginBufferedPaint(canvas *Canvas, bounds Rectangle, format win.BP_BUFFERFORMAT) (*BufferedPaint, error) {
	params := win.BP_PAINTPARAMS{
		Flags: win.BPPF_ERASE,
	}
	params.Size = uint32(unsafe.Sizeof(params))

	return BeginBufferedPaintWithParams(canvas, bounds, format, &params)
}

// BeginBufferedPaintWithParams obtains a back buffer from the OS according to
// format and paintParams, and maps it to canvas using bounds.
func BeginBufferedPaintWithParams(canvas *Canvas, bounds Rectangle, format win.BP_BUFFERFORMAT, paintParams *win.BP_PAINTPARAMS) (*BufferedPaint, error) {
	rect := bounds.toRECT()
	return beginBufferedPaint(canvas.HDC(), &rect, format, paintParams)
}

func beginBufferedPaint(hdcTarget win.HDC, rectTarget *win.RECT, format win.BP_BUFFERFORMAT, paintParams *win.BP_PAINTPARAMS) (result *BufferedPaint, err error) {
	result = &BufferedPaint{}
	result.h, err = win.BeginBufferedPaint(hdcTarget, rectTarget, format, paintParams, &result.dc)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// Canvas returns the Canvas associated with the back buffer.
func (bp *BufferedPaint) Canvas() (*Canvas, error) {
	return newCanvasFromHDC(bp.dc)
}

func (bp *BufferedPaint) end(copyDC bool) {
	hr := win.EndBufferedPaint(bp.h, copyDC)
	if win.FAILED(hr) {
		return
	}

	bp.h = 0
	bp.dc = 0
}

// End blits the contents of bp back to its target Canvas and then returns bp
// back to the OS.
func (bp *BufferedPaint) End() {
	bp.end(true)
}

// Drop returns bp back to the OS without blitting its contents back to the
// target Canvas.
func (bp *BufferedPaint) Drop() {
	bp.end(false)
}
