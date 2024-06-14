// Copyright 2010 The Walk Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package walk

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/tailscale/win"
)

type Menu struct {
	hMenu                      win.HMENU
	window                     Window
	actions                    *ActionList
	getDPI                     func() int
	initPopupPublisher         EventPublisher
	sharedMetrics              *menuSharedMetrics  // shared theme metrics across all menus associated with the current window
	perMenuMetrics             menuSpecificMetrics // per-menu metrics
	allowOwnerDrawInvalidation bool
}

func newMenuBar(window Window) (menu *Menu, _ error) {
	hMenu := win.CreateMenu()
	if hMenu == 0 {
		return nil, lastError("CreateMenu")
	}
	defer func() {
		if menu == nil {
			win.DestroyMenu(hMenu)
		}
	}()

	m := &Menu{
		hMenu:  hMenu,
		window: window,
	}

	// For resolveMenu to work, we must set DwMenuData to m.
	mi := win.MENUINFO{
		FMask:      win.MIM_MENUDATA,
		DwMenuData: uintptr(unsafe.Pointer(m)),
	}
	mi.CbSize = uint32(unsafe.Sizeof(mi))

	if !win.SetMenuInfo(hMenu, &mi) {
		return nil, lastError("SetMenuInfo")
	}

	m.actions = newActionList(m)
	menu = m

	return menu, nil
}

func NewMenu() (menu *Menu, _ error) {
	hMenu := win.CreatePopupMenu()
	if hMenu == 0 {
		return nil, lastError("CreatePopupMenu")
	}
	defer func() {
		if menu == nil {
			win.DestroyMenu(hMenu)
		}
	}()

	mi := win.MENUINFO{FMask: win.MIM_STYLE}
	mi.CbSize = uint32(unsafe.Sizeof(mi))

	if !win.GetMenuInfo(hMenu, &mi) {
		return nil, lastError("GetMenuInfo")
	}

	mi.DwStyle |= win.MNS_CHECKORBMP
	mi.DwStyle &= ^uint32(win.MNS_NOCHECK)

	m := &Menu{
		hMenu: hMenu,
	}

	// For resolveMenu to work, we must set DwMenuData to m.
	mi.FMask |= win.MIM_MENUDATA
	mi.DwMenuData = uintptr(unsafe.Pointer(m))

	if !win.SetMenuInfo(hMenu, &mi) {
		return nil, lastError("SetMenuInfo")
	}

	m.actions = newActionList(m)
	menu = m

	return menu, nil
}

// resolveMenu resolves a Walk Menu from an hmenu.
func resolveMenu(hmenu win.HMENU) *Menu {
	mi := win.MENUINFO{FMask: win.MIM_MENUDATA}
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if !win.GetMenuInfo(hmenu, &mi) {
		return nil
	}

	return (*Menu)(unsafe.Pointer(mi.DwMenuData))
}

// InitPopup returns the event that is published when m is about to be displayed
// as a popup menu.
func (m *Menu) InitPopup() *Event {
	return m.initPopupPublisher.Event()
}

func (m *Menu) Dispose() {
	m.actions.Clear()

	if m.hMenu != 0 {
		win.DestroyMenu(m.hMenu)
		m.hMenu = 0
	}
}

func (m *Menu) IsDisposed() bool {
	return m.hMenu == 0
}

// onInitPopup is invoked whenever m is about to be displayed as a popup menu.
// window specifies the parent Window for which the menu is to be shown.
func (m *Menu) onInitPopup(window Window) {
	m.allowOwnerDrawInvalidation = true
	defer func() {
		m.allowOwnerDrawInvalidation = false
	}()
	m.initPopupPublisher.Publish()
	m.perMenuMetrics.reset()
	m.updateItemsForWindow(window)
}

func (m *Menu) Actions() *ActionList {
	return m.actions
}

