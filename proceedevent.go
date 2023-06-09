// Copyright 2023 Tailscale Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package walk

type proceedEventHandlerInfo struct {
	handler ProceedEventHandler
	once    bool
}

// ProceedEventHandler is a func that should return true to proceed past the
// event, or false to abort.
type ProceedEventHandler func() bool

// ProceedEvent is an event where, if any of its handlers return false, its
// publisher should not proceed further past the event. Note that once a given
// handler returns false, the event is immediately aborted; no additional
// handlers are run.
type ProceedEvent struct {
	handlers []proceedEventHandlerInfo
}

// Attach adds handler to e and will be invoked when the event associated with
// e is triggered. It returns an integral handle to the event that may be used
// with Detach.
func (e *ProceedEvent) Attach(handler ProceedEventHandler) int {
	handlerInfo := proceedEventHandlerInfo{handler, false}

	for i, h := range e.handlers {
		if h.handler == nil {
			e.handlers[i] = handlerInfo
			return i
		}
	}

	e.handlers = append(e.handlers, handlerInfo)

	return len(e.handlers) - 1
}

// Detach removes the handler specified by handle, which was obtained as the
// result of a call to Attach.
func (e *ProceedEvent) Detach(handle int) {
	e.handlers[handle].handler = nil
}

// Once is similar to Attach, except that handler is attached as a "one-shot"
// occurrence; handler will automatically be detached after its first invocation.
func (e *ProceedEvent) Once(handler ProceedEventHandler) {
	i := e.Attach(handler)
	e.handlers[i].once = true
}

// ProceedEventPublisher is the event publisher used by any code that supports
// ProceedEvent.
type ProceedEventPublisher struct {
	event ProceedEvent
}

// Event obtains a pointer to the ProceedEvent associated with p.
func (p *ProceedEventPublisher) Event() *ProceedEvent {
	return &p.event
}

// Publish dispatches the event to all registered handlers. The first handler
// to return false will abort event dispatch and Publish will return false.
// Otherwise, Publish returns true.
func (p *ProceedEventPublisher) Publish() bool {
	for i, h := range p.event.handlers {
		if h.handler != nil {
			proceed := h.handler()

			if h.once {
				p.event.Detach(i)
			}

			if !proceed {
				return false
			}
		}
	}

	return true
}

type proceedWithArgEventHandlerInfo[T any] struct {
	handler ProceedWithArgEventHandler[T]
	once    bool
}

// ProceedWithArgEventHandler is a func that accepts param of type T and should
// return true to proceed past the event, or false to abort.
type ProceedWithArgEventHandler[T any] func(param T) bool

// ProceedWithArgEvent is an event where, if any of its handlers return false, its
// publisher should not proceed further past the event. Note that once a given
// handler returns false, the event is immediately aborted; no additional
// handlers are run.
type ProceedWithArgEvent[T any] struct {
	handlers []proceedWithArgEventHandlerInfo[T]
}

// Attach adds handler to e and will be invoked when the event associated with
// e is triggered. It returns an integral handle to the event that may be used
// with Detach.
func (e *ProceedWithArgEvent[T]) Attach(handler ProceedWithArgEventHandler[T]) int {
	handlerInfo := proceedWithArgEventHandlerInfo[T]{handler, false}

	for i, h := range e.handlers {
		if h.handler == nil {
			e.handlers[i] = handlerInfo
			return i
		}
	}

	e.handlers = append(e.handlers, handlerInfo)

	return len(e.handlers) - 1
}

// Detach removes the handler specified by handle, which was obtained as the
// result of a call to Attach.
func (e *ProceedWithArgEvent[T]) Detach(handle int) {
	e.handlers[handle].handler = nil
}

// Once is similar to Attach, except that handler is attached as a "one-shot"
// occurrence; handler will automatically be detached after its first invocation.
func (e *ProceedWithArgEvent[T]) Once(handler ProceedWithArgEventHandler[T]) {
	i := e.Attach(handler)
	e.handlers[i].once = true
}

// ProceedWithArgEventPublisher is the event publisher used by any code that supports
// ProceedWithArgEvent.
type ProceedWithArgEventPublisher[T any] struct {
	event ProceedWithArgEvent[T]
}

// Event obtains a pointer to the ProceedWithArgEvent associated with p.
func (p *ProceedWithArgEventPublisher[T]) Event() *ProceedWithArgEvent[T] {
	return &p.event
}

// Publish dispatches the event with param of type T to all registered handlers.
// The first handler to return false will abort event dispatch and Publish will
// return false. Otherwise, Publish returns true.
func (p *ProceedWithArgEventPublisher[T]) Publish(param T) bool {
	for i, h := range p.event.handlers {
		if h.handler != nil {
			proceed := h.handler(param)

			if h.once {
				p.event.Detach(i)
			}

			if !proceed {
				return false
			}
		}
	}

	return true
}
