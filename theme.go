// Copyright 2023 Tailscale Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package walk

import (
	"unsafe"

	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
)

// Theme encapsulates access to Windows theming for built-in widgets. Themes
// may be obtained by calling ThemeForClass on a Window. Many of Theme's
// methods require part and state IDs, which are listed in the
// [Microsoft documentation].
//
// [Microsoft documentation]: https://web.archive.org/web/20230203181612/https://learn.microsoft.com/en-us/windows/win32/controls/parts-and-states
type Theme struct {
	wb     *WindowBase
	htheme win.HTHEME
}

// Implementation note: Most of the methods on Theme come in two flavors.
// The public flavor uses walk types for certain values, while the internal
// flavor uses win types.

func openTheme(wb *WindowBase, name string) (*Theme, error) {
	nameUTF16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}

	result := &Theme{wb: wb, htheme: win.OpenThemeData(wb.hWnd, nameUTF16)}
	if result.htheme == 0 {
		return nil, lastError("OpenThemeData")
	}

	return result, nil
}

func (t *Theme) close() {
	if t.htheme != 0 && win.SUCCEEDED(win.CloseThemeData(t.htheme)) {
		t.wb = nil
		t.htheme = 0
	}
}

// BackgroundPartiallyTransparent returns true when the theme component resolved
// by partID and stateID is not 100% opaque.
func (t *Theme) BackgroundPartiallyTransparent(partID, stateID int32) bool {
	return win.IsThemeBackgroundPartiallyTransparent(t.htheme, partID, stateID)
}

// PartSize obtains a Size property as specified by partID, stateID and propID.
// esize indicates the requested win.THEMESIZE. For more information about
// THEMESIZE, consult the [Microsoft documentation].
//
// [Microsoft documentation]: https://web.archive.org/web/20221001094810/https://learn.microsoft.com/en-us/windows/win32/api/uxtheme/ne-uxtheme-themesize
func (t *Theme) PartSize(partID, stateID int32, bounds Rectangle, esize win.THEMESIZE) (result Size, err error) {
	rect := bounds.toRECT()
	size, err := t.partSize(partID, stateID, &rect, esize)
	if err != nil {
		return result, err
	}

	result = sizeFromSIZE(size)
	return result, nil
}

func (t *Theme) partSize(partID, stateID int32, rect *win.RECT, esize win.THEMESIZE) (size win.SIZE, _ error) {
	hr := win.GetThemePartSize(t.htheme, win.HDC(0), partID, stateID, rect, esize, &size)
	if win.FAILED(hr) {
		return size, errorFromHRESULT("GetThemePartSize", hr)
	}

	return size, nil
}

// Integer obtains an integral property as resolved by partID, stateID and propID.
func (t *Theme) Integer(partID, stateID, propID int32) (ret int32, err error) {
	hr := win.GetThemeInt(t.htheme, partID, stateID, propID, &ret)
	if win.FAILED(hr) {
		err = errorFromHRESULT("GetThemeInt", hr)
	}
	return ret, err
}

// Margins obtains a margin property as resolved by partID, stateID, and propID,
// bounded by bounds.
func (t *Theme) Margins(partID, stateID, propID int32, bounds Rectangle) (win.MARGINS, error) {
	rect := bounds.toRECT()
	return t.margins(partID, stateID, propID, &rect)
}

func (t *Theme) margins(partID, stateID, propID int32, rect *win.RECT) (ret win.MARGINS, err error) {
	hr := win.GetThemeMargins(t.htheme, win.HDC(0), partID, stateID, propID, rect, &ret)
	if win.FAILED(hr) {
		err = errorFromHRESULT("GetThemeMargins", hr)
	}
	return ret, err
}

// DrawBackground draws a theme background specified by partID and stateID into
// canvas, bounded by bounds.
func (t *Theme) DrawBackground(canvas *Canvas, partID, stateID int32, bounds Rectangle) (err error) {
	rect := bounds.toRECT()
	return t.drawBackground(canvas, partID, stateID, &rect)
}

func (t *Theme) drawBackground(canvas *Canvas, partID, stateID int32, rect *win.RECT) (err error) {
	hr := win.DrawThemeBackground(t.htheme, canvas.HDC(), partID, stateID, rect, nil)
	if win.FAILED(hr) {
		err = errorFromHRESULT("DrawThemeBackground", hr)
	}
	return err
}

