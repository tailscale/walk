// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows
// +build windows

package walk

import (
	"bytes"
	"math/bits"
	"runtime"
	"time"
	"unsafe"

	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
)

var taskDialogCallback uintptr

// Ordered by corresponding win.TDCBF_* constant
var taskDialogCommonButtonIDs = []int32{
	win.IDOK,
	win.IDYES,
	win.IDNO,
	win.IDCANCEL,
	win.IDRETRY,
	win.IDCLOSE,
}

func taskDialogCallbackWrapper(hwnd win.HWND, msg uint32, wParam uintptr, lParam uintptr, cbData uintptr) uintptr {
	td := (*taskDialog)(unsafe.Pointer(cbData))
	if td == nil {
		return win.E_UNEXPECTED
	}
	return uintptr(td.msgProc(hwnd, msg, wParam, lParam))
}

// TaskDialogCustomButton describes custom buttons or command links, depending
// on the value of TaskDialogOpts.CommandLinkMode.
type TaskDialogCustomButton struct {
	MainText          string // Text for the button, or main text in the command link.
	Note              string // Note text (command links only).
	Default           bool   // true to make this button the default.
	UAC               bool   // true to show a UAC badge next to the button text.
	InitiallyDisabled bool   // true to disable the button when first shown.
	clicked           *ProceedEventPublisher
}

// Clicked returns a ProceedEvent that will be published when tdcb is clicked.
// If the event handler returns false, the TaskDialog terminates.
func (tdcb *TaskDialogCustomButton) Clicked() *ProceedEvent {
	if tdcb.clicked == nil {
		tdcb.clicked = new(ProceedEventPublisher)
	}
	return tdcb.clicked.Event()
}

// TaskDialogRadioButton describes a radio button that will be inserted into
// the TaskDialog.
type TaskDialogRadioButton struct {
	Text              string // The text for the button.
	Default           bool   // true to make this button the default.
	InitiallyDisabled bool   // true to disable the button when first shown.
	clicked           *EventPublisher
}

// Clicked returns an Event that will be published when tdrb is clicked.
func (tdrb *TaskDialogRadioButton) Clicked() *Event {
	if tdrb.clicked == nil {
		tdrb.clicked = new(EventPublisher)
	}
	return tdrb.clicked.Event()
}

const (
	firstIDCustomButton = 128
	firstIDRadioButton  = 256
)

// TaskDialogDefaultButton is an enumeration that indicates which button should
// be considered to be the default button in a TaskDialog. If its value is set
// to TaskDialogDefaultButtonCustom, then the first TaskDialogCustomButton whose
// Default value is set to true will be treated as the default button.
type TaskDialogDefaultButton int

const (
	TaskDialogDefaultButtonOK     TaskDialogDefaultButton = win.IDOK
	TaskDialogDefaultButtonYes    TaskDialogDefaultButton = win.IDYES
	TaskDialogDefaultButtonNo     TaskDialogDefaultButton = win.IDNO
	TaskDialogDefaultButtonCancel TaskDialogDefaultButton = win.IDCANCEL
	TaskDialogDefaultButtonRetry  TaskDialogDefaultButton = win.IDRETRY
	TaskDialogDefaultButtonClose  TaskDialogDefaultButton = win.IDCLOSE
	TaskDialogDefaultButtonCustom TaskDialogDefaultButton = firstIDCustomButton
)

// TaskDialogCommandLinkMode describes whether TaskDialogCustomButton represents
// custom buttons or command links.
type TaskDialogCommandLinkMode int

const (
	TaskDialogCommandLinksDisabled  TaskDialogCommandLinkMode = iota // Custom button.
	TaskDialogCommandLinks                                           // Command link.
	TaskDialogCommandLinksWithGlyph                                  // Command link with an arrow glyph.
)

// TaskDialogProgressBarMode describes whether and how the TaskDialog should
// display its progress bar. While the progress bar may be toggled between
// TaskDialogProgressBar and TaskDialogProgressBarMarquee after the dialog is
// displayed, TaskDialogProgressBarDisabled may only be used at creation time.
type TaskDialogProgressBarMode int

const (
	TaskDialogProgressBarDisabled TaskDialogProgressBarMode = iota // Do not show the progress bar.
	TaskDialogProgressBar                                          // Show the progress bar.
	TaskDialogProgressBarMarquee                                   // Show the progress bar in marquee mode.
)

