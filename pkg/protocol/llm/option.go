package llm

import (
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// ToolChoice re-exports schema tool choice modes for call options.
type ToolChoice = schema.ToolChoice

const (
	ToolChoiceForbidden = schema.ToolChoiceForbidden
	ToolChoiceAllowed   = schema.ToolChoiceAllowed
	ToolChoiceForced    = schema.ToolChoiceForced
)

// Options holds per-request model parameters.
type Options struct {
	Temperature *float64
	MaxTokens   *int
	TopP        *float64
	Stop        []string
	Tools       []*schema.ToolInfo
	ToolChoice  ToolChoice
	// Model overrides the default model name for this call only.
	Model string
}

// Option mutates Options.
type Option func(*Options)

// WithTemperature sets sampling temperature.
func WithTemperature(v float64) Option {
	return func(o *Options) { o.Temperature = &v }
}

// WithMaxTokens sets the completion token limit.
func WithMaxTokens(v int) Option {
	return func(o *Options) { o.MaxTokens = &v }
}

// WithTopP sets nucleus sampling.
func WithTopP(v float64) Option {
	return func(o *Options) { o.TopP = &v }
}

// WithStop sets stop sequences.
func WithStop(seq ...string) Option {
	return func(o *Options) { o.Stop = append([]string(nil), seq...) }
}

// WithTools binds tools for this request.
func WithTools(tools ...*schema.ToolInfo) Option {
	return func(o *Options) {
		o.Tools = append([]*schema.ToolInfo(nil), tools...)
	}
}

// WithToolChoice sets tool calling mode.
func WithToolChoice(c ToolChoice) Option {
	return func(o *Options) { o.ToolChoice = c }
}

// WithModel overrides the model id for one call.
func WithModel(name string) Option {
	return func(o *Options) { o.Model = name }
}

// ApplyOptions merges opts into a zero Options value.
func ApplyOptions(opts ...Option) Options {
	var o Options
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	return o
}
