// Copyright (c) Tailscale Inc. and AUTHORS
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package walk

import (
	"syscall"
	"unsafe"

	"github.com/tailscale/win"
	"golang.org/x/exp/slices"
)

// DefaultOwnerDrawHandler is the ActionOwnerDrawHandler used by owner-drawn
// menu items for emulating the way themed menu items are drawn by the system.
var DefaultOwnerDrawHandler defaultOwnerDrawHandler

// MenuItemMeasureContext is the data passed into an ActionOwnerDrawHandler's
// OnMeasure method to facilitate measurement of an owner-draw menu item.
type MenuItemMeasureContext struct {
	DPI        int
	Theme      *Theme
	Window     Window
	Canvas     *Canvas
	NormalFont *Font
	BoldFont   *Font
	ThemeFont  *Font // The Font that the theme expects to be used for this item in its current state.
	Padding    int   // Theme-compliant spacing that may be used for positioning between sub-components of the menu content.
}

// MenuItemDrawContext is the data passed into an ActionOwnerDrawHandler's
// OnDraw method to facilitate drawing of an owner-draw menu item.
type MenuItemDrawContext struct {
	Action       uint32 // Drawing action requested by Windows. win.ODA_* constants.
	State        uint32 // Menu item state provided by Windows. win.ODS_* constants.
	Theme        *Theme
	ThemeStateID int32 // State ID to use when calling any methods on Theme.
	Window       Window
	Canvas       *Canvas
	NormalFont   *Font
	BoldFont     *Font
	ThemeFont    *Font     // The Font that the theme expects to be used for this item in its current state.
	Rectangle    Rectangle // Bounds of the content within Canvas.
	Padding      int       // Theme-compliant spacing that may be used for positioning between sub-components of the menu content.
}

// menuItemLayout contains the computed bounds for each component of an
// owner-drawn menu.
type menuItemLayout struct {
	contentSize         win.SIZE
	combinedContentSize win.SIZE

	checkboxRect    win.RECT
	checkboxBgRect  win.RECT
	contentRect     win.RECT
	gutterRect      win.RECT
	selectionRect   win.RECT
	separatorRect   win.RECT
	chevronRect     win.RECT
	chevronClipRect win.RECT
}

// menuSpecificMetrics contains per-menu (as opposed to per-item) metrics.
type menuSpecificMetrics struct {
	maxAccelTextExtent win.SIZE
}

func (am *menuSpecificMetrics) reset() {
	am.maxAccelTextExtent = win.SIZE{}
}

// measureAccelTextExtent measures the size, in pixels, of the right-justified
// text that will be drawn in the menu item for the item's Shortcut.
func (am *menuSpecificMetrics) measureAccelTextExtent(window Window, action *Action) {
	if action.shortcut.Key == 0 {
		// This action does not have a Shortcut, so don't bother measuring it.
		return
	}

	wb := window.AsWindowBase()
	sm := wb.menuSharedMetrics()

	theme, err := window.ThemeForClass(win.VSCLASS_MENU)
	if err != nil {
		return
	}

	canvas, err := newCanvasFromWindow(wb)
	if err != nil {
		return
	}
	defer canvas.Dispose()

	font := sm.fontNormal
	if action.Default() {
		font = sm.fontBold
	}

	extent, err := theme.textExtent(canvas, font, win.MENU_POPUPITEM, 0, action.shortcut.String(), win.DT_RIGHT|win.DT_SINGLELINE)
	if err != nil {
		return
	}

	// We don't need to track the extents of every single item, just the maximum
	// size across all items.
	am.maxAccelTextExtent.CX = Max(am.maxAccelTextExtent.CX, extent.CX)
	am.maxAccelTextExtent.CY = Max(am.maxAccelTextExtent.CY, extent.CY)
}

