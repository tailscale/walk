// Copyright 2023 Tailscale Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package walk

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"github.com/dblohm7/wingoes/com"
	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
)

var gdiplusCtx gdiplusContext

type gdiplusContext struct {
	once    sync.Once
	initErr error
	fnPtrs  win.GdiplusStartupOutput
}

func (gc *gdiplusContext) notifyUIThread() {
	if gc.fnPtrs.NotificationHook == 0 {
		return
	}

	var token uintptr
	ret, _, _ := syscall.SyscallN(gc.fnPtrs.NotificationHook, uintptr(unsafe.Pointer(&token)))
	if status := win.GpStatus(ret); status != win.Ok {
		gc.initErr = newError(fmt.Sprintf("GDI+ notification hook failed with status '%s'", status))
	}
}

func (gc *gdiplusContext) init() {
	gc.once.Do(func() {
		var token uintptr
		si := win.GdiplusStartupInput{
			GdiplusVersion:           1,
			SuppressBackgroundThread: win.TRUE,
		}
		if status := win.GdiplusStartup(&token, &si, &gc.fnPtrs); status != win.Ok {
			gc.initErr = newError(fmt.Sprintf("GdiplusStartup failed with status '%s'", status))
			return
		}
		gc.notifyUIThread()
	})
}

func (gc *gdiplusContext) ensure() error {
	gc.once.Do(func() {
		panic("application not initialized")
	})
	return gc.initErr
}

func init() {
	AppendToWalkInit(func() {
		gdiplusCtx.init()
	})
}

func ensureGDIPlus() error {
	return gdiplusCtx.ensure()
}

// GDIPlusCanvas facilitates performing graphics operations against a rendering
// target.
type GDIPlusCanvas struct {
	gpGraphics *win.GpGraphics
}

// NewGDIPlusCanvasFromBitmap creates a new GDIPlusCanvas for rendering to the
// Bitmap bitmap.
func NewGDIPlusCanvasFromBitmap(bitmap *Bitmap) (*GDIPlusCanvas, error) {
	gbmp, err := NewGDIPlusBitmapFromBitmap(bitmap)
	if err != nil {
		return nil, err
	}
	defer gbmp.Dispose()

	return NewGDIPlusCanvasFromGDIPlusBitmap(gbmp)
}

// NewGDIPlusCanvasFromBitmap creates a new GDIPlusCanvas for rendering to the
// GDIPlusBitmap bitmap.
func NewGDIPlusCanvasFromGDIPlusBitmap(bitmap *GDIPlusBitmap) (*GDIPlusCanvas, error) {
	result := &GDIPlusCanvas{}
	if status := win.GdipGetImageGraphicsContext(bitmap.gpImage(), &result.gpGraphics); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipGetImageGraphicsContext failed with status '%s'", status))
	}

	return result, nil
}

func newGDIPlusCanvas(canvas *Canvas) (*GDIPlusCanvas, error) {
	if err := ensureGDIPlus(); err != nil {
		return nil, err
	}

	result := &GDIPlusCanvas{}
	if status := win.GdipCreateFromHDC(canvas.HDC(), &result.gpGraphics); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipCreateFromHDC failed with status '%s'", status))
	}

	return result, nil
}

// Dispose frees system resources associated with g.
func (g *GDIPlusCanvas) Dispose() {
	if win.GdipDeleteGraphics(g.gpGraphics) == win.Ok {
		g.gpGraphics = nil
	}
}

// Clear erases g, making its target fully transparent.
func (g *GDIPlusCanvas) Clear() error {
	return g.ClearWithColor(0)
}

// Clear erases g using color.
func (g *GDIPlusCanvas) ClearWithColor(color win.ARGB) error {
	if status := win.GdipGraphicsClear(g.gpGraphics, color); status != win.Ok {
		return newError(fmt.Sprintf("GdipGraphicsClear failed with status '%s'", status))
	}

	return nil
}

