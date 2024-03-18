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

// IsBackgroundPartiallyTransparent returns true when the theme component
// resolved by partID and stateID is not 100% opaque.
func (t *Theme) IsBackgroundPartiallyTransparent(partID, stateID int32) bool {
	return win.IsThemeBackgroundPartiallyTransparent(t.htheme, partID, stateID)
}

// PartSize obtains a ThemeSizeMetric as specified by partID and stateID.
// bounds is optional and may be nil. esize indicates the requested
// win.THEMESIZE. For more information about THEMESIZE, consult the [Microsoft documentation].
//
// [Microsoft documentation]: https://web.archive.org/web/20221001094810/https://learn.microsoft.com/en-us/windows/win32/api/uxtheme/ne-uxtheme-themesize
func (t *Theme) PartSize(partID, stateID int32, bounds *Rectangle, esize win.THEMESIZE) (ThemeSizeMetric, error) {
	var rect *win.RECT
	if bounds != nil {
		br := bounds.toRECT()
		rect = &br
	}

	return t.partSize(partID, stateID, rect, esize)
}

func (t *Theme) partSize(partID, stateID int32, rect *win.RECT, esize win.THEMESIZE) (ThemeSizeMetric, error) {
	if t.isTrueSize(partID, stateID) {
		result := &themeTrueSizeMetric{
			theme:   t,
			partID:  partID,
			stateID: stateID,
			bounds:  rect,
			esize:   esize,
		}
		result.setInterface(result)
		return result, nil
	}

	tssm := new(themeSizeScalableMetric)
	if hr := win.GetThemePartSize(t.htheme, win.HDC(0), partID, stateID, rect, esize, &tssm.themeSizeMetric.size); win.FAILED(hr) {
		return nil, errorFromHRESULT("GetThemePartSize", hr)
	}

	tssm.dpi = t.wb.DPI()
	tssm.setInterface(tssm)
	return tssm, nil
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

var themeDrawTextDefaultOptions = win.DTTOPTS{
	DwSize:  uint32(unsafe.Sizeof(win.DTTOPTS{})),
	DwFlags: win.DTT_COMPOSITED,
}

func (t *Theme) drawText(canvas *Canvas, font *Font, partID, stateID int32, text string, flags uint32, rect *win.RECT, options *win.DTTOPTS) error {
	textUTF16, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}

	if options == nil {
		options = &themeDrawTextDefaultOptions
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
//   - [win.TMT_CAPTIONFONT]
//   - [win.TMT_SMALLCAPTIONFONT]
//   - [win.TMT_MENUFONT]
//   - [win.TMT_STATUSFONT]
//   - [win.TMT_MSGBOXFONT]
//   - [win.TMT_ICONTITLEFONT]
func (t *Theme) SysFont(fontID int32) (*Font, error) {
	var lf win.LOGFONT
	hr := win.GetThemeSysFont(t.htheme, fontID, &lf)
	if win.FAILED(hr) {
		return nil, errorFromHRESULT("GetThemeSysFont", hr)
	}

	// GetThemeSysFont appears to always use the screen DPI, despite its documentation.
	return newFontFromLOGFONT(&lf, screenDPI())
}

// isTrueSize determines whether the theme component with the given partID and
// stateID is a "true-size" component: such components are not scaled linearly,
// but rather consist of multiple raster images, one of which is chosen
// depending on display density.
func (t *Theme) isTrueSize(partID, stateID int32) bool {
	var enumVal int32
	if hr := win.GetThemeEnumValue(t.htheme, partID, stateID, win.TMT_BGTYPE, &enumVal); win.FAILED(hr) {
		return false
	}
	if enumVal != win.BT_IMAGEFILE {
		return false
	}

	if hr := win.GetThemeEnumValue(t.htheme, partID, stateID, win.TMT_SIZINGTYPE, &enumVal); win.FAILED(hr) {
		return false
	}
	return enumVal == win.ST_TRUESIZE
}

// ThemeSizeMetric is an interface that represents the Size associated with a
// particular theme component.
type ThemeSizeMetric interface {
	// PartSize returns the Size of the part associated with this metric.
	PartSize() (result Size, err error)
	partSize() (result win.SIZE, err error)
}

// ThemeSizeScaler is an interface that some metrics optionally implement when
// they support scaling to a different DPI.
type ThemeSizeScaler interface {
	CopyForDPI(dpi int) ThemeSizeMetric
}

type themeSizeMetric struct {
	iface ThemeSizeMetric
	size  win.SIZE
}

func (tsm *themeSizeMetric) setInterface(i ThemeSizeMetric) {
	tsm.iface = i
}

func (tsm *themeSizeMetric) PartSize() (result Size, err error) {
	size, err := tsm.iface.partSize()
	if err != nil {
		return result, err
	}

	result = sizeFromSIZE(size)
	return result, nil
}

func (tsm *themeSizeMetric) partSize() (win.SIZE, error) {
	return tsm.size, nil
}

// themeSizeScalableMetric also satisfies ThemeSizeScaler.
type themeSizeScalableMetric struct {
	themeSizeMetric
	dpi int
}

func (tssm *themeSizeScalableMetric) CopyForDPI(dpi int) ThemeSizeMetric {
	newSize := scaleSIZE(tssm.themeSizeMetric.size, float64(dpi)/float64(tssm.dpi))
	// The copy should not satisfy ThemeSizeScaler, so we return a *themeSizeMetric.
	result := &themeSizeMetric{size: newSize}
	result.setInterface(result)
	return result
}

type themeTrueSizeMetric struct {
	themeSizeMetric
	theme   *Theme
	partID  int32
	stateID int32
	bounds  *win.RECT
	esize   win.THEMESIZE
}

func (ttsm *themeTrueSizeMetric) partSize() (result win.SIZE, err error) {
	// True-sized metrics must query for their part size at every DPI.
	// We cache the result in the themeSizeMetric's size field.
	pSize := &ttsm.themeSizeMetric.size
	var zero win.SIZE
	if *pSize != zero {
		return *pSize, nil
	}

	if hr := win.GetThemePartSize(ttsm.theme.htheme, win.HDC(0), ttsm.partID, ttsm.stateID, ttsm.bounds, ttsm.esize, pSize); win.FAILED(hr) {
		return result, errorFromHRESULT("GetThemePartSize", hr)
	}

	return *pSize, nil
}

func (ttsm *themeTrueSizeMetric) CopyForDPI(dpi int) ThemeSizeMetric {
	result := *ttsm
	result.setInterface(&result)
	result.themeSizeMetric.size = win.SIZE{}
	return &result
}