// menuSharedMetrics contains the font, margin, and size metrics for all menus
// associated with a specific window and theme.
type menuSharedMetrics struct {
	dpi int

	checkMargins   win.MARGINS // Margins surrounding a check mark
	checkBgMargins win.MARGINS // Margins surrounding checkMargins to provide space for check background
	itemMargins    win.MARGINS // Margins surrounding an item (excluding checkbox)
	contentMargins win.MARGINS // Margins surrounding the item's content
	chevronMargins win.MARGINS // Margins surrounding a submenu chevron

	checkSize           win.SIZE // Size of a check mark
	combinedCheckSize   win.SIZE // Size of a check mark, plus margins
	combinedCheckBgSize win.SIZE // combinedCheckSize, plus check background margins

	chevronSize         win.SIZE // Size of a submenu chevron
	combinedChevronSize win.SIZE // Size of a submenu chevron, plus margins

	separatorSize         win.SIZE // Size of a separator
	combinedSeparatorSize win.SIZE // Size of a separator, plus margins

	fontNormal *Font
	fontBold   *Font
}

// DPI returns the pixel density used for the metrics in sm.
func (sm *menuSharedMetrics) DPI() int {
	return sm.dpi
}

// CopyForDPI creates a new menuSharedMetrics whose contents have been scaled
// for use at dpi. sm is expected to be 96dpi (100%).
func (sm *menuSharedMetrics) CopyForDPI(dpi int) *menuSharedMetrics {
	if sm.dpi != 96 {
		panic("CopyForDPI should only be called on menuSharedMetrics at 96dpi!")
	}

	result := &menuSharedMetrics{
		dpi:                   dpi,
		checkMargins:          MARGINSFrom96DPI(sm.checkMargins, dpi),
		checkBgMargins:        MARGINSFrom96DPI(sm.checkBgMargins, dpi),
		itemMargins:           MARGINSFrom96DPI(sm.itemMargins, dpi),
		contentMargins:        MARGINSFrom96DPI(sm.contentMargins, dpi),
		chevronMargins:        MARGINSFrom96DPI(sm.chevronMargins, dpi),
		checkSize:             SIZEFrom96DPI(sm.checkSize, dpi),
		combinedCheckSize:     SIZEFrom96DPI(sm.combinedCheckSize, dpi),
		combinedCheckBgSize:   SIZEFrom96DPI(sm.combinedCheckBgSize, dpi),
		chevronSize:           SIZEFrom96DPI(sm.chevronSize, dpi),
		combinedChevronSize:   SIZEFrom96DPI(sm.combinedChevronSize, dpi),
		separatorSize:         SIZEFrom96DPI(sm.separatorSize, dpi),
		combinedSeparatorSize: SIZEFrom96DPI(sm.combinedSeparatorSize, dpi),
		fontNormal:            sm.fontNormal, // DPI scaling handled within Font
		fontBold:              sm.fontBold,   // DPI scaling handled within Font
	}

	return result
}