// TaskDialogProgressBarState describes the error state to use for the TaskDialog's
// progress bar when its mode is set to TaskDialogProgressBar. It has no effect for
// the other two progress bar modes.
type TaskDialogProgressBarState int32

const (
	TaskDialogProgressBarStateNormal TaskDialogProgressBarState = win.PBST_NORMAL // The progress bar is displayed normally.
	TaskDialogProgressBarStateError  TaskDialogProgressBarState = win.PBST_ERROR  // An error has occurred. The progress bar is displayed in red.
	TaskDialogProgressBarStatePaused TaskDialogProgressBarState = win.PBST_PAUSED // The progress bar is displayed in yellow.
)

// TaskDialogSystemIcon describes predefined icons provided by the OS.
type TaskDialogSystemIcon uintptr

const (
	TaskDialogSystemIconWarning     = TaskDialogSystemIcon(win.TD_WARNING_ICON)
	TaskDialogSystemIconError       = TaskDialogSystemIcon(win.TD_ERROR_ICON)
	TaskDialogSystemIconInformation = TaskDialogSystemIcon(win.TD_INFORMATION_ICON)
	TaskDialogSystemIconShield      = TaskDialogSystemIcon(win.TD_SHIELD_ICON)
)

// TaskDialogOpts contains the options used for creating a TaskDialog.
type TaskDialogOpts struct {
	Owner                          Form                               // Optional owner for the dialog.
	Title                          string                             // Title bar text.
	IconImage                      Image                              // Optional custom Image to use as the main dialog icon.
	IconSystem                     TaskDialogSystemIcon               // Predefined icon to use for the main dialog icon. Ignored if IconImage is non-nil.
	Instruction                    string                             // Main instruction / heading text for the dialog.
	Content                        string                             // Main text content for the dialog.
	ProgressBarMode                TaskDialogProgressBarMode          // Initial state for the progress bar.
	RadioButtons                   []TaskDialogRadioButton            // Radio buttons to be included in the TaskDialog. If multiple elements have their Default flag set, only the lowest-indexed element's flag is honoured.
	CommonButtons                  win.TASKDIALOG_COMMON_BUTTON_FLAGS // Common buttons to use in the dialog.
	CommonButtonsUAC               win.TASKDIALOG_COMMON_BUTTON_FLAGS // Of the buttons in CommonButtons, which ones should include a UAC shield.
	CommonButtonsInitiallyDisabled win.TASKDIALOG_COMMON_BUTTON_FLAGS // Of the buttons in CommonButtons, which ones should initially be disabled.
	commonButtonEvents             map[int32]*ProceedEventPublisher
	CustomButtons                  []TaskDialogCustomButton  // Custom buttons / command links to be included in the TaskDialog. If multiple elements have their Default flag set, only the lowest-indexed element's flag is honoured.
	DefaultButton                  TaskDialogDefaultButton   // The default button to use for the TaskDialog. This is the button that will receive a click event if the user presses Enter.
	CommandLinkMode                TaskDialogCommandLinkMode // Whether CustomButtons describes custom buttons or command links.
	ExpandLabel                    string                    // Label for the expand button. Ignored unless ExpandedInformation is non-empty.
	CollapseLabel                  string                    // Label for the collapse button. Ignored unless ExpandedInformation is non-empty.
	ExpandedInformation            string                    // Additional text to be displayed while the dialog is expanded.
	VerificationText               string                    // When non-empty, the TaskDialog will show a verification checkbox labelled with this text.
	FooterIconImage                Image                     // Optional custom Image to use as the icon for the footer.
	FooterIconSystem               TaskDialogSystemIcon      // Predefined icon to use as the footer icon. Ignored if FooterIconImage is non-nil.
	Footer                         string                    // Text for the footer.
	ForceRTLLayout                 *bool                     // When non-nil, forcibly sets RTL layout to the value referenced by the pointer.
	AllowHyperlinks                bool                      // Enable hyperlinks in the Content, ExpandedInformation, and Footer fields.
	InitiallyExpanded              bool                      // When true and least ExpandLabel and ExpandedInformation are non-empty, the dialog will start in an expanded state.
	InitiallyChecked               bool                      // When true and VerificationText is not empty, the verification checkbox will initially be checked.
	Minimizable                    bool                      // true to allow the TaskDialog to be minimizable
	UseTimer                       bool                      // true to start an internal 200ms periodic timer. Call TimerFired() to obtain its event.
}