// TextExtent obtains the size (in pixels) of text, should it be rendered using
// the font derived from partID and stateID. If the theme part does not
// explicitly specify a font, TextExtent will fall back to using the font
// specified by the font argument. flags may contain an OR'd combination of DT_*
// flags defined in the win package. For more information about flags, consult
// the [Microsoft documentation].
//
// [Microsoft documentation]: https://web.archive.org/web/20221129191837/https://learn.microsoft.com/en-us/windows/win32/controls/theme-format-values
func (t *Theme) TextExtent(canvas *Canvas, font *Font, partID, stateID int32, text string, flags uint32) (result Size, _ error) {
	output, err := t.textExtent(canvas, font, partID, stateID, text, flags)
	if err != nil {
		return result, err
	}

	result = sizeFromSIZE(output)
	return result, nil
}

func (t *Theme) textExtent(canvas *Canvas, font *Font, partID, stateID int32, text string, flags uint32) (ret win.SIZE, _ error) {
	textUTF16, err := windows.UTF16FromString(text)
	if err != nil {
		return ret, err
	}

	var rect win.RECT
	err = canvas.withFont(font, func() error {
		hr := win.GetThemeTextExtent(t.htheme, canvas.HDC(), partID, stateID, &textUTF16[0], int32(len(textUTF16)-1), flags, nil, &rect)
		if win.FAILED(hr) {
			return errorFromHRESULT("GetThemeTextExtent", hr)
		}

		return nil
	})

	ret.CX = rect.Width()
	ret.CY = rect.Height()
	return ret, err
}

// DrawText draws text into canvas within bounds using the font derived from
// partID and stateID. If the theme part does not explicitly specify a font,
// DrawText will fall back to using the font specified by the font argument.
// flags may contain an OR'd combination of DT_* flags defined in the win
// package. options may be nil, in which case default options enabling
// alpha-blending will be used.
//
// See the [Microsoft documentation] for DrawThemeTextEx for more detailed
// information concerning the semantics of flags and options.
//
// [Microsoft documentation]: https://web.archive.org/web/20221111230136/https://learn.microsoft.com/en-us/windows/win32/api/uxtheme/nf-uxtheme-drawthemetextex
func (t *Theme) DrawText(canvas *Canvas, font *Font, partID, stateID int32, text string, flags uint32, bounds Rectangle, options *win.DTTOPTS) error {
	rect := bounds.toRECT()
	return t.drawText(canvas, font, partID, stateID, text, flags, &rect, options)
}

func (t *Theme) drawText(canvas *Canvas, font *Font, partID, stateID int32, text string, flags uint32, rect *win.RECT, options *win.DTTOPTS) error {
	textUTF16, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}

	if options == nil {
		options = &win.DTTOPTS{
			DwSize:  uint32(unsafe.Sizeof(*options)),
			DwFlags: win.DTT_COMPOSITED,
		}
	}

	return canvas.withFont(font, func() error {
		hr := win.DrawThemeTextEx(t.htheme, canvas.HDC(), partID, stateID, &textUTF16[0], int32(len(textUTF16)-1), flags, rect, options)
		if win.FAILED(hr) {
			return errorFromHRESULT("DrawThemeTextEx", hr)
		}

		return nil
	})
}

// Font obtains the themes's font associated with t and the provided
// part, state and property IDs.
func (t *Theme) Font(partID, stateID, propID int32) (*Font, error) {
	var lf win.LOGFONT
	hr := win.GetThemeFont(t.htheme, win.HDC(0), partID, stateID, propID, &lf)
	if win.FAILED(hr) {
		return nil, errorFromHRESULT("GetThemeFont", hr)
	}

	return newFontFromLOGFONT(&lf, t.wb.DPI())
}

// SysFont obtains the theme's font associated with the system fontID, which
// must be one of the following constants:
//	 * [win.TMT_CAPTIONFONT]
//	 * [win.TMT_SMALLCAPTIONFONT]
//	 * [win.TMT_MENUFONT]
//	 * [win.TMT_STATUSFONT]
//	 * [win.TMT_MSGBOXFONT]
//	 * [win.TMT_ICONTITLEFONT]
func (t *Theme) SysFont(fontID int32) (*Font, error) {
	var lf win.LOGFONT
	hr := win.GetThemeSysFont(t.htheme, fontID, &lf)
	if win.FAILED(hr) {
		return nil, errorFromHRESULT("GetThemeSysFont", hr)
	}

	// GetThemeSysFont appears to always use 96DPI, despite its documentation.
	return newFontFromLOGFONT(&lf, 96)
}