// newMenuSharedMetrics constructs a new menuSharedMetrics containing
// measurements as they apply to window at 96 (ie, 100%) DPI. Metrics for
// other pixel densities may be obtained by calling CopyForDPI on the metrics
// returned by this function.
func newMenuSharedMetrics(window Window) *menuSharedMetrics {
	sm := &menuSharedMetrics{dpi: 96}

	theme, err := window.ThemeForClass(win.VSCLASS_MENU)
	if err != nil {
		return nil
	}

	sm.separatorSize, err = theme.partSize(win.MENU_POPUPSEPARATOR, 0, nil, win.TS_TRUE)
	if err != nil {
		return nil
	}

	sm.checkSize, err = theme.partSize(win.MENU_POPUPCHECK, 0, nil, win.TS_TRUE)
	if err != nil {
		return nil
	}

	borderSize, err := theme.Integer(win.MENU_POPUPITEM, 0, win.TMT_BORDERSIZE)
	if err != nil {
		return nil
	}

	bgBorderSize, err := theme.Integer(win.MENU_POPUPBACKGROUND, 0, win.TMT_BORDERSIZE)
	if err != nil {
		return nil
	}

	sm.checkMargins, err = theme.margins(win.MENU_POPUPCHECK, 0, win.TMT_CONTENTMARGINS, nil)
	if err != nil {
		return nil
	}

	sm.checkBgMargins, err = theme.margins(win.MENU_POPUPCHECKBACKGROUND, 0, win.TMT_CONTENTMARGINS, nil)
	if err != nil {
		return nil
	}

	sm.itemMargins, err = theme.margins(win.MENU_POPUPITEM, 0, win.TMT_CONTENTMARGINS, nil)
	if err != nil {
		return nil
	}

	sm.chevronMargins, err = theme.margins(win.MENU_POPUPSUBMENU, 0, win.TMT_CONTENTMARGINS, nil)
	if err != nil {
		return nil
	}

	sm.chevronSize, err = theme.partSize(win.MENU_POPUPSUBMENU, 0, nil, win.TS_TRUE)
	if err != nil {
		return nil
	}

	sm.fontNormal, err = theme.SysFont(win.TMT_MENUFONT)
	if err != nil {
		return nil
	}

	// A menu's default item is expected to be drawn using bold text.
	// Themes do not provide a specific bold font for menus, so we make one by
	// adjusting fontNormal.
	lf := sm.fontNormal.LOGFONTForDPI(96)
	if lf == nil {
		return nil
	}

	lf.LfWeight = win.FW_BOLD
	sm.fontBold, err = newFontFromLOGFONT(lf, 96)
	if err != nil {
		return nil
	}

	sm.combinedChevronSize = sm.chevronSize
	addMargins(&sm.combinedChevronSize, sm.chevronMargins)

	sm.contentMargins = sm.itemMargins
	sm.contentMargins.LeftWidth = bgBorderSize
	sm.contentMargins.RightWidth = borderSize

	sm.combinedCheckSize = sm.checkSize
	addMargins(&sm.combinedCheckSize, sm.checkMargins)

	sm.combinedCheckBgSize = sm.combinedCheckSize
	addMargins(&sm.combinedCheckBgSize, sm.checkBgMargins)

	sm.combinedSeparatorSize = sm.separatorSize
	addMargins(&sm.combinedSeparatorSize, sm.itemMargins)

	return sm
}

// measure measures an entire menu item, delegating measurement of the content
// area to odi.handler.onMeasure. This allows Walk to handle the measurement of
// all common menu features (backgrounds, checkboxes, margins, chevrons, etc.)
// while enabling the application to focus only on measuring its custom content.
func (ml *menuItemLayout) measure(w Window, odi *ownerDrawnMenuItemInfo) (uint32, uint32) {
	sm := odi.sharedMetrics

	if odi.action.IsSeparator() {
		return uint32(sm.combinedSeparatorSize.CX), uint32(sm.combinedSeparatorSize.CY)
	}

	theme, err := w.ThemeForClass(win.VSCLASS_MENU)
	if err != nil {
		return 0, 0
	}

	wb := w.AsWindowBase()
	canvas, err := newCanvasFromWindow(wb)
	if err != nil {
		return 0, 0
	}
	defer canvas.Dispose()

	// Ask the ActionOwnerDrawHandler for its custom content's measurements.
	mctx := MenuItemMeasureContext{
		DPI:        odi.resolveDPI(),
		Theme:      theme,
		Window:     w,
		Canvas:     canvas,
		NormalFont: sm.fontNormal,
		BoldFont:   sm.fontBold,
		Padding:    int(sm.contentMargins.LeftWidth),
	}

	if odi.action.Default() {
		mctx.ThemeFont = sm.fontBold
	} else {
		mctx.ThemeFont = sm.fontNormal
	}

	contentCX, contentCY := odi.handler.OnMeasure(odi.action, &mctx)

	// Add accelerator text into the content size.
	mm := odi.perMenuMetrics
	if mm.maxAccelTextExtent.CX > 0 {
		// The metrics for spacing between the end of menu text and the beginning
		// of accelerator text are undocumented. A decent heuristic seems to be to
		// make that space equal to the width of the widest accelerator text in
		// the menu (hence multiplying mm.maxAccelTextExtent.CX by 2: one copy for
		// the spacer, one copy for the text itself).
		contentCX += 2 * uint32(mm.maxAccelTextExtent.CX)
		contentCY = Max(contentCY, uint32(mm.maxAccelTextExtent.CY))
	}

	ml.contentSize.CX = int32(contentCX)
	ml.contentSize.CY = int32(contentCY)

	// Add margins to the content size.
	ml.combinedContentSize = ml.contentSize
	addMargins(&ml.combinedContentSize, sm.contentMargins)

	// Add the width of a submenu chevron (even when not a submenu).
	ml.combinedContentSize.CX += sm.combinedChevronSize.CX

	// combinedContentItemSize is the accumulated size of everything to the right
	// of the checkbox area.
	combinedContentItemSize := ml.combinedContentSize
	addMargins(&combinedContentItemSize, sm.itemMargins)

	// Start with the width of the entire checkbox area, including background,
	// and then add in the width of the rest of the menu item.
	cx := uint32(sm.combinedCheckBgSize.CX + combinedContentItemSize.CX)

	// On the Y-axis, we want the maxiumum height across checkbox, content, and chevron.
	cy := uint32(Max(sm.combinedCheckBgSize.CY, combinedContentItemSize.CY, sm.combinedChevronSize.CY))

	return cx, cy
}

