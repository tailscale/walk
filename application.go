// Copyright 2010 The Walk Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package walk

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/dblohm7/wingoes/com"
	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
)

var (
	// ErrNoMoreMessages is returned by Application.AllocMessage and
	// Window.AllocMessage when their supply of message constants has been exhausted.
	ErrNoMoreMessages       = errors.New("message numbering space exhausted")
	errInitCommonControlsEx = errors.New("InitCommonControlsEx failed")
)

const (
	appMsgWindowClassName = "Walk Application Message Window"
	// firstRegisteredMessage is the beginning of the Win32 message numbering
	// space for RegisterWindowMessage. All user-defined message codes allocated
	// as offsets from WM_APP must be less than this value.
	firstRegisteredMessage = 0xC000
)

type Settings interface {
	Get(key string) (string, bool)
	Timestamp(key string) (time.Time, bool)
	Put(key, value string) error
	PutExpiring(key, value string) error
	Remove(key string) error
	ExpireDuration() time.Duration
	SetExpireDuration(expireDuration time.Duration)
	Load() error
	Save() error
}

type Persistable interface {
	Persistent() bool
	SetPersistent(value bool)
	SaveState() error
	RestoreState() error
}

// onceWithPreInit is similar to sync.Once, however it also permits "pre-Init"
// functions that are run within the same mutex as Init, however they do not
// change the state to initialized.
type onceWithPreInit struct {
	init uint32
	mu   sync.Mutex
}

// Init runs f with idential semantics to its sync.Once counterpart.
func (o *onceWithPreInit) Init(f func()) {
	if atomic.LoadUint32(&o.init) == 0 {
		o.initSlow(f)
	}
}

func (o *onceWithPreInit) initSlow(f func()) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.init == 0 {
		defer atomic.StoreUint32(&o.init, 1)
		f()
	}
}

// PreInit runs f as long as o is not yet initialized. It returns true if f
// was executed, or false if o was already initialized.
func (o *onceWithPreInit) PreInit(f func()) bool {
	if atomic.LoadUint32(&o.init) == 0 {
		return o.doPreInit(f)
	}
	return false
}

func (o *onceWithPreInit) doPreInit(f func()) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.init == 0 {
		f()
		return true
	}
	return false
}

// Application encapsulates process-wide state that persists for the lifetime
// of the application. There is only one singleton instance. Use InitApp to
// initialize it, and then use App for the duration of the process to access it.
type Application struct {
	uiThreadID          uint32
	ctx                 context.Context
	ctxCancel           context.CancelFunc
	walkInit            []func()
	organizationName    atomic.Pointer[string]
	productName         atomic.Pointer[string]
	settings            atomic.Value // of Settings
	exiting             atomic.Bool
	panickingPublisher  ErrorEventPublisher
	nextMsg             uint32
	syncFuncMsg         uint32
	syncLayoutMsg       uint32
	cloakChangeMsg      uint32
	winEventProc        uintptr
	winEventHook        win.HWINEVENTHOOK
	msgWindow           win.HWND
	syncFuncsMutex      sync.Mutex
	syncFuncs           []func()
	syncLayoutMutex     sync.Mutex
	layoutResultsByForm map[Form]*formLayoutResult // Layout computations queued for application
	pToolTip            *ToolTip
	activeMessageLoops  int
	runMsgFilters       bool
}

// Bare minimum initialization that must happen ASAP. While we typically do
// not perform non-trivial work in init(), this case is an exception: we
// absolutely need to lock our OS thread and get single-threaded COM up
// and running as early as possible.
func init() {
	runtime.LockOSThread()
	appSingleton.uiThreadID = win.GetCurrentThreadId()
	if err := com.StartRuntime(com.GUIApp); err != nil {
		log.Printf("wingoes/com.StartRuntime returned %v", err)
	}
}

var (
	appOnce      onceWithPreInit
	appSingleton Application
)

// InitApp must be the first walk function called by the application. It
// returns the singleton *Application, or err if initialization failed.
// It must be called from the main goroutine.
func InitApp() (app *Application, err error) {
	var finalInitOutsideOnce func() error
	appOnce.Init(func() {
		finalInitOutsideOnce, err = appSingleton.init()
	})
	if err != nil {
		return nil, err
	}

	if finalInitOutsideOnce != nil {
		if err := finalInitOutsideOnce(); err != nil {
			return nil, err
		}
	}

	return &appSingleton, nil
}

