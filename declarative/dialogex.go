// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows
// +build windows

package declarative

import (
	"github.com/tailscale/walk"
)

type DialogEx struct {
	Background Brush
	Layout     Layout
	Children   []Widget
	Icon       Property
	Title      string
	Size       Size

	AssignTo **walk.DialogEx
}

func (d DialogEx) Create(owner walk.Form) error {
	dlg, err := walk.NewDialogEx(owner, d.Title, d.Size.toW())
	if err != nil {
		return err
	}

	if d.AssignTo != nil {
		*d.AssignTo = dlg
	}

	fi := formInfo{
		// Window
		Background: d.Background,
		Enabled:    true,

		// Container
		Children: d.Children,
		Layout:   d.Layout,

		// Form
		Icon:  d.Icon,
		Title: d.Title,
	}

	builder := NewBuilder(nil)
	dlg.SetSuspended(true)
	builder.Defer(func() error {
		dlg.SetSuspended(false)
		return nil
	})

	return builder.InitWidget(fi, dlg, nil)
}