// layout takes the bounds of the menu item, as specified by rect, and positions
// common menu item features within that rect.
func (ml *menuItemLayout) layout(sm *menuSharedMetrics, rect *win.RECT) {
	// The selection rect is simply the entire menu item.
	ml.selectionRect = *rect

	x := rect.Left
	y := rect.Top
	h := rect.Height()

	// Checkbox background: Leftmost item, centered vertically.
	offsetVCenter := (h - sm.combinedCheckBgSize.CY) / 2
	ml.checkboxBgRect = win.RECT{x, y + offsetVCenter, x + sm.combinedCheckBgSize.CX, y + sm.combinedCheckBgSize.CY + offsetVCenter}
	x += ml.checkboxBgRect.Width()
	stripMargins(&ml.checkboxBgRect, sm.checkBgMargins)

	// Checkbox: Rendered overtop of checkbox background. Just strip margins
	// from checkboxBgRect to obtain the checkboxRect.
	ml.checkboxRect = ml.checkboxBgRect
	stripMargins(&ml.checkboxRect, sm.checkMargins)

	// Gutter: Background extending from the left of the item, across the checkbox
	// background and the left content margins. Full height.
	x += sm.contentMargins.LeftWidth
	ml.gutterRect = win.RECT{rect.Left, y, x, y + h}

	// Separator: Starts to the right of gutter, extends all the way to the right.
	// Centered vertically.
	offsetVCenter = (h - sm.combinedSeparatorSize.CY) / 2
	ml.separatorRect = win.RECT{x, y + offsetVCenter, rect.Right, rect.Bottom + offsetVCenter}
	stripMargins(&ml.separatorRect, sm.itemMargins)

	// Content: Start to the right of gutter, extend all the way to the right.
	// Center vertically, then strip margins.
	offsetVCenter = (h - ml.combinedContentSize.CY) / 2
	ml.contentRect = win.RECT{x, y + offsetVCenter, rect.Right, y + ml.combinedContentSize.CY + offsetVCenter}
	stripMargins(&ml.contentRect, sm.contentMargins)

	// Chevron: Rightmost item, centered vertically.
	offsetVCenter = (h - sm.combinedChevronSize.CY) / 2
	ml.chevronClipRect = win.RECT{rect.Right - sm.combinedChevronSize.CX, y + offsetVCenter, rect.Right, y + sm.combinedChevronSize.CY + offsetVCenter}
	ml.chevronRect = ml.chevronClipRect
	stripMargins(&ml.chevronRect, sm.chevronMargins)
}

// ownerDrawnMenuItemInfo is the per-item data that must be associated with any
// menu item.
type ownerDrawnMenuItemInfo struct {
	win.MSAAMENUINFO // must embed MSAAMENUINFO for proper a11y support
	prevText         string
	action           *Action
	handler          ActionOwnerDrawHandler
	sharedMetrics    *menuSharedMetrics
	perMenuMetrics   *menuSpecificMetrics
	layout           menuItemLayout
	mnemonic         Key
}