// App returns the *Application singleton. It panics if InitApp has not been
// called yet.
//
// App may be called from any goroutine once InitApp has completed successfully.
func App() *Application {
	appOnce.Init(func() {
		panic("walk.InitApp must be called first")
	})
	return &appSingleton
}

// OrganizationName returns the string previously set by SetOrganizationName.
// It may be called from any goroutine.
func (app *Application) OrganizationName() string {
	if pn := app.organizationName.Load(); pn != nil {
		return *pn
	}
	return ""
}

// SetOrganizationName sets the app's organization name to value.
// It may be called from any goroutine.
func (app *Application) SetOrganizationName(value string) {
	app.organizationName.Store(&value)
}

// ProductName returns the string previously set by SetProductName.
// It may be called from any goroutine.
func (app *Application) ProductName() string {
	if pn := app.productName.Load(); pn != nil {
		return *pn
	}
	return ""
}

// SetProductName sets the app's product name to value.
// It may be called from any goroutine.
func (app *Application) SetProductName(value string) {
	app.productName.Store(&value)
}

// Settings returns the Settings previously set by SetSettings, or else nil.
// It may be called from any goroutine.
func (app *Application) Settings() Settings {
	if settings, ok := app.settings.Load().(Settings); ok {
		return settings
	}
	return nil
}

// SetSettings sets the app's settings to value.
// It may be called from any goroutine.
func (app *Application) SetSettings(value Settings) {
	app.settings.Store(value)
}

// Exit initiates app shutdown. The app's context is cancelled and the message
// loop running on the main goroutine exits. exitCode is the value that will
// be returned by (*Application).Run. Exit may be called from any goroutine,
// however only the first call has any effect; subsequent invocations are no-ops.
func (app *Application) Exit(exitCode int) {
	if !app.exiting.CompareAndSwap(false, true) {
		return
	}

	app.ctxCancel()

	postQuitMsg := func() {
		win.PostQuitMessage(int32(exitCode))
	}

	if !app.IsUIThread() {
		app.Synchronize(postQuitMsg)
		return
	}

	postQuitMsg()
}

// Panicking returns the ErrorEvent that will be published if walk detects a
// panic during message dispatch. It must be called from the main goroutine.
func (app *Application) Panicking() *ErrorEvent {
	app.AssertUIThread()
	return app.panickingPublisher.Event()
}

// maybePublishPanic is used by walk's top-level WndProcs to recover any
// panics that occurred further down the call stack, convert them to errors,
// and publish them as an event.
func (app *Application) maybePublishPanic() {
	if len(app.panickingPublisher.event.handlers) == 0 {
		return
	}

	var err error
	if x := recover(); x != nil {
		if e, ok := x.(error); ok {
			err = wrapErrorNoPanic(e)
		} else {
			err = newErrorNoPanic(fmt.Sprint(x))
		}
	}
	if err != nil {
		app.panickingPublisher.Publish(err)
	}
}

// IsUIThread returns true if the current goroutine is running on the UI thread.
func (app *Application) IsUIThread() bool {
	// We don't need to lock the OS thread:
	// If we're on the UI thread, we're already locked;
	// If we're not on the UI thread, whatever tid we get will be wrong.
	return win.GetCurrentThreadId() == app.uiThreadID
}

// AssertUIThread panics if the current goroutine is not running on the UI thread.
func (app *Application) AssertUIThread() {
	if !app.IsUIThread() {
		panic("walk: not the UI thread")
	}
}

