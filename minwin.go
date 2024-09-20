// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows
// +build windows

package walk

import (
	"fmt"
	"os"
	"unsafe"

	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
)

const minWinClassName = "Walk MinWin"

var (
	minWinProcCb uintptr
	minWins      = map[*MinWin]struct{}{} // Set of all MinWin instances that are currently associated with valid HWNDs.
)

// MinWinType is an enumeration specifying the type of the MinWin window.
type MinWinType uint

const (
	MinWinTypeTopLevel    MinWinType = iota // A top-level window with title bar. Must be initialized using [MinWinTopLevelOptions].
	MinWinTypePopup                         // An ephemeral pop-up window (think menu, tooltip, or volume control).
	MinWinTypeChild                         // A child window hosted within a parent window. Child windows must have a parent window.
	MinWinTypeZeroSize                      // An invisible container used for hosting a parent XAML islands that spawn UI.
	MinWinTypeMessageOnly                   // An invisible window that only processes messages and does not display content. Message-only windows cannot have a parent/owner.
)

// MinWinOptions specifies the common configuration values across all types of MinWin.
type MinWinOptions struct {
	Type             MinWinType  // The type of the MinWin. If Type is [MinWinTypeTopLevel], the MinWinOptions must be embedded within a [MinWinTopLevelOptions] struct.
	ParentOrOwner    Win32Window // The parent or owner window. May be nil unless Type is [MinWinTypeChild].
	Title            string      // The window text of the MinWin. When type is [MinWinTypeTopLevel], this text appears on the window's title bar.
	BoundsPx         Rectangle   // The bounds of the MinWin in native pixels.
	Size             Size        // The size of the MinWin at 100% DPI (ignored if BoundsPx is specified).
	Centered         bool        // The MinWin is centered within the monitor when created.
	Disabled         bool        // The MinWin is initially disabled.
	NoDWMCompositing bool        // The MinWin needs a redirection surface for painting its content.
	Visible          bool        // The MinWin is initially visible.
}

// MinWinTopLevelOptions specifies options specific to windows of type [MinWinTypeTopLevel].
type MinWinTopLevelOptions struct {
	MinWinOptions
	AlwaysOnTop  bool // The MinWin will be initialized as an always-on-top window.
	NoMaximize   bool // The MinWin will omit its maximize button.
	NoMinimize   bool // The MinWin will omit its minimize button.
	NoResize     bool // The MinWin will not be resizable by the user.
	NoCaption    bool // The MinWin will be initialized without a caption (implies NoSysmenu).
	NoSysmenu    bool // The MinWin will be initialized without a title bar icon.
	SolidSurface bool // The MinWin will be drawn with a solid background surface provided by DWM.
}

// MinWin implements a minimal API for managing windows that host XAML islands.
// Because its windows are mere hosts for other content, MinWins do not paint
// any content themselves.
type MinWin struct {
	Win32WindowImpl
	minWinType                 MinWinType
	activatePublisher          EventPublisher
	createPublisher            ProceedEventPublisher
	destroyPublisher           EventPublisher
	deactivatePublisher        EventPublisher
	dpiChangedPublisher        IntEventPublisher
	movePublisher              GenericEventPublisher[Point]
	sizePublisher              GenericEventPublisher[Size]
	textChangedPublisher       GenericEventPublisher[string]
	visibilityChangedPublisher GenericEventPublisher[bool]
}

type minWinCreateContext struct {
	mw           *MinWin
	err          error // Error to return out of CreateWindowEx if WM_NCCREATE or WM_CREATE fails.
	size         Size  // Desired size at 100% DPI.
	doSize       bool  // Resize the window during WM_CREATE.
	doCenter     bool  // Center the window during WM_CREATE.
	solidSurface bool  // Use DWM APIs to extend the window frame to cover the entire client area.
}

// MinWinOptionTypes is a type constraint limiting an argument to be either
// a [MinWinOptions] or a [MinWinTopLevelOptions].
type MinWinOptionTypes interface {
	MinWinOptions | MinWinTopLevelOptions
}