// CommonButtonClicked obtains the ProceedEvent that will be published when btn
// is clicked, or nil if btn is not set in opts.CommonButtons. Only a single
// button flag should be set in btn.
func (opts *TaskDialogOpts) CommonButtonClicked(btn win.TASKDIALOG_COMMON_BUTTON_FLAGS) *ProceedEvent {
	cbs := opts.CommonButtons
	if cbs&btn == 0 {
		return nil
	}

	if opts.commonButtonEvents == nil {
		opts.commonButtonEvents = make(map[int32]*ProceedEventPublisher, bits.OnesCount32(uint32(cbs)))
	}

	for i, id := range taskDialogCommonButtonIDs {
		if btn&(1<<i) != 0 {
			pub := opts.commonButtonEvents[id]
			if pub == nil {
				pub = new(ProceedEventPublisher)
				opts.commonButtonEvents[id] = pub
			}
			return pub.Event()
		}
	}

	return nil
}

// TaskDialogResult represents state information obtained from the TaskDialog
// after it has terminated.
type TaskDialogResult struct {
	Canceled         bool  // true if the dialog was canceled either via Cancel button or via Escape, Alt+F4...
	Checked          *bool // When non-nil, the state of the validation checkbox.
	RadioButtonIndex *int  // When non-nil, the index of the radio button that was selected.
}

// TaskDialog is an interface that provides support for Windows "Task Dialogs."
// These are dialog boxes with standardized layouts that are quicker to implement
// than custom dialog boxes.
type TaskDialog interface {
	// Show synchronously displays a TaskDialog using opts and returns information
	// about the dialog's state at the time it was dismissed, or an error.
	Show(opts TaskDialogOpts) (result TaskDialogResult, err error)
	// Created returns an Event that is triggered when the Task Dialog has been
	// created but before it is displayed.
	Created() *Event
	// ExpandoClicked returns the event that is triggered when the Task Dialog's
	// expando button is clicked (when present). The event's argument is true
	// when the dialog is to be expanded, and false when the dialog is to be
	// collapsed.
	ExpandoClicked() *ProceedWithArgEvent[bool]
	// Help returns the Event that is triggered when the user requests help.
	Help() *Event
	// HyperlinkClicked returns the event that is triggered when a hyperlink is
	// clicked in the task dialog. The event's argument contains the url for the
	// affected hyperlink.
	HyperlinkClicked() *ProceedWithArgEvent[string]
	// VerificationClicked returns the event that is triggered when the
	// verification checkbox is toggled. The event's argument is true when checked.
	VerificationClicked() *ProceedWithArgEvent[bool]
	// EnableCommonButtons adjusts the enabled/disabled state of common buttons
	// that are present in the currently shown task dialog. btn may contain
	// multiple flags. enable determines whether to enable or disable the affected
	// buttons.
	EnableCommonButtons(btn win.TASKDIALOG_COMMON_BUTTON_FLAGS, enable bool)
	// EnableCustomButton adjusts the enabled/disabled state of a custom button
	// or command link. index is the index of the custom button inside the
	// TaskDialogOpts.CustomButtons slice that was originally provided to
	// TaskDialog.Show(). enable determines whether to enable or disable the
	// affected button.
	EnableCustomButton(index int, enable bool)
	// EnableRadioButton adjusts the enabled/disabled state of a radio button.
	// index is the index of the custom button inside the
	// TaskDialogOpts.RadioButtons slice that was originally provided to
	// TaskDialog.Show(). enable determines whether to enable or disable the
	// affected button.
	EnableRadioButton(index int, enable bool)
	// SetContent updates the dialog's main content text.
	SetContent(text string)
	// SetExpandedInformation updates the additional text displayed when the
	// task dialog is expanded.
	SetExpandedInformation(text string)
	// SetFooter updates the dialog's footer text.
	SetFooter(text string)
	// SetIcon updates the dialog's main icon. Only one of img or sys should be
	// provided; if both are specified, img will be used.
	SetIcon(img Image, sys TaskDialogSystemIcon)
	// SetInstruction updates the dialog's main instruction / heading text.
	SetInstruction(text string)
	// SetProgressBarMode updates the task dialog's progress bar to mode.
	SetProgressBarMode(mode TaskDialogProgressBarMode)
	// SetProgressBarPosition updates the task dialog's progress bar position.
	// This method only has an effect when the progress bar mode is set to
	// TaskDialogProgressBar.
	SetProgressBarPosition(pos uint16)
	// SetProgressBarRange updates the task dialog's progress bar range.
	// This method only has an effect when the progress bar mode is set to
	// TaskDialogProgressBar.
	SetProgressBarRange(low, high uint16)
	// SetProgressBarState updates the task dialog's progress bar state.
	// This method only has an effect when the progress bar mode is set to
	// TaskDialogProgressBar.
	SetProgressBarState(state TaskDialogProgressBarState)
	// SetFooterIcon updates the dialog's footer icon. Only one of img or sys
	// should be provided; if both are specified, img will be used.
	SetFooterIcon(img Image, sys TaskDialogSystemIcon)
	// TimerFired returns the event that is triggered when the built-in timer
	// fires. The event's argument contains the elapsed time since the dialog
	// was created.
	TimerFired() *GenericEvent[time.Duration]
}