// newOwnerDrawnMenuItemInfo instantiates an ownerDrawnMenuItemInfo and sets up
// the association between action and handler, the latter of which performs the
// actual measurement and drawing.
func newOwnerDrawnMenuItemInfo(action *Action, handler ActionOwnerDrawHandler) *ownerDrawnMenuItemInfo {
	result := &ownerDrawnMenuItemInfo{
		MSAAMENUINFO: win.MSAAMENUINFO{
			MSAASignature: win.MSAA_MENU_SIG,
		},
		action:  action,
		handler: handler,
	}

	result.updateText()
	action.addChangedHandler(result)

	return result
}

// updateText synchronizes odi's a11y text and keyboard mnemonics with
// odi.action.text.
func (odi *ownerDrawnMenuItemInfo) updateText() {
	if odi.action.text == odi.prevText {
		return
	}

	odi.prevText = odi.action.text

	textUTF16 := syscall.StringToUTF16(odi.action.text)
	textUTF16 = odi.updateMnemonic(textUTF16)
	if len(textUTF16) == 0 {
		odi.MSAAMENUINFO.TextLenExclNul = 0
		odi.MSAAMENUINFO.Text = nil
		return
	}

	odi.MSAAMENUINFO.TextLenExclNul = uint32(len(textUTF16) - 1)
	odi.MSAAMENUINFO.Text = &textUTF16[0]
}

func (odi *ownerDrawnMenuItemInfo) updateMnemonic(textUTF16 []uint16) (result []uint16) {
	odi.mnemonic, result = stripMnemonic(textUTF16)
	return result
}

// stripMnemonic searches the menu text for the first '&'-prefixed character
// (if present) and then returns that character's virtual key code as the
// mnemonic. textUTF16 is stripped of all ampersands used for escaping mnemonics
// and is also returned.
func stripMnemonic(textUTF16 []uint16) (newMnemonic Key, _ []uint16) {
	var maybeMnemonic bool
	var stripIdx []int

	for i, p := range textUTF16 {
		if maybeMnemonic {
			maybeMnemonic = false
			stripIdx = append(stripIdx, i-1)
			if p == '&' {
				continue
			}
			// Only the first valid mnemonic in the string will be returned as
			// newMnemonic, however we still continue the loop to strip out any
			// remaining ampersands.
			if newMnemonic != 0 {
				continue
			}
			// Convert the UTF-16 code unit into a virtual key code.
			vkInfo := win.VkKeyScan(p)
			if vkInfo != -1 {
				// The virtual key code is in lower byte of vkInfo.
				newMnemonic = Key(vkInfo & 0xFF)
			}
		} else if p == '&' {
			maybeMnemonic = true
		}
	}

	// Strip out any ampersands that we recorded above. The values in stripIdx are
	// sorted in descending order, so we scan the slice in reverse so that lower
	// indices are not invalidated as we delete.
	for i := len(stripIdx) - 1; i >= 0; i-- {
		j := stripIdx[i]
		textUTF16 = slices.Delete(textUTF16, j, j+1)
	}

	return newMnemonic, textUTF16
}

func (odi *ownerDrawnMenuItemInfo) onActionChanged(action *Action) error {
	odi.updateText()
	return nil
}

func (odi *ownerDrawnMenuItemInfo) onActionVisibleChanged(action *Action) error {
	return nil
}

func (odi *ownerDrawnMenuItemInfo) resolveDPI() int {
	dpi := 96
	if odi != nil && odi.action != nil && odi.action.menu != nil {
		dpi = odi.action.menu.resolveDPI()
	}
	return dpi
}

func (odi *ownerDrawnMenuItemInfo) onMeasure(w Window, mis *win.MEASUREITEMSTRUCT) {
	mis.ItemWidth, mis.ItemHeight = odi.layout.measure(w, odi)
}

// addMargins accumulates the total width and height of m into sz.
func addMargins(sz *win.SIZE, m win.MARGINS) {
	sz.CX += m.LeftWidth + m.RightWidth
	sz.CY += m.TopHeight + m.BottomHeight
}

