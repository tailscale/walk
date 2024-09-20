// Copyright 2012 The Walk Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package declarative

import (
	"github.com/tailscale/walk"
)

type PushButton struct {
	// Window

	Accessibility      Accessibility
	Background         Brush
	ContextMenuItems   []MenuItem
	DoubleBuffering    bool
	Enabled            Property
	Font               Font
	MaxSize            Size
	MinSize            Size
	Name               string
	OnBoundsChanged    walk.EventHandler
	OnKeyDown          walk.KeyEventHandler
	OnKeyPress         walk.KeyEventHandler
	OnKeyUp            walk.KeyEventHandler
	OnMouseDown        walk.MouseEventHandler
	OnMouseMove        walk.MouseEventHandler
	OnMouseUp          walk.MouseEventHandler
	OnSizeChanged      walk.EventHandler
	Persistent         bool
	RightToLeftReading bool
	ToolTipText        Property
	Visible            Property

	// Widget

	Alignment          Alignment2D
	AlwaysConsumeSpace bool
	Column             int
	ColumnSpan         int
	GraphicsEffects    []walk.WidgetGraphicsEffect
	Row                int
	RowSpan            int
	StretchFactor      int

	// Button

	Image          Property
	OnClicked      walk.EventHandler
	Text           Property
	LayoutFlags    walk.LayoutFlags // ignored unless UseLayoutFlags is true
	UseLayoutFlags bool

	// PushButton

	AssignTo       **walk.PushButton
	PredefinedID   int
	ImageAboveText bool
	Default        bool
}

func (pb PushButton) Create(builder *Builder) (err error) {
	opts := walk.PushButtonOptions{
		LayoutFlags:  pb.LayoutFlags,
		Default:      pb.Default,
		PredefinedID: pb.PredefinedID,
	}
	if !pb.UseLayoutFlags {
		opts.LayoutFlags = walk.GrowableHorz
	}

	w, err := walk.NewPushButtonWithOptions(builder.Parent(), opts)
	if err != nil {
		return err
	}

	if pb.AssignTo != nil {
		*pb.AssignTo = w
	}

	return builder.InitWidget(pb, w, func() error {
		if err := w.SetImageAboveText(pb.ImageAboveText); err != nil {
			return err
		}

		if pb.OnClicked != nil {
			w.Clicked().Attach(pb.OnClicked)
		}

		return nil
	})
}