func (m *Menu) updateItemsForWindow(window Window) {
	if m.window == nil {
		m.window = window
		defer func() {
			m.window = nil
		}()
	}

	var needAccelSpace bool
	var numOwnerDraw int
	var sm *menuSharedMetrics

	for _, action := range m.actions.actions {
		needAccelSpace = needAccelSpace || action.shortcut.Key != 0
		switch {
		case action.ownerDrawInfo != nil:
			if numOwnerDraw == 0 {
				// We've encountered the first owner-drawn item in the menu. Obtain
				// shared metrics from the window theme.
				sm = window.AsWindowBase().menuSharedMetrics()
			}
			action.ownerDrawInfo.sharedMetrics = sm
			action.ownerDrawInfo.perMenuMetrics = &m.perMenuMetrics
			numOwnerDraw++
			fallthrough
		case action.image != nil:
			m.onActionChanged(action)
		}

		if action.menu != nil {
			action.menu.updateItemsForWindow(window)
		}
	}

	if numOwnerDraw > 0 && (needAccelSpace || numOwnerDraw < len(m.actions.actions)) {
		// If we need accelerator space, then we need to measure each item's
		// shortcut text.
		// If we have any owner-drawn items, any remaining actions that are not
		// owner-drawn must be set to owner-drawn via DefaultActionOwnerDrawHandler.
		// Failure to do so would result in non-owner-drawn items being rendered
		// without any theming whatsoever.
		m.actions.forEach(func(a *Action) bool {
			if needAccelSpace {
				defer m.perMenuMetrics.measureAccelTextExtent(m.window, a)
			}
			if a.OwnerDraw() {
				return true
			}
			a.ownerDrawInfo = newOwnerDrawnMenuItemInfo(a, DefaultActionOwnerDrawHandler)
			a.ownerDrawInfo.sharedMetrics = sm
			a.ownerDrawInfo.perMenuMetrics = &m.perMenuMetrics
			m.onActionChanged(a)
			return true
		})
	}
}

func (m *Menu) resolveDPI() int {
	switch {
	case m.getDPI != nil:
		return m.getDPI()
	case m.window != nil:
		return m.window.DPI()
	default:
		return screenDPI()
	}
}

func (m *Menu) initMenuItemInfoFromAction(mii *win.MENUITEMINFO, action *Action) {
	mii.CbSize = uint32(unsafe.Sizeof(*mii))
	mii.FMask = win.MIIM_FTYPE | win.MIIM_ID | win.MIIM_STATE | win.MIIM_DATA
	mii.FType = 0
	mii.DwItemData = 0

	setString := true

	switch {
	case action.ownerDrawInfo != nil:
		mii.FType |= win.MFT_OWNERDRAW
		if m.allowOwnerDrawInvalidation {
			// Terrible hack: owner-drawn items won't be asked to recompute their sizes
			// without specifying win.MIIM_BITMAP with a zero HbmpItem!
			mii.FMask |= win.MIIM_BITMAP
			mii.HbmpItem = 0
		}
		// Setting DwItemData to the pointer to our ownerDrawInfo enables
		// (*WindowBase).WndProc to quickly resolve the menu item being drawn.
		mii.DwItemData = uintptr(unsafe.Pointer(action.ownerDrawInfo))
		setString = false
	case action.image != nil:
		mii.FMask |= win.MIIM_BITMAP
		dpi := m.resolveDPI()
		if bmp, err := iconCache.Bitmap(action.image, dpi); err == nil {
			mii.HbmpItem = bmp.hBmp
		}
	case action.IsSeparator():
		mii.FType |= win.MFT_SEPARATOR
		setString = false
	default:
	}

	if setString {
		mii.FMask |= win.MIIM_STRING
		var text string
		if s := action.shortcut; s.Key != 0 {
			text = fmt.Sprintf("%s\t%s", action.text, s.String())
		} else {
			text = action.text
		}
		mii.DwTypeData = syscall.StringToUTF16Ptr(text)
	}

	mii.WID = uint32(action.id)

	if action.Enabled() {
		mii.FState &^= win.MFS_DISABLED
	} else {
		mii.FState |= win.MFS_DISABLED
	}

	if action.Checked() {
		mii.FState |= win.MFS_CHECKED
	}
	if action.Exclusive() {
		mii.FType |= win.MFT_RADIOCHECK
	}

	menu := action.menu
	if menu != nil {
		mii.FMask |= win.MIIM_SUBMENU
		mii.HSubMenu = menu.hMenu
	}
}

