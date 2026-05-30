package modelcb

import (
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// CallbackInput is ChatModel-specific callback input.
type CallbackInput struct {
	Messages   []*schema.Message
	Tools      []*schema.ToolInfo
	ToolChoice schema.ToolChoice
	Options    llm.Options
	Model      string
	Stream     bool
	Extra      map[string]any
}

// CallbackOutput is ChatModel-specific callback output.
type CallbackOutput struct {
	Message    *schema.Message
	TokenUsage *schema.TokenUsage
	Stream     bool
	Timing     *CallbackTiming
	Extra      map[string]any
}

// CallbackTiming carries latency metrics on callback output (no metrics import to avoid cycles).
type CallbackTiming struct {
	UpstreamLatency time.Duration
	TTFT            time.Duration
	TokensPerSecond float64
}

// ConvCallbackInput casts generic callback input to *CallbackInput.
func ConvCallbackInput(src callback.CallbackInput) *CallbackInput {
	switch t := src.(type) {
	case *CallbackInput:
		return t
	case []*schema.Message:
		return &CallbackInput{Messages: t}
	default:
		return nil
	}
}

// ConvCallbackOutput casts generic callback output to *CallbackOutput.
func ConvCallbackOutput(src callback.CallbackOutput) *CallbackOutput {
	switch t := src.(type) {
	case *CallbackOutput:
		return t
	case *schema.Message:
		out := &CallbackOutput{Message: t}
		if t.ResponseMeta != nil {
			out.TokenUsage = t.ResponseMeta.Usage
		}
		return out
	default:
		return nil
	}
}
