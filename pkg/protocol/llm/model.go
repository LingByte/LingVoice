package llm

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// ChatModel is the core LLM contract: synchronous and streaming generation
// over a message history.
type ChatModel interface {
	// Name identifies provider and model, e.g. "openai/gpt-4o-mini".
	Name() string

	// Generate returns a complete assistant message.
	Generate(ctx context.Context, input []*schema.Message, opts ...Option) (*schema.Message, error)

	// Stream yields incremental message chunks. The caller must Close the reader.
	Stream(ctx context.Context, input []*schema.Message, opts ...Option) (*schema.StreamReader[*schema.Message], error)
}

// ToolCallingChatModel extends ChatModel with immutable tool binding, similar
// to CloudWeGo Eino's ToolCallingChatModel.WithTools pattern.
type ToolCallingChatModel interface {
	ChatModel

	// WithTools returns a new instance with tools attached; the receiver is unchanged.
	WithTools(tools []*schema.ToolInfo) (ToolCallingChatModel, error)
}

// GenerateFunc adapts a function to ChatModel (testing and lightweight wrappers).
type GenerateFunc func(ctx context.Context, input []*schema.Message, opts Options) (*schema.Message, error)

// StreamFunc adapts streaming to ChatModel.
type StreamFunc func(ctx context.Context, input []*schema.Message, opts Options) (*schema.StreamReader[*schema.Message], error)

// FuncModel implements ChatModel from generate/stream funcs.
type FuncModel struct {
	modelName string
	generate  GenerateFunc
	stream    StreamFunc
}

// NewFuncModel builds a ChatModel from callbacks. At least generate must be set.
func NewFuncModel(name string, gen GenerateFunc, stream StreamFunc) *FuncModel {
	return &FuncModel{modelName: name, generate: gen, stream: stream}
}

func (m *FuncModel) Name() string {
	if m == nil {
		return ""
	}
	return m.modelName
}

func (m *FuncModel) Generate(ctx context.Context, input []*schema.Message, opts ...Option) (*schema.Message, error) {
	if m == nil || m.generate == nil {
		return nil, ErrNotImplemented
	}
	return m.generate(ctx, input, ApplyOptions(opts...))
}

func (m *FuncModel) Stream(ctx context.Context, input []*schema.Message, opts ...Option) (*schema.StreamReader[*schema.Message], error) {
	if m == nil || m.stream == nil {
		return nil, ErrNotImplemented
	}
	return m.stream(ctx, input, ApplyOptions(opts...))
}