func (m *Menu) handleDefaultState(action *Action) {
	if action.Default() {
		// Unset other default actions before we set this one. Otherwise insertion fails.
		win.SetMenuDefaultItem(m.hMenu, ^uint32(0), false)
		for _, otherAction := range m.actions.actions {
			if otherAction != action {
				otherAction.SetDefault(false)
			}
		}
	}
}

func (m *Menu) onActionChanged(action *Action) error {
	defer m.ensureMenuBarRedrawn()

	m.handleDefaultState(action)

	if !action.Visible() {
		return nil
	}

	var mii win.MENUITEMINFO

	m.initMenuItemInfoFromAction(&mii, action)

	if !win.SetMenuItemInfo(m.hMenu, uint32(m.actions.indexInObserver(action)), true, &mii) {
		return newError("SetMenuItemInfo failed")
	}

	if action.Default() {
		win.SetMenuDefaultItem(m.hMenu, uint32(m.actions.indexInObserver(action)), true)
	}

	if action.Checked() && action.Exclusive() {
		first, last, index, err := m.actions.positionsForExclusiveCheck(action)
		if err != nil {
			return err
		}

		if !win.CheckMenuRadioItem(m.hMenu, uint32(first), uint32(last), uint32(index), win.MF_BYPOSITION) {
			return newError("CheckMenuRadioItem failed")
		}

		if err := m.actions.uncheckActionsForExclusiveCheck(first, last, index); err != nil {
			return err
		}
	}

	return nil
}

func (m *Menu) onActionVisibleChanged(action *Action) error {
	if !action.IsSeparator() {
		defer m.actions.updateSeparatorVisibility()
	}

	if action.Visible() {
		return m.insertAction(action, true)
	}

	return m.removeAction(action, true)
}

func (m *Menu) insertAction(action *Action, visibleChanged bool) (err error) {
	m.handleDefaultState(action)

	if !visibleChanged {
		action.addChangedHandler(m)
		defer func() {
			if err != nil {
				action.removeChangedHandler(m)
			}
		}()
	}

	if !action.Visible() {
		return
	}

	index := m.actions.indexInObserver(action)

	var mii win.MENUITEMINFO

	m.initMenuItemInfoFromAction(&mii, action)

	if !win.InsertMenuItem(m.hMenu, uint32(index), true, &mii) {
		return newError("InsertMenuItem failed")
	}

	if action.Default() {
		win.SetMenuDefaultItem(m.hMenu, uint32(m.actions.indexInObserver(action)), true)
	}

	menu := action.menu
	if menu != nil {
		menu.window = m.window
	}

	m.ensureMenuBarRedrawn()

	return
}

func (m *Menu) removeAction(action *Action, visibleChanged bool) error {
	index := m.actions.indexInObserver(action)

	if !win.RemoveMenu(m.hMenu, uint32(index), win.MF_BYPOSITION) {
		return lastError("RemoveMenu")
	}

	if !visibleChanged {
		action.removeChangedHandler(m)
	}

	m.ensureMenuBarRedrawn()

	return nil
}

func (m *Menu) ensureMenuBarRedrawn() {
	if m.window != nil {
		if mw, ok := m.window.(*MainWindow); ok && mw.menu == m {
			win.DrawMenuBar(mw.Handle())
		}
	}
}

func (m *Menu) onInsertedAction(action *Action) error {
	return m.insertAction(action, false)
}

func (m *Menu) onRemovingAction(action *Action) error {
	return m.removeAction(action, false)
}

func (m *Menu) onClearingActions() error {
	for i := m.actions.Len() - 1; i >= 0; i-- {
		if action := m.actions.At(i); action.Visible() {
			if err := m.onRemovingAction(action); err != nil {
				return err
			}
		}
	}

	return nil
}

// onMnemonic is called when m contains owner-drawn items and its parent Window
// receives a keypress. It enumerates all visible menu items, and if a match is
// found, it returns an action code telling Windows to execute the item specifed
// by positional index.
func (m *Menu) onMnemonic(key Key) (index, action uint16) {
	m.actions.forEachVisible(func(a *Action) bool {
		if odi := a.ownerDrawInfo; odi != nil {
			if aKey := odi.mnemonic; aKey != 0 && aKey == key {
				action = win.MNC_EXECUTE
				return false
			}
		}

		index++
		return true
	})

	return index, action
}
