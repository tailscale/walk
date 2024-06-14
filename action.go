// Copyright 2010 The Walk Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package walk

import (
	"runtime"

	"github.com/tailscale/walk/idalloc"
	"github.com/tailscale/win"
)

type actionChangedHandler interface {
	onActionChanged(action *Action) error
	onActionVisibleChanged(action *Action) error
}

// ActionOwnerDrawHandler must be implemented by any struct that wants to
// provide measurement and drawing for owner-drawn menu items.
type ActionOwnerDrawHandler interface {
	OnMeasure(action *Action, mctx *MenuItemMeasureContext) (widthPixels, heightPixels uint32)
	OnDraw(action *Action, dctx *MenuItemDrawContext)
}

var (
	actionIDs       = makeIDAllocator()
	actionsById     = make(map[uint16]*Action)
	shortcut2Action = make(map[Shortcut]*Action)
)

type Action struct {
	menu                          *Menu
	triggeredPublisher            EventPublisher
	changedHandlers               []actionChangedHandler
	text                          string
	toolTip                       string
	image                         Image
	checkedCondition              Condition
	checkedConditionChangedHandle int
	defaultCondition              Condition
	defaultConditionChangedHandle int
	enabledCondition              Condition
	enabledConditionChangedHandle int
	visibleCondition              Condition
	visibleConditionChangedHandle int
	refCount                      int
	shortcut                      Shortcut
	enabled                       bool
	visible                       bool
	checkable                     bool
	checked                       bool
	defawlt                       bool
	exclusive                     bool
	id                            uint16
	ownerDrawInfo                 *ownerDrawnMenuItemInfo
}

// aaron: I don't know why Walk uses menu item IDs for IDOK and IDCANCEL, but
// for now we need to reserve IDs up to and including IDCANCEL (2).
const maxReservedID = win.IDCANCEL

func makeIDAllocator() idalloc.IDAllocator {
	alloc := idalloc.New(1 << 16)
	for i := 0; i <= maxReservedID; i++ {
		alloc.Allocate()
	}
	return alloc
}

func allocActionID() uint16 {
	id, err := actionIDs.Allocate()
	if err != nil {
		panic(err)
	}
	return uint16(id)
}

func freeActionID(id uint16) {
	if id <= maxReservedID {
		return
	}
	actionIDs.Free(uint32(id))
}

func NewAction() *Action {
	a := &Action{
		enabled: true,
		id:      allocActionID(),
		visible: true,
	}
	runtime.SetFinalizer(a, (*Action).finalize)
	return a
}

func NewMenuAction(menu *Menu) *Action {
	a := NewAction()
	a.menu = menu
	return a
}

func NewSeparatorAction() *Action {
	a := &Action{
		enabled: true,
		visible: true,
	}
	runtime.SetFinalizer(a, (*Action).finalize)
	return a
}

func (a *Action) Dispose() {
	a.SetEnabledCondition(nil)
	a.SetVisibleCondition(nil)

	if a.menu != nil {
		a.menu.actions.Clear()
		a.menu.Dispose()
	}

	if a.ownerDrawInfo != nil {
		a.ownerDrawInfo.Dispose()
		a.ownerDrawInfo = nil
	}
}

func (a *Action) finalize() {
	if app := App(); !app.IsUIThread() {
		app.Synchronize(a.finalize)
		return
	}
	a.Dispose()
	freeActionID(a.id)
}

func (a *Action) addRef() {
	a.refCount++
	if a.refCount == 1 {
		actionsById[a.id] = a
		if sc := a.shortcut; sc.Key != 0 {
			shortcut2Action[sc] = a
		}
	}
}

func (a *Action) release() {
	a.refCount--
	if a.refCount == 0 {
		delete(actionsById, a.id)
		if sc := a.shortcut; sc.Key != 0 && shortcut2Action[sc] == a {
			delete(shortcut2Action, sc)
		}
	}
}

func (a *Action) Menu() *Menu {
	return a.menu
}

func (a *Action) Checkable() bool {
	return a.checkable
}

func (a *Action) SetCheckable(value bool) (err error) {
	if value != a.checkable {
		old := a.checkable

		a.checkable = value

		if err = a.raiseChanged(); err != nil {
			a.checkable = old
			a.raiseChanged()
		}
	}

	return
}

func (a *Action) Checked() bool {
	return a.checked
}