type taskDialog struct {
	opts                *TaskDialogOpts
	hwnd                win.HWND
	created             EventPublisher
	expandoClicked      ProceedWithArgEventPublisher[bool]
	help                EventPublisher
	hyperlinkClicked    ProceedWithArgEventPublisher[string]
	timerFired          GenericEventPublisher[time.Duration]
	verificationClicked ProceedWithArgEventPublisher[bool]
}

// NewTaskDialog instantiates a new TaskDialog. It must only be called from the
// UI goroutine.
func NewTaskDialog() TaskDialog {
	App().AssertUIThread()
	if taskDialogCallback == 0 {
		taskDialogCallback = windows.NewCallback(taskDialogCallbackWrapper)
	}

	return new(taskDialog)
}

func (td *taskDialog) Show(opts TaskDialogOpts) (result TaskDialogResult, err error) {
	App().AssertUIThread()
	td.opts = &opts
	defer func() {
		td.opts = nil
	}()

	var rtl bool
	var ownerHWND win.HWND
	if opts.Owner != nil {
		ownerHWND = opts.Owner.Handle()
		if opts.ForceRTLLayout == nil {
			// Use the owner to determine RTL
			rtl = opts.Owner.RightToLeftReading()
		}
	}

	if opts.ForceRTLLayout != nil {
		rtl = *opts.ForceRTLLayout
	}

	flags := win.TDF_SIZE_TO_CONTENT

	if rtl {
		flags |= win.TDF_RTL_LAYOUT
	}

	// Always allow cancellation; it's bad UX not to.
	if opts.CommonButtons&win.TDCBF_CANCEL_BUTTON == 0 {
		flags |= win.TDF_ALLOW_DIALOG_CANCELLATION
	}

	if opts.IconImage != nil {
		flags |= win.TDF_USE_HICON_MAIN
	}

	if opts.FooterIconImage != nil {
		flags |= win.TDF_USE_HICON_FOOTER
	}

	if opts.AllowHyperlinks {
		flags |= win.TDF_ENABLE_HYPERLINKS
	}

	if opts.InitiallyExpanded {
		flags |= win.TDF_EXPANDED_BY_DEFAULT
	}

	if opts.InitiallyChecked {
		flags |= win.TDF_VERIFICATION_FLAG_CHECKED
	}

	if opts.Minimizable {
		flags |= win.TDF_CAN_BE_MINIMIZED
	}

	if opts.UseTimer {
		flags |= win.TDF_CALLBACK_TIMER
	}

	switch opts.ProgressBarMode {
	case TaskDialogProgressBar:
		flags |= win.TDF_SHOW_PROGRESS_BAR
	case TaskDialogProgressBarMarquee:
		flags |= win.TDF_SHOW_MARQUEE_PROGRESS_BAR
	default:
	}

	switch opts.CommandLinkMode {
	case TaskDialogCommandLinks:
		flags |= win.TDF_USE_COMMAND_LINKS_NO_ICON
	case TaskDialogCommandLinksWithGlyph:
		flags |= win.TDF_USE_COMMAND_LINKS
	default:
	}

	defaultRadioButtonID := td.defaultRadioButtonID()
	if len(opts.RadioButtons) > 0 && defaultRadioButtonID == 0 {
		flags |= win.TDF_NO_DEFAULT_RADIO_BUTTON
	}

	var title *uint16
	if opts.Title != "" {
		title, err = windows.UTF16PtrFromString(opts.Title)
		if err != nil {
			return result, err
		}
	}

	var instruction *uint16
	if opts.Instruction != "" {
		instruction, err = windows.UTF16PtrFromString(opts.Instruction)
		if err != nil {
			return result, err
		}
	}

	var content *uint16
	if opts.Content != "" {
		content, err = windows.UTF16PtrFromString(opts.Content)
		if err != nil {
			return result, err
		}
	}

	var expandLabel *uint16
	if opts.ExpandLabel != "" {
		expandLabel, err = windows.UTF16PtrFromString(opts.ExpandLabel)
		if err != nil {
			return result, err
		}
	}

	var collapseLabel *uint16
	if opts.CollapseLabel != "" {
		collapseLabel, err = windows.UTF16PtrFromString(opts.CollapseLabel)
		if err != nil {
			return result, err
		}
	}

	var expandedInfo *uint16
	if opts.ExpandedInformation != "" {
		expandedInfo, err = windows.UTF16PtrFromString(opts.ExpandedInformation)
		if err != nil {
			return result, err
		}
	}

	var verificationText *uint16
	if opts.VerificationText != "" {
		verificationText, err = windows.UTF16PtrFromString(opts.VerificationText)
		if err != nil {
			return result, err
		}
	}

	var footer *uint16
	if opts.Footer != "" {
		footer, err = windows.UTF16PtrFromString(opts.Footer)
		if err != nil {
			return result, err
		}
	}

	customButtons := make([]win.TASKDIALOG_BUTTON_UNPACKED, 0, len(opts.CustomButtons))
	for i, btn := range opts.CustomButtons {
		text := btn.MainText
		// Notes are only usable when command links are enabled.
		if opts.CommandLinkMode > TaskDialogCommandLinksDisabled && btn.Note != "" {
			text += "\n" + btn.Note
		}

		text16, err := windows.UTF16PtrFromString(text)
		if err != nil {
			return result, err
		}

		customButtons = append(customButtons, win.TASKDIALOG_BUTTON_UNPACKED{
			ButtonID:   td.customIndexToID(i),
			ButtonText: text16,
		})
	}

	pButtons, err := td.packButtonSlice(customButtons)
	if err != nil {
		return result, err
	}

	radioButtons := make([]win.TASKDIALOG_BUTTON_UNPACKED, 0, len(opts.RadioButtons))
	for i, btn := range opts.RadioButtons {
		text16, err := windows.UTF16PtrFromString(btn.Text)
		if err != nil {
			return result, err
		}

		radioButtons = append(radioButtons, win.TASKDIALOG_BUTTON_UNPACKED{
			ButtonID:   td.radioIndexToID(i),
			ButtonText: text16,
		})
	}

	pRadioButtons, err := td.packButtonSlice(radioButtons)
	if err != nil {
		return result, err
	}

	cfg := win.TASKDIALOGCONFIG_UNPACKED{
		HWNDParent:           ownerHWND,
		Flags:                flags,
		CommonButtons:        opts.CommonButtons,
		WindowTitle:          title,
		MainIcon:             td.getIcon(opts.IconImage, opts.IconSystem),
		MainInstruction:      instruction,
		Content:              content,
		CButtons:             uint32(len(customButtons)),
		PButtons:             pButtons,
		DefaultButton:        td.defaultButtonID(),
		CRadioButtons:        uint32(len(radioButtons)),
		PRadioButtons:        pRadioButtons,
		DefaultRadioButton:   defaultRadioButtonID,
		VerificationText:     verificationText,
		ExpandedInformation:  expandedInfo,
		ExpandedControlText:  collapseLabel,
		CollapsedControlText: expandLabel,
		FooterIcon:           td.getIcon(opts.FooterIconImage, opts.FooterIconSystem),
		Footer:               footer,
		Callback:             taskDialogCallback,
		CallbackData:         uintptr(unsafe.Pointer(td)),
	}

	var buttonID, radioButtonID int32
	var checked win.BOOL
	if hr := win.TaskDialogIndirect(cfg.Pack(), &buttonID, &radioButtonID, &checked); win.FAILED(hr) {
		return result, errorFromHRESULT("TaskDialogIndirect", hr)
	}

	// We've taken various pointers and packed them in a way that Go won't
	// recognize. Ensure the original data is kept alive during the API call.
	runtime.KeepAlive(cfg)
	runtime.KeepAlive(customButtons)
	runtime.KeepAlive(radioButtons)

	result.Canceled = buttonID == win.IDCANCEL
	if opts.VerificationText != "" {
		vchecked := checked != 0
		result.Checked = &vchecked
	}
	if cfg.CRadioButtons > 0 && cfg.PRadioButtons != nil {
		rbidx := td.radioIDToIndex(radioButtonID)
		result.RadioButtonIndex = &rbidx
	}

	return result, nil
}

