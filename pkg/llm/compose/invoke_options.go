package compose

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/protocol/llm"
)

type graphChatModelOptsKey struct{}

// WithGraphChatModelOption attaches per-invoke ChatModel options (Eino WithChatModelOption subset).
func WithGraphChatModelOption(opts ...llm.Option) GraphInvokeOption {
	return func(c *graphInvokeConfig) {
		c.chatModelOpts = append(c.chatModelOpts, opts...)
	}
}

func chatModelOptsFromContext(ctx context.Context) []llm.Option {
	if ctx == nil {
		return nil
	}
	opts, _ := ctx.Value(graphChatModelOptsKey{}).([]llm.Option)
	if len(opts) == 0 {
		return nil
	}
	out := make([]llm.Option, len(opts))
	copy(out, opts)
	return out
}

func withGraphChatModelOpts(ctx context.Context, opts []llm.Option) context.Context {
	if len(opts) == 0 {
		return ctx
	}
	return context.WithValue(ctx, graphChatModelOptsKey{}, opts)
}

func mergeModelOpts(base, extra []llm.Option) []llm.Option {
	if len(extra) == 0 {
		return base
	}
	out := make([]llm.Option, 0, len(base)+len(extra))
	out = append(out, base...)
	out = append(out, extra...)
	return out
}
