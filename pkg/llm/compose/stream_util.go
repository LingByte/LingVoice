package compose

import (
	"errors"
	"io"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// CollectMessageStream drains a message stream into one message (uses schema.ConcatMessages).
func CollectMessageStream(sr *schema.StreamReader[*schema.Message]) (*schema.Message, error) {
	if sr == nil {
		return nil, errors.New("compose: nil message stream")
	}
	defer sr.Close()
	var chunks []*schema.Message
	for {
		chunk, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) == 0 {
		return nil, nil
	}
	return schema.ConcatMessages(chunks)
}

// CollectStreamFrames drains a frame reader into a slice.
func CollectStreamFrames(r *StreamFrameReader) ([]*StreamFrame, error) {
	if r == nil {
		return nil, errors.New("compose: nil stream frame reader")
	}
	defer r.Close()
	var out []*StreamFrame
	for {
		f, err := r.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
		if f != nil {
			out = append(out, f)
		}
	}
}