func (td *taskDialog) Created() *Event {
	return td.created.Event()
}

func (td *taskDialog) Help() *Event {
	return td.help.Event()
}

func (td *taskDialog) ExpandoClicked() *ProceedWithArgEvent[bool] {
	return td.expandoClicked.Event()
}

func (td *taskDialog) VerificationClicked() *ProceedWithArgEvent[bool] {
	return td.verificationClicked.Event()
}

func (td *taskDialog) HyperlinkClicked() *ProceedWithArgEvent[string] {
	return td.hyperlinkClicked.Event()
}

func (td *taskDialog) TimerFired() *GenericEvent[time.Duration] {
	return td.timerFired.Event()
}

func (td *taskDialog) setText(text string, elem win.TASKDIALOG_ELEMENTS) {
	if td.hwnd == 0 {
		// We are not showing yet so we can just update opts.
		if opts := td.opts; opts != nil {
			switch elem {
			case win.TDE_CONTENT:
				opts.Content = text
			case win.TDE_EXPANDED_INFORMATION:
				opts.ExpandedInformation = text
			case win.TDE_FOOTER:
				opts.Footer = text
			case win.TDE_MAIN_INSTRUCTION:
				opts.Instruction = text
			default:
			}
		}
		return
	}

	// Otherwise we need to send a message to update the dialog.
	text16, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return
	}

	win.SendMessage(td.hwnd, win.TDM_SET_ELEMENT_TEXT, uintptr(elem), uintptr(unsafe.Pointer(text16)))
}

