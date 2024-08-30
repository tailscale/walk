// Copyright 2024 Tailscale Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package walk

import (
	"unsafe"

	"github.com/dblohm7/wingoes/com"
	"github.com/tailscale/win"
	"golang.org/x/exp/constraints"
	"golang.org/x/sys/windows"
)

// Resource encapsulates data loaded from the executable's Win32 resources.
type Resource []byte

// Bytes returns the resource's data as a byte slice.
func (res Resource) Bytes() []byte {
	return []byte(res)
}

// Stream returns the resource's data as a COM Stream.
func (res Resource) Stream() (com.Stream, error) {
	return com.NewMemoryStream(res.Bytes())
}

// LoadResourceByID locates the resource identified by id and resType
// from the current process's executable binary and returns its contents
// as a Resource. resType must be one of the win.RT_* constants.
func LoadResourceByID[ID constraints.Integer](id ID, resType win.ResourceType) (Resource, error) {
	return loadResource(win.MAKEINTRESOURCE(id), resType)
}

// LoadCustomResourceByID locates the custom resource identified by
// id from the current process's executable binary and returns its contents
// as a Resource.
func LoadCustomResourceByID[ID constraints.Integer](id ID) (Resource, error) {
	return LoadResourceByID(id, win.RT_RCDATA)
}

// LoadResourceByName locates the resource identified by name and resType
// from the current process's executable binary and returns its contents
// as a Resource. resType must be one of the win.RT_* constants.
func LoadResourceByName(name string, resType win.ResourceType) (Resource, error) {
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}

	return loadResource(name16, resType)
}

// LoadCustomResourceByName locates the resource identified by name
// from the current process's executable binary and returns its contents
// as a *Resource.
func LoadCustomResourceByName(name string) (Resource, error) {
	return LoadResourceByName(name, win.RT_RCDATA)
}

func loadResource(name *uint16, resType win.ResourceType) (result Resource, err error) {
	hres := win.FindResource(0, name, win.MAKEINTRESOURCE(uint16(resType)))
	if hres == 0 {
		return nil, lastError("FindResource")
	}

	loadedRes := win.LoadResource(0, hres)
	if loadedRes == 0 {
		return nil, lastError("LoadResource")
	}

	resAddr := win.LockResource(loadedRes)
	if resAddr == 0 {
		return nil, lastError("LockResource")
	}

	resLen := win.SizeofResource(0, hres)
	if resLen == 0 {
		return nil, lastError("SizeofResource")
	}

	// The memory backing the resource remains loaded as long as its binary
	// remains loaded. Since we're only loading from this process's .exe,
	// the memory will never freed for the duration of the process, therefore
	// we can safely convert resAddr to a pointer and pass it around without any
	// UAF fears.
	resPtr := (*byte)(unsafe.Pointer(resAddr))
	return Resource(unsafe.Slice(resPtr, resLen)), nil
}
