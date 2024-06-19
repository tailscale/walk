// Copyright 2011 The Walk Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package walk

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
)

const notifyIconWindowClass = `WalkNotifyIconSink`

var (
	notifyIconIDs          = map[uint16]*NotifyIcon{}
	notifyIconMessageID    uint32
	notifyIcons            = map[*NotifyIcon]struct{}{}
	notifyIconSharedWindow *notifyIconWindow
	taskbarCreatedMsgId    uint32
)

func init() {
	AppendToWalkInit(func() {
		MustRegisterWindowClass(notifyIconWindowClass)
		notifyIconMessageID = mustAllocWindowClassMessage(notifyIconWindowClass)
		taskbarCreatedMsgId = win.RegisterWindowMessage(syscall.StringToUTF16Ptr("TaskbarCreated"))
	})
}

type notifyIconWindow struct {
	WindowBase
	owner *NotifyIcon // nil for non-GUID notifications
}

func (niw *notifyIconWindow) Dispose() {
	niw.owner = nil
	niw.WindowBase.Dispose()
}

func (niw *notifyIconWindow) WndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case notifyIconMessageID:
		lp32 := uint32(lParam)
		ni := niw.owner
		if ni == nil {
			// No GUID, try resolving via integral ID.
			ni = notifyIconIDs[win.HIWORD(lp32)]
			if ni == nil {
				// We don't need to call DefWindowProc because this is an app-defined message.
				return 0
			}
		}

		ni.wndProc(hwnd, win.LOWORD(lp32), wParam)
		// We don't need to call DefWindowProc because this is an app-defined message.
		return 0
	case taskbarCreatedMsgId:
		niw.forIcon(func(ni *NotifyIcon) { ni.reAddToTaskbar() })
	case win.WM_DISPLAYCHANGE:
		// Ensure (0,0) placement so that we always reside on the primary monitor.
		win.SetWindowPos(hwnd, 0, 0, 0, 0, 0, win.SWP_HIDEWINDOW|win.SWP_NOACTIVATE|win.SWP_NOSIZE|win.SWP_NOZORDER)
	case win.WM_DPICHANGED:
		niw.forIcon(func(ni *NotifyIcon) { ni.applyDPI() })
	default:
	}

	return niw.WindowBase.WndProc(hwnd, msg, wParam, lParam)
}

func (niw *notifyIconWindow) forIcon(fn func(*NotifyIcon)) {
	if ni := niw.owner; ni != nil {
		fn(ni)
		return
	}

	// Shared window. Update all icons that have integral IDs.
	for _, ni := range notifyIconIDs {
		fn(ni)
	}
}

func (ni *NotifyIcon) wndProc(hwnd win.HWND, msg uint16, wParam uintptr) {
	defer func() {
		ni.disableShowContextMenu = false
	}()

	switch msg {
	case win.WM_LBUTTONDOWN:
		ni.mouseDownPublisher.Publish(int(win.GET_X_LPARAM(wParam)), int(win.GET_Y_LPARAM(wParam)), LeftButton)

	case win.WM_LBUTTONUP:
		if len(ni.mouseDownPublisher.event.handlers) == 0 && len(ni.mouseUpPublisher.event.handlers) == 0 {
			// If there are no mouse event handlers, then treat WM_LBUTTONUP as
			// a "show context menu" event; this is consistent with Windows 7
			// UX guidelines for notification icons.
			ni.doContextMenu(hwnd, win.GET_X_LPARAM(wParam), win.GET_Y_LPARAM(wParam))
			break
		}

		ni.mouseUpPublisher.Publish(int(win.GET_X_LPARAM(wParam)), int(win.GET_Y_LPARAM(wParam)), LeftButton)

	case win.WM_RBUTTONDOWN:
		// As the result of a right-click we're going to be receiving a
		// WM_CONTEXTMENU message, triggering the context menu. Suppress explicit
		// ShowContextMenu calls to prevent a recursion mess.
		ni.disableShowContextMenu = true
		ni.mouseDownPublisher.Publish(int(win.GET_X_LPARAM(wParam)), int(win.GET_Y_LPARAM(wParam)), RightButton)

	case win.WM_RBUTTONUP:
		// As the result of a right-click we're going to be receiving a
		// WM_CONTEXTMENU message, triggering the context menu. Suppress explicit
		// ShowContextMenu calls to prevent a recursion mess.
		ni.disableShowContextMenu = true
		ni.mouseUpPublisher.Publish(int(win.GET_X_LPARAM(wParam)), int(win.GET_Y_LPARAM(wParam)), RightButton)

	case win.WM_CONTEXTMENU:
		ni.doContextMenu(hwnd, win.GET_X_LPARAM(wParam), win.GET_Y_LPARAM(wParam))

	case win.NIN_BALLOONUSERCLICK:
		ni.reEnableToolTip()
		ni.messageClickedPublisher.Publish()
	}
}

