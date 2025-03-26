// Copyright 2013 The Walk Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tailscale/walk"

	. "github.com/tailscale/walk/declarative"
)

func main() {
	err := run()
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	fmt.Println("Exited cleanly")
	os.Exit(0)
}

func run() error {
	var mw *walk.MainWindow

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := (MainWindow{
		AssignTo: &mw,
		Title:    "Window Closing Test",
		Layout:   VBox{Spacing: 2},
		Size:     Size{800, 600},
	}.Create()); err != nil {
		walk.MsgBox(nil, "Error", fmt.Sprintf("%v", err), walk.MsgBoxIconError)
		return fmt.Errorf("creating main window: %w", err)
	}

	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		//walk.MsgBox(nil, "Info", fmt.Sprintf("Closing now (reason %d)", reason), walk.MsgBoxIconInformation)
		//check if context is done
		if ctx.Err() != nil {
			return
		}
		*canceled = true
		fmt.Println("Got close message")
		mw.SetTitle("Closing...")
		cancel()
	})

	go func() {
		<-ctx.Done()
		fmt.Println("Doing clean up process...")
		time.Sleep(1 * time.Second)
		mw.Close()
		walk.App().Exit(0)
	}()
	code := mw.Run()
	if code != 0 {
		return fmt.Errorf("main window closed with %d", code)
	}
	return nil
}
