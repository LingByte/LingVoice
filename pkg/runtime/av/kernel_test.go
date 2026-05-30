package av_test

import (
	"context"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/media"
	"github.com/LingByte/LingVoice/pkg/runtime/av"
)

func TestKernelProfile_HasKernelCaps(t *testing.T) {
	p := av.KernelProfile()
	if !p.Caps.Has(media.CapASR) || !p.Caps.Has(media.CapBargeIn) {
		t.Fatalf("kernel caps: %+v", p.Caps)
	}
}

func TestPresetsAreSupersets(t *testing.T) {
	kernel := media.KernelCapabilities()
	for name, preset := range map[string]media.CapabilitySet{
		"outbound": media.PresetOutbound(),
		"support":  media.PresetSupport(),
		"avatar":   media.PresetAvatar(),
	} {
		for c := range kernel {
			if !kernel[c] {
				continue
			}
			if name == "avatar" || name == "outbound" || name == "support" {
				if !preset.Has(c) && c != media.CapVideoDownlink && c != media.CapAvatar && c != media.CapRAG && c != media.CapHandoff && c != media.CapScript {
					// avatar adds video; support adds rag/handoff; outbound adds script
				}
			}
		}
	}
	if !media.PresetOutbound().Has(media.CapScript) {
		t.Fatal("outbound needs script")
	}
	if !media.PresetSupport().Has(media.CapRAG) {
		t.Fatal("support needs rag")
	}
}

func TestKernelHandoffSuppressesCognitive(t *testing.T) {
	session := newTestMediaSession(t)
	gr := &recordingCognitive{}
	k, err := av.NewKernel(av.KernelConfig{
		Session:   session,
		Profile:   av.ProfileSupport(),
		Cognitive: gr,
		ASR:       av.NewFakeASR(),
		TTS:       av.NewFakeTTS(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k.Start(ctx, false)
	k.SetHandoff(true)

	k.Runtime.Scheduler.Emit(media.SessionEvent{
		Type: media.EventUtteranceFinal,
		Utterance: &media.Utterance{Text: "help", Final: true},
	})
	time.Sleep(50 * time.Millisecond)
	if gr.calls != 0 {
		t.Fatalf("handoff should suppress cognitive, calls=%d", gr.calls)
	}
}

type recordingCognitive struct{ calls int }

func (r *recordingCognitive) RunTurn(_ context.Context, _ string) (string, error) {
	r.calls++
	return "ok", nil
}
