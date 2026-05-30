package compose

import (
	"errors"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// StreamFrame is one streaming event across ReAct rounds (model token or phase marker).
type StreamFrame struct {
	Stage     string            `json:"stage,omitempty"`
	Node      string            `json:"node,omitempty"`
	Round     int               `json:"round"`
	Phase     LoopPhase         `json:"phase"`
	Chunk     *schema.Message   `json:"-"`
	Done      bool              `json:"done,omitempty"`
	ToolCalls []schema.ToolCall `json:"-"`
}

// StreamFrameReader wraps a frame stream.
type StreamFrameReader struct {
	inner *schema.StreamReader[*StreamFrame]
}

// Recv reads the next frame.
func (r *StreamFrameReader) Recv() (*StreamFrame, error) {
	if r == nil || r.inner == nil {
		return nil, errors.New("compose: closed stream frame reader")
	}
	return r.inner.Recv()
}

// Close closes the frame stream.
func (r *StreamFrameReader) Close() {
	if r != nil && r.inner != nil {
		r.inner.Close()
	}
}