// DrawBitmap draws bmp into g, scaling bmp within the bounds specified by rect.
func (g *GDIPlusCanvas) DrawBitmap(bmp *Bitmap, rect Rectangle) error {
	if bmp == nil || bmp.hBmp == 0 {
		return os.ErrInvalid
	}

	gbmp, err := NewGDIPlusBitmapFromHBITMAP(bmp.hBmp)
	if err != nil {
		return err
	}
	defer gbmp.Dispose()

	return g.DrawGDIPlusBitmap(gbmp, rect)
}

// DrawBitmapWithSourceRectangle draws the portion of bmp within srcRect into
// g, scaling bmp to fit within the bounds specified by dstRect.
func (g *GDIPlusCanvas) DrawBitmapWithSourceRectangle(bmp *Bitmap, dstRect, srcRect Rectangle) error {
	if bmp == nil || bmp.hBmp == 0 {
		return os.ErrInvalid
	}

	gbmp, err := NewGDIPlusBitmapFromHBITMAP(bmp.hBmp)
	if err != nil {
		return err
	}
	defer gbmp.Dispose()

	return g.DrawGDIPlusBitmapWithSourceRectangle(gbmp, dstRect, srcRect)
}

// DrawBitmap draws bmp into g, scaling bmp within the bounds specified by rect.
func (g *GDIPlusCanvas) DrawGDIPlusBitmap(bmp *GDIPlusBitmap, rect Rectangle) error {
	if status := win.GdipDrawImageRectI(g.gpGraphics, bmp.gpImage(), int32(rect.X), int32(rect.Y), int32(rect.Width), int32(rect.Height)); status != win.Ok {
		return newError(fmt.Sprintf("GdipDrawImageRectI failed with status '%s'", status))
	}

	return nil
}

// DrawBitmapWithSourceRectangle draws the portion of bmp within srcRect into
// g, scaling bmp to fit within the bounds specified by dstRect.
func (g *GDIPlusCanvas) DrawGDIPlusBitmapWithSourceRectangle(bmp *GDIPlusBitmap, dstRect, srcRect Rectangle) error {
	if status := win.GdipDrawImageRectRectI(g.gpGraphics, bmp.gpImage(), int32(dstRect.X), int32(dstRect.Y), int32(dstRect.Width), int32(dstRect.Height), int32(srcRect.X), int32(srcRect.Y), int32(srcRect.Width), int32(srcRect.Height), win.UnitPixel, nil, 0, 0); status != win.Ok {
		return newError(fmt.Sprintf("GdipDrawImageRectRectI failed with status '%s'", status))
	}

	return nil
}

// DrawText draws text into g, laying out within rect using font, strFmt, and
// brush. strFmt controls how the text is laid out within rect, though it may be
// set to nil. For more information about the semantics of this method, consult
// the [Microsoft documentation].
//
// [Microsoft documentation]: https://web.archive.org/web/20230203174316/https://learn.microsoft.com/en-us/windows/win32/api/gdiplusgraphics/nf-gdiplusgraphics-graphics-drawstring%28constwchar_int_constfont_constrectf__conststringformat_constbrush%29
func (g *GDIPlusCanvas) DrawText(text string, rect Rectangle, font *GDIPlusFont, strFmt *GDIPlusStringFormat, brush *GDIPlusBrush) error {
	utf16Text, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}

	rectf := win.GpRectF{
		X:      float32(rect.X),
		Y:      float32(rect.Y),
		Width:  float32(rect.Width),
		Height: float32(rect.Height),
	}

	var useFmt *win.GpStringFormat
	if strFmt != nil {
		useFmt = strFmt.gpStringFormat
	}

	if status := win.GdipDrawString(g.gpGraphics, &utf16Text[0], int32(len(utf16Text)-1), font.gpFont, &rectf, useFmt, brush.gpBrush); status != win.Ok {
		return newError(fmt.Sprintf("GdipDrawString failed with status '%s'", status))
	}

	return nil
}

// FillEllipse draws a filled ellipse bounded by rect into g using brush.
func (g *GDIPlusCanvas) FillEllipse(brush *GDIPlusBrush, rect Rectangle) error {
	if status := win.GdipFillEllipseI(g.gpGraphics, brush.gpBrush, int32(rect.X), int32(rect.Y), int32(rect.Width), int32(rect.Height)); status != win.Ok {
		return newError(fmt.Sprintf("GdipFillEllipse failed with status '%s'", status))
	}

	return nil
}