// ShowContextMenu displays ni's context menu at screen coordinates (x, y),
// which are provided when handling a MouseEvent. It is a no-op if called
// while handling right-button mouse events, as the context menu is always
// displayed implicitly in that case. If x or y does not fall within
// ni's bounding rectangle, the infringing coordinate will be adjusted so that
// it does.
func (ni *NotifyIcon) ShowContextMenu(x, y int) {
	if ni.disableShowContextMenu {
		return
	}

	x32, y32 := int32(x), int32(y)

	// Ensure that (x32,y32) is in rect, and adjust if not. Best effort.
	if rect, err := ni.shellIcon.rect(); err == nil {
		// win.RECT Left and Top are inclusive, Right and Bottom are exclusive
		x32 = min(max(x32, rect.Left), rect.Right-1)
		y32 = min(max(y32, rect.Top), rect.Bottom-1)
	}

	ni.doContextMenu(ni.shellIcon.hwnd(), x32, y32)
}

func (ni *NotifyIcon) doContextMenu(hwnd win.HWND, x, y int32) {
	if !ni.showingContextMenuPublisher.Publish() || !ni.contextMenu.Actions().HasVisible() {
		return
	}

	// When calling TrackPopupMenu(Ex) for notification icons, we need to do a
	// little dance to ensure that focus arrives and leaves the context menu
	// correctly. The original source for this information is long gone, but
	// fortunately it was archived.
	// See https://web.archive.org/web/20000205130053/http://support.microsoft.com/support/kb/articles/q135/7/88.asp
	win.SetForegroundWindow(hwnd)

	actionId := uint16(win.TrackPopupMenuEx(
		ni.contextMenu.hMenu,
		win.TPM_NOANIMATION|win.TPM_RETURNCMD,
		x,
		y,
		hwnd,
		nil))

	// See the above comment.
	win.PostMessage(hwnd, win.WM_NULL, 0, 0)

	if actionId != 0 {
		if action, ok := actionsById[actionId]; ok {
			action.raiseTriggered()
		}
	}
}

func isTaskbarPresent() bool {
	var abd win.APPBARDATA
	abd.CbSize = uint32(unsafe.Sizeof(abd))
	return win.SHAppBarMessage(win.ABM_GETTASKBARPOS, &abd) != 0
}

func copyStringToSlice(dst []uint16, src string) error {
	ss, err := syscall.UTF16FromString(src)
	if err != nil {
		return err
	}

	// Reserve final element for nul character.
	copy(dst[:len(dst)-1], ss)
	return nil
}

// notification icons are uniquely identified by the shell via one of two ways:
// either a (win.HWND, uint32) pair, or a GUID. shellNotificationIcon supports
// both ID schemes.
type shellNotificationIcon struct {
	window *notifyIconWindow
	guid   *windows.GUID
	id     *uint32
}

func newNotificationIconWindow() (*notifyIconWindow, error) {
	niw := new(notifyIconWindow)
	niwCfg := windowCfg{
		Window:    niw,
		ClassName: notifyIconWindowClass,
		Style:     win.WS_OVERLAPPEDWINDOW,
		// Always create the window at the origin, thus ensuring that the window
		// resides on the desktop's primary monitor, which is the same monitor where
		// the taskbar notification area resides. This ensures that the window's
		// DPI setting matches that of the notification area.
		Bounds: Rectangle{
			X: 0, Y: 0, Width: 1, Height: 1,
		},
	}
	if err := initWindowWithCfg(&niwCfg); err != nil {
		return nil, err
	}
	return niw, nil
}

