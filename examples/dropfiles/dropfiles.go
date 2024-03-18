// Copyright 2013 The Walk Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"log"
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

func main() {
	app, err := walk.InitApp()
	if err != nil {
		log.Fatal(err)
	}

	var textEdit *walk.TextEdit
	MainWindow{
		Title:   "Walk DropFiles Example",
		MinSize: Size{320, 240},
		Layout:  VBox{},
		OnDropFiles: func(files []string) {
			textEdit.SetText(strings.Join(files, "\r\n"))
		},
		Children: []Widget{
			TextEdit{
				AssignTo: &textEdit,
				ReadOnly: true,
				Text:     "Drop files here, from windows explorer...",
			},
		},
	}.Create()

	app.Run()
}
