package compose

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type streamResume struct {
	msgs       []*schema.Message
	round      int
	atTools    bool
	lastOutput *schema.Message
}

func (r *CompiledGraph) loadStreamResume(ctx context.Context, checkpointID string) *streamResume {
	if r == nil || checkpointID == "" || r.checkpointStore == nil {
		return nil
	}
	cp, ok, err := r.loadCheckpoint(ctx, checkpointID)
	if err != nil || !ok || cp == nil || cp.State == nil {
		return nil
	}
	if cp.NextNode == END && cp.Interrupt == nil {
		return nil
	}
	sr := &streamResume{
		msgs:       append([]*schema.Message(nil), cp.State.Messages...),
		round:      StreamRoundFromCheckpoint(cp.State),
		lastOutput: cp.State.LastOutput,
	}
	switch cp.NextNode {
	case NodeTools:
		sr.atTools = true
	case NodeChatModel:
		sr.atTools = false
	default:
		sr.atTools = false
	}
	return sr
}
