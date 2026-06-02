package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
)

// LambdaComponent is a quick helper to wrap a custom process function into an avflow.Component.
type LambdaComponent struct {
	id      string
	typ     string
	inputs  []string
	outputs []string
	handler func(ctx context.Context, inputs map[string]<-chan *Packet, outputs map[string]chan<- *Packet) error
}

// NewLambdaComponent creates an instance of a LambdaComponent.
func NewLambdaComponent(
	id string,
	typ string,
	inputs []string,
	outputs []string,
	handler func(ctx context.Context, inputs map[string]<-chan *Packet, outputs map[string]chan<- *Packet) error,
) *LambdaComponent {
	return &LambdaComponent{
		id:      id,
		typ:     typ,
		inputs:  inputs,
		outputs: outputs,
		handler: handler,
	}
}

func (l *LambdaComponent) ID() string      { return l.id }
func (l *LambdaComponent) Type() string    { return l.typ }
func (l *LambdaComponent) Inputs() []string  { return l.inputs }
func (l *LambdaComponent) Outputs() []string { return l.outputs }

func (l *LambdaComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	if l.handler == nil {
		return nil
	}
	return l.handler(ctx, inputs, outputs)
}

// BaseComponent is a helper struct that implements the descriptive methods of Component.
// Embedded inside custom structs, it reduces boilerplate.
type BaseComponent struct {
	id      string
	typ     string
	inputs  []string
	outputs []string
}

func NewBaseComponent(id, typ string, inputs, outputs []string) BaseComponent {
	return BaseComponent{
		id:      id,
		typ:     typ,
		inputs:  inputs,
		outputs: outputs,
	}
}

func (b *BaseComponent) ID() string      { return b.id }
func (b *BaseComponent) Type() string    { return b.typ }
func (b *BaseComponent) Inputs() []string  { return b.inputs }
func (b *BaseComponent) Outputs() []string { return b.outputs }
