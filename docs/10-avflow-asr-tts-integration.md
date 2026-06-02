---
description: AVFlow Framework ASR/TTS Integration Guide
---

# AVFlow Framework ASR/TTS Integration

## Overview

This document describes how to integrate the refactored `SpeechRecognitionEngine` (ASR) and `AudioSynthesisEngine` (TTS) interfaces into the AVFlow component-based orchestration framework.

## Architecture

### Component Graph Model

AVFlow treats ASR and TTS as pluggable graph components that:
- Consume typed streaming packets from input ports
- Process data concurrently
- Produce typed streaming packets to output ports
- Can be replaced with different implementations without affecting the graph

### Packet Flow

```
Microphone → [MicComponent] → audio_out
                                    ↓
                            [VADComponent] → audio_out (filtered)
                                    ↓
                            [ASRComponent] → text_out (transcriptions)
                                    ↓
                            [LLMComponent] → text_out (responses)
                                    ↓
                            [TTSComponent] → audio_out (synthesized)
                                    ↓
                            [SpeakerComponent] → Speaker
```

## ASR Integration (SpeechRecognitionEngine)

### Current Implementation

The `ASRComponent` in `pkg/avflow/av_components.go` provides a mock ASR that:
1. Reads `PacketTypeAudio` packets from `audio_in`
2. Converts UTF-8 payloads to text (demo mode)
3. Outputs `PacketTypeText` packets to `text_out`

### Integration with Real ASR Providers

To integrate a real `SpeechRecognitionEngine` (e.g., QCloud, Google, FunASR):

```go
// Example: Integrate QCloud ASR into AVFlow
type RealASRComponent struct {
    BaseComponent
    engine recognizer.SpeechRecognitionEngine
    mu     sync.Mutex
}

func NewRealASRComponent(id string, engine recognizer.SpeechRecognitionEngine) *RealASRComponent {
    return &RealASRComponent{
        BaseComponent: NewBaseComponent(id, "RealASR", 
            []string{"audio_in"}, 
            []string{"text_out"}),
        engine: engine,
    }
}

func (r *RealASRComponent) Process(
    ctx context.Context,
    inputs map[string]<-chan *Packet,
    outputs map[string]chan<- *Packet,
) error {
    in := inputs["audio_in"]
    out := outputs["text_out"]
    
    // Initialize the engine with callbacks
    r.engine.Init(
        func(text string, isLast bool, duration time.Duration, uuid string) {
            // Send transcription to downstream
            select {
            case out <- NewPacket(PacketTypeText, &media.TextPacket{
                Text:          text,
                IsTranscribed: true,
                IsPartial:     !isLast,
                IsEnd:         isLast,
            }):
            case <-ctx.Done():
            }
        },
        func(err error, isFatal bool) {
            // Handle recognition errors
            if isFatal {
                // Log and potentially restart
            }
        },
    )
    
    // Connect and start receiving
    if err := r.engine.ConnAndReceive(""); err != nil {
        return fmt.Errorf("asr connect error: %w", err)
    }
    
    for {
        select {
        case <-ctx.Done():
            r.engine.StopConn()
            return nil
        case p, ok := <-in:
            if !ok {
                r.engine.SendEnd()
                r.engine.StopConn()
                return nil
            }
            if p.Type == PacketTypeAudio {
                rawPkt, ok := p.Data.(*media.AudioPacket)
                if !ok {
                    continue
                }
                // Send audio to ASR engine
                if err := r.engine.SendAudioBytes(rawPkt.Payload); err != nil {
                    r.engine.RestartClient()
                }
            }
        }
    }
}
```

### Key Integration Points

1. **Initialization**: Call `engine.Init()` with result and error callbacks
2. **Connection**: Call `engine.ConnAndReceive()` to establish connection
3. **Audio Input**: Call `engine.SendAudioBytes()` for each audio packet
4. **Termination**: Call `engine.SendEnd()` and `engine.StopConn()` on completion
5. **Error Handling**: Implement `RecognitionError` callback for fault tolerance

## TTS Integration (AudioSynthesisEngine)

### Current Implementation

The `TTSComponent` in `pkg/avflow/av_components.go` provides a mock TTS that:
1. Reads `PacketTypeText` packets from `text_in`
2. Simulates speech synthesis with mock audio bytes
3. Outputs `PacketTypeAudio` packets to `audio_out`
4. Respects barge-in control signals from `control_in`

### Integration with Real TTS Providers

To integrate a real `AudioSynthesisEngine` (e.g., OpenAI, ElevenLabs, Azure):

