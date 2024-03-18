// Copyright 2011 The Walk Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package walk

type eventHandlerInfo struct {
	handler EventHandler
	once    bool
}

type EventHandler func()

type Event struct {
	handlers []eventHandlerInfo
}

func (e *Event) Attach(handler EventHandler) int {
	handlerInfo := eventHandlerInfo{handler, false}

	for i, h := range e.handlers {
		if h.handler == nil {
			e.handlers[i] = handlerInfo
			return i
		}
	}

	e.handlers = append(e.handlers, handlerInfo)

	return len(e.handlers) - 1
}

func (e *Event) Detach(handle int) {
	e.handlers[handle].handler = nil
}

func (e *Event) Once(handler EventHandler) {
	i := e.Attach(handler)
	e.handlers[i].once = true
}

type EventPublisher struct {
	event Event
}

func (p *EventPublisher) Event() *Event {
	return &p.event
}

func (p *EventPublisher) Publish() {
	for i, h := range p.event.handlers {
		if h.handler != nil {
			h.handler()

			if h.once {
				p.event.Detach(i)
			}
		}
	}
}

type genericEventHandlerInfo[T any] struct {
	handler GenericEventHandler[T]
	once    bool
}

type GenericEventHandler[T any] func(param T)

type GenericEvent[T any] struct {
	handlers []genericEventHandlerInfo[T]
}

func (e *GenericEvent[T]) Attach(handler GenericEventHandler[T]) int {
	handlerInfo := genericEventHandlerInfo[T]{handler, false}

	for i, h := range e.handlers {
		if h.handler == nil {
			e.handlers[i] = handlerInfo
			return i
		}
	}

	e.handlers = append(e.handlers, handlerInfo)

	return len(e.handlers) - 1
}

func (e *GenericEvent[T]) Detach(handle int) {
	e.handlers[handle].handler = nil
}

func (e *GenericEvent[T]) Once(handler GenericEventHandler[T]) {
	i := e.Attach(handler)
	e.handlers[i].once = true
}

type GenericEventPublisher[T any] struct {
	event GenericEvent[T]
}

func (p *GenericEventPublisher[T]) Event() *GenericEvent[T] {
	return &p.event
}

func (p *GenericEventPublisher[T]) Publish(param T) {
	for i, h := range p.event.handlers {
		if h.handler != nil {
			h.handler(param)

			if h.once {
				p.event.Detach(i)
			}
		}
	}
}