func (td *taskDialog) SetInstruction(text string) {
	td.setText(text, win.TDE_MAIN_INSTRUCTION)
}

func (td *taskDialog) SetContent(text string) {
	td.setText(text, win.TDE_CONTENT)
}

func (td *taskDialog) SetFooter(text string) {
	td.setText(text, win.TDE_FOOTER)
}

func (td *taskDialog) SetExpandedInformation(text string) {
	td.setText(text, win.TDE_EXPANDED_INFORMATION)
}

func (td *taskDialog) SetProgressBarMode(mode TaskDialogProgressBarMode) {
	if td.hwnd == 0 {
		// We are not showing yet so we can just update opts.
		if opts := td.opts; opts != nil {
			opts.ProgressBarMode = mode
		}
		return
	}

	if mode == TaskDialogProgressBarMarquee {
		win.SendMessage(td.hwnd, win.TDM_SET_MARQUEE_PROGRESS_BAR, 1, 0)
		win.SendMessage(td.hwnd, win.TDM_SET_PROGRESS_BAR_MARQUEE, 1, 0)
		return
	}

	// Notice these calls are ordered in reverse from the ones above.
	win.SendMessage(td.hwnd, win.TDM_SET_PROGRESS_BAR_MARQUEE, 0, 0)
	win.SendMessage(td.hwnd, win.TDM_SET_MARQUEE_PROGRESS_BAR, 0, 0)
}

func (td *taskDialog) SetProgressBarPosition(pos uint16) {
	win.SendMessage(td.hwnd, win.TDM_SET_PROGRESS_BAR_POS, uintptr(pos), 0)
}

func (td *taskDialog) SetProgressBarRange(low, high uint16) {
	win.SendMessage(td.hwnd, win.TDM_SET_PROGRESS_BAR_RANGE, 0, uintptr(win.MAKELONG(low, high)))
}

func (td *taskDialog) SetProgressBarState(state TaskDialogProgressBarState) {
	win.SendMessage(td.hwnd, win.TDM_SET_PROGRESS_BAR_STATE, uintptr(state), 0)
}

func (td *taskDialog) updateIcon(img Image, sys TaskDialogSystemIcon, elem win.TASKDIALOG_ICON_ELEMENTS) {
	if td.hwnd == 0 {
		// We are not showing yet so we can just update opts.
		if opts := td.opts; opts != nil {
			switch elem {
			case win.TDIE_ICON_MAIN:
				opts.IconImage = img
				opts.IconSystem = sys
			case win.TDIE_ICON_FOOTER:
				opts.FooterIconImage = img
				opts.FooterIconSystem = sys
			default:
			}
		}
		return
	}

	// Otherwise we need to send a message to update the dialog.
	lparam := td.getIcon(img, sys)
	win.SendMessage(td.hwnd, win.TDM_UPDATE_ICON, uintptr(elem), lparam)
}