// NewCompatibleBitmap creates a new GDIPlusBitmap with size whose format is
// compatible with g.
func (g *GDIPlusCanvas) NewCompatibleBitmap(size Size) (*GDIPlusBitmap, error) {
	result := &GDIPlusBitmap{}
	if status := win.GdipCreateBitmapFromGraphics(int32(size.Width), int32(size.Height), g.gpGraphics, &result.gpBitmap); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipCreateBitmapFromGraphics failed with status '%s'", status))
	}

	return result, nil
}

// ResetClip resets any clipping region associated with g.
func (g *GDIPlusCanvas) ResetClip() error {
	if status := win.GdipResetClip(g.gpGraphics); status != win.Ok {
		return newError(fmt.Sprintf("GdipResetClip failed with status '%s'", status))
	}

	return nil
}

// SetClipPath adds path to g's clipping region. combineMode specifies how the
// path should be combined with the existing clipping region. For more
// information about the semantics of combineMode, consult the [Microsoft documentation].
//
// [Microsoft documentation]: https://web.archive.org/web/20230206194140/https://learn.microsoft.com/en-us/windows/win32/api/gdiplusenums/ne-gdiplusenums-combinemode
func (g *GDIPlusCanvas) SetClipPath(path *GDIPlusPath, combineMode win.CombineMode) error {
	if status := win.GdipSetClipPath(g.gpGraphics, path.gpPath, combineMode); status != win.Ok {
		return newError(fmt.Sprintf("GdipSetClipPath failed with status '%s'", status))
	}

	return nil
}

// GetCompositingMode obtains the current compositing mode associated with g.
func (g *GDIPlusCanvas) GetCompositingMode() (result win.CompositingMode, _ error) {
	if status := win.GdipGetCompositingMode(g.gpGraphics, &result); status != win.Ok {
		return result, newError(fmt.Sprintf("GdipGetCompositingMode failed with status '%s'", status))
	}

	return result, nil
}

// SetCompositingMode sets g's compositing mode to compositingMode.
// For more information about the semantics of this method, consult the [Microsoft documentation].
//
// [Microsoft documentation]: https://web.archive.org/web/20230203174615/https://learn.microsoft.com/en-us/windows/win32/api/gdiplusgraphics/nf-gdiplusgraphics-graphics-setcompositingmode
func (g *GDIPlusCanvas) SetCompositingMode(compositingMode win.CompositingMode) error {
	if status := win.GdipSetCompositingMode(g.gpGraphics, compositingMode); status != win.Ok {
		return newError(fmt.Sprintf("GdipSetCompositingMode failed with status '%s'", status))
	}

	return nil
}

// SetCompositingQuality sets g's compositing quality to compositingQuality.
// For more information about the semantics of this method, consult the [Microsoft documentation].
//
// [Microsoft documentation]: https://web.archive.org/web/20230203174639/https://learn.microsoft.com/en-us/windows/win32/api/gdiplusgraphics/nf-gdiplusgraphics-graphics-setcompositingquality
func (g *GDIPlusCanvas) SetCompositingQuality(compositingQuality win.CompositingQuality) error {
	if status := win.GdipSetCompositingQuality(g.gpGraphics, compositingQuality); status != win.Ok {
		return newError(fmt.Sprintf("GdipSetCompositingQuality failed with status '%s'", status))
	}

	return nil
}

// SetInterpolationMode sets g's interpolation mode to interpolationMode.
// For more information about the semantics of this method, consult the [Microsoft documentation].
//
// [Microsoft documentation]: https://web.archive.org/web/20230203174657/https://learn.microsoft.com/en-us/windows/win32/api/gdiplusgraphics/nf-gdiplusgraphics-graphics-setinterpolationmode
func (g *GDIPlusCanvas) SetInterpolationMode(interpolationMode win.InterpolationMode) error {
	if status := win.GdipSetInterpolationMode(g.gpGraphics, interpolationMode); status != win.Ok {
		return newError(fmt.Sprintf("GdipSetInterpolationMode failed with status '%s'", status))
	}

	return nil
}

