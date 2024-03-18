// Copyright 2017 The Walk Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"log"

	"github.com/tailscale/walk"

	. "github.com/tailscale/walk/declarative"
)

func main() {
	app, err := walk.InitApp()
	if err != nil {
		log.Fatal(err)
	}

	if err := (MainWindow{
		Title:   "Walk LinkLabel Example",
		MinSize: Size{300, 200},
		Layout:  VBox{},
		Children: []Widget{
			LinkLabel{
				MaxSize: Size{100, 0},
				Text:    `I can contain multiple links like <a id="this" href="https://golang.org">this</a> or <a id="that" href="https://github.com/tailscale/walk">that one</a>.`,
				OnLinkActivated: func(link *walk.LinkLabelLink) {
					log.Printf("id: '%s', url: '%s'\n", link.Id(), link.URL())
				},
			},
		},
	}).Create(); err != nil {
		log.Fatal(err)
	}

	app.Run()
}
