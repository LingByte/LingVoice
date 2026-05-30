package compose

import (
	"errors"
	"io"
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// MergeMessageStreams multiplexes message chunks from multiple readers until all EOF (Eino fan-in stream subset).
func MergeMessageStreams(readers ...*schema.StreamReader[*schema.Message]) *schema.StreamReader[*schema.Message] {
	outSR, outSW := schema.Pipe[*schema.Message](32)
	var wg sync.WaitGroup
	for _, r := range readers {
		if r == nil {
			continue
		}
		wg.Add(1)
		go func(sr *schema.StreamReader[*schema.Message]) {
			defer wg.Done()
			defer sr.Close()
			for {
				msg, err := sr.Recv()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						outSW.Send(nil, err)
					}
					return
				}
				if !outSW.Send(msg, nil) {
					return
				}
			}
		}(r)
	}
	go func() {
		wg.Wait()
		outSW.Close()
	}()
	return outSR
}

// MergeStreamFrames multiplexes frame readers until all EOF.
func MergeStreamFrames(readers ...*StreamFrameReader) *StreamFrameReader {
	outSR, outSW := schema.Pipe[*StreamFrame](32)
	var wg sync.WaitGroup
	for _, r := range readers {
		if r == nil {
			continue
		}
		wg.Add(1)
		go func(fr *StreamFrameReader) {
			defer wg.Done()
			defer fr.Close()
			for {
				f, err := fr.Recv()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						outSW.Send(nil, err)
					}
					return
				}
				if !outSW.Send(f, nil) {
					return
				}
			}
		}(r)
	}
	go func() {
		wg.Wait()
		outSW.Close()
	}()
	return &StreamFrameReader{inner: outSR}
}
