// Copyright 2010 The Walk Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package walk

import (
	"fmt"

	"github.com/tailscale/win"
)

type PushButton struct {
	Button
	contentMargins win.MARGINS
	layoutFlags    LayoutFlags
}

// NewPushButton creates a new PushButton as a child of parent with its
// LayoutFlags set to GrowableHorz.
func NewPushButton(parent Container) (*PushButton, error) {
	return NewPushButtonWithOptions(parent, PushButtonOptions{LayoutFlags: GrowableHorz})
}

// PushButtonOptions provides the optional fields that are passed into
// [NewPushButtonWithOptions].
type PushButtonOptions struct {
	LayoutFlags  LayoutFlags // LayoutFlags to be used by the PushButton.
	PredefinedID int         // When non-zero, must be one of the predefined control IDs <= [win.IDCONTINUE].
	Default      bool        // When true, the PushButton will be initially created as a default PushButton.
}

// NewPushButtonWithOptions creates a new PushButton as a child of parent
// using options.
func NewPushButtonWithOptions(parent Container, opts PushButtonOptions) (*PushButton, error) {
	if opts.PredefinedID > maxPredefinedCtrlID {
		return nil, fmt.Errorf("Requested ID must be <= IDCONTINUE")
	}

	pb := &PushButton{
		layoutFlags: opts.LayoutFlags,
	}

	style := uint32(win.WS_TABSTOP | win.WS_VISIBLE)
	if opts.Default {
		style |= win.BS_DEFPUSHBUTTON
	} else {
		style |= win.BS_PUSHBUTTON
	}

	if err := InitWidget(
		pb,
		parent,
		"BUTTON",
		style,
		0); err != nil {
		return nil, err
	}

	pb.Button.init()

	if opts.PredefinedID > 0 {
		pb.setPredefinedID(uint16(opts.PredefinedID))
	}

	pb.GraphicsEffects().Add(InteractionEffect)
	pb.GraphicsEffects().Add(FocusEffect)

	return pb, nil
}

func (pb *PushButton) ImageAboveText() bool {
	return pb.hasStyleBits(win.BS_TOP)
}

func (pb *PushButton) SetImageAboveText(value bool) error {
	if err := pb.ensureStyleBits(win.BS_TOP, value); err != nil {
		return err
	}

	// We need to set the image again, or Windows will fail to calculate the
	// button control size correctly.
	return pb.SetImage(pb.image)
}

func (pb *PushButton) ensureProperDialogDefaultButton(hwndFocus win.HWND) {
	widget := windowFromHandle(hwndFocus)
	if widget == nil {
		return
	}

	if _, ok := widget.(*PushButton); ok {
		return
	}

	form := ancestor(pb)
	if form == nil {
		return
	}

	dlg, ok := form.(dialogish)
	if !ok {
		return
	}

	defBtn := dlg.DefaultButton()
	if defBtn == nil {
		return
	}

	if err := defBtn.setAndClearStyleBits(win.BS_DEFPUSHBUTTON, win.BS_PUSHBUTTON); err != nil {
		return
	}

	if err := defBtn.Invalidate(); err != nil {
		return
	}
}

func (pb *PushButton) WndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	if _, isDialogEx := pb.ancestor().(DialogExResolver); !isDialogEx {
		switch msg {
		case win.WM_GETDLGCODE:
			hwndFocus := win.GetFocus()
			if hwndFocus == pb.hWnd {
				form := ancestor(pb)
				if form == nil {
					break
				}

				dlg, ok := form.(dialogish)
				if !ok {
					break
				}

				defBtn := dlg.DefaultButton()
				if defBtn == pb {
					pb.setAndClearStyleBits(win.BS_DEFPUSHBUTTON, win.BS_PUSHBUTTON)
					if pb.origWndProcPtr == 0 {
						return win.DLGC_BUTTON | win.DLGC_DEFPUSHBUTTON
					}
					return win.CallWindowProc(pb.origWndProcPtr, hwnd, msg, wParam, lParam)
				}

				break
			}

			pb.ensureProperDialogDefaultButton(hwndFocus)

		case win.WM_KILLFOCUS:
			pb.ensureProperDialogDefaultButton(win.HWND(wParam))
		}
	}

	if msg == win.WM_THEMECHANGED {
		pb.contentMargins = win.MARGINS{}
	}

	return pb.Button.WndProc(hwnd, msg, wParam, lParam)
}

func (pb *PushButton) ensureMargins() win.MARGINS {
	var zeroMargins win.MARGINS
	if pb.contentMargins != zeroMargins {
		return pb.contentMargins
	}

	theme, err := pb.ThemeForClass(win.VSCLASS_BUTTON)
	if err != nil {
		return zeroMargins
	}

	result, err := theme.margins(win.BP_PUSHBUTTON, win.PBS_NORMAL, win.TMT_CONTENTMARGINS, nil)
	if err != nil {
		return zeroMargins
	}

	pb.contentMargins = result
	return result
}

func (pb *PushButton) idealSize() Size {
	s := pb.Button.idealSize().toSIZE()
	m := MARGINSFrom96DPI(pb.ensureMargins(), pb.DPI())
	addMargins(&s, m)
	return sizeFromSIZE(s)
}

func (pb *PushButton) CreateLayoutItem(ctx *LayoutContext) LayoutItem {
	return &pushButtonLayoutItem{
		buttonLayoutItem: buttonLayoutItem{
			idealSize: pb.idealSize(),
		},
		layoutFlags: pb.layoutFlags,
	}
}

type pushButtonLayoutItem struct {
	buttonLayoutItem
	layoutFlags LayoutFlags
}

func (pbli *pushButtonLayoutItem) LayoutFlags() LayoutFlags {
	return pbli.layoutFlags
}