// SetPixelOffsetMode sets g's pixel offset mode to pixelOffsetMode.
// For more information about the semantics of this method, consult the [Microsoft documentation].
//
// [Microsoft documentation]: https://web.archive.org/web/20230203174728/https://learn.microsoft.com/en-us/windows/win32/api/gdiplusgraphics/nf-gdiplusgraphics-graphics-setpixeloffsetmode
func (g *GDIPlusCanvas) SetPixelOffsetMode(pixelOffsetMode win.PixelOffsetMode) error {
	if status := win.GdipSetPixelOffsetMode(g.gpGraphics, pixelOffsetMode); status != win.Ok {
		return newError(fmt.Sprintf("GdipSetPixelOffsetMode failed with status '%s'", status))
	}

	return nil
}

// SetSmoothingMode sets g's smoothing mode to smoothingMode.
// For more information about the semantics of this method, consult the [Microsoft documentation].
//
// [Microsoft documentation]: https://web.archive.org/web/20221124215420/https://learn.microsoft.com/en-us/windows/win32/api/gdiplusgraphics/nf-gdiplusgraphics-graphics-setsmoothingmode
func (g *GDIPlusCanvas) SetSmoothingMode(smoothingMode win.SmoothingMode) error {
	if status := win.GdipSetSmoothingMode(g.gpGraphics, smoothingMode); status != win.Ok {
		return newError(fmt.Sprintf("GdipSetSmoothingMode failed with status '%s'", status))
	}

	return nil
}

// SetTextRenderingHint sets g's text rendering hint to hint.
// For more information about the semantics of this method, consult the [Microsoft documentation].
//
// [Microsoft documentation]: https://web.archive.org/web/20230203174813/https://learn.microsoft.com/en-us/windows/win32/api/gdiplusgraphics/nf-gdiplusgraphics-graphics-settextrenderinghint
func (g *GDIPlusCanvas) SetTextRenderingHint(hint win.TextRenderingHint) error {
	if status := win.GdipSetTextRenderingHint(g.gpGraphics, hint); status != win.Ok {
		return newError(fmt.Sprintf("GdipSetTextRenderingHint failed with status '%s'", status))
	}

	return nil
}

// GDIPlusPath encapsulates an instance of a GDI+ path.
type GDIPlusPath struct {
	gpPath *win.GpPath
}

// NewGDIPlusPath creates a new GDIPlusPath using fillMode. For more information
// about fill modes, consult the [Microsoft documentation].
//
// [Microsoft documentation]: https://web.archive.org/web/20230203175246/https://learn.microsoft.com/en-us/windows/win32/api/gdiplusenums/ne-gdiplusenums-fillmode
func NewGDIPlusPath(fillMode win.FillMode) (*GDIPlusPath, error) {
	if err := ensureGDIPlus(); err != nil {
		return nil, err
	}

	result := &GDIPlusPath{}
	if status := win.GdipCreatePath(fillMode, &result.gpPath); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipCreatePath failed with status '%s'", status))
	}

	return result, nil
}

// AddEllipse adds an ellipse bounded by rect to p.
func (p *GDIPlusPath) AddEllipse(rect Rectangle) error {
	if status := win.GdipAddPathEllipseI(p.gpPath, int32(rect.X), int32(rect.Y), int32(rect.Width), int32(rect.Height)); status != win.Ok {
		return newError(fmt.Sprintf("GdipAddPathEllipse failed with status '%s'", status))
	}

	return nil
}

// Dispose frees system resources associated with p.
func (p *GDIPlusPath) Dispose() {
	if win.GdipDeletePath(p.gpPath) == win.Ok {
		p.gpPath = nil
	}
}

// GDIPlusBrush encapsulates and instance of a GDI+ brush.
type GDIPlusBrush struct {
	gpBrush *win.GpBrush
}

// NewGDIPlusSolidBrush creates a new solid brush for color.
func NewGDIPlusSolidBrush(color win.ARGB) (*GDIPlusBrush, error) {
	if err := ensureGDIPlus(); err != nil {
		return nil, err
	}

	var brush *win.GpSolidFill
	if status := win.GdipCreateSolidFill(color, &brush); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipCreateSolidFill failed with status '%s'", status))
	}

	return &GDIPlusBrush{gpBrush: (*win.GpBrush)(brush)}, nil
}

