// Copyright (c) Tailscale Inc. and AUTHORS
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package walk

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestFindExplicitMnemonic(t *testing.T) {
	testCases := []struct {
		text    string
		wantKey Key
	}{
		{"", 0},
		{"Law 'N' Order", 0},
		{"Law && Order", 0},
		{"Law && &Order", KeyO},
		{"&Law && &Order && Bacon", KeyL},
	}

	for _, c := range testCases {
		utext, err := windows.UTF16FromString(c.text)
		if err != nil {
			t.Fatalf("UTF16FromString error %v", err)
		}
		k := findExplicitMnemonic(utext)
		if k != c.wantKey {
			t.Errorf("key for %q got 0x%02X, want 0x%02X", c.text, k, c.wantKey)
		}
	}
}