// getWindowForNotifyIcon returns an appropriate notifyIconWindow for use with a
// notification icon. When guid is non-nil, a new notifyIconWindow is created.
// When guid is nil, a shared notifyIconWindow is returned. This is necessary
// because the notify icon window procedure can only differentiate between
// uint32 IDs, not GUIDs. In the latter case we need to give each notification
// icon its own window.
func getWindowForNotifyIcon(guid *windows.GUID) (*notifyIconWindow, error) {
	if guid != nil {
		return newNotificationIconWindow()
	}

	if notifyIconSharedWindow == nil {
		niw, err := newNotificationIconWindow()
		if err != nil {
			return nil, err
		}
		notifyIconSharedWindow = niw
	}

	return notifyIconSharedWindow, nil
}

func newShellNotificationIcon(guid *windows.GUID) (*shellNotificationIcon, error) {
	w, err := getWindowForNotifyIcon(guid)
	if err != nil {
		return nil, err
	}

	shellIcon := &shellNotificationIcon{window: w, guid: guid}
	if !isTaskbarPresent() {
		return shellIcon, nil
	}

	if guid != nil {
		// If we're using a GUID, an add operation can fail if a previous instance
		// using this GUID terminated abnormally and its notification icon was left
		// behind on the taskbar. Preemptively delete any pre-existing icon.
		if delCmd := shellIcon.newCmd(win.NIM_DELETE); delCmd != nil {
			// The previous instance would have used a different, now-defunct HWND, so
			// we can't use one here...
			delCmd.nid.HWnd = win.HWND(0)
			// We expect delCmd.execute() to fail if there isn't a pre-existing icon,
			// so no error checking for this call.
			delCmd.execute()
		}
	}

	// Add our notify icon to the status area and make sure it is hidden.
	addCmd := shellIcon.newCmd(win.NIM_ADD)
	addCmd.setCallbackMessage(notifyIconMessageID)
	addCmd.setVisible(false)
	if err := addCmd.execute(); err != nil {
		return nil, err
	}

	return shellIcon, nil
}

func (i *shellNotificationIcon) setOwner(ni *NotifyIcon) {
	// Only icons identified via GUID use the owner field; non-GUID icons share
	// the same window and thus need to be looked up via notifyIconIDs.
	if i.guid == nil {
		return
	}
	i.window.owner = ni
}

func (i *shellNotificationIcon) Dispose() error {
	if cmd := i.newCmd(win.NIM_DELETE); cmd != nil {
		if err := cmd.execute(); err != nil {
			return err
		}
	}

	if i.guid != nil {
		// GUID icons get their own window, so we need to dispose of it.
		i.window.Dispose()
	}

	*i = shellNotificationIcon{}
	return nil
}

func (i *shellNotificationIcon) hwnd() win.HWND {
	if i == nil || i.window == nil {
		return 0
	}
	return i.window.WindowBase.hWnd
}

func (i *shellNotificationIcon) rect() (result win.RECT, err error) {
	nid := win.NOTIFYICONIDENTIFIER{
		CbSize: uint32(unsafe.Sizeof(win.NOTIFYICONIDENTIFIER{})),
	}
	if id := i.id; id != nil {
		nid.HWnd = i.hwnd()
		nid.UID = *id
	} else {
		nid.GuidItem = syscall.GUID(*(i.guid))
	}

	if hr := win.Shell_NotifyIconGetRect(&nid, &result); win.FAILED(hr) {
		return result, errorFromHRESULT("Shell_NotifyIconGetRect", hr)
	}

	return result, nil
}

type niCmd struct {
	shellIcon *shellNotificationIcon
	op        uint32
	nid       win.NOTIFYICONDATA
}

// newCmd creates a niCmd for the specified operation (one of the win.NIM_*
// constants). If i does not yet have a unique identifier and op is not
// win.NIM_ADD, newCmd returns nil.
func (i *shellNotificationIcon) newCmd(op uint32) *niCmd {
	if i.guid == nil && i.id == nil && op != win.NIM_ADD {
		return nil
	}

	cmd := niCmd{
		shellIcon: i,
		op:        op,
		nid: win.NOTIFYICONDATA{
			CbSize: uint32(unsafe.Sizeof(win.NOTIFYICONDATA{})),
			HWnd:   i.hwnd(),
			UFlags: win.NIF_SHOWTIP,
		},
	}

	switch {
	case i.guid != nil:
		cmd.nid.UFlags |= win.NIF_GUID
		cmd.nid.GuidItem = syscall.GUID(*(i.guid))
	case i.id != nil:
		cmd.nid.UID = *(i.id)
	}

	return &cmd
}

