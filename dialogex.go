// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows
// +build windows

package walk

import (
	"bytes"
	"encoding/binary"
	"os"
	"unsafe"

	"github.com/tailscale/win"
	"golang.org/x/exp/constraints"
	"golang.org/x/sys/windows"
)

const emptyDlgClassName = "Walk Empty Dialog Class"

var (
	modUser32      = windows.NewLazySystemDLL("user32.dll")
	defDlgProcCb   = modUser32.NewProc("DefDlgProcW")
	dialogExProcCb uintptr
)

func registerEmptyDialogClass() (className []uint16, err error) {
	className, err = windows.UTF16FromString(emptyDlgClassName)
	if err != nil {
		return nil, err
	}

	if registeredWindowClasses[emptyDlgClassName] != nil {
		return className, nil
	}

	// See https://web.archive.org/web/20240607202142/https://devblogs.microsoft.com/oldnewthing/20031113-00/?p=41843
	wc := win.WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(win.WNDCLASSEX{})),
		Style:         win.CS_DBLCLKS | win.CS_SAVEBITS | win.CS_BYTEALIGNWINDOW,
		LpfnWndProc:   defDlgProcCb.Addr(),
		CbWndExtra:    win.DLGWINDOWEXTRA + int32(unsafe.Sizeof(uintptr(0))), // Include sufficient space for an unsafe pointer to the DlgEx itself.
		HCursor:       win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_ARROW)),
		LpszClassName: unsafe.SliceData(className),
	}
	if win.RegisterClassEx(&wc) == 0 {
		return nil, lastError("RegisterClassEx")
	}

	registeredWindowClasses[emptyDlgClassName] = newWndClassInfo()
	return className, nil
}

// headerDLGTEMPLATEEX is the fixed-length header used for the beginning of a
// DLGTEMPLATEEX.
type headerDLGTEMPLATEEX struct {
	dlgVer    uint16
	signature uint16
	helpID    uint32
	exStyle   uint32
	style     uint32
	cDlgItems uint16
	x         int16
	y         int16
	cx        int16
	cy        int16
	menu      uint16
	// Then write the window class name (as NUL-terminated UTF-16)
	// Then write the title text (as NUL-terminated UTF-16)
}

type emptyDlgParam struct {
	dlg    *DialogEx
	lParam uintptr
	err    error
}

// createEmptyDialog creates a new dialog box associated with dlg via the
// Windows dialog manager. It does so by specifying a basic dialog template
// that uses a window class created by walk and but not contain any controls.
// This is what gives DialogEx the ability to be managed by the Windows dialog
// manager and yet have the flexibility to function as a walk Form, complete
// with layout.
//
// We use a custom dialog class essentially because that guarantees us full
// control over the WM_USER message space (except for the first three messages
// used by IsDialogMessage); we want walk's Form and Layout code to be able
// to assume that DialogEx is just another window.
func createEmptyDialog(dlg *DialogEx, parent Form, title string, param uintptr) error {
	className16, err := registerEmptyDialogClass()
	if err != nil {
		return err
	}

	title16, err := windows.UTF16FromString(title)
	if err != nil {
		return err
	}

	alignment := int(unsafe.Alignof(headerDLGTEMPLATEEX{}))
	numBytes := alignUp(int(unsafe.Sizeof(headerDLGTEMPLATEEX{}))+len(className16)+len(title16), alignment)
	buf32 := make([]uint32, numBytes/alignment)
	bufBytes := unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(buf32))), numBytes)[:0]

	buf := bytes.NewBuffer(bufBytes)

	header := headerDLGTEMPLATEEX{
		dlgVer:    1,
		signature: 0xFFFF,
		style:     win.WS_CAPTION | win.WS_SYSMENU, // DO NOT specify DS_SETFONT or DS_SHELLFONT! They enable additional fields in the header that are only present when at least one of those style bits are present!
		cx:        100,                             // temporary, will update during WM_INITDIALOG
		cy:        100,                             // temporary, will update during WM_INITDIALOG
	}

	if err := binary.Write(buf, binary.LittleEndian, header); err != nil {
		return err
	}

	if err := binary.Write(buf, binary.LittleEndian, className16); err != nil {
		return err
	}

	if err := binary.Write(buf, binary.LittleEndian, title16); err != nil {
		return err
	}

	if dialogExProcCb == 0 {
		dialogExProcCb = windows.NewCallback(dialogExProc)
	}

	var parentHWND win.HWND
	if parent != nil {
		parentHWND = parent.Handle()
	}

	params := emptyDlgParam{
		dlg:    dlg,
		lParam: param,
	}
	_, err = win.CreateDialogIndirectParam(
		0,
		unsafe.Pointer(unsafe.SliceData(buf.Bytes())),
		parentHWND,
		dialogExProcCb,
		uintptr(unsafe.Pointer(&params)),
	)
	if err != nil {
		return err
	}
	if params.err != nil {
		return params.err
	}

	return nil
}

