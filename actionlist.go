// Copyright 2010 The Walk Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package walk

import "errors"

type actionListObserver interface {
	onInsertedAction(action *Action) error
	onRemovingAction(action *Action) error
	onClearingActions() error
}

type nopActionListObserver struct{}

func (nopActionListObserver) onInsertedAction(action *Action) error {
	return nil
}

func (nopActionListObserver) onRemovingAction(action *Action) error {
	return nil
}

func (nopActionListObserver) onClearingActions() error {
	return nil
}

type ActionList struct {
	actions  []*Action
	observer actionListObserver
}

func newActionList(observer actionListObserver) *ActionList {
	if observer == nil {
		panic("observer == nil")
	}

	return &ActionList{observer: observer}
}

func (l *ActionList) Add(action *Action) error {
	return l.Insert(len(l.actions), action)
}

func (l *ActionList) AddMenu(menu *Menu) (*Action, error) {
	return l.InsertMenu(len(l.actions), menu)
}

func (l *ActionList) At(index int) *Action {
	return l.actions[index]
}

func (l *ActionList) Clear() error {
	if err := l.observer.onClearingActions(); err != nil {
		return err
	}

	for _, a := range l.actions {
		a.release()
	}

	l.actions = l.actions[:0]

	return nil
}

func (l *ActionList) Contains(action *Action) bool {
	return l.Index(action) > -1
}

func (l *ActionList) Index(action *Action) int {
	for i, a := range l.actions {
		if a == action {
			return i
		}
	}

	return -1
}

func (l *ActionList) indexInObserver(action *Action) int {
	var idx int

	for _, a := range l.actions {
		if a == action {
			return idx
		}
		if a.Visible() {
			idx++
		}
	}

	return -1
}

// positionsForExclusiveCheck determines the positional index values to be
// passed in to win.CheckMenuRadioItem when action is to be set as checked.
func (l *ActionList) positionsForExclusiveCheck(action *Action) (first, last, index int, err error) {
	actionsIdx := -1

	// Find the index of action in actions. We must compute this index in two
	// distinct ways: both including and excluding hidden items.
	// We need both because we need to iterate through l.actions, but we also need
	// to provide indexes to Windows that exclude hidden items.

	// This loop is identical to (*ActionList).indexInObserver except that we also
	// retain actionsIdx, the index that includes hidden items.
	for i, a := range l.actions {
		if a == action {
			actionsIdx = i
			break
		}
		if a.Visible() {
			index++
		}
	}

	if actionsIdx < 0 {
		return first, last, index, newError("action not found")
	}

	// Iterate backward from action's index, only including visible items.
	first = index
	for i := actionsIdx - 1; i >= 0; i-- {
		a := l.actions[i]
		if !a.Exclusive() {
			break
		}
		if a.Visible() {
			first--
		}
	}

	// Iterate forward from action's index, only including visible items.
	last = index
	for _, a := range l.actions[actionsIdx+1:] {
		if !a.Exclusive() {
			break
		}
		if a.Visible() {
			last++
		}
	}

	return first, last, index, nil
}

// uncheckActionsForExclusiveCheck marks actions that have positional index values
// between first and last, excluding the checked action, as unchecked.
func (l *ActionList) uncheckActionsForExclusiveCheck(first, last, checked int) error {
	if first < 0 || first > last {
		return errors.New("first must be >= 0 and <= last")
	}
	if checked < first || checked > last {
		return errors.New("checked must be >= first and <= last")
	}
	if first == last {
		return nil
	}

	i := -1
	for _, a := range l.actions {
		if a.Visible() {
			i++
			if i == checked {
				continue
			}
		}
		if i < first {
			continue
		}
		if i > last {
			break
		}
		if err := a.SetChecked(false); err != nil {
			return err
		}
	}

	return nil
}

func (l *ActionList) Insert(index int, action *Action) error {
	l.actions = append(l.actions, nil)
	copy(l.actions[index+1:], l.actions[index:])
	l.actions[index] = action

	if err := l.observer.onInsertedAction(action); err != nil {
		l.actions = append(l.actions[:index], l.actions[index+1:]...)

		return err
	}

	action.addRef()

	if action.Visible() {
		return l.updateSeparatorVisibility()
	}

	return nil
}

func (l *ActionList) InsertMenu(index int, menu *Menu) (*Action, error) {
	action := NewAction()
	action.menu = menu

	if err := l.Insert(index, action); err != nil {
		return nil, err
	}

	return action, nil
}

func (l *ActionList) Len() int {
	return len(l.actions)
}

func (l *ActionList) Remove(action *Action) error {
	index := l.Index(action)
	if index == -1 {
		return nil
	}

	return l.RemoveAt(index)
}

func (l *ActionList) RemoveAt(index int) error {
	action := l.actions[index]
	if action.Visible() {
		if err := l.observer.onRemovingAction(action); err != nil {
			return err
		}
	}

	action.release()

	l.actions = append(l.actions[:index], l.actions[index+1:]...)

	if action.Visible() {
		return l.updateSeparatorVisibility()
	}

	return nil
}

func (l *ActionList) updateSeparatorVisibility() error {
	var hasCurVisAct bool
	var curVisSep *Action

	for _, a := range l.actions {
		if visible := a.Visible(); a.IsSeparator() {
			toggle := visible != hasCurVisAct

			if toggle {
				visible = !visible
				if err := a.SetVisible(visible); err != nil {
					return err
				}
			}

			if visible {
				curVisSep = a
			}

			hasCurVisAct = false
		} else if visible {
			hasCurVisAct = true
		}
	}

	if !hasCurVisAct && curVisSep != nil {
		return curVisSep.SetVisible(false)
	}

	return nil
}

// forEach iterates through each action in l, calling f for each one. If f
// returns false, the iteration is aborted.
func (l *ActionList) forEach(f func(*Action) bool) {
	for _, a := range l.actions {
		if !f(a) {
			break
		}
	}
}

// forEach iterates through each action in l, calling f for each visible action.
// If f returns false, the iteration is aborted.
func (l *ActionList) forEachVisible(f func(*Action) bool) {
	for _, a := range l.actions {
		if !a.Visible() {
			continue
		}
		if !f(a) {
			break
		}
	}
}

// HasVisible returns true if l contains any visible Actions. Note that this is
// a linear-time operation in the worst-case.
func (l *ActionList) HasVisible() (result bool) {
	l.forEachVisible(func(*Action) bool {
		result = true
		return false
	})
	return result
}