func (app *Application) init() (finalInitOutsideOnce func() error, err error) {
	app.AssertUIThread()

	app.ctx, app.ctxCancel = context.WithCancel(context.Background())

	app.nextMsg = win.WM_APP
	// No point checking for errors here because we're the first caller; we're
	// not going to exhaust the number space allocating the first few messages.
	app.syncFuncMsg, _ = app.AllocMessage()
	app.syncLayoutMsg, _ = app.AllocMessage()
	app.cloakChangeMsg, _ = app.AllocMessage()

	icc := win.INITCOMMONCONTROLSEX{
		DwSize: uint32(unsafe.Sizeof(win.INITCOMMONCONTROLSEX{})),
		DwICC: win.ICC_BAR_CLASSES | win.ICC_LINK_CLASS | win.ICC_LISTVIEW_CLASSES |
			win.ICC_PROGRESS_CLASS | win.ICC_STANDARD_CLASSES | win.ICC_TAB_CLASSES |
			win.ICC_TREEVIEW_CLASSES,
	}
	if !win.InitCommonControlsEx(&icc) {
		return nil, errInitCommonControlsEx
	}

	// Cloaking is a DWM feature that makes windows invisible, even if they're
	// still "visible" in the traditional sense. This is used by features like
	// virtual desktops. This hook allows us to gain insight as to when a window
	// has been (de)cloaked, which is useful for occlusion detection, animations,
	// timers, etc. It's non-fatal if this call returns an error.
	app.winEventHook, _ = win.SetWinEventHook(
		win.EVENT_OBJECT_CLOAKED, win.EVENT_OBJECT_UNCLOAKED,
		0, appWinEventProc,
		windows.GetCurrentProcessId(), app.uiThreadID,
		win.WINEVENT_OUTOFCONTEXT)

	MustRegisterWindowClassWithWndProcPtr(appMsgWindowClassName, windows.NewCallback(appMsgWndProc))

	wndClass16, err := windows.UTF16PtrFromString(appMsgWindowClassName)
	if err != nil {
		return nil, err
	}

	wndTitle16, err := windows.UTF16PtrFromString(fmt.Sprintf("%s for tid %d", appMsgWindowClassName, app.uiThreadID))
	if err != nil {
		return nil, err
	}

	app.msgWindow = win.CreateWindowEx(
		0, // exStyle
		wndClass16,
		wndTitle16,
		0,                 // style (hidden because win.WS_VISIBLE is absent)
		win.CW_USEDEFAULT, // x
		win.CW_USEDEFAULT, // y
		win.CW_USEDEFAULT, // width
		win.CW_USEDEFAULT, // height
		win.HWND_MESSAGE,  // indicates that this window is a mere message processor
		0,                 // hMenu
		0,                 // hinstance
		nil,               // lpParam
	)
	if app.msgWindow == 0 {
		panic(fmt.Sprintf("unable to create msgWindow for tid %d: Win32 error %d", app.uiThreadID, win.GetLastError()))
	}

	app.layoutResultsByForm = make(map[Form]*formLayoutResult)
	defaultWndProcPtr = windows.NewCallback(defaultWndProc)

	walkInits := app.walkInit
	app.walkInit = nil
	finalInitOutsideOnce = func() (err error) {
		// Need to run these outside appOnce because inits call App() which would
		// attempt to reenter appOnce.
		for _, fn := range walkInits {
			fn()
		}

		app.pToolTip, err = NewToolTip()
		return err
	}

	return finalInitOutsideOnce, nil
}

// AllocMessage allocates a Win32 message code to be used for an
// application-defined purpose. It returns win.WM_NULL and ErrNoMoreMessages if
// all message codes are exhausted. It must be called from the main goroutine.
func (app *Application) AllocMessage() (uint32, error) {
	app.AssertUIThread()

	if app.nextMsg >= firstRegisteredMessage {
		return win.WM_NULL, ErrNoMoreMessages
	}

	ret := app.nextMsg
	app.nextMsg++

	return ret, nil
}

// Run starts the main message loop for the application, and will continue
// running until (*Application).Exit is called. It returns the exitCode that
// was passed into Exit. Run must be called from the main goroutine.
func (app *Application) Run() int {
	app.AssertUIThread()
	exitCode := app.runMainMessageLoop()

	// Critical shutdown goes here; only the minimum necessary work to prevent
	// data loss.

	return exitCode
}

func (app *Application) runMainMessageLoop() int {
	if app.activeMessageLoops != 0 {
		panic("Unexpected nesting of top-level message loop")
	}
	app.activeMessageLoops++
	defer func() {
		app.activeMessageLoops--
	}()

	// DO NOT put anything else here! Put it in (*Application).Run() instead!

	var msg win.MSG
	for win.GetMessage(&msg, 0, 0, 0) != 0 {
		if app.runPreTranslateHandler(&msg) {
			continue
		}

		win.TranslateMessage(&msg)
		win.DispatchMessage(&msg)

		app.runPostDispatchHandler(&msg)
	}

	// DO NOT put anything here! Put it in (*Application).Run() instead!

	return int(msg.WParam)
}