func dialogExProc(hdlg win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	defer appSingleton.maybePublishPanic()

	var dlgEx *DialogEx
	if msg == win.WM_INITDIALOG {
		params := (*emptyDlgParam)(unsafe.Pointer(lParam))
		dlgEx = params.dlg
		if win.SetWindowLongPtr(hdlg, win.DLGWINDOWEXTRA, uintptr(unsafe.Pointer(dlgEx))) == 0 {
			if e := win.GetLastError(); e != 0 {
				params.err = windows.Errno(e)
				win.DestroyWindow(hdlg)
				return 0
			}
		}
		lParam = params.lParam
	} else {
		dlgEx = (*DialogEx)(unsafe.Pointer(win.GetWindowLongPtr(hdlg, win.DLGWINDOWEXTRA)))
	}

	if dlgEx == nil || !dlgEx.dlgProc(hdlg, msg, wParam, lParam) {
		return 0
	}
	return 1
}

// OnPreTranslate satisfies PreTranslateHandler and implements the necessary
// code for layout and navigation by tab key.
func (dlg *DialogEx) OnPreTranslate(msg *win.MSG) bool {
	if !win.IsDialogMessage(dlg.hWnd, msg) {
		return false
	}
	dlg.OnPostDispatch()
	return true
}

func (dlg *DialogEx) dlgProc(hdlg win.HWND, msg uint32, wParam, lParam uintptr) bool {
	switch msg {
	case win.WM_INITDIALOG:
		// mapHWND must happen first
		dlg.mapHWND(hdlg)
		App().AddPreTranslateHandlerForHWND(hdlg, dlg)
		dlg.reCenter()
		dlg.finishInit()
		return true
	case win.WM_COMMAND:
		return dlg.routeWM_COMMAND(wParam, lParam)
	case win.WM_DESTROY:
		if !dlg.isModal {
			App().DeletePreTranslateHandlerForHWND(hdlg)
		}
		fallthrough
	case win.WM_NOTIFY:
		dlg.FormBase.WndProc(hdlg, msg, wParam, lParam)
		return true
	case win.WM_SETTINGCHANGE, win.WM_THEMECHANGED, win.WM_SETTEXT, win.WM_WINDOWPOSCHANGED, win.WM_SYSCOLORCHANGE, win.WM_DPICHANGED, win.WM_GETMINMAXINFO:
		dlg.FormBase.WndProc(hdlg, msg, wParam, lParam)
		// Once we handle these messages, we return false so that DefDlgProc also receives them.
		return false
	default:
		return false
	}
}

// alignment must be a power of 2
func alignUp[V constraints.Integer](v V, alignment int) V {
	return v + ((-v) & (V(alignment) - 1))
}

// DialogEx is an implementation of Form that utilizes the Windows dialog
// manager for its presentation and event handling.
type DialogEx struct {
	FormBase
	size    Size
	isModal bool
}

// DialogExResolver is an interface used by widgets for resolving a *DialogEx
// from their parent Form (if applicable).
type DialogExResolver interface {
	// AsDialogEx returns a pointer to the concrete DialogEx implementation.
	AsDialogEx() *DialogEx
}

// AsDialogEx satisfies DialogExResolver.
func (dlg *DialogEx) AsDialogEx() *DialogEx {
	return dlg
}

// Cancel executes a cancellation operation on dlg.
func (dlg *DialogEx) Cancel() {
	dlg.Dispose()
}

// SetFocusNext moves keyboard focus to the next Widget in the dialog's tab
// sequence.
func (dlg *DialogEx) SetFocusNext() {
	win.PostMessage(dlg.hWnd, win.WM_NEXTDLGCTL, 0, 0)
}

// SetFocusPrev moves keyboard focus to the previous Widget in the dialog's tab
// sequence.
func (dlg *DialogEx) SetFocusPrev() {
	win.PostMessage(dlg.hWnd, win.WM_NEXTDLGCTL, 1, 0)
}

// SetFocusToWindow sets keyboard focus within dlg to the Window specified by w.
func (dlg *DialogEx) SetFocusToWindow(w Window) {
	// Dialogs should not call win.SetFocus directly; they should use
	// WM_NEXTDLGCTL instead.
	if w == nil {
		return
	}

	hwnd := w.Handle()
	if !win.IsChild(dlg.hWnd, hwnd) {
		return
	}

	win.PostMessage(dlg.hWnd, win.WM_NEXTDLGCTL, uintptr(hwnd), 1)
}