// stripMargins adjusts the bounding box specified by r by removing the margins
// specified by m. The resulting bounding box is centered within the initial
// bounding box.
func stripMargins(r *win.RECT, m win.MARGINS) {
	r.Left += m.LeftWidth
	r.Top += m.TopHeight
	r.Right -= m.RightWidth
	r.Bottom -= m.BottomHeight
}

// themeStates holds the uxtheme part states for the various components of the
// menu item.
type themeStates struct {
	checkBg int32
	checkFg int32 // checkFg is ignored unless checked == true
	checked bool
	item    int32
	chevron int32
}

// itemStateToThemeStates takes the menu item's state from a win.DRAWITEMSTRUCT
// and converts it to the theme states for each sub-component of a menu item.
// These values derived from the vsstyle constants defined in the win package.
func (odi *ownerDrawnMenuItemInfo) itemStateToThemeStates(state uint32) (result themeStates) {
	result.checked = (state & win.ODS_CHECKED) != 0
	disabled := (state & (win.ODS_DISABLED | win.ODS_GRAYED)) != 0
	hot := (state & (win.ODS_HOTLIGHT | win.ODS_SELECTED)) != 0

	result.item = win.MPI_NORMAL
	result.chevron = win.MSM_NORMAL

	if hot {
		result.item++
	}
	if disabled {
		result.chevron = win.MSM_DISABLED
		// An item's disabled state is offset by 2 from its enabled state.
		result.item += 2
	}

	if !result.checked {
		return result
	}

	checkFg := int32(win.MC_CHECKMARKNORMAL)
	if odi.action.Exclusive() {
		checkFg = win.MC_BULLETNORMAL
	}

	if disabled {
		result.checkBg = win.MCB_DISABLED
		// Foreground disabled state is the normal state, plus one.
		checkFg++
	} else {
		result.checkBg = win.MCB_NORMAL
	}

	result.checkFg = checkFg
	return result
}

