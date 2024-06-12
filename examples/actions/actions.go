// Copyright 2013 The Walk Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/tailscale/walk"

	. "github.com/tailscale/walk/declarative"
)

var viewModes [4]*walk.Action
var isSpecialMode = walk.NewMutableCondition()

type MyMainWindow struct {
	*walk.MainWindow
}

func main() {
	app, err := walk.InitApp()
	if err != nil {
		log.Fatal(err)
	}

	MustRegisterCondition("isSpecialMode", isSpecialMode)

	mw := new(MyMainWindow)

	var openAction, showAboutBoxAction *walk.Action
	var recentMenu *walk.Menu
	var toggleSpecialModePB *walk.PushButton

	if err := (MainWindow{
		AssignTo: &mw.MainWindow,
		Title:    "Walk Actions Example",
		MenuItems: []MenuItem{
			Menu{
				Text: "&File",
				Items: []MenuItem{
					Action{
						AssignTo:    &openAction,
						Text:        "&Open",
						Image:       "../img/open.png",
						Enabled:     Bind("enabledCB.Checked"),
						Visible:     Bind("!openHiddenCB.Checked"),
						Shortcut:    Shortcut{walk.ModControl, walk.KeyO},
						OnTriggered: mw.openAction_Triggered,
					},
					Menu{
						AssignTo: &recentMenu,
						Text:     "Recent",
					},
					Separator{},
					Action{
						Text:        "E&xit",
						OnTriggered: func() { mw.Close() },
					},
				},
			},
			Menu{
				Text: "&View",
				Items: []MenuItem{
					Action{
						Text:    "Open / Special Enabled",
						Checked: Bind("enabledCB.Visible"),
					},
					Action{
						Text:    "Open Hidden",
						Checked: Bind("openHiddenCB.Visible"),
					},
				},
			},
			Menu{
				Text: "&Help",
				Items: []MenuItem{
					Action{
						AssignTo:    &showAboutBoxAction,
						Text:        "About",
						OnTriggered: mw.showAboutBoxAction_Triggered,
					},
				},
			},
		},
		ToolBar: ToolBar{
			ButtonStyle: ToolBarButtonImageBeforeText,
			Items: []MenuItem{
				ActionRef{&openAction},
				Menu{
					Text:  "New A",
					Image: "../img/document-new.png",
					Items: []MenuItem{
						Action{
							Text:        "A",
							OnTriggered: mw.newAction_Triggered,
						},
						Action{
							Text:        "B",
							OnTriggered: mw.newAction_Triggered,
						},
						Action{
							Text:        "C",
							OnTriggered: mw.newAction_Triggered,
						},
					},
					OnTriggered: mw.newAction_Triggered,
				},
				Separator{},
				Menu{
					Text:  "View",
					Image: "../img/document-properties.png",
					Items: []MenuItem{
						Action{
							AssignTo:    &viewModes[0],
							Text:        "X",
							OnTriggered: mw.changeViewAction_Triggered,
							Checkable:   true,
							Exclusive:   true,
						},
						Action{
							AssignTo:    &viewModes[1],
							Text:        "(Hidden)",
							OnTriggered: mw.changeViewAction_Triggered,
							Checkable:   true,
							Exclusive:   true,
							Visible:     false,
							Checked:     true,
						},
						Action{
							AssignTo:    &viewModes[2],
							Text:        "Y",
							OnTriggered: mw.changeViewAction_Triggered,
							Checkable:   true,
							Exclusive:   true,
						},
						Action{
							AssignTo:    &viewModes[3],
							Text:        "Z",
							OnTriggered: mw.changeViewAction_Triggered,
							Checkable:   true,
							Exclusive:   true,
						},
					},
				},
				Separator{},
				Action{
					Text:        "Special",
					Image:       "../img/system-shutdown.png",
					Enabled:     Bind("isSpecialMode && enabledCB.Checked"),
					OnTriggered: mw.specialAction_Triggered,
				},
			},
		},
		ContextMenuItems: []MenuItem{
			ActionRef{&showAboutBoxAction},
		},
		MinSize: Size{300, 200},
		Layout:  VBox{},
		Children: []Widget{
			CheckBox{
				Name:    "enabledCB",
				Text:    "Open / Special Enabled",
				Checked: true,
				Accessibility: Accessibility{
					Help: "Enables Open and Special",
				},
			},
			CheckBox{
				Name:    "openHiddenCB",
				Text:    "Open Hidden",
				Checked: true,
			},
			PushButton{
				AssignTo: &toggleSpecialModePB,
				Text:     "Enable Special Mode",
				OnClicked: func() {
					isSpecialMode.SetSatisfied(!isSpecialMode.Satisfied())

					if isSpecialMode.Satisfied() {
						toggleSpecialModePB.SetText("Disable Special Mode")
					} else {
						toggleSpecialModePB.SetText("Enable Special Mode")
					}
				},
				Accessibility: Accessibility{
					Help: "Toggles special mode",
				},
			},
		},
	}.Create()); err != nil {
		log.Fatal(err)
	}

	addRecentFileActions := func(texts ...string) {
		for _, text := range texts {
			a := walk.NewAction()
			a.SetText(text)
			a.Triggered().Attach(mw.openAction_Triggered)
			recentMenu.Actions().Add(a)
		}
	}

	addRecentFileActions("Foo", "Bar", "Baz")

	app.Run()
}

func (mw *MyMainWindow) openAction_Triggered() {
	walk.MsgBox(mw, "Open", "Pretend to open a file...", walk.MsgBoxIconInformation)
}

func (mw *MyMainWindow) newAction_Triggered() {
	walk.MsgBox(mw, "New", "Newing something up... or not.", walk.MsgBoxIconInformation)
}

func (mw *MyMainWindow) changeViewAction_Triggered() {
	var msg strings.Builder
	msg.WriteString("Current view mode:\n")
	for _, m := range viewModes {
		if m.Checked() {
			fmt.Fprintf(&msg, " - %s\n", m.Text())
		}
	}
	walk.MsgBox(mw, "Change View", msg.String(), walk.MsgBoxIconInformation)
}

func (mw *MyMainWindow) showAboutBoxAction_Triggered() {
	walk.MsgBox(mw, "About", "Walk Actions Example", walk.MsgBoxIconInformation)
}

func (mw *MyMainWindow) specialAction_Triggered() {
	walk.MsgBox(mw, "Special", "Nothing to see here.", walk.MsgBoxIconInformation)
}