// InitMinWin initializes an already allocated MinWin structure according to
// the options supplied by opts.
//
// It returns an error if some combination of options are invalid for the
// requested [MinWinType].
func InitMinWin[O MinWinOptionTypes](mw *MinWin, opts O) error {
	App().AssertUIThread()
	if mw == nil {
		return os.ErrInvalid
	}

	var mainOpts *MinWinOptions
	var topLevelOpts *MinWinTopLevelOptions
	switch v := any(opts).(type) {
	case MinWinOptions:
		mainOpts = &v
	case MinWinTopLevelOptions:
		mainOpts = &v.MinWinOptions
		topLevelOpts = &v
	}

	if mainOpts.Type == MinWinTypeTopLevel && topLevelOpts == nil {
		return fmt.Errorf("%w: MinWinTopLevelOptions must be provided to create a top-level MinWin", os.ErrInvalid)
	}
	if mainOpts.Type == MinWinTypeMessageOnly && mainOpts.ParentOrOwner != nil {
		return fmt.Errorf("%w: message-only MinWin must not have a parent or owner", os.ErrInvalid)
	}
	if mainOpts.Type == MinWinTypeChild && mainOpts.ParentOrOwner == nil {
		return fmt.Errorf("%w: child MinWin must have a parent", os.ErrInvalid)
	}

	className, err := registerMinWinClass()
	if err != nil {
		return err
	}

	title, err := windows.UTF16PtrFromString(mainOpts.Title)
	if err != nil {
		return err
	}

	var style, exStyle uint32
	if mainOpts.Disabled {
		style |= win.WS_DISABLED
	}
	if mainOpts.Visible {
		style |= win.WS_VISIBLE
	}
	if !mainOpts.NoDWMCompositing || mainOpts.Type == MinWinTypeMessageOnly {
		exStyle |= win.WS_EX_NOREDIRECTIONBITMAP
	}

	switch mainOpts.Type {
	case MinWinTypeTopLevel:
		style |= win.WS_OVERLAPPEDWINDOW
		if topLevelOpts.AlwaysOnTop {
			exStyle |= win.WS_EX_TOPMOST
		}
		if topLevelOpts.NoMaximize {
			style &^= win.WS_MAXIMIZEBOX
		}
		if topLevelOpts.NoMinimize {
			style &^= win.WS_MINIMIZEBOX
		}
		if topLevelOpts.NoResize {
			style &^= win.WS_THICKFRAME
		}
		if topLevelOpts.NoSysmenu {
			style &^= win.WS_SYSMENU
		}
		if topLevelOpts.NoCaption {
			// Implies NoSysmenu
			style &^= win.WS_CAPTION | win.WS_SYSMENU
		}
	case MinWinTypeChild:
		style |= win.WS_CHILD
		exStyle |= win.WS_EX_CONTROLPARENT
	case MinWinTypePopup, MinWinTypeZeroSize:
		style |= win.WS_POPUP
	}

	mw.Win32WindowImpl.defWindowProc = win.DefWindowProc

	createCtx := minWinCreateContext{
		mw:           mw,
		size:         mainOpts.Size,
		doSize:       mainOpts.BoundsPx.IsZero() && !mainOpts.Size.IsZero() && mainOpts.ParentOrOwner == nil, // The caller specified a size but we don't know which monitor we're going to be on until WM_CREATE.
		doCenter:     mainOpts.BoundsPx.IsZero() && mainOpts.Centered && mainOpts.ParentOrOwner == nil,       // The caller requested that we center but we don't know which monitor we're going to be on until WM_CREATE.
		solidSurface: topLevelOpts != nil && topLevelOpts.SolidSurface,
	}

	var x, y, w, h int32
	switch {
	case mainOpts.Type == MinWinTypeZeroSize:
		// Just break, leaving x, y, w, h as zero values.
	case !mainOpts.BoundsPx.IsZero():
		x = int32(mainOpts.BoundsPx.X)
		y = int32(mainOpts.BoundsPx.Y)
		w = int32(mainOpts.BoundsPx.Width)
		h = int32(mainOpts.BoundsPx.Height)
	case !mainOpts.Size.IsZero() && mainOpts.ParentOrOwner != nil:
		// Since we have a parent, we can position prior to calling CreateWindowEx
		// because we're going to reside on the same monitor as our parent.
		parent := mainOpts.ParentOrOwner
		dpi := parent.DPI()
		sizePx := SizeFrom96DPI(mainOpts.Size, dpi)
		w = int32(sizePx.Width)
		h = int32(sizePx.Height)
		if mainOpts.Centered {
			var parentRc win.RECT
			if !win.GetWindowRect(parent.Handle(), &parentRc) {
				x = win.CW_USEDEFAULT
				y = win.CW_USEDEFAULT
				break
			}

			cx := int32(sizePx.Width)
			cy := int32(sizePx.Height)
			if cx > parentRc.Width() || cy > parentRc.Height() {
				wa := parent.Monitor().WorkArea()
				// Clamp size to work area
				cx = min(cx, int32(wa.Width))
				cy = min(cy, int32(wa.Height))
			}

			x = parentRc.Left + ((parentRc.Width() - cx) / 2)
			y = parentRc.Top + ((parentRc.Height() - cy) / 2)
			w = cx
			h = cy
		} else {
			x = win.CW_USEDEFAULT
			y = win.CW_USEDEFAULT
		}
	default:
		x = win.CW_USEDEFAULT
		y = win.CW_USEDEFAULT
		w = win.CW_USEDEFAULT
		h = win.CW_USEDEFAULT
	}

	var parentHWND win.HWND
	if mainOpts.ParentOrOwner != nil {
		parentHWND = mainOpts.ParentOrOwner.Handle()
	} else if mainOpts.Type == MinWinTypeMessageOnly {
		parentHWND = win.HWND_MESSAGE
	}

	hwnd := win.CreateWindowEx(
		exStyle,
		className,
		title,
		style,
		x,
		y,
		w,
		h,
		parentHWND,
		0, // HMENU
		0, // HINSTANCE
		unsafe.Pointer(&createCtx),
	)
	if hwnd == 0 {
		if createCtx.err == nil {
			return lastError("CreateWindowEx")
		}
		return createCtx.err
	}

	mw.minWinType = mainOpts.Type
	return nil
}