func (cmd *niCmd) setBalloonInfo(title, info string, icon interface{}) error {
	if err := copyStringToSlice(cmd.nid.SzInfoTitle[:], title); err != nil {
		return err
	}

	if err := copyStringToSlice(cmd.nid.SzInfo[:], info); err != nil {
		return err
	}

	switch i := icon.(type) {
	case nil:
		cmd.nid.DwInfoFlags = win.NIIF_NONE
	case uint32:
		cmd.nid.DwInfoFlags |= i
	case win.HICON:
		if i == 0 {
			cmd.nid.DwInfoFlags = win.NIIF_NONE
		} else {
			cmd.nid.DwInfoFlags |= win.NIIF_USER
			cmd.nid.HBalloonIcon = i
		}
	default:
		return ErrInvalidType
	}

	cmd.nid.UFlags |= win.NIF_INFO
	// An empty SzInfo buffer implies that we're tearing down (popping?) the
	// balloon. On the other hand, a non-empty SzInfo means that we're showing the
	// balloon and need to hide ToolTips.
	if cmd.nid.SzInfo[0] != 0 {
		// Hide the ToolTip so that it doesn't overlap with the balloon.
		cmd.hideToolTip()
	}
	return nil
}

func (cmd *niCmd) setIcon(icon win.HICON) {
	cmd.nid.HIcon = icon
	cmd.nid.UFlags |= win.NIF_ICON
}

func (cmd *niCmd) hideToolTip() {
	cmd.nid.UFlags &= ^uint32(win.NIF_SHOWTIP)
}

func (cmd *niCmd) setToolTip(tt string) error {
	if err := copyStringToSlice(cmd.nid.SzTip[:], tt); err != nil {
		return err
	}

	cmd.nid.UFlags |= win.NIF_TIP
	return nil
}

func (cmd *niCmd) setCallbackMessage(msg uint32) {
	cmd.nid.UCallbackMessage = msg
	cmd.nid.UFlags |= win.NIF_MESSAGE
}

func (cmd *niCmd) setVisible(v bool) {
	cmd.nid.UFlags |= win.NIF_STATE
	cmd.nid.DwStateMask |= win.NIS_HIDDEN
	if v {
		cmd.nid.DwState &= ^uint32(win.NIS_HIDDEN)
	} else {
		cmd.nid.DwState |= win.NIS_HIDDEN
	}
}

func (cmd *niCmd) execute() error {
	if !win.Shell_NotifyIcon(cmd.op, &cmd.nid) {
		return lastError(fmt.Sprintf("Shell_NotifyIcon(%d, %#v)", cmd.op, cmd.nid))
	}

	if cmd.op != win.NIM_ADD {
		return nil
	}

	if cmd.shellIcon.guid == nil {
		newId := cmd.nid.UID
		cmd.shellIcon.id = &newId
	}

	// When executing an add, we also need to do a NIM_SETVERSION.
	verCmd := *cmd
	verCmd.op = win.NIM_SETVERSION
	// Use Vista+ behaviour.
	verCmd.nid.UVersion = win.NOTIFYICON_VERSION_4
	return verCmd.execute()
}

// NotifyIcon represents an icon in the taskbar notification area.
type NotifyIcon struct {
	shellIcon                   *shellNotificationIcon
	contextMenu                 *Menu
	icon                        Image
	toolTip                     string
	mouseDownPublisher          MouseEventPublisher
	mouseUpPublisher            MouseEventPublisher
	messageClickedPublisher     EventPublisher
	showingContextMenuPublisher ProceedEventPublisher
	disableShowContextMenu      bool
	visible                     bool
}

// NewNotifyIcon creates and returns a new NotifyIcon.
//
// The NotifyIcon is initially invisible.
func NewNotifyIcon() (*NotifyIcon, error) {
	return newNotifyIcon(nil)
}

// NewNotifyIcon creates and returns a new NotifyIcon associated with guid.
//
// The NotifyIcon is initially invisible.
func NewNotifyIconWithGUID(guid windows.GUID) (*NotifyIcon, error) {
	var zeroGUID windows.GUID
	if guid == zeroGUID {
		return nil, os.ErrInvalid
	}
	return newNotifyIcon(&guid)
}

