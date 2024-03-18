// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"fmt"
	"log"
	"time"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)

var (
	marquee   bool
	state     = walk.TaskDialogProgressBarStateNormal
	test      bool
	tickCount int
)

// A tick fires roughly every 200ms. Change mode every 5 seconds.
const changeModeAtTick = 25

func modeStr() string {
	if marquee {
		return "marquee"
	}
	return "progress"
}

func stateStr() string {
	switch state {
	case walk.TaskDialogProgressBarStateNormal:
		return "normal"
	case walk.TaskDialogProgressBarStateError:
		return "error"
	case walk.TaskDialogProgressBarStatePaused:
		return "paused"
	default:
		return "<invalid>"
	}
}

func tick(td walk.TaskDialog) {
	if !test {
		return
	}

	tickCount++
	step := tickCount % changeModeAtTick
	td.SetExpandedInformation(fmt.Sprintf("mode: %s, state: %s, step %d", modeStr(), stateStr(), step))
	if !marquee {
		td.SetProgressBarPosition(uint16(float32(step) / changeModeAtTick * 100))
	}
	if step != 0 {
		return
	}

	if !marquee {
		state++
		if state > walk.TaskDialogProgressBarStatePaused {
			state = walk.TaskDialogProgressBarStateNormal
		}
		td.SetProgressBarState(state)
		if state != walk.TaskDialogProgressBarStateNormal {
			return
		}
	}

	marquee = !marquee

	mode := walk.TaskDialogProgressBar
	if marquee {
		mode = walk.TaskDialogProgressBarMarquee
	}
	td.SetProgressBarMode(mode)
}

func main() {
	if _, err := walk.InitApp(); err != nil {
		log.Fatal(err)
	}

	td := walk.NewTaskDialog()

	opts := walk.TaskDialogOpts{
		Title:           "Breaking News",
		IconSystem:      walk.TaskDialogSystemIconInformation,
		Instruction:     "Main Instruction",
		Content:         "Here is some content.\n\nNote that there are some line breaks.",
		ProgressBarMode: walk.TaskDialogProgressBar,
		RadioButtons: []walk.TaskDialogRadioButton{
			walk.TaskDialogRadioButton{
				Text: "Progress Bar On",
			},
			walk.TaskDialogRadioButton{
				Text:    "Progress Bar Off",
				Default: true,
			},
			walk.TaskDialogRadioButton{
				Text:              "This option is disabled",
				InitiallyDisabled: true,
			},
		},
		CommonButtons:                  win.TDCBF_OK_BUTTON | win.TDCBF_NO_BUTTON,
		CommonButtonsUAC:               win.TDCBF_NO_BUTTON,
		CommonButtonsInitiallyDisabled: win.TDCBF_NO_BUTTON,
		DefaultButton:                  walk.TaskDialogDefaultButtonOK,
		CustomButtons: []walk.TaskDialogCustomButton{
			walk.TaskDialogCustomButton{
				MainText: "Main Text 1",
				Note:     "Note 1",
				UAC:      true,
			},
			walk.TaskDialogCustomButton{
				MainText:          "Main Text 2",
				Note:              "Note 2",
				InitiallyDisabled: true,
			},
		},
		CommandLinkMode:     walk.TaskDialogCommandLinksWithGlyph,
		ExpandLabel:         "Show state information",
		CollapseLabel:       "Hide state information",
		ExpandedInformation: fmt.Sprintf("mode: %s, state: %s, step: 0", modeStr(), stateStr()),
		Footer:              `This footer contains a <a href="https://tailscale.com">hyperlink</a>`,
		AllowHyperlinks:     true,
		InitiallyExpanded:   true,
		UseTimer:            true,
	}

	if onOK := opts.CommonButtonClicked(win.TDCBF_OK_BUTTON); onOK != nil {
		onOK.Attach(func() bool {
			log.Println("OK clicked")
			return false
		})
	}

	for i := range opts.RadioButtons {
		e := opts.RadioButtons[i].Clicked()
		i := i
		e.Attach(func() {
			var pbMode walk.TaskDialogProgressBarMode
			test = i == 0
			if test {
				pbMode = walk.TaskDialogProgressBar
			}

			td.SetProgressBarMode(pbMode)
		})
	}

	for i := range opts.CustomButtons {
		e := opts.CustomButtons[i].Clicked()
		i := i
		e.Attach(func() bool {
			log.Printf("Custom button %d clicked\n", i+1)
			return true
		})
	}

	he := td.HyperlinkClicked()
	he.Attach(func(url string) bool {
		log.Printf("Hyperlink clicked: %q\n", url)
		return false
	})

	te := td.TimerFired()
	te.Attach(func(d time.Duration) {
		tick(td)
	})

	tdr, err := td.Show(opts)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("result: %#v\n", tdr)
}
