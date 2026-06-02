package toolcb

import (
	"github.com/LingByte/LingVoice/pkg/llm/callback"
)

// CallbackInput is tool-specific callback input.
type CallbackInput struct {
	Name      string
	CallID    string
	Arguments string
}

// CallbackOutput is tool-specific callback output.
type CallbackOutput struct {
	Response string
	Error    string
}

// ConvCallbackInput casts generic callback input.
func ConvCallbackInput(src callback.CallbackInput) *CallbackInput {
	if v, ok := src.(*CallbackInput); ok {
		return v
	}
	return nil
}

// ConvCallbackOutput casts generic callback output.
func ConvCallbackOutput(src callback.CallbackOutput) *CallbackOutput {
	if v, ok := src.(*CallbackOutput); ok {
		return v
	}
	return nil
}