func newNotifyIcon(guid *windows.GUID) (*NotifyIcon, error) {
	shellIcon, err := newShellNotificationIcon(guid)
	if err != nil {
		return nil, err
	}

	// Create and initialize the NotifyIcon already.
	menu, err := NewMenu()
	if err != nil {
		return nil, err
	}
	menu.window = shellIcon.window

	ni := &NotifyIcon{
		shellIcon:   shellIcon,
		contextMenu: menu,
	}

	shellIcon.setOwner(ni)
	menu.getDPI = ni.DPI

	notifyIcons[ni] = struct{}{}
	if ni.shellIcon.id != nil {
		notifyIconIDs[uint16(*(ni.shellIcon.id))] = ni
	}

	return ni, nil
}

func (ni *NotifyIcon) DPI() int {
	return ni.shellIcon.window.DPI()
}

func (ni *NotifyIcon) isDefunct() bool {
	return ni.shellIcon == nil
}

func (ni *NotifyIcon) reAddToTaskbar() {
	// The icon ID may or may not change; save the previous ID so we can properly
	// track this once the add command successfully executes.
	prevID := ni.shellIcon.id

	cmd := ni.shellIcon.newCmd(win.NIM_ADD)
	cmd.setCallbackMessage(notifyIconMessageID)
	cmd.setVisible(ni.visible)
	cmd.setIcon(ni.getHICON(ni.icon))
	if err := cmd.setToolTip(ni.toolTip); err != nil {
		return
	}

	if err := cmd.execute(); err != nil {
		return
	}

	newID := ni.shellIcon.id
	if prevID != nil && (newID == nil || *prevID != *newID) {
		// The ID has changed. Remove defunct prevID from notifyIconIDs.
		delete(notifyIconIDs, uint16(*prevID))
	}
	if newID != nil {
		// Add the new ID
		notifyIconIDs[uint16(*newID)] = ni
	}
}

func (ni *NotifyIcon) reEnableToolTip() error {
	// newCmd always returns a command that, by default, enables ToolTips.
	// All we need to do is create a modify command and execute it.
	cmd := ni.shellIcon.newCmd(win.NIM_MODIFY)
	if cmd == nil {
		return nil
	}

	return cmd.execute()
}

func (ni *NotifyIcon) applyDPI() {
	// Forcibly set the icon even though ni.icon isn't changing. This will force
	// the shell to redraw the icon using the new DPI.
	ni.forciblySetIcon(ni.icon)
}

// Dispose releases the operating system resources associated with the
// NotifyIcon.
//
// The associated Icon is not disposed of.
func (ni *NotifyIcon) Dispose() error {
	if ni.isDefunct() {
		return nil
	}

	// Save the ID now since ni.shellIcon.Dispose() will clear it.
	nid := ni.shellIcon.id
	if err := ni.shellIcon.Dispose(); err != nil {
		return err
	}
	ni.shellIcon = nil

	delete(notifyIcons, ni)
	if nid != nil {
		delete(notifyIconIDs, uint16(*nid))
		if len(notifyIconIDs) == 0 && notifyIconSharedWindow != nil {
			notifyIconSharedWindow.Dispose()
			notifyIconSharedWindow = nil
		}
	}

	return nil
}

func (ni *NotifyIcon) getHICON(icon Image) win.HICON {
	if icon == nil {
		return 0
	}

	dpi := ni.DPI()
	ic, err := iconCache.Icon(icon, dpi)
	if err != nil {
		return 0
	}

	return ic.handleForDPI(dpi)
}

func (ni *NotifyIcon) showMessage(title, info string, iconType uint32, icon Image) error {
	cmd := ni.shellIcon.newCmd(win.NIM_MODIFY)
	if cmd == nil {
		return nil
	}

	switch iconType {
	case win.NIIF_NONE, win.NIIF_INFO, win.NIIF_WARNING, win.NIIF_ERROR:
		if err := cmd.setBalloonInfo(title, info, iconType); err != nil {
			return err
		}
	case win.NIIF_USER:
		if err := cmd.setBalloonInfo(title, info, ni.getHICON(icon)); err != nil {
			return err
		}
	default:
		return os.ErrInvalid
	}

	return cmd.execute()
}

// ShowMessage displays a neutral message balloon above the NotifyIcon.
//
// The NotifyIcon must be visible before calling this method.
func (ni *NotifyIcon) ShowMessage(title, info string) error {
	return ni.showMessage(title, info, win.NIIF_NONE, nil)
}