func (mw *MinWin) Dispose() {
	if mw.hWnd != 0 {
		win.DestroyWindow(mw.hWnd)
	}
}

// Activated returns the event that will be published when mw is activated.
func (mw *MinWin) Activated() *Event {
	return mw.activatePublisher.Event()
}

// Created returns the event that will be published when mw is created.
// Event handlers should return true to allow the window to finish its creation.
func (mw *MinWin) Created() *ProceedEvent {
	return mw.createPublisher.Event()
}

// Deactivated returns the event that will be published when mw is deactivated.
func (mw *MinWin) Deactivated() *Event {
	return mw.deactivatePublisher.Event()
}

// Destroyed returns the event that will be published when mw is destroyed.
func (mw *MinWin) Destroyed() *Event {
	return mw.destroyPublisher.Event()
}

// DPIChanged returns the event that will be published when mw's DPI has been
// changed. The handler's argument contains the new DPI.
func (mw *MinWin) DPIChanged() *IntEvent {
	return mw.dpiChangedPublisher.Event()
}

// Moved returns the event that will be published when mw is moved. The
// handler's argument contains the new screen coordinates of mw's top-left corner.
func (mw *MinWin) Moved() *GenericEvent[Point] {
	return mw.movePublisher.Event()
}

// Sized returns the event that will be published when mw is resized. The
// handler's argument contains mw's new size at 100% DPI.
func (mw *MinWin) Sized() *GenericEvent[Size] {
	return mw.sizePublisher.Event()
}

// TextChanged returns the event that will be published when mw's text has
// changed. The handler's argument contains the new window text.
func (mw *MinWin) TextChanged() *GenericEvent[string] {
	return mw.textChangedPublisher.Event()
}

// Type returns the [MinWinType] used for creating mw.
func (mw *MinWin) Type() MinWinType {
	return mw.minWinType
}

// VisibilityChanged returns the event that will be published when mw's
// visibility changes. The handler's argument is true for visible, false for
// hidden.
func (mw *MinWin) VisibilityChanged() *GenericEvent[bool] {
	return mw.visibilityChangedPublisher.Event()
}