func (app *Application) runPreTranslateHandler(msg *win.MSG) bool {
	w := getMsgWindow(msg)
	if w == nil {
		return false
	}

	if h, ok := w.(PreTranslateHandler); ok {
		return h.OnPreTranslate(msg)
	}

	return false
}

func (app *Application) runPostDispatchHandler(msg *win.MSG) {
	w := getMsgWindow(msg)
	if w == nil {
		return
	}

	if h, ok := w.(PostDispatchHandler); ok {
		h.OnPostDispatch()
	}
}

func getMsgWindow(msg *win.MSG) Window {
	return windowFromHandle(msg.HWnd)
}

func areSentMessagesPending() bool {
	return (win.HIWORD(win.GetQueueStatus(win.QS_SENDMESSAGE)) & win.QS_SENDMESSAGE) != 0
}

func arePostedMessagesPending() bool {
	// Why do we use PeekMessage and not GetQueueStatus? Essentially because we
	// want to distinguish between posted messages that belong to our thread vs
	// posted messages that belong to any other thread attached to our input
	// queue. MsgWaitForMultipleObjectsEx has already told us that *something*
	// is pending; this is the best way for us to determine whether that thing
	// belongs to our thread.
	var msg win.MSG
	return win.PeekMessage(&msg, 0, 0, 0, win.PM_NOREMOVE)
}

func areAnyMessagesPending() bool {
	// NOTE: areSentMessagesPending must always be called *before*
	// arePostedMessagesPending; the latter call has side effects that affect the former!
	return areSentMessagesPending() || arePostedMessagesPending()
}

func waitForNextMessage() {
	// (dblohm7): I don't like WaitMessage. Instead I've implemented an
	// alternative (similar to the one I wrote for Firefox) that is more versatile
	// and does a better job dealing with third-party apps doing bad things like
	// calling AttachThreadInput.
	// See https://web.archive.org/web/20240116100629/https://dblohm7.ca/blog/2015/03/12/waitmessage-considered-harmful/
	// and https://web.archive.org/web/20240308184933/https://searchfox.org/mozilla-central/rev/d572999cdc8f41591ce2f4d0b13486cc9aad2123/widget/windows/WinUtils.cpp#435
	waitForNextMessageOrHandleWithTimeout(nil, windows.INFINITE)
}

const _MAXIMUM_WAIT_OBJECTS = 64

func waitForNextMessageOrHandleWithTimeout(handles []windows.Handle, timeoutMilliseconds uint32) int {
	isTimeoutInfinite := timeoutMilliseconds == windows.INFINITE

	// MsgWaitForMultipleObjectsEx actually uses _MAXIMUM_WAIT_OBJECTS-1
	hl := min(uint32(len(handles)), uint32(_MAXIMUM_WAIT_OBJECTS-1))
	hp := unsafe.SliceData(handles)

	start := win.GetTickCount64()
	elapsed := uint32(0)

	for {
		if !isTimeoutInfinite {
			elapsed = uint32(win.GetTickCount64() - start)
		}
		if elapsed >= timeoutMilliseconds {
			break
		}

		waitCode, err := win.MsgWaitForMultipleObjectsEx(hl, hp, timeoutMilliseconds-elapsed, win.QS_ALLINPUT, win.MWMO_INPUTAVAILABLE)
		if err != nil {
			panic(fmt.Sprintf("MsgWaitForMultipleObjectsEx: %v", err))
		}
		if windows.Errno(waitCode) == windows.WAIT_TIMEOUT {
			break
		}
		if waitCode >= windows.WAIT_OBJECT_0 && waitCode < (windows.WAIT_OBJECT_0+hl) {
			return int(waitCode - windows.WAIT_OBJECT_0)
		}

		if areAnyMessagesPending() {
			break
		}

		// Message is intended for another thread whose input queue is synchronized with ours.
		// Yield to that thread, allowing it to process its messages.
		win.SwitchToThread()
	}

	return -1
}

