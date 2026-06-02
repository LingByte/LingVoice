package av

import (
	"context"
	"strings"
	"sync"
)

// FakeASR accumulates text fed as audio (bytes interpreted as UTF-8 for demo).
type FakeASR struct {
	mu       sync.Mutex
	onPartial func(string)
	onFinal   func(string)
	buf      strings.Builder
}

func NewFakeASR() *FakeASR { return &FakeASR{} }

func (f *FakeASR) OnPartial(fn func(string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onPartial = fn
}

func (f *FakeASR) OnFinal(fn func(string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onFinal = fn
}

func (f *FakeASR) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buf.Reset()
}

func (f *FakeASR) FeedAudio(_ context.Context, pcm []byte) error {
	text := strings.TrimSpace(string(pcm))
	if text == "" {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buf.WriteString(text)
	if f.onPartial != nil {
		f.onPartial(f.buf.String())
	}
	return nil
}

// FlushFinal emits the accumulated transcript as a final utterance.
func (f *FakeASR) FlushFinal() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onFinal != nil && f.buf.Len() > 0 {
		f.onFinal(f.buf.String())
	}
	f.buf.Reset()
}

// FakeTTS "speaks" by echoing text as PCM bytes in fixed-size chunks.
type FakeTTS struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	chunkSz int
}

func NewFakeTTS() *FakeTTS {
	return &FakeTTS{chunkSz: 64}
}

func (f *FakeTTS) Cancel() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
}

func (f *FakeTTS) Speak(ctx context.Context, text string, onFrame func(pcm []byte, first, last bool) error) error {
	f.Cancel()
	ctx, cancel := context.WithCancel(ctx)
	f.mu.Lock()
	f.cancel = cancel
	f.mu.Unlock()

	data := []byte("[TTS:" + text + "]")
	if len(data) == 0 {
		return onFrame(nil, true, true)
	}
	for i := 0; i < len(data); i += f.chunkSz {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		end := i + f.chunkSz
		if end > len(data) {
			end = len(data)
		}
		first := i == 0
		last := end == len(data)
		if err := onFrame(data[i:end], first, last); err != nil {
			return err
		}
	}
	return nil
}