// Show makes mw visible.
func (mw *MinWin) Show() {
	win.ShowWindow(mw.hWnd, win.SW_SHOW)
}

// Hide hides mw.
func (mw *MinWin) Hide() {
	win.ShowWindow(mw.hWnd, win.SW_HIDE)
}

// SetText sets mw's window text to the value of text. For top-level windows,
// this is the text that appears in the title bar.
func (mw *MinWin) SetText(text string) error {
	text16, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return err
	}
	return win.SetWindowText(mw.hWnd, text16)
}

// Text retrieves mw's window text.
func (mw *MinWin) Text() (string, error) {
	win.SetLastError(0)
	lenExclNul := win.GetWindowTextLength(mw.hWnd)
	if lenExclNul == 0 {
		if e := win.GetLastError(); e != 0 {
			return "", windows.Errno(e)
		}
	}

	lenInclNul := lenExclNul + 1
	buf := make([]uint16, lenInclNul)

	win.SetLastError(0)
	actualLenExclNul := win.GetWindowText(mw.hWnd, unsafe.SliceData(buf), lenInclNul)
	if actualLenExclNul == 0 {
		if e := win.GetLastError(); e != 0 {
			return "", windows.Errno(e)
		}
	}

	return windows.UTF16ToString(buf[:actualLenExclNul]), nil
}

// SetEnabled enables or disables mw depending on the value of enable.
func (mw *MinWin) SetEnabled(enable bool) {
	win.EnableWindow(mw.hWnd, enable)
}

func (mw *MinWin) setUserData() error {
	win.SetLastError(0)
	prev := win.SetWindowLongPtr(mw.hWnd, win.GWLP_USERDATA, uintptr(unsafe.Pointer(mw)))
	if prev != 0 {
		return nil
	}
	if le := win.GetLastError(); le != 0 {
		return windows.Errno(le)
	}
	return nil
}

func resolveMinWin(hwnd win.HWND) *MinWin {
	val := win.GetWindowLongPtr(hwnd, win.GWLP_USERDATA)
	return (*MinWin)(unsafe.Pointer(val))
}

func minWinProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	defer appSingleton.HandlePanicFromNativeCallback()

	if msg == win.WM_NCCREATE {
		cs := (*win.CREATESTRUCT)(unsafe.Pointer(lParam))
		createCtx := (*minWinCreateContext)(unsafe.Pointer(cs.CreateParams))
		if createCtx == nil || createCtx.mw == nil {
			return 0
		}

		mw := createCtx.mw
		mw.hWnd = hwnd
		if err := mw.setUserData(); err != nil {
			createCtx.err = err
			return 0
		}

		minWins[mw] = struct{}{}
		return mw.defWindowProc(hwnd, msg, wParam, lParam)
	} else if mw := resolveMinWin(hwnd); mw != nil {
		return mw.WndProc(hwnd, msg, wParam, lParam)
	}

	return win.DefWindowProc(hwnd, msg, wParam, lParam)
}