func popMessage(msg *win.MSG) (gotMsg bool, quit bool) {
	gotMsg = win.PeekMessage(msg, 0, 0, 0, win.PM_REMOVE)
	if gotMsg {
		quit = msg.Message == win.WM_QUIT
		if quit {
			// re-post the quit message so that any outer message loops will pick it up
			win.PostQuitMessage(int32(msg.WParam))
		}
	}

	return gotMsg, quit
}

// PreTranslateHandler is an optional interface that may be implemented by
// components that would like to examine (and possibly alter) certain messages
// prior to translation and dispatch.
type PreTranslateHandler interface {
	// OnPreTranslate supplies msg to the handler for processing. The handler
	// must return true if it processed msg itself and should not be dispatched.
	OnPreTranslate(msg *win.MSG) bool
}

// PostDispatchHander is an optional interface that may be implemented by
// components that would like to be notified once message dispatch has
// completed and control has returned to the event loop.
type PostDispatchHandler interface {
	OnPostDispatch()
}

// Modal is the interface that must be implemented by any component intending
// to utilize walk's common modal event loop implmentation.
type Modal interface {
	// A Modal is Disposed as soon as its event loop completes.
	Disposable
	// Most Modals should implement PreTranslateHandler by delegating to
	// DefaultModalPreTranslate.
	PreTranslateHandler
	// HandleKeyDown processes WM_KEYDOWN messages prior to any translation
	// to WM_CHAR messages. Used for processing hotkeys. Return true if the
	// key event was processed, otherwide return false to ensure default handling.
	HandleKeyDown(msg *win.MSG) bool
	// EnterMode is called immediately prior to entering the modal event loop.
	// Note that there is no corresponding ExitMode, as Dispose is an implicit
	// indicator that the modal loop has terminated.
	EnterMode()
	// OwnerWindow returns the Modal's owner/parent Window, or nil if there is none.
	OwnerWindow() Window
	// Running must return true until the user requests to close the Modal, after
	// which it must return false.
	Running() bool
	// Window returns the Window implemented by the Modal.
	Window() Window
}

// RunModal runs a modal message loop for modal. It does not return until
// the modal window is closed. RunModal supports PostDispatchHandler when
// implemented by modal. modal will be automatically Disposed before RunModal
// returns.
//
// Note that RunModal itself is not responsible for the initial show operation
// on the modal's associated window; the Modal implementation must either
// perform the show in its implementation of EnterMode, or must show prior
// to invoking RunModal.
func (app *Application) RunModal(modal Modal) {
	app.AssertUIThread()
	app.activeMessageLoops++
	defer func() {
		app.activeMessageLoops--
	}()
	defer modal.Dispose()

	postDispatch, handlePostDispatch := modal.(PostDispatchHandler)

	// We're essentially implementing the modal message loop algorithm described by
	// https://web.archive.org/web/20231201222728/https://devblogs.microsoft.com/oldnewthing/20050406-57/?p=35963
	// with a few extras thrown in.
	if owner := modal.OwnerWindow(); owner != nil {
		ohwnd := owner.Handle()
		if !win.EnableWindow(ohwnd, false) {
			defer win.EnableWindow(ohwnd, true)
		}
	}

	modal.EnterMode()

	var msg win.MSG
	for modal.Running() {
		if gotMsg, quit := popMessage(&msg); gotMsg {
			if quit {
				return
			}

			// Useful for debugging, but also disabled by default.
			if app.runMsgFilters && win.CallMsgFilter(&msg, int32(win.MSGF_USER)+int32(app.activeMessageLoops)) {
				continue
			}

			if modal.OnPreTranslate(&msg) {
				continue
			}

			win.TranslateMessage(&msg)
			win.DispatchMessage(&msg)

			if handlePostDispatch {
				postDispatch.OnPostDispatch()
			}
		} else if modal.Running() {
			waitForNextMessage()
		}
	}
}

// DefaultModalPreTranslate contains common code that Modals should invoke
// in order to correctly implement their PreTranslateHandler. m must be the
// Modal itself, and msg must be the pointer that was passed into m's
// OnPreTranslate implementation. Its return value must also be returned
// by the PreTranslateHandler.
func DefaultModalPreTranslate(m Modal, msg *win.MSG) bool {
	hwnd := m.Window().Handle()
	if msgHwnd := msg.HWnd; msg.Message == win.WM_KEYDOWN && (hwnd == msgHwnd || win.IsChild(hwnd, msgHwnd)) && m.HandleKeyDown(msg) {
		return true
	}

	return win.IsDialogMessage(hwnd, msg)
}