// Dispose frees system resources associated with b.
func (b *GDIPlusBrush) Dispose() {
	if win.GdipDeleteBrush(b.gpBrush) == win.Ok {
		b.gpBrush = nil
	}
}

// MakeARGB creates a win.ARGB representing a 32-bit color from alpha, red,
// green, and blue components.
func MakeARGB(a byte, r byte, g byte, b byte) win.ARGB {
	result := win.ARGB(b)
	result |= win.ARGB(g) << 8
	result |= win.ARGB(r) << 16
	result |= win.ARGB(a) << 24
	return result
}

// GDIPlusBitmap encapsulates an instance of a GDI+ bitmap.
type GDIPlusBitmap struct {
	gpBitmap *win.GpBitmap
}

// NewGDIPlusMemoryBitmap creates a new bitmap using size (in pixels).
// The returned GDIPlusBitmap is formatted as 32 bits per pixel, with
// premultipled alpha.
func NewGDIPlusMemoryBitmap(size Size) (*GDIPlusBitmap, error) {
	return NewGDIPlusMemoryBitmapWithPixelFormat(size, win.PixelFormat32bppPARGB)
}

// NewGDIPlusMemoryBitmapWithPixelFormat creates a new bitmap in the format
// specified by pixelFormat, using size (in pixels).
func NewGDIPlusMemoryBitmapWithPixelFormat(size Size, pixelFormat win.PixelFormat) (*GDIPlusBitmap, error) {
	if err := ensureGDIPlus(); err != nil {
		return nil, err
	}

	// This is the same way that the C++ bindings for GDI+ construct bitmaps.
	result := &GDIPlusBitmap{}
	if status := win.GdipCreateBitmapFromScan0(int32(size.Width), int32(size.Height), 0, pixelFormat, nil, &result.gpBitmap); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipCreateBitmapFromScan0 failed with status '%s'", status))
	}

	return result, nil
}

// NewGDIPlusBitmapFromBitmap creates a GDIPlusBitmap that references the same
// underlying memory as bitmap.
func NewGDIPlusBitmapFromBitmap(bitmap *Bitmap) (*GDIPlusBitmap, error) {
	if bitmap == nil || bitmap.hBmp == 0 {
		return nil, os.ErrInvalid
	}

	return NewGDIPlusBitmapFromHBITMAP(bitmap.hBmp)
}

// NewGDIPlusBitmapFromFile loads the file at filePath and creates a
// GDIPlusBitmap based on its data. The file must be a format supported by GDI+.
func NewGDIPlusBitmapFromFile(filePath string) (*GDIPlusBitmap, error) {
	if err := ensureGDIPlus(); err != nil {
		return nil, err
	}

	utf16FilePath, err := windows.UTF16PtrFromString(filePath)
	if err != nil {
		return nil, err
	}

	result := &GDIPlusBitmap{}
	if status := win.GdipCreateBitmapFromFile(utf16FilePath, &result.gpBitmap); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipCreateBitmapFromFile failed with status '%s' for file '%s'", status, filePath))
	}

	return result, nil
}

// NewGDIPlusBitmapFromHBITMAP creates a GDIPlusBitmap that references the same
// underlying memory as hbitmap.
func NewGDIPlusBitmapFromHBITMAP(hbitmap win.HBITMAP) (*GDIPlusBitmap, error) {
	if err := ensureGDIPlus(); err != nil {
		return nil, err
	}

	result := &GDIPlusBitmap{}
	if status := win.GdipCreateBitmapFromHBITMAP(hbitmap, win.HPALETTE(0), &result.gpBitmap); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipCreateBitmapFromHBITMAP failed with status '%s'", status))
	}

	return result, nil
}