func (mw *MinWin) WndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case win.WM_CREATE:
		cs := (*win.CREATESTRUCT)(unsafe.Pointer(lParam))
		createCtx := (*minWinCreateContext)(unsafe.Pointer(cs.CreateParams))
		if createCtx == nil {
			return ^uintptr(0)
		}

		if doSize, doCenter := createCtx.doSize, createCtx.doCenter; doSize || doCenter {
			swpFlags := uint32(win.SWP_NOZORDER | win.SWP_NOACTIVATE)

			var sizePx win.SIZE
			if doSize {
				sizePx = SizeFrom96DPI(createCtx.size, mw.DPI()).toSIZE()
			} else {
				swpFlags |= win.SWP_NOSIZE
			}

			if doCenter && createCtx.size.IsZero() {
				// We need the size to compute position even if we're not resizing.
				var rc win.RECT
				if win.GetWindowRect(hwnd, &rc) {
					sizePx.CX = rc.Width()
					sizePx.CY = rc.Height()
				}
			}

			mon := mw.Monitor()
			wa := mon.WorkArea()
			cx := min(sizePx.CX, int32(wa.Width))
			cy := min(sizePx.CY, int32(wa.Height))
			var x, y int32
			if doCenter {
				x = int32(wa.X) + ((int32(wa.Width) - cx) / 2)
				y = int32(wa.Y) + ((int32(wa.Height) - cy) / 2)
			} else {
				swpFlags |= win.SWP_NOMOVE
			}

			win.SetWindowPos(hwnd, 0, x, y, cx, cy, swpFlags)
		}

		if createCtx.solidSurface {
			mw.SetSolidSurface()
		}

		if !mw.createPublisher.Publish() {
			return ^uintptr(0)
		}
		return 0
	case win.WM_ACTIVATE:
		if wParam > 0 {
			mw.activatePublisher.Publish()
		} else {
			mw.deactivatePublisher.Publish()
		}
		return 0
	case win.WM_ERASEBKGND:
		return 0
	case win.WM_DPICHANGED:
		dpi := int(win.LOWORD(uint32(wParam)))
		mw.dpiChangedPublisher.Publish(dpi)
		newRect := (*win.RECT)(unsafe.Pointer(lParam))
		win.SetWindowPos(
			hwnd,
			0,
			newRect.Left,
			newRect.Top,
			newRect.Width(),
			newRect.Height(),
			win.SWP_NOACTIVATE|win.SWP_NOZORDER,
		)
		return 0
	case win.WM_DESTROY:
		mw.destroyPublisher.Publish()
		return 0
	case win.WM_NCDESTROY:
		mw.hWnd = 0
		delete(minWins, mw)
	case win.WM_WINDOWPOSCHANGED:
		wp := (*win.WINDOWPOS)(unsafe.Pointer(lParam))
		if (wp.Flags & win.SWP_SHOWWINDOW) != 0 {
			mw.visibilityChangedPublisher.Publish(true)
		} else if (wp.Flags & win.SWP_HIDEWINDOW) != 0 {
			mw.visibilityChangedPublisher.Publish(false)
		}

		if (wp.Flags & win.SWP_NOMOVE) == 0 {
			mw.movePublisher.Publish(Point{X: int(wp.X), Y: int(wp.X)})
		}

		if (wp.Flags & win.SWP_NOSIZE) == 0 {
			sizePx := Size{Width: int(wp.Cx), Height: int(wp.Cy)}
			mw.sizePublisher.Publish(SizeTo96DPI(sizePx, mw.DPI()))
		}
		return 0
	case win.WM_SETTEXT:
		result := mw.defWindowProc(hwnd, msg, wParam, lParam)
		if result != 0 {
			newText := windows.UTF16PtrToString((*uint16)(unsafe.Pointer(lParam)))
			mw.textChangedPublisher.Publish(newText)
		}
		return result
	}

	return mw.defWindowProc(hwnd, msg, wParam, lParam)
}

func registerMinWinClass() (className16 *uint16, err error) {
	className16, err = windows.UTF16PtrFromString(minWinClassName)
	if err != nil {
		return nil, err
	}

	if registeredWindowClasses[minWinClassName] != nil {
		return className16, nil
	}

	if minWinProcCb == 0 {
		minWinProcCb = windows.NewCallback(minWinProc)
	}

	wc := win.WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(win.WNDCLASSEX{})),
		Style:         win.CS_DBLCLKS, // We don't bother with redrawing since we're just a host window.
		LpfnWndProc:   minWinProcCb,
		HCursor:       win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_ARROW)),
		LpszClassName: className16,
	}
	if win.RegisterClassEx(&wc) == 0 {
		return nil, lastError("RegisterClassEx")
	}

	registeredWindowClasses[minWinClassName] = newWndClassInfo()
	return className16, err
}

// SetFocus sets keyboard focus to focusTo, a window contained within topLevel.
// It tries to respect walk focus-setting conventions when possible, otherwise
// it falls back to calling [win.SetFocus] directly.
func (mw *MinWin) SetFocus(topLevel, focusTo win.HWND) {
	if form, ok := windowFromHandle(topLevel).(Form); ok {
		if focusToWindow := windowFromHandle(focusTo); focusToWindow != nil {
			form.SetFocusToWindow(focusToWindow)
			return
		}
	}

	win.SetFocus(focusTo)
}
