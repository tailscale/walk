// Copyright 2023 Tailscale Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

// Package dpicache provides a type-agnostic cache for structures whose data
// must be adjusted for various DPI settings.
package dpicache

import (
	"sync"
)

var (
	// <original instance> -> dpi -> <copy at that dpi>
	cache map[any]map[int]any
	mu    sync.Mutex
)

// DPICopier is the interface that must be implemented by any type that is
// intended to work with dpicache.
type DPICopier[T any] interface {
	// CopyForDPI creates a copy of the receiver that is appropriate for dpi.
	// Its return value must not require a separate method call to release its
	// resources; use a finalizer if necessary.
	CopyForDPI(dpi int) T
}

// DPIGetter is an optional interface that returns the DPI of an existing
// value. When available, InstanceForDPI uses DPIGetter as an optimization hint.
type DPIGetter interface {
	DPI() int
}

// InstanceForDPI returns an instance of inst that is appropriate for dpi. If
// inst is already appropriate for dpi, InstanceForDPI may simply return inst.
// Otherwise a copy of inst may be made and cached for future use, keyed on
// inst itself.
func InstanceForDPI[T DPICopier[T]](inst T, dpi int) T {
	if getter, ok := any(inst).(DPIGetter); ok && getter.DPI() == dpi {
		// The DPI is already what we want; nothing needs to be done.
		return inst
	}

	mu.Lock()
	defer mu.Unlock()

	if cache == nil {
		cache = make(map[any]map[int]any)
	}

	sub := cache[inst]
	if sub == nil {
		sub = make(map[int]any)
		cache[inst] = sub
	}

	if s := sub[dpi]; s != nil {
		return s.(T)
	}

	t := inst.CopyForDPI(dpi)
	sub[dpi] = t

	return t
}

// Delete removes any cached variants of inst from the DPI cache, if present.
func Delete[T DPICopier[T]](inst T) {
	mu.Lock()
	defer mu.Unlock()

	delete(cache, inst)
}