// NewGDIPlusBitmapFromHICON creates a GDIPlusBitmap based on hicon.
func NewGDIPlusBitmapFromHICON(hicon win.HICON) (*GDIPlusBitmap, error) {
	if err := ensureGDIPlus(); err != nil {
		return nil, err
	}

	result := &GDIPlusBitmap{}
	if status := win.GdipCreateBitmapFromHICON(hicon, &result.gpBitmap); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipCreateBitmapFromHICON failed with status '%s'", status))
	}

	return result, nil
}

// NewGDIPlusBitmapFromIcon returns a GDIPlusBitmap based on img, scaled to dpi.
func NewGDIPlusBitmapFromIcon(img Image, dpi int) (*GDIPlusBitmap, error) {
	cachedIcon, err := iconCache.Icon(img, dpi)
	if err != nil {
		return nil, err
	}

	cachedHICON, err := cachedIcon.handleForDPIWithError(dpi)
	if err != nil {
		return nil, err
	}

	return NewGDIPlusBitmapFromHICON(cachedHICON)
}

// NewGDIPlusBitmapFromStream creates a GDIPlusBitmap whose contents are
// initialized from stream. The data in stream must be in a format that is
// supported by GDI+.
func NewGDIPlusBitmapFromStream(stream com.Stream) (*GDIPlusBitmap, error) {
	if err := ensureGDIPlus(); err != nil {
		return nil, err
	}

	result := &GDIPlusBitmap{}
	if status := win.GdipCreateBitmapFromStream(stream.UnsafeUnwrap(), &result.gpBitmap); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipCreateBitmapFromStream failed with status '%s'", status))
	}

	return result, nil
}

func (b *GDIPlusBitmap) gpImage() *win.GpImage {
	return (*win.GpImage)(b.gpBitmap)
}

// Size returns the width and height of b as a walk Size.
func (b *GDIPlusBitmap) Size() (ret Size, _ error) {
	var width, height uint32
	if status := win.GdipGetImageWidth(b.gpImage(), &width); status != win.Ok {
		return ret, newError(fmt.Sprintf("GdipGetImageWidth failed with status '%s'", status))
	}
	if status := win.GdipGetImageHeight(b.gpImage(), &height); status != win.Ok {
		return ret, newError(fmt.Sprintf("GdipGetImageHeight failed with status '%s'", status))
	}

	ret.Width = int(width)
	ret.Height = int(height)
	return ret, nil
}

// Dispose frees system resources associcated with b.
func (b *GDIPlusBitmap) Dispose() {
	if win.GdipDisposeImage(b.gpImage()) == win.Ok {
		b.gpBitmap = nil
	}
}

// Bitmap returns a Walk Bitmap that references the same memory as b.
func (b *GDIPlusBitmap) Bitmap() (*Bitmap, error) {
	hBmp, err := b.HBITMAP()
	if err != nil {
		return nil, err
	}

	dpi := 96
	if br, err := b.GetDPI(); err == nil && br > 0 {
		dpi = br
	}

	return newBitmapFromHBITMAP(hBmp, dpi)
}

// Icon returns a Walk Icon based on b.
func (b *GDIPlusBitmap) Icon() (*Icon, error) {
	hIcon, err := b.HICON()
	if err != nil {
		return nil, err
	}

	dpi := 96
	if br, err := b.GetDPI(); err == nil && br > 0 {
		dpi = br
	}

	return NewIconFromHICONForDPI(hIcon, dpi)
}

// GetDPI returns b's pixel density, if that information is available.
func (b *GDIPlusBitmap) GetDPI() (int, error) {
	var hres float32
	if status := win.GdipGetImageHorizontalResolution(b.gpImage(), &hres); status != win.Ok {
		return 0, newError(fmt.Sprintf("GdipGetImageHorizontalResolution failed with status '%s'", status))
	}

	return int(hres), nil
}

// SetDPI sets b's pixel density to dpi.
func (b *GDIPlusBitmap) SetDPI(dpi int) error {
	if status := win.GdipBitmapSetResolution(b.gpBitmap, float32(dpi), float32(dpi)); status != win.Ok {
		return newError(fmt.Sprintf("GdipBitmapSetResolution failed with status '%s'", status))
	}

	return nil
}