func (td *taskDialog) SetIcon(img Image, sys TaskDialogSystemIcon) {
	td.updateIcon(img, sys, win.TDIE_ICON_MAIN)
}

func (td *taskDialog) SetFooterIcon(img Image, sys TaskDialogSystemIcon) {
	td.updateIcon(img, sys, win.TDIE_ICON_FOOTER)
}

func (td *taskDialog) EnableCommonButtons(btns win.TASKDIALOG_COMMON_BUTTON_FLAGS, enable bool) {
	if td.opts == nil {
		return
	}

	btns &= td.opts.CommonButtons
	if btns == 0 {
		return
	}

	var lparam uintptr
	if enable {
		lparam = 1
	}

	for i, id := range taskDialogCommonButtonIDs {
		if btns&(1<<i) != 0 {
			win.SendMessage(td.hwnd, win.TDM_ENABLE_BUTTON, uintptr(id), lparam)
		}
	}
}

func (td *taskDialog) EnableCustomButton(index int, enable bool) {
	wparam := uintptr(td.customIndexToID(index))
	lparam := uintptr(0)
	if enable {
		lparam = 1
	}

	win.SendMessage(td.hwnd, win.TDM_ENABLE_BUTTON, wparam, lparam)
}

func (td *taskDialog) EnableRadioButton(index int, enable bool) {
	wparam := uintptr(td.radioIndexToID(index))
	lparam := uintptr(0)
	if enable {
		lparam = 1
	}

	win.SendMessage(td.hwnd, win.TDM_ENABLE_RADIO_BUTTON, wparam, lparam)
}

func (td *taskDialog) msgProc(hwnd win.HWND, msg uint32, wParam uintptr, lParam uintptr) win.HRESULT {
	switch msg {
	case win.TDN_CREATED:
		td.hwnd = hwnd
		td.configureUACButtons()
		td.disableButtons()
		td.maybeHideTitleBarIcon()
		td.created.Publish()
	case win.TDN_NAVIGATED:
	case win.TDN_BUTTON_CLICKED:
		if td.handleButtonClicked(int32(wParam)) {
			return win.S_FALSE
		}
	case win.TDN_HYPERLINK_CLICKED:
		url := windows.UTF16PtrToString((*uint16)(unsafe.Pointer(uintptr(lParam))))
		if td.hyperlinkClicked.Publish(url) {
			return win.S_FALSE
		}
	case win.TDN_TIMER:
		td.timerFired.Publish(time.Duration(wParam) * time.Millisecond)
	case win.TDN_DESTROYED:
		td.hwnd = 0
	case win.TDN_RADIO_BUTTON_CLICKED:
		td.handleRadioButtonClicked(int32(wParam))
	case win.TDN_DIALOG_CONSTRUCTED:
	case win.TDN_VERIFICATION_CLICKED:
		if td.verificationClicked.Publish(wParam != 0) {
			return win.S_FALSE
		}
	case win.TDN_HELP:
		td.help.Publish()
	case win.TDN_EXPANDO_BUTTON_CLICKED:
		if td.expandoClicked.Publish(wParam != 0) {
			return win.S_FALSE
		}
	default:
	}

	return win.S_OK
}

func (td *taskDialog) handleButtonClicked(id int32) bool {
	var pub *ProceedEventPublisher
	if id < firstIDCustomButton {
		pub = td.opts.commonButtonEvents[id]
		if pub == nil {
			// By default, clicking a common button will terminate the dialog
			return false
		}
	} else {
		pub = td.opts.CustomButtons[td.customIDToIndex(id)].clicked
		if pub == nil {
			// By default, clicking a custom button will terminate the dialog, but
			// clicking a command link will not.
			return td.opts.CommandLinkMode != TaskDialogCommandLinksDisabled
		}
	}

	return pub.Publish()
}

func (td *taskDialog) handleRadioButtonClicked(id int32) {
	if pub := td.opts.RadioButtons[td.radioIDToIndex(id)].clicked; pub != nil {
		pub.Publish()
	}
}

func (td *taskDialog) configureUACButtons() {
	if common := (td.opts.CommonButtons & td.opts.CommonButtonsUAC); common != 0 {
		for i, id := range taskDialogCommonButtonIDs {
			if common&(1<<i) != 0 {
				win.SendMessage(td.hwnd, win.TDM_SET_BUTTON_ELEVATION_REQUIRED_STATE, uintptr(id), 1)
			}
		}
	}

	for i, btn := range td.opts.CustomButtons {
		if btn.UAC {
			win.SendMessage(td.hwnd, win.TDM_SET_BUTTON_ELEVATION_REQUIRED_STATE, uintptr(td.customIndexToID(i)), 1)
		}
	}
}

