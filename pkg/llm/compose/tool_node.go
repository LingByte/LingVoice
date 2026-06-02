package compose

import (
	"context"
	"fmt"
	"sync"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	toolcb "github.com/LingByte/LingVoice/pkg/llm/callback/tool"
	"github.com/LingByte/LingVoice/pkg/llm/internal/core"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// ToolNodeConfig configures a ToolsNode (Eino compose.ToolsNodeConfig subset).
type ToolNodeConfig struct {
	Tools []tool.InvokableTool
	// UnknownToolHandler handles hallucinated tool names. When nil, unknown tools error.
	UnknownToolHandler func(ctx context.Context, name, input string) (string, error)
	// ToolArgumentsHandler preprocesses arguments before execution.
	ToolArgumentsHandler func(ctx context.Context, name, input string) (string, error)
	// ErrorHandler converts tool errors to model-visible strings. When set, tools are wrapped.
	ErrorHandler tool.ErrorHandler
	// Middleware applies a middleware chain to each tool before registration.
	Middleware []tool.Middleware
	// NameAliases maps alias → canonical tool name.
	NameAliases map[string]string
	// Handlers observe each tool invocation.
	Handlers []callback.Handler
	// ExecuteSequentially runs tool calls in order instead of parallel.
	ExecuteSequentially bool
}

// ToolNode executes tool calls from an assistant message.
type ToolNode struct {
	byName               map[string]tool.InvokableTool
	unknownToolHandler   func(ctx context.Context, name, input string) (string, error)
	toolArgumentsHandler func(ctx context.Context, name, input string) (string, error)
	handlers             []callback.Handler
	executeSequentially  bool
}

// NewToolNode builds a ToolNode from config.
func NewToolNode(_ context.Context, cfg *ToolNodeConfig) (*ToolNode, error) {
	if cfg == nil {
		return nil, fmt.Errorf("compose: nil ToolNodeConfig")
	}
	n := &ToolNode{
		byName:               make(map[string]tool.InvokableTool, len(cfg.Tools)),
		unknownToolHandler:   cfg.UnknownToolHandler,
		toolArgumentsHandler: cfg.ToolArgumentsHandler,
		handlers:             cfg.Handlers,
		executeSequentially:  cfg.ExecuteSequentially,
	}
	for _, t := range cfg.Tools {
		if t == nil {
			return nil, fmt.Errorf("compose: nil tool")
		}
		if cfg.ErrorHandler != nil {
			t = tool.WrapInvokableToolWithErrorHandler(t, cfg.ErrorHandler)
		}
		if len(cfg.Middleware) > 0 {
			t = tool.Chain(t, cfg.Middleware...)
		}
		info, err := t.Info(context.Background())
		if err != nil {
			return nil, err
		}
		if info == nil || info.Name == "" {
			return nil, fmt.Errorf("compose: tool with empty name")
		}
		if _, dup := n.byName[info.Name]; dup {
			return nil, fmt.Errorf("compose: duplicate tool %q", info.Name)
		}
		n.byName[info.Name] = t
		for alias, canonical := range cfg.NameAliases {
			if canonical == info.Name {
				n.byName[alias] = t
			}
		}
	}
	return n, nil
}

// Invoke runs all tool calls on msg and returns tool role messages in call order.
func (n *ToolNode) Invoke(ctx context.Context, msg *schema.Message) ([]*schema.Message, error) {
	if n == nil {
		return nil, fmt.Errorf("compose: nil ToolNode")
	}
	if msg == nil || len(msg.ToolCalls) == 0 {
		return nil, nil
	}
	if n.executeSequentially {
		out := make([]*schema.Message, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			m, err := n.runOne(ctx, tc)
			if err != nil {
				return out, err
			}
			out = append(out, m)
		}
		return out, nil
	}
	out := make([]*schema.Message, len(msg.ToolCalls))
	errs := make([]error, len(msg.ToolCalls))
	var wg sync.WaitGroup
	for i, tc := range msg.ToolCalls {
		wg.Add(1)
		go func(i int, tc schema.ToolCall) {
			defer wg.Done()
			m, err := n.runOne(ctx, tc)
			out[i] = m
			errs[i] = err
		}(i, tc)
	}
	wg.Wait()
	if err := CollectInterrupts(errs...); err != nil {
		return out, err
	}
	return out, nil
}

func (n *ToolNode) runOne(ctx context.Context, tc schema.ToolCall) (*schema.Message, error) {
	name := tc.Function.Name
	args := tc.Function.Arguments
	if n.toolArgumentsHandler != nil {
		processed, err := n.toolArgumentsHandler(ctx, name, args)
		if err != nil {
			return nil, fmt.Errorf("compose: tool args %q: %w", name, err)
		}
		args = processed
	}

	runInfo := &callback.RunInfo{Name: name, Component: callback.ComponentTool}
	ctx = callback.InitRun(ctx, runInfo, n.handlers...)
	ctx = core.WithToolCallID(ctx, tc.ID)
	if id := ActiveInterruptID(ctx); id != "" {
		if was, info, state := GetInterruptState[any](ctx); was {
			ctx = tool.WithToolResume(ctx, id, info, state)
		}
	}
	in := &toolcb.CallbackInput{Name: name, CallID: tc.ID, Arguments: args}
	ctx = callback.OnStart(ctx, in)

	t, ok := n.byName[name]
	if !ok {
		if n.unknownToolHandler != nil {
			result, err := n.unknownToolHandler(ctx, name, args)
			if err != nil {
				callback.OnError(ctx, err)
				return nil, err
			}
			callback.OnEnd(ctx, &toolcb.CallbackOutput{Response: result})
			return schema.ToolMessage(result, tc.ID, schema.WithToolName(name)), nil
		}
		err := fmt.Errorf("compose: unknown tool %q", name)
		callback.OnError(ctx, err)
		return nil, err
	}
	result, err := t.InvokableRun(ctx, args)
	if err != nil {
		if core.IsInterrupt(err) {
			callback.OnError(ctx, err)
			return nil, InterruptAtNode(name, err)
		}
		callback.OnError(ctx, err)
		return nil, fmt.Errorf("compose: tool %q: %w", name, err)
	}
	callback.OnEnd(ctx, &toolcb.CallbackOutput{Response: result})
	return schema.ToolMessage(result, tc.ID, schema.WithToolName(name)), nil
}