func (a *Action) SetChecked(value bool) (err error) {
	if a.checkedCondition != nil {
		if bp, ok := a.checkedCondition.(*boolProperty); ok {
			if err := bp.Set(value); err != nil {
				return err
			}
		} else {
			return newError("CheckedCondition != nil")
		}
	}

	if value != a.checked {
		old := a.checked

		a.checked = value

		if err = a.raiseChanged(); err != nil {
			a.checked = old
			a.raiseChanged()
		}
	}

	return
}

func (a *Action) CheckedCondition() Condition {
	return a.checkedCondition
}

func (a *Action) SetCheckedCondition(c Condition) {
	if a.checkedCondition != nil {
		a.checkedCondition.Changed().Detach(a.checkedConditionChangedHandle)
	}

	a.checkedCondition = c

	if c != nil {
		a.checked = c.Satisfied()

		a.checkedConditionChangedHandle = c.Changed().Attach(func() {
			if a.checked != c.Satisfied() {
				a.checked = !a.checked

				a.raiseChanged()
			}
		})
	}

	a.raiseChanged()
}

func (a *Action) Default() bool {
	return a.defawlt
}

func (a *Action) SetDefault(value bool) (err error) {
	if a.defaultCondition != nil {
		if bp, ok := a.defaultCondition.(*boolProperty); ok {
			if err := bp.Set(value); err != nil {
				return err
			}
		} else {
			return newError("DefaultCondition != nil")
		}
	}

	if value != a.defawlt {
		old := a.defawlt

		a.defawlt = value

		if err = a.raiseChanged(); err != nil {
			a.defawlt = old
			a.raiseChanged()
		}
	}

	return
}

func (a *Action) DefaultCondition() Condition {
	return a.defaultCondition
}

func (a *Action) SetDefaultCondition(c Condition) {
	if a.defaultCondition != nil {
		a.defaultCondition.Changed().Detach(a.defaultConditionChangedHandle)
	}

	a.defaultCondition = c

	if c != nil {
		a.defawlt = c.Satisfied()

		a.defaultConditionChangedHandle = c.Changed().Attach(func() {
			if a.defawlt != c.Satisfied() {
				a.defawlt = !a.defawlt

				a.raiseChanged()
			}
		})
	}

	a.raiseChanged()
}

func (a *Action) Enabled() bool {
	return a.enabled
}

func (a *Action) SetEnabled(value bool) (err error) {
	if a.enabledCondition != nil {
		return newError("EnabledCondition != nil")
	}

	if value != a.enabled {
		old := a.enabled

		a.enabled = value

		if err = a.raiseChanged(); err != nil {
			a.enabled = old
			a.raiseChanged()
		}
	}

	return
}

func (a *Action) EnabledCondition() Condition {
	return a.enabledCondition
}

func (a *Action) SetEnabledCondition(c Condition) {
	prev := a.enabledCondition
	if a.enabledCondition != nil {
		a.enabledCondition.Changed().Detach(a.enabledConditionChangedHandle)
	}

	a.enabledCondition = c

	if c != nil {
		a.enabled = c.Satisfied()

		a.enabledConditionChangedHandle = c.Changed().Attach(func() {
			if a.enabled != c.Satisfied() {
				a.enabled = !a.enabled

				a.raiseChanged()
			}
		})
	}

	if prev != c {
		a.raiseChanged()
	}
}

func (a *Action) Exclusive() bool {
	return a.exclusive
}

func (a *Action) SetExclusive(value bool) (err error) {
	if value != a.exclusive {
		old := a.exclusive

		a.exclusive = value

		if err = a.raiseChanged(); err != nil {
			a.exclusive = old
			a.raiseChanged()
		}
	}

	return
}

func (a *Action) Image() Image {
	return a.image
}

func (a *Action) SetImage(value Image) (err error) {
	if value != a.image {
		old := a.image

		a.image = value

		if err = a.raiseChanged(); err != nil {
			a.image = old
			a.raiseChanged()
		}
	}

	return
}

func (a *Action) Shortcut() Shortcut {
	return a.shortcut
}