func (app *Application) appendToWalkInit(fn func()) {
	app.walkInit = append(app.walkInit, fn)
}

// AppendToWalkInit enqueues fn to be executed by walk during InitApp.
// AppendToWalkInit will panic if called after InitApp has already run.
func AppendToWalkInit(fn func()) {
	ok := appOnce.doPreInit(func() {
		appSingleton.appendToWalkInit(fn)
	})
	if !ok {
		panic("walk.AppendToWalkInit cannot be called after walk.InitApp")
	}
}

func appWinEventProc(hook win.HWINEVENTHOOK, event uint32, hwnd win.HWND, idObject int32, idChild int32, idEventThread uint32, eventTimeMilliseconds uint32) uintptr {
	switch event {
	case win.EVENT_OBJECT_CLOAKED, win.EVENT_OBJECT_UNCLOAKED:
		var wparam uintptr
		if event == win.EVENT_OBJECT_CLOAKED {
			wparam = 1
		}
		win.SendMessage(hwnd, appSingleton.cloakChangeMsg, wparam, 0)
	default:
	}

	return 0
}

func appMsgWndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	defer appSingleton.maybePublishPanic()

	switch msg {
	case appSingleton.syncFuncMsg:
		appSingleton.runSyncFunc()
		return 0
	case appSingleton.syncLayoutMsg:
		appSingleton.runSyncLayout()
		return 0
	default:
		return win.DefWindowProc(hwnd, msg, wParam, lParam)
	}
}

// Synchronize enqueues func f to be called some time later by the main
// goroutine during message loop processing.
func (app *Application) Synchronize(fn func()) {
	app.syncFuncsMutex.Lock()
	app.syncFuncs = append(app.syncFuncs, fn)
	app.syncFuncsMutex.Unlock()
	win.PostMessage(app.msgWindow, app.syncFuncMsg, 0, 0)
}

// synchronizeLayout causes the given layout computations to be applied
// later by the message loop running on the UI thread.
//
// Any previously queued layout computations that have not yet been applied
// will be replaced.
func (app *Application) synchronizeLayout(result *formLayoutResult) {
	app.syncLayoutMutex.Lock()
	app.layoutResultsByForm[result.form] = result
	app.syncLayoutMutex.Unlock()
	win.PostMessage(app.msgWindow, app.syncLayoutMsg, 0, 0)
}

func (app *Application) runSyncFunc() {
	app.syncFuncsMutex.Lock()

	var fn func()
	if len(app.syncFuncs) > 0 {
		fn = app.syncFuncs[0]
		app.syncFuncs = app.syncFuncs[1:]
	}

	app.syncFuncsMutex.Unlock()

	if fn != nil {
		fn()
	}
}

func (app *Application) runSyncLayout() {
	app.syncLayoutMutex.Lock()

	var layoutResults []*formLayoutResult
	if l := len(app.layoutResultsByForm); l > 0 {
		layoutResults = make([]*formLayoutResult, 0, l)
	}

	for _, lr := range app.layoutResultsByForm {
		layoutResults = append(layoutResults, lr)
	}
	clear(app.layoutResultsByForm)

	app.syncLayoutMutex.Unlock()

	for _, lr := range layoutResults {
		applyLayoutResults(lr.results, lr.stopwatch)
	}
}

func (app *Application) toolTip() *ToolTip {
	return app.pToolTip
}

// EnableMessageFilterHooks controls whether WH_MSGFILTER hooks are invoked
// during modal message loops. These hooks are disabled by default. This
// method must be called from the main goroutine.
func (app *Application) EnableMessageFilterHooks(enable bool) {
	app.AssertUIThread()
	app.runMsgFilters = enable
}

// Context returns a Context created during app initialization. It is canceled
// during the first call to (*Application).Exit. Use this Context in place of
// context.Background when needed. This method may be called from any goroutine.
func (app *Application) Context() context.Context {
	return app.ctx
}
