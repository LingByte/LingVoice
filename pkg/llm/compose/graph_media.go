package compose

import (
	"context"
	"fmt"

	pmedi "github.com/LingByte/LingVoice/pkg/protocol/media"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// AddUtteranceInputNode stores the latest user utterance in GraphState.Vars[textKey].
func (g *Graph) AddUtteranceInputNode(name, textKey string) error {
	if textKey == "" {
		textKey = pmedi.ChannelText
	}
	return g.AddLambdaNode(name, func(ctx context.Context, st *GraphState) error {
		if st.Vars == nil {
			st.Vars = map[string]any{}
		}
		text, _ := st.Vars[textKey].(string)
		if text == "" {
			text = lastUserPlainText(st.Messages)
		}
		if text == "" {
			return fmt.Errorf("compose: utterance node %q: empty text", name)
		}
		st.Vars[textKey] = text
		if len(st.Messages) == 0 {
			st.Messages = []*schema.Message{schema.UserMessage(text)}
		}
		return nil
	})
}

// AddEchoReplyNode appends a demo assistant message (M1 local voice).
func (g *Graph) AddEchoReplyNode(name, prefix string) error {
	if prefix == "" {
		prefix = "收到："
	}
	return g.AddLambdaNode(name, func(ctx context.Context, st *GraphState) error {
		text, _ := st.Vars[pmedi.ChannelText].(string)
		if text == "" {
			text = lastUserPlainText(st.Messages)
		}
		reply := prefix + text
		st.Messages = append(st.Messages, schema.AssistantMessage(reply, nil))
		if st.Vars == nil {
			st.Vars = map[string]any{}
		}
		st.Vars["assistant_text"] = reply
		return nil
	})
}

// AddMediaEventTapNode records SessionEvent payloads into GraphState.Vars[eventKey].
func (g *Graph) AddMediaEventTapNode(name, eventKey string) error {
	if eventKey == "" {
		eventKey = pmedi.ChannelEvent
	}
	return g.AddLambdaNode(name, func(ctx context.Context, st *GraphState) error {
		if st.Vars == nil {
			st.Vars = map[string]any{}
		}
		if _, ok := st.Vars[eventKey]; !ok {
			st.Vars[eventKey] = []pmedi.SessionEvent{}
		}
		return nil
	})
}

// AddScriptVarsNode merges script step variables from GraphState into the turn context.
func (g *Graph) AddScriptVarsNode(name string) error {
	return g.AddLambdaNode(name, func(ctx context.Context, st *GraphState) error {
		if st.Vars == nil {
			st.Vars = map[string]any{}
		}
		step, _ := st.Vars["script_step"].(string)
		if step != "" {
			st.Vars["system_prompt"] = step
		}
		return nil
	})
}

// AddHandoffGateNode skips assistant generation when handoff control is active.
func (g *Graph) AddHandoffGateNode(name string) error {
	return g.AddLambdaNode(name, func(ctx context.Context, st *GraphState) error {
		if st.Vars == nil {
			return nil
		}
		if ctrl, _ := st.Vars[pmedi.ChannelControl].(string); ctrl == string(pmedi.ControlHandoffStart) {
			st.Vars["assistant_text"] = ""
		}
		return nil
	})
}