func (a *Action) SetShortcut(shortcut Shortcut) (err error) {
	if shortcut != a.shortcut {
		old := a.shortcut

		a.shortcut = shortcut
		defer func() {
			if err != nil {
				a.shortcut = old
			}
		}()

		if err := a.raiseChanged(); err != nil {
			a.shortcut = old
			a.raiseChanged()
			return err
		}

		if a.refCount > 0 {
			if shortcut.Key == 0 {
				if shortcut2Action[old] == a {
					delete(shortcut2Action, old)
				}
			} else {
				shortcut2Action[shortcut] = a
			}
		}
	}
	return nil
}

func (a *Action) Text() string {
	return a.text
}

func (a *Action) SetText(value string) (err error) {
	if value != a.text {
		old := a.text

		a.text = value

		if err = a.raiseChanged(); err != nil {
			a.text = old
			a.raiseChanged()
		}
	}

	return
}

func (a *Action) IsSeparator() bool {
	return a.id == 0 || a.text == "-"
}

func (a *Action) ToolTip() string {
	return a.toolTip
}

func (a *Action) SetToolTip(value string) (err error) {
	if value != a.toolTip {
		old := a.toolTip

		a.toolTip = value

		if err = a.raiseChanged(); err != nil {
			a.toolTip = old
			a.raiseChanged()
		}
	}

	return
}

// SetOwnerDraw converts a into an owner-drawn action whose measurement and
// drawing is carried out by handler.
func (a *Action) SetOwnerDraw(handler ActionOwnerDrawHandler) (err error) {
	if a.ownerDrawInfo == nil && handler == nil {
		// No change
		return
	}

	if a.ownerDrawInfo != nil && a.ownerDrawInfo.handler == handler {
		// No change
		return
	}

	old := a.ownerDrawInfo
	defer func() {
		if old != nil {
			old.Dispose()
		}
	}()
	if handler != nil {
		a.ownerDrawInfo = newOwnerDrawnMenuItemInfo(a, handler)
	} else {
		a.ownerDrawInfo = nil
	}

	if err = a.raiseChanged(); err != nil {
		a.ownerDrawInfo, old = old, a.ownerDrawInfo
		a.raiseChanged()
	}

	return
}

// OwnerDraw returns true when a has a handler registered for owner drawing.
func (a *Action) OwnerDraw() bool {
	return a.ownerDrawInfo != nil
}

func (a *Action) Visible() bool {
	return a.visible
}

func (a *Action) SetVisible(value bool) (err error) {
	if a.visibleCondition != nil {
		return newError("VisibleCondition != nil")
	}

	if value != a.visible {
		old := a.visible

		a.visible = value

		if err = a.raiseVisibleChanged(); err != nil {
			a.visible = old
			a.raiseVisibleChanged()
		}
	}

	return
}

func (a *Action) VisibleCondition() Condition {
	return a.visibleCondition
}

func (a *Action) SetVisibleCondition(c Condition) {
	prev := a.visibleCondition
	if a.visibleCondition != nil {
		a.visibleCondition.Changed().Detach(a.visibleConditionChangedHandle)
	}

	a.visibleCondition = c

	if c != nil {
		a.visible = c.Satisfied()

		a.visibleConditionChangedHandle = c.Changed().Attach(func() {
			if a.visible != c.Satisfied() {
				a.visible = !a.visible

				a.raiseVisibleChanged()
			}
		})
	}

	if prev != c {
		a.raiseChanged()
	}
}

func (a *Action) Triggered() *Event {
	return a.triggeredPublisher.Event()
}

func (a *Action) raiseTriggered() {
	if a.Checkable() {
		a.SetChecked(a.Exclusive() || !a.Checked())
	}

	a.triggeredPublisher.Publish()
}

func (a *Action) addChangedHandler(handler actionChangedHandler) {
	a.changedHandlers = append(a.changedHandlers, handler)
}

func (a *Action) removeChangedHandler(handler actionChangedHandler) {
	for i, h := range a.changedHandlers {
		if h == handler {
			a.changedHandlers = append(a.changedHandlers[:i], a.changedHandlers[i+1:]...)
			break
		}
	}
}

func (a *Action) raiseChanged() error {
	for _, handler := range a.changedHandlers {
		if err := handler.onActionChanged(a); err != nil {
			return err
		}
	}

	return nil
}

func (a *Action) raiseVisibleChanged() error {
	for _, handler := range a.changedHandlers {
		if err := handler.onActionVisibleChanged(a); err != nil {
			return err
		}
	}

	return nil
}