// HBITMAP returns a GDI HBITMAP that references the same memory as b.
func (b *GDIPlusBitmap) HBITMAP() (win.HBITMAP, error) {
	var hBmp win.HBITMAP
	if status := win.GdipCreateHBITMAPFromBitmap(b.gpBitmap, &hBmp, 0); status != win.Ok {
		return 0, newError(fmt.Sprintf("GdipCreateHBITMAPFromBitmap failed with status '%s'", status))
	}

	return hBmp, nil
}

// HICON returns a GDI HICON based on b.
func (b *GDIPlusBitmap) HICON() (win.HICON, error) {
	var hIcon win.HICON
	if status := win.GdipCreateHICONFromBitmap(b.gpBitmap, &hIcon); status != win.Ok {
		return 0, newError(fmt.Sprintf("GdipCreateHICONFromBitmap failed with status '%s'", status))
	}

	return hIcon, nil
}

// GDIPlusFontFamily encapsulates an instance of a GDI+ Font Family.
type GDIPlusFontFamily struct {
	gpFontFamily *win.GpFontFamily
}

// NewGDIPlusFontFamily creates a new GDIPlusFontFamily for name.
func NewGDIPlusFontFamily(name string) (*GDIPlusFontFamily, error) {
	utf16Name, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}

	if err := ensureGDIPlus(); err != nil {
		return nil, err
	}

	result := &GDIPlusFontFamily{}
	if status := win.GdipCreateFontFamilyFromName(utf16Name, nil, &result.gpFontFamily); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipCreateFontFamilyFromName failed with status '%s'", status))
	}

	return result, nil
}

// NewGDIPlusFontFamilySerif creates a new GDIPlusFontFamily for the default
// serif font.
func NewGDIPlusFontFamilySerif() (*GDIPlusFontFamily, error) {
	if err := ensureGDIPlus(); err != nil {
		return nil, err
	}

	result := &GDIPlusFontFamily{}
	if status := win.GdipGetGenericFontFamilySerif(&result.gpFontFamily); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipGetGenericFontFamilySerif failed with status '%s'", status))
	}

	return result, nil
}

// NewGDIPlusFontFamilySerif creates a new GDIPlusFontFamily for the default
// sans serif font.
func NewGDIPlusFontFamilySansSerif() (*GDIPlusFontFamily, error) {
	if err := ensureGDIPlus(); err != nil {
		return nil, err
	}

	result := &GDIPlusFontFamily{}
	if status := win.GdipGetGenericFontFamilySansSerif(&result.gpFontFamily); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipGetGenericFontFamilySansSerif failed with status '%s'", status))
	}

	return result, nil
}

// NewGDIPlusFontFamilySerif creates a new GDIPlusFontFamily for the default
// monospace font.
func NewGDIPlusFontFamilyMonospace() (*GDIPlusFontFamily, error) {
	if err := ensureGDIPlus(); err != nil {
		return nil, err
	}

	result := &GDIPlusFontFamily{}
	if status := win.GdipGetGenericFontFamilyMonospace(&result.gpFontFamily); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipGetGenericFontFamilyMonospace failed with status '%s'", status))
	}

	return result, nil
}

// Dispose frees system resources associated with ff.
func (ff *GDIPlusFontFamily) Dispose() {
	if status := win.GdipDeleteFontFamily(ff.gpFontFamily); status == win.Ok {
		ff.gpFontFamily = nil
	}
}

// GDIPlusFont encapsulates an instance of a GDI+ font.
type GDIPlusFont struct {
	gpFont *win.GpFont
}

// NewGDIPlusGenericFontSerif creates a new GDI font using the default serif
// family, with style, sized using emSize. The unit for emSize is described
// via unit.
func NewGDIPlusGenericFontSerif(style win.FontStyle, emSize float32, unit win.Unit) (*GDIPlusFont, error) {
	fam, err := NewGDIPlusFontFamilySerif()
	if err != nil {
		return nil, err
	}
	defer fam.Dispose()

	return NewGDIPlusFont(fam, style, emSize, unit)
}