// onDraw draws an entire menu item, delegating rendering of the content area
// to odi.handler.onDraw. This allows walk to handle the layout of all common
// menu features (backgrounds, checkboxes, margins etc) while enabling the
// application to focus only on rendering its custom content.
func (odi *ownerDrawnMenuItemInfo) onDraw(w Window, dis *win.DRAWITEMSTRUCT) {
	sm := odi.sharedMetrics

	odi.layout.layout(sm, &dis.RcItem)

	isSubMenu := odi.action.menu != nil
	if isSubMenu {
		// Windows unconditionally tries to draw an unthemed submenu chevron atop
		// submenu items when they're owner-drawn. We don't want that because
		// we're trying to draw a themed submenu chevron ourselves.
		// We can achieve this by drawing our chevron from within this method,
		// and then excluding the chevron's rect from the DC's clip rect before
		// returning.
		// (Note that we need to do this on dis.HDC, but *after* the buffered paint
		//  blitting that occurs below, so we set up this defer here.)
		cr := odi.layout.chevronClipRect
		defer win.ExcludeClipRect(dis.HDC, cr.Left, cr.Top, cr.Right, cr.Bottom)
	}

	theme, err := w.ThemeForClass(win.VSCLASS_MENU)
	if err != nil {
		return
	}

	bpp := win.BP_PAINTPARAMS{
		Flags: win.BPPF_ERASE,
	}
	bpp.Size = uint32(unsafe.Sizeof(bpp))

	// We need to request a top-down DIB so that the system can utilize alpha
	// blending. We draw into bp instead of dis.HDC. The former is blitted back
	// into the latter when we return from this method.
	// We render to bp using the same coordinates that we would have used with dis.HDC.
	bp, err := beginBufferedPaint(dis.HDC, &dis.RcItem, win.BPBF_TOPDOWNDIB, &bpp)
	if err != nil {
		return
	}
	defer bp.End()

	canvas, err := bp.Canvas()
	if err != nil {
		return
	}
	defer canvas.Dispose()

	dpi := odi.resolveDPI()
	canvas.dpi = dpi

	themeStates := odi.itemStateToThemeStates(dis.ItemState)

	theme.drawBackground(canvas, win.MENU_POPUPBACKGROUND, 0, &dis.RcItem)
	theme.drawBackground(canvas, win.MENU_POPUPGUTTER, 0, &odi.layout.gutterRect)

	if odi.action.IsSeparator() {
		theme.drawBackground(canvas, win.MENU_POPUPSEPARATOR, 0, &odi.layout.separatorRect)
		return
	}

	theme.drawBackground(canvas, win.MENU_POPUPITEM, themeStates.item, &odi.layout.selectionRect)

	if themeStates.checked {
		theme.drawBackground(canvas, win.MENU_POPUPCHECKBACKGROUND, themeStates.checkBg, &odi.layout.checkboxBgRect)
		theme.drawBackground(canvas, win.MENU_POPUPCHECK, themeStates.checkFg, &odi.layout.checkboxRect)
	} else if odi.action.image != nil {
		// Use the same bounds that we'd use for the checkbox.
		if bmp, err := iconCache.Bitmap(odi.action.image, dpi); err == nil {
			canvas.DrawBitmapWithOpacityPixels(bmp, rectangleFromRECT(odi.layout.checkboxRect), 0xff)
		}
	}

	odCtx := MenuItemDrawContext{
		Action:       dis.ItemAction,
		State:        dis.ItemState,
		Theme:        theme,
		ThemeStateID: themeStates.item,
		Window:       w,
		Canvas:       canvas,
		NormalFont:   sm.fontNormal,
		BoldFont:     sm.fontBold,
		Rectangle:    rectangleFromRECT(odi.layout.contentRect),
		Padding:      int(sm.contentMargins.LeftWidth),
	}

	if odi.action.Default() {
		odCtx.ThemeFont = sm.fontBold
	} else {
		odCtx.ThemeFont = sm.fontNormal
	}

	odi.handler.OnDraw(odi.action, &odCtx)

	if isSubMenu {
		theme.drawBackground(canvas, win.MENU_POPUPSUBMENU, themeStates.chevron, &odi.layout.chevronRect)
	}
}

func (odi *ownerDrawnMenuItemInfo) Dispose() {
	odi.MSAAMENUINFO.TextLenExclNul = 0
	odi.MSAAMENUINFO.Text = nil
	odi.action.removeChangedHandler(odi)
	odi.action = nil
	odi.sharedMetrics = nil
	odi.perMenuMetrics = nil
}

type defaultOwnerDrawHandler struct{}

// OnMeasure by default just measures the extents of the menu text.
func (defaultOwnerDrawHandler) OnMeasure(action *Action, mctx *MenuItemMeasureContext) (widthPixels, heightPixels uint32) {
	extent, err := mctx.Theme.textExtent(mctx.Canvas, mctx.ThemeFont, win.MENU_POPUPITEM, 0, action.Text(), win.DT_LEFT|win.DT_SINGLELINE)
	if err == nil {
		widthPixels = uint32(extent.CX)
		heightPixels = uint32(extent.CY)
	}

	return widthPixels, heightPixels
}

// OnDraw by default draws both the menu text and the accelerator text, if any.
func (defaultOwnerDrawHandler) OnDraw(action *Action, dctx *MenuItemDrawContext) {
	flags := uint32(win.DT_LEFT | win.DT_SINGLELINE)
	if (dctx.State & win.ODS_NOACCEL) != 0 {
		flags |= win.DT_HIDEPREFIX
	}

	dctx.Theme.DrawText(dctx.Canvas, dctx.ThemeFont, win.MENU_POPUPITEM, dctx.ThemeStateID, action.Text(), flags, dctx.Rectangle, nil)

	if action.shortcut.Key != 0 {
		flags = win.DT_RIGHT | win.DT_SINGLELINE | win.DT_HIDEPREFIX
		dctx.Theme.DrawText(dctx.Canvas, dctx.ThemeFont, win.MENU_POPUPITEM, dctx.ThemeStateID, action.shortcut.String(), flags, dctx.Rectangle, nil)
	}
}
