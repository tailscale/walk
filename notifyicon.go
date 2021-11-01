// Copyright 2011 The Walk Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package walk

import (
	"os"
	"syscall"
	"unsafe"

	"github.com/lxn/win"
)

var notifyIcons = make(map[*NotifyIcon]bool)

func notifyIconWndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) (result uintptr) {
	// Retrieve our *NotifyIcon from the message window.
	ptr := win.GetWindowLongPtr(hwnd, win.GWLP_USERDATA)
	ni := (*NotifyIcon)(unsafe.Pointer(ptr))

	switch lParam {
	case win.WM_LBUTTONDOWN:
		ni.publishMouseEvent(&ni.mouseDownPublisher, LeftButton)

	case win.WM_LBUTTONUP:
		ni.publishMouseEvent(&ni.mouseUpPublisher, LeftButton)

	case win.WM_RBUTTONDOWN:
		ni.publishMouseEvent(&ni.mouseDownPublisher, RightButton)

	case win.WM_RBUTTONUP:
		ni.publishMouseEvent(&ni.mouseUpPublisher, RightButton)

		win.SendMessage(hwnd, msg, wParam, win.WM_CONTEXTMENU)

	case win.WM_CONTEXTMENU:
		if ni.contextMenu.Actions().Len() == 0 {
			break
		}

		win.SetForegroundWindow(hwnd)

		var p win.POINT
		if !win.GetCursorPos(&p) {
			lastError("GetCursorPos")
		}

		ni.applyDPI()

		actionId := uint16(win.TrackPopupMenuEx(
			ni.contextMenu.hMenu,
			win.TPM_NOANIMATION|win.TPM_RETURNCMD,
			p.X,
			p.Y,
			hwnd,
			nil))
		if actionId != 0 {
			if action, ok := actionsById[actionId]; ok {
				action.raiseTriggered()
			}
		}

		return 0
	case win.NIN_BALLOONUSERCLICK:
		ni.messageClickedPublisher.Publish()
	}

	return win.DefWindowProc(hwnd, msg, wParam, lParam)
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

	copy(dst, ss)
	return nil
}

type shellNotificationIcon struct {
	id   *uint32
	hWnd win.HWND
}

func newShellNotificationIcon(hWnd win.HWND) (*shellNotificationIcon, error) {
	shellIcon := &shellNotificationIcon{hWnd: hWnd}
	if !isTaskbarPresent() {
		return shellIcon, nil
	}

	// Add our notify icon to the status area and make sure it is hidden.
	cmd := shellIcon.newCmd(win.NIM_ADD)
	cmd.setCallbackMessage(notifyIconMessageId)
	cmd.setVisible(false)
	if err := cmd.execute(); err != nil {
		return nil, err
	}

	return shellIcon, nil
}

func (i *shellNotificationIcon) Dispose() error {
	if cmd := i.newCmd(win.NIM_DELETE); cmd != nil {
		if err := cmd.execute(); err != nil {
			return err
		}
	}

	i.id = nil
	i.hWnd = 0
	return nil
}

type niCmd struct {
	shellIcon *shellNotificationIcon
	op        uint32
	nid       win.NOTIFYICONDATA
}

// newCmd creates a niCmd for the specified operation (one of the win.NIM_*
// constants). If the taskbar does not exist, it returns nil.
func (i *shellNotificationIcon) newCmd(op uint32) *niCmd {
	if i.id == nil && op != win.NIM_ADD {
		return nil
	}

	cmd := niCmd{shellIcon: i, op: op, nid: win.NOTIFYICONDATA{HWnd: i.hWnd}}
	cmd.nid.CbSize = uint32(unsafe.Sizeof(cmd.nid))
	if i.id != nil {
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
	return nil
}

func (cmd *niCmd) setIcon(icon win.HICON) {
	cmd.nid.HIcon = icon
	cmd.nid.UFlags |= win.NIF_ICON
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
		return newError("Shell_NotifyIcon")
	}

	if cmd.op != win.NIM_ADD {
		return nil
	}

	newId := cmd.nid.UID
	cmd.shellIcon.id = &newId

	// When executing an add, we also need to do a NIM_SETVERSION.
	verCmd := *cmd
	verCmd.op = win.NIM_SETVERSION
	// We want XP-compatible message behavior.
	verCmd.nid.UVersion = win.NOTIFYICON_VERSION
	return verCmd.execute()
}

// NotifyIcon represents an icon in the taskbar notification area.
type NotifyIcon struct {
	shellIcon               *shellNotificationIcon
	lastDPI                 int
	contextMenu             *Menu
	icon                    Image
	toolTip                 string
	visible                 bool
	mouseDownPublisher      MouseEventPublisher
	mouseUpPublisher        MouseEventPublisher
	messageClickedPublisher EventPublisher
}

// NewNotifyIcon creates and returns a new NotifyIcon.
//
// The NotifyIcon is initially not visible.
func NewNotifyIcon(form Form) (*NotifyIcon, error) {
	fb := form.AsFormBase()
	shellIcon, err := newShellNotificationIcon(fb.hWnd)
	if err != nil {
		return nil, err
	}

	// Create and initialize the NotifyIcon already.
	menu, err := NewMenu()
	if err != nil {
		return nil, err
	}
	menu.window = form

	ni := &NotifyIcon{
		shellIcon:   shellIcon,
		contextMenu: menu,
	}

	menu.getDPI = ni.DPI

	// Set our *NotifyIcon as user data for the message window.
	win.SetWindowLongPtr(fb.hWnd, win.GWLP_USERDATA, uintptr(unsafe.Pointer(ni)))

	notifyIcons[ni] = true
	return ni, nil
}

func (ni *NotifyIcon) DPI() int {
	fakeWb := WindowBase{hWnd: win.FindWindow(syscall.StringToUTF16Ptr("Shell_TrayWnd"), syscall.StringToUTF16Ptr(""))}
	return fakeWb.DPI()
}

func (ni *NotifyIcon) isDefunct() bool {
	return ni.shellIcon.hWnd == 0
}

func (ni *NotifyIcon) readdToTaskbar() error {
	cmd := ni.shellIcon.newCmd(win.NIM_ADD)
	cmd.setCallbackMessage(notifyIconMessageId)
	cmd.setVisible(true)
	cmd.setIcon(ni.getHICON(ni.icon))
	if err := cmd.setToolTip(ni.toolTip); err != nil {
		return err
	}

	return cmd.execute()
}

func (ni *NotifyIcon) applyDPI() {
	dpi := ni.DPI()
	if dpi == ni.lastDPI {
		return
	}
	ni.lastDPI = dpi
	for _, action := range ni.contextMenu.actions.actions {
		if action.image != nil {
			ni.contextMenu.onActionChanged(action)
		}
	}
	icon := ni.icon
	ni.icon = nil
	if icon != nil {
		ni.SetIcon(icon)
	}
}

// Dispose releases the operating system resources associated with the
// NotifyIcon.
//
// The associated Icon is not disposed of.
func (ni *NotifyIcon) Dispose() error {
	delete(notifyIcons, ni)
	if ni.isDefunct() {
		return nil
	}

	return ni.shellIcon.Dispose()
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

func (ni *NotifyIcon) publishMouseEvent(publisher *MouseEventPublisher, button MouseButton) {
	var p win.POINT
	if !win.GetCursorPos(&p) {
		lastError("GetCursorPos")
	}

	publisher.Publish(int(p.X), int(p.Y), button)
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
