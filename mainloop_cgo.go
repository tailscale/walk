// Copyright 2019 The Walk Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows && walk_use_cgo
// +build windows,walk_use_cgo

package walk

import (
	"unsafe"

	"github.com/tailscale/win"
)

// #include <windows.h>
//
// extern unsigned char shimHandleKeyDown(uintptr_t fb, uintptr_t m);
//
// static int mainloop(uintptr_t handle_ptr, uintptr_t fb_ptr)
// {
//     HANDLE *hwnd = (HANDLE *)handle_ptr;
//     MSG m;
//     int r;
//
//     while (*hwnd) {
//         r = GetMessage(&m, NULL, 0, 0);
//         if (!r)
//             return m.wParam;
//         else if (r < 0)
//             return -1;
//         if (m.message == WM_KEYDOWN && shimHandleKeyDown(fb_ptr, (uintptr_t)&m))
//             continue;
//         if (!IsDialogMessage(*hwnd, &m)) {
//             TranslateMessage(&m);
//             DispatchMessage(&m);
//         }
//     }
//     return 0;
// }
import "C"

//export shimHandleKeyDown
func shimHandleKeyDown(fb uintptr, msg uintptr) bool {
	return (*FormBase)(unsafe.Pointer(fb)).handleKeyDown((*win.MSG)(unsafe.Pointer(msg)))
}

func (fb *FormBase) mainLoop() int {
	return int(C.mainloop(C.uintptr_t(uintptr(unsafe.Pointer(&fb.hWnd))), C.uintptr_t(uintptr(unsafe.Pointer(fb)))))
}