// ShowInfo displays an info message balloon above the NotifyIcon.
//
// The NotifyIcon must be visible before calling this method.
func (ni *NotifyIcon) ShowInfo(title, info string) error {
	return ni.showMessage(title, info, win.NIIF_INFO, nil)
}

// ShowWarning displays a warning message balloon above the NotifyIcon.
//
// The NotifyIcon must be visible before calling this method.
func (ni *NotifyIcon) ShowWarning(title, info string) error {
	return ni.showMessage(title, info, win.NIIF_WARNING, nil)
}

// ShowError displays an error message balloon above the NotifyIcon.
//
// The NotifyIcon must be visible before calling this method.
func (ni *NotifyIcon) ShowError(title, info string) error {
	return ni.showMessage(title, info, win.NIIF_ERROR, nil)
}

// ShowCustom displays a custom icon message balloon above the NotifyIcon.
// If icon is nil, the main notification icon is used instead of a custom one.
//
// The NotifyIcon must be visible before calling this method.
func (ni *NotifyIcon) ShowCustom(title, info string, icon Image) error {
	return ni.showMessage(title, info, win.NIIF_USER, icon)
}

// ContextMenu returns the context menu of the NotifyIcon.
func (ni *NotifyIcon) ContextMenu() *Menu {
	return ni.contextMenu
}

// Icon returns the Icon of the NotifyIcon.
func (ni *NotifyIcon) Icon() Image {
	return ni.icon
}

// SetIcon sets the Icon of the NotifyIcon.
func (ni *NotifyIcon) SetIcon(icon Image) error {
	if icon == ni.icon {
		return nil
	}

	return ni.forciblySetIcon(icon)
}

// forciblySetIcon sets ni's icon even when icon == ni.icon.
func (ni *NotifyIcon) forciblySetIcon(icon Image) error {
	if icon == nil {
		return os.ErrInvalid
	}

	if cmd := ni.shellIcon.newCmd(win.NIM_MODIFY); cmd != nil {
		cmd.setIcon(ni.getHICON(icon))
		if err := cmd.execute(); err != nil {
			return err
		}
	}

	ni.icon = icon

	return nil
}

// ToolTip returns the tool tip text of the NotifyIcon.
func (ni *NotifyIcon) ToolTip() string {
	return ni.toolTip
}

// SetToolTip sets the tool tip text of the NotifyIcon.
func (ni *NotifyIcon) SetToolTip(toolTip string) error {
	if toolTip == ni.toolTip {
		return nil
	}

	if cmd := ni.shellIcon.newCmd(win.NIM_MODIFY); cmd != nil {
		if err := cmd.setToolTip(toolTip); err != nil {
			return err
		}
		if err := cmd.execute(); err != nil {
			return err
		}
	}

	ni.toolTip = toolTip

	return nil
}

// Visible returns if the NotifyIcon is visible.
func (ni *NotifyIcon) Visible() bool {
	return ni.visible
}

// SetVisible sets if the NotifyIcon is visible.
func (ni *NotifyIcon) SetVisible(visible bool) error {
	if visible == ni.visible {
		return nil
	}

	if cmd := ni.shellIcon.newCmd(win.NIM_MODIFY); cmd != nil {
		cmd.setVisible(visible)
		if err := cmd.execute(); err != nil {
			return err
		}
	}

	ni.visible = visible

	return nil
}

// MouseDown returns the event that is published when a mouse button is pressed
// while the cursor is over the NotifyIcon.
func (ni *NotifyIcon) MouseDown() *MouseEvent {
	return ni.mouseDownPublisher.Event()
}

// MouseDown returns the event that is published when a mouse button is released
// while the cursor is over the NotifyIcon.
func (ni *NotifyIcon) MouseUp() *MouseEvent {
	return ni.mouseUpPublisher.Event()
}

// MessageClicked occurs when the user clicks a message shown with ShowMessage or
// one of its iconed variants.
func (ni *NotifyIcon) MessageClicked() *Event {
	return ni.messageClickedPublisher.Event()
}

// ShowingContextMenu returns the event that is published when ni's context menu
// is going to be shown. Its handlers may return false to prevent the
// context menu from being shown.
func (ni *NotifyIcon) ShowingContextMenu() *ProceedEvent {
	return ni.showingContextMenuPublisher.Event()
}