// NewGDIPlusGenericFontSansSerif creates a new GDI font using the default sans
// serif family, with style, sized using emSize. The unit for emSize is
// described via unit.
func NewGDIPlusGenericFontSansSerif(style win.FontStyle, emSize float32, unit win.Unit) (*GDIPlusFont, error) {
	fam, err := NewGDIPlusFontFamilySansSerif()
	if err != nil {
		return nil, err
	}
	defer fam.Dispose()

	return NewGDIPlusFont(fam, style, emSize, unit)
}

// NewGDIPlusGenericFontMonospace creates a new GDI font using the default
// monospace family, with style, sized using emSize. The unit for emSize is
// described via unit.
func NewGDIPlusGenericFontMonospace(style win.FontStyle, emSize float32, unit win.Unit) (*GDIPlusFont, error) {
	fam, err := NewGDIPlusFontFamilyMonospace()
	if err != nil {
		return nil, err
	}
	defer fam.Dispose()

	return NewGDIPlusFont(fam, style, emSize, unit)
}

// NewGDIPlusFont creates a new GDI font using family, with style, sized using
// emSize. The unit for emSize is described by unit.
func NewGDIPlusFont(family *GDIPlusFontFamily, style win.FontStyle, emSize float32, unit win.Unit) (*GDIPlusFont, error) {
	result := &GDIPlusFont{}
	if status := win.GdipCreateFont(family.gpFontFamily, emSize, style, unit, &result.gpFont); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipCreateFont failed with status '%s'", status))
	}

	return result, nil
}

// Dispose frees system resources associated with f.
func (f *GDIPlusFont) Dispose() {
	if win.GdipDeleteFont(f.gpFont) == win.Ok {
		f.gpFont = nil
	}
}

// GDIPlusStringFormat encapsulates an instance of a GDI+ string format, which
// is used to control how text is laid out within a bounding rectangle.
type GDIPlusStringFormat struct {
	gpStringFormat *win.GpStringFormat
}

// NewGDIPlusStringFormat creates a new, empty GDIPlusStringFormat.
func NewGDIPlusStringFormat() (*GDIPlusStringFormat, error) {
	return NewGDIPlusStringFormatWithFlags(0, 0)
}

// NewGDIPlusStringFormatWithFlags creates a new GDIPlusStringFormat. flags
// must be a bitwise-OR'd combination of win.StringFormatFlags. langid must
// specify a valid language ID, or 0 to utilize the user's default language.
// For more information about string format flags, consult the [Microsoft documentation].
//
// [Microsoft documentation]: https://web.archive.org/web/20221207175532/https://learn.microsoft.com/en-us/windows/win32/api/gdiplusenums/ne-gdiplusenums-stringformatflags
func NewGDIPlusStringFormatWithFlags(flags win.StringFormatFlags, langid win.LANGID) (*GDIPlusStringFormat, error) {
	if err := ensureGDIPlus(); err != nil {
		return nil, err
	}

	result := &GDIPlusStringFormat{}
	if status := win.GdipCreateStringFormat(flags, langid, &result.gpStringFormat); status != win.Ok {
		return nil, newError(fmt.Sprintf("GdipCreateStringFormat failed with status '%s'", status))
	}

	return result, nil
}

// Dispose frees system sources associated with sf.
func (sf *GDIPlusStringFormat) Dispose() {
	if win.GdipDeleteStringFormat(sf.gpStringFormat) == win.Ok {
		sf.gpStringFormat = nil
	}
}

// SetAlign sets sf's text alignment along the horizontal axis.
func (sf *GDIPlusStringFormat) SetAlign(align win.StringAlignment) error {
	if status := win.GdipSetStringFormatAlign(sf.gpStringFormat, align); status != win.Ok {
		return newError(fmt.Sprintf("GdipSetStringFormatAlign failed with status '%s'", status))
	}

	return nil
}

// SetLineAlign sets sf's text alignment along the vertical axis.
func (sf *GDIPlusStringFormat) SetLineAlign(align win.StringAlignment) error {
	if status := win.GdipSetStringFormatLineAlign(sf.gpStringFormat, align); status != win.Ok {
		return newError(fmt.Sprintf("GdipSetStringFormatLineAlign failed with status '%s'", status))
	}

	return nil
}