// EnterMode satisfies Modal.
func (dlg *DialogEx) EnterMode() {
	// Modal dialogs don't need a HWND-specific pre-translate handler because
	// they don't use the top-level message pump.
	App().DeletePreTranslateHandlerForHWND(dlg.hWnd)
	dlg.isModal = true
	dlg.FormBase.EnterMode()
}

// NewDialogEx instantiates a new DialogEx with optional parent, text for its
// title bar, and its desired size at 100% DPI.
func NewDialogEx(parent Form, title string, size Size) (*DialogEx, error) {
	var zeroSize Size
	if size == zeroSize {
		return nil, os.ErrInvalid
	}

	dlg := &DialogEx{
		size: size,
	}
	// We must set defwindowProc to an empty func because dialog event handling
	// is inverted: DefDlgProc is delegating to _us_, so we should not be
	// delegating to a default proc.
	dlg.defWindowProc = func(win.HWND, uint32, uintptr, uintptr) uintptr { return 0 }

	// Minimal setup to make FormBase happy.
	dlg.window = dlg
	dlg.form = dlg
	dlg.enabled = true
	dlg.calcTextSizeInfo2TextSize = make(map[calcTextSizeInfo]Size)
	dlg.name2Property = make(map[string]Property)
	dlg.themes = make(map[string]*Theme)

	if err := createEmptyDialog(dlg, parent, title, 0); err != nil {
		return nil, err
	}
	return dlg, nil
}

// size is at 100% dpi, result is in screen pixels
func centerInMonitor(mon Monitor, size Size) (Rectangle, error) {
	var zero Rectangle
	if !mon.IsValid() {
		return zero, os.ErrInvalid
	}

	workArea := mon.WorkArea()
	rcw := workArea.Width
	rch := workArea.Height

	dpi, err := mon.DPI()
	if err != nil {
		return zero, err
	}

	size = SizeFrom96DPI(size, dpi)
	// clamp size to work area
	size.Width = min(size.Width, rcw)
	size.Height = min(size.Height, rch)
	cx := size.Width
	cy := size.Height

	return Rectangle{
		X:      workArea.X + ((rcw - cx) / 2),
		Y:      workArea.Y + ((rch - cy) / 2),
		Width:  size.Width,
		Height: size.Height,
	}, nil
}

func (dlg *DialogEx) reCenter() {
	parent := dlg.owner
	size := dlg.size
	var boundsPx Rectangle

	if parent == nil {
		var err error
		boundsPx, err = centerInMonitor(dlg.Monitor(), size)
		if err != nil {
			return
		}
	} else {
		sizePx := SizeFrom96DPI(size, parent.DPI())
		boundsPx = parent.BoundsPixels()
		if sizePx.Width > boundsPx.Width || sizePx.Height > boundsPx.Height {
			workArea := parent.Monitor().WorkArea()
			// clamp size to work area
			size.Width = min(size.Width, workArea.Width)
			size.Height = min(size.Height, workArea.Height)
		}

		boundsPx = Rectangle{
			X:      boundsPx.X + ((boundsPx.Width - sizePx.Width) / 2),
			Y:      boundsPx.Y + ((boundsPx.Height - sizePx.Height) / 2),
			Width:  sizePx.Width,
			Height: sizePx.Height,
		}
	}

	win.SetWindowPos(
		dlg.hWnd,
		0,
		int32(boundsPx.X),
		int32(boundsPx.Y),
		int32(boundsPx.Width),
		int32(boundsPx.Height),
		win.SWP_NOZORDER|win.SWP_NOACTIVATE,
	)
}

func (dlg *DialogEx) handlePredefinedID(id uint16) bool {
	switch id {
	case win.IDCANCEL:
		// This is what makes the ESC key work as a cancel button in dialogs.
		dlg.Cancel()
		return true
	default:
		return false
	}
}

func (dlg *DialogEx) routeWM_COMMAND(wParam, lParam uintptr) (result bool) {
	wp32 := uint32(wParam)
	if lParam == 0 {
		if isAccel := win.HIWORD(wp32) != 0; isAccel {
			// Walk currently does not support accelerator tables, so we just return
			// false to indicate that this message was unhandled.
			return false
		}
		// We must be dealing with a menu item. DialogEx currently only supports
		// the system menu.
		return dlg.handlePredefinedID(win.LOWORD(wp32))
	}
	defer func() {
		// Ensure we handle any predefined IDs before returning.
		result = dlg.handlePredefinedID(uint16(wParam)) || result
	}()

	// Redirect the WM_COMMAND to the Widget that is the source of the event.
	hwndSrc := win.HWND(lParam)
	if window := windowFromHandle(hwndSrc); window != nil {
		window.WndProc(hwndSrc, win.WM_COMMAND, wParam, lParam)
		return true
	}
	return false
}