func (td *taskDialog) disableButtons() {
	if common := (td.opts.CommonButtons & td.opts.CommonButtonsInitiallyDisabled); common != 0 {
		td.EnableCommonButtons(common, false)
	}

	for i, btn := range td.opts.CustomButtons {
		if btn.InitiallyDisabled {
			td.EnableCustomButton(i, false)
		}
	}

	for i, btn := range td.opts.RadioButtons {
		if btn.InitiallyDisabled {
			td.EnableRadioButton(i, false)
		}
	}
}

// maybeHideTitleBarIcon hides the title bar icon on unowned TaskDialogs, which
// duplicate their main icon in the title bar and looks silly.
func (td *taskDialog) maybeHideTitleBarIcon() {
	// Only unowned windows need this.
	if td.opts.Owner != nil {
		return
	}

	// Get window style bits.
	win.SetLastError(win.ERROR_SUCCESS)
	style := win.GetWindowLong(td.hwnd, win.GWL_STYLE)
	if style == 0 && win.GetLastError() != win.ERROR_SUCCESS {
		return
	}

	style &= ^int32(win.WS_SYSMENU)

	// Set window style bits.
	win.SetLastError(win.ERROR_SUCCESS)
	if win.SetWindowLong(td.hwnd, win.GWL_STYLE, style) == 0 && win.GetLastError() != win.ERROR_SUCCESS {
		return
	}

	// After changing window style, call SetWindowPos with SWP_FRAMECHANGED to repaint the window frame.
	const swpFlags = win.SWP_FRAMECHANGED | win.SWP_NOACTIVATE | win.SWP_NOZORDER | win.SWP_NOMOVE | win.SWP_NOSIZE
	win.SetWindowPos(td.hwnd, 0, 0, 0, 0, 0, swpFlags)
}

func (td *taskDialog) defaultButtonID() int32 {
	opts := td.opts
	if opts.DefaultButton != TaskDialogDefaultButtonCustom {
		return int32(opts.DefaultButton)
	}

	for i, btn := range opts.CustomButtons {
		if btn.Default {
			return td.customIndexToID(i)
		}
	}

	return 0
}

func (td *taskDialog) defaultRadioButtonID() int32 {
	opts := td.opts
	for i, btn := range opts.RadioButtons {
		if btn.Default {
			return td.radioIndexToID(i)
		}
	}

	return 0
}

func (td *taskDialog) customIndexToID(index int) int32 {
	return int32(index) + firstIDCustomButton
}

func (td *taskDialog) customIDToIndex(id int32) int {
	return int(id) - firstIDCustomButton
}

func (td *taskDialog) radioIndexToID(index int) int32 {
	return int32(index) + firstIDRadioButton
}

func (td *taskDialog) radioIDToIndex(id int32) int {
	return int(id) - firstIDRadioButton
}

// getIcon resolves the icon to use in the dialog. sys is ignored if img is
// non-nil.
func (td *taskDialog) getIcon(img Image, sys TaskDialogSystemIcon) uintptr {
	if img != nil {
		dpi := td.getDPI()
		ic, err := iconCache.Icon(img, dpi)
		if err != nil {
			return 0
		}

		return uintptr(ic.handleForDPI(dpi))
	}

	return uintptr(sys)
}

func (td *taskDialog) getDPI() int {
	if td.hwnd != 0 {
		wb := WindowBase{hWnd: td.hwnd}
		return wb.DPI()
	}
	if td.opts != nil && td.opts.Owner != nil {
		return td.opts.Owner.DPI()
	}
	return screenDPI()
}

func (td *taskDialog) packButtonSlice(unpacked []win.TASKDIALOG_BUTTON_UNPACKED) (*win.TASKDIALOG_BUTTON, error) {
	if len(unpacked) == 0 {
		return nil, nil
	}

	buf := bytes.NewBuffer(make([]byte, 0, len(unpacked)*int(unsafe.Sizeof(win.TASKDIALOG_BUTTON{}))))
	for _, btn := range unpacked {
		if err := btn.Pack(buf); err != nil {
			return nil, err
		}
	}

	bb := buf.Bytes()
	return (*win.TASKDIALOG_BUTTON)(unsafe.Pointer(&bb[0])), nil
}
