package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/media"
	pmedi "github.com/LingByte/LingVoice/pkg/protocol/media"
	"github.com/LingByte/LingVoice/pkg/runtime/av"
)

func main() {
	utterance := flag.String("say", "你好 LingVoice", "simulated user utterance (UTF-8 bytes as PCM)")
	preset := flag.String("preset", "kernel", "profile preset: kernel|outbound|support|meeting|avatar")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rx := av.NewChanTransport("mic", media.DirectionInput, 8)
	tx := av.NewChanTransport("speaker", media.DirectionOutput, 8)

	session := media.NewDefaultSession().
		Context(ctx).
		Input(rx).
		Output(tx)

	profile, caps := resolvePreset(*preset)
	g, err := (&compose.GraphBlueprint{
		Name:       "voice-" + profile.Name,
		Caps:       caps,
		EchoPrefix: "助手：",
	}).Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build graph: %v\n", err)
		os.Exit(1)
	}
	gr, err := av.NewGraphRunner(g)
	if err != nil {
		fmt.Fprintf(os.Stderr, "compile graph: %v\n", err)
		os.Exit(1)
	}

	k, err := av.NewKernel(av.KernelConfig{
		Session:   session,
		Profile:   profile,
		Cognitive: gr,
		ASR:       av.NewFakeASR(),
		TTS:       av.NewFakeTTS(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "kernel: %v\n", err)
		os.Exit(1)
	}
	k.Start(ctx, true)

	for _, chunk := range chunkString(*utterance, 8) {
		rx.Inject(&media.AudioPacket{Payload: []byte(chunk)})
		time.Sleep(30 * time.Millisecond)
	}
	rx.Inject(&media.TextPacket{
		Text:          *utterance,
		IsTranscribed: true,
		IsEnd:         true,
		Sequence:      1,
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case pkt := <-tx.Out():
			if ap, ok := pkt.(*media.AudioPacket); ok && ap.IsSynthesized {
				fmt.Printf("[%s] downlink TTS: %q\n", profile.Name, string(ap.Payload))
				_ = session.Close()
				return
			}
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	fmt.Println("timeout waiting for TTS output")
	_ = session.Close()
}

func resolvePreset(name string) (av.RuntimeProfile, pmedi.CapabilitySet) {
	switch name {
	case "outbound":
		p := av.ProfileOutbound()
		return p, p.Caps
	case "support":
		p := av.ProfileSupport()
		return p, p.Caps
	case "meeting":
		p := av.ProfileMeeting()
		return p, p.Caps
	case "avatar":
		p := av.ProfileAvatar()
		return p, p.Caps
	default:
		p := av.KernelProfile()
		return p, p.Caps
	}
}

func chunkString(s string, n int) []string {
	if n <= 0 {
		return []string{s}
	}
	runes := []rune(s)
	var out []string
	for i := 0; i < len(runes); i += n {
		end := i + n
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}