```go
// Example: Integrate OpenAI TTS into AVFlow
type RealTTSComponent struct {
    BaseComponent
    engine synthesizer.AudioSynthesisEngine
    mu     sync.Mutex
}

func NewRealTTSComponent(id string, engine synthesizer.AudioSynthesisEngine) *RealTTSComponent {
    return &RealTTSComponent{
        BaseComponent: NewBaseComponent(id, "RealTTS",
            []string{"text_in", "control_in"},
            []string{"audio_out", "playback_state_out"}),
        engine: engine,
    }
}

func (t *RealTTSComponent) Process(
    ctx context.Context,
    inputs map[string]<-chan *Packet,
    outputs map[string]chan<- *Packet,
) error {
    inText := inputs["text_in"]
    inCtrl := inputs["control_in"]
    outAudio := outputs["audio_out"]
    outState := outputs["playback_state_out"]
    
    cancelChan := make(chan struct{})
    
    // Control signal handler (barge-in)
    go func() {
        for {
            select {
            case <-ctx.Done():
                return
            case p, ok := <-inCtrl:
                if !ok {
                    return
                }
                if p.Type == PacketTypeControl && p.Data == "barge-in" {
                    close(cancelChan)
                    cancelChan = make(chan struct{})
                    select {
                    case outState <- NewPacket(PacketTypeControl, false):
                    case <-ctx.Done():
                        return
                    }
                }
            }
        }
    }()
    
    for {
        select {
        case <-ctx.Done():
            return nil
        case p, ok := <-inText:
            if !ok {
                return nil
            }
            if p.Type == PacketTypeText {
                textPkt, ok := p.Data.(*media.TextPacket)
                if !ok {
                    continue
                }
                if textPkt.IsEnd {
                    continue
                }
                
                // Notify playback start
                select {
                case outState <- NewPacket(PacketTypeControl, true):
                case <-ctx.Done():
                    return nil
                }
                
                // Create synthesis prompt
                prompt := &synthesizer.SynthesisPrompt{
                    Text:  textPkt.Text,
                    Speed: 1.0,
                    Pitch: 1.0,
                }
                
                // Synthesize and stream audio
                audioChan, err := t.engine.Synthesize(ctx, prompt)
                if err != nil {
                    return fmt.Errorf("tts synthesis error: %w", err)
                }
                
                currentCancel := cancelChan
                interrupted := false
                
                for audioFrame := range audioChan {
                    select {
                    case <-ctx.Done():
                        return nil
                    case <-currentCancel:
                        interrupted = true
                        break
                    default:
                    }
                    
                    if interrupted {
                        break
                    }
                    
                    // Send audio packet
                    select {
                    case outAudio <- NewPacket(PacketTypeAudio, &media.AudioPacket{
                        Payload:       audioFrame.Payload,
                        IsSynthesized: true,
                    }):
                    case <-ctx.Done():
                        return nil
                    }
                }
                
                // Notify playback stop
                select {
                case outState <- NewPacket(PacketTypeControl, false):
                case <-ctx.Done():
                    return nil
                }
            }
        }
    }
}
```

### Key Integration Points

1. **Initialization**: Instantiate engine with appropriate configuration
2. **Prompt Creation**: Build `SynthesisPrompt` with text and parameters
3. **Synthesis**: Call `engine.Synthesize()` to get audio stream
4. **Audio Output**: Stream `AudioFrame` packets to downstream
5. **Barge-in Handling**: Respect control signals to interrupt synthesis
6. **Cleanup**: Properly close synthesis on context cancellation

## Factory Pattern Integration

### Recognizer Factory

```go
// Create ASR engine from factory
factory := recognizer.GetGlobalFactory()
config := &recognizer.QCloudASROption{
    // ... configuration
}
engine, err := factory.CreateTranscriber(config)
if err != nil {
    return err
}

// Wrap in AVFlow component
asrComp := NewRealASRComponent("qcloud-asr", engine.(recognizer.SpeechRecognitionEngine))
```

### Synthesizer Factory

```go
// Create TTS engine from factory
ttsFactory := synthesizer.NewFactory()
config := &synthesizer.OpenAITTSConfig{
    // ... configuration
}
engine, err := ttsFactory.Create(synthesizer.ProviderOpenAI, "openai-tts", config)
if err != nil {
    return err
}

// Wrap in AVFlow component
ttsComp := NewRealTTSComponent("openai-tts", engine)
```

## Graph Construction Example

```go
func BuildAudioLLMGraph() (*Graph, error) {
    // Create components
    mic := NewMicComponent("mic", transport)
    vad := NewVADComponent("vad")
    asr := NewRealASRComponent("asr", qcloudEngine)
    llm := NewLLMComponent("llm", llmHandler)
    tts := NewRealTTSComponent("tts", openaiEngine)
    speaker := NewSpeakerComponent("speaker", transport)
    
    // Build graph
    g := NewGraph()
    g.AddComponent(mic)
    g.AddComponent(vad)
    g.AddComponent(asr)
    g.AddComponent(llm)
    g.AddComponent(tts)
    g.AddComponent(speaker)
    
    // Connect ports
    g.Connect("mic", "audio_out", "vad", "audio_in")
    g.Connect("vad", "audio_out", "asr", "audio_in")
    g.Connect("asr", "text_out", "llm", "text_in")
    g.Connect("llm", "text_out", "tts", "text_in")
    g.Connect("tts", "audio_out", "speaker", "audio_in")
    
    return g, nil
}
```

## Benefits of AVFlow Integration

1. **Modularity**: Swap ASR/TTS implementations without changing graph structure
2. **Concurrency**: All components run in parallel with typed channels
3. **Observability**: Packet flow is traceable and debuggable
4. **Extensibility**: Add new components (DSP, emotion detection, etc.) easily
5. **Testability**: Mock components for unit testing
6. **Scalability**: Graph can be distributed across multiple processes/machines

## Next Steps

1. Implement `RealASRComponent` and `RealTTSComponent` wrappers
2. Add configuration management for different ASR/TTS providers
3. Implement comprehensive error handling and retry logic
4. Add metrics and observability (OpenTelemetry)
5. Create example applications demonstrating different configurations
