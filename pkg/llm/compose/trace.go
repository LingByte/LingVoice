package compose

import (
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// LoopPhase identifies a step inside ToolLoop / ReActGraph.
type LoopPhase string

const (
	PhaseModel LoopPhase = "model"
	PhaseTools LoopPhase = "tools"
)

// LoopStep is one ReAct iteration event for logging and demos.
type LoopStep struct {
	Round     int               `json:"round"`
	Phase     LoopPhase         `json:"phase"`
	Message   *schema.Message   `json:"-"`
	ToolMsgs  []*schema.Message `json:"-"`
	StartedAt time.Time         `json:"started_at"`
	Duration  string            `json:"duration"`
}

// LoopTrace captures the full ReAct run for observability.
type LoopTrace struct {
	Steps       []LoopStep `json:"steps"`
	TotalRounds int        `json:"total_rounds"`
	UsedTools   bool       `json:"used_tools"`
}

// AppendModel records a model phase step.
func (t *LoopTrace) AppendModel(round int, msg *schema.Message, start time.Time) {
	if t == nil {
		return
	}
	if msg != nil && len(msg.ToolCalls) > 0 {
		t.UsedTools = true
	}
	t.Steps = append(t.Steps, LoopStep{
		Round: round, Phase: PhaseModel, Message: msg,
		StartedAt: start, Duration: time.Since(start).String(),
	})
}

// AppendTools records a tools phase step.
func (t *LoopTrace) AppendTools(round int, msgs []*schema.Message, start time.Time) {
	if t == nil {
		return
	}
	if len(msgs) > 0 {
		t.UsedTools = true
	}
	t.Steps = append(t.Steps, LoopStep{
		Round: round, Phase: PhaseTools, ToolMsgs: msgs,
		StartedAt: start, Duration: time.Since(start).String(),
	})
}

func (t *LoopTrace) appendGraphStep(round int, phase LoopPhase, msg *schema.Message, toolMsgs []*schema.Message, step GraphStep) {
	if t == nil {
		return
	}
	if phase == PhaseTools && len(toolMsgs) > 0 {
		t.UsedTools = true
	}
	if phase == PhaseModel && msg != nil && len(msg.ToolCalls) > 0 {
		t.UsedTools = true
	}
	t.Steps = append(t.Steps, LoopStep{
		Round: round, Phase: phase, Message: msg, ToolMsgs: toolMsgs,
		StartedAt: step.StartedAt, Duration: step.Duration,
	})
}

func (t *LoopTrace) finish() {
	if t == nil {
		return
	}
	maxRound := 0
	for _, s := range t.Steps {
		if s.Round > maxRound {
			maxRound = s.Round
		}
	}
	t.TotalRounds = maxRound + 1
}

// LoopTraceFromGraph converts compiled graph steps + final state to ReAct trace.
func LoopTraceFromGraph(steps []GraphStep, st *GraphState, inputLen int) *LoopTrace {
	trace := &LoopTrace{}
	if len(steps) == 0 {
		return trace
	}
	msgs := []*schema.Message(nil)
	if st != nil {
		msgs = st.Messages
	}
	idx := inputLen
	if idx > len(msgs) {
		idx = len(msgs)
	}
	round := 0
	for _, step := range steps {
		switch step.Node {
		case NodeChatModel:
			var m *schema.Message
			for idx < len(msgs) {
				cand := msgs[idx]
				idx++
				if cand != nil && cand.Role == schema.Assistant {
					m = cand
					break
				}
			}
			trace.appendGraphStep(round, PhaseModel, m, nil, step)
		case NodeTools:
			var toolMsgs []*schema.Message
			for idx < len(msgs) {
				cand := msgs[idx]
				if cand == nil || cand.Role != schema.Tool {
					break
				}
				toolMsgs = append(toolMsgs, cand)
				idx++
			}
			trace.appendGraphStep(round, PhaseTools, nil, toolMsgs, step)
			round++
		}
	}
	trace.finish()
	return trace
}

// MessageDelta returns messages appended after inputLen.
func MessageDelta(all []*schema.Message, inputLen int) []*schema.Message {
	if inputLen >= len(all) {
		return nil
	}
	return append([]*schema.Message(nil), all[inputLen:]...)
}
