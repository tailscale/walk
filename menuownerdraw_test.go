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

func TestStripMnemonic(t *testing.T) {
	testCases := []struct {
		text    string
		want    string
		wantKey Key
	}{
		{"", "", 0},
		{"Law 'N' Order", "Law 'N' Order", 0},
		{"Law && Order", "Law & Order", 0},
		{"Law && &Order", "Law & Order", KeyO},
		{"&Law && &Order && Bacon", "Law & Order & Bacon", KeyL},
	}

	for _, c := range testCases {
		utext, err := windows.UTF16FromString(c.text)
		if err != nil {
			t.Fatalf("UTF16FromString error %v", err)
		}
		k, s := stripMnemonic(utext)
		got := windows.UTF16ToString(s)
		if got != c.want {
			t.Errorf("stripped text for %q got %q, want %q", c.text, got, c.want)
		}
		if k != c.wantKey {
			t.Errorf("key for %q got 0x%02X, want 0x%02X", c.text, k, c.wantKey)
		}
	}
}
