# Quick Start: ASR/TTS with AVFlow

## 1. Create an ASR Engine

```go
import "github.com/LingByte/LingVoice/pkg/recognizer"

// Using factory pattern
factory := recognizer.GetGlobalFactory()
config := &recognizer.QCloudASROption{
    // ... configuration
}
engine, err := factory.CreateTranscriber(config)
if err != nil {
    log.Fatal(err)
}
asrEngine := engine.(recognizer.SpeechRecognitionEngine)
```

## 2. Create a TTS Engine

```go
import "github.com/LingByte/LingVoice/pkg/synthesizer"

// Using factory pattern
factory := synthesizer.NewFactory()
config := &synthesizer.OpenAITTSConfig{
    // ... configuration
}
engine, err := factory.Create(synthesizer.ProviderOpenAI, "openai-tts", config)
if err != nil {
    log.Fatal(err)
}
ttsEngine := engine.(synthesizer.AudioSynthesisEngine)
```

## 3. Wrap in AVFlow Components

```go
import "github.com/LingByte/LingVoice/pkg/avflow"

asrComp := avflow.NewRealASRComponent("asr", asrEngine)
ttsComp := avflow.NewRealTTSComponent("tts", ttsEngine)
```

## 4. Build the Graph

```go
// Create transport
transport := av.NewChanTransport("demo", "duplex", 64)

// Create components
mic := avflow.NewMicComponent("mic", transport)
vad := avflow.NewVADComponent("vad", 0.5)
llm := avflow.NewLLMComponent("llm", "Assistant: ", llmHandler)
speaker := avflow.NewSpeakerComponent("speaker", transport)

// Build graph
g := avflow.NewGraph("audio-llm")
g.AddComponent(mic)
g.AddComponent(vad)
g.AddComponent(asrComp)
g.AddComponent(llm)
g.AddComponent(ttsComp)
g.AddComponent(speaker)

// Connect ports
g.Connect("mic", "audio_out", "vad", "audio_in")
g.Connect("vad", "audio_out", "asr", "audio_in")
g.Connect("vad", "control_out", "tts", "control_in")
g.Connect("asr", "text_out", "llm", "text_in")
g.Connect("llm", "text_out", "tts", "text_in")
g.Connect("tts", "audio_out", "speaker", "audio_in")
```

## 5. Run the Graph

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := g.Run(ctx); err != nil {
    log.Fatal(err)
}
```

## Key Interfaces

### SpeechRecognitionEngine (ASR)

```go
type SpeechRecognitionEngine interface {
    Init(resultCallback SpeechRecognitionResult, errorCallback RecognitionError)
    Vendor() string
    ConnAndReceive(dialogId string) error
    Activity() bool
    RestartClient()
    SendAudioBytes(data []byte) error
    SendEnd() error
    StopConn() error
}
```

### AudioSynthesisEngine (TTS)

```go
type AudioSynthesisEngine interface {
    Provider() TTSProvider
    Format() media.StreamFormat
    CacheKey(text string) string
    Synthesize(ctx context.Context, handler AudioSynthesisHandler, text string) error
    Close() error
}

type AudioSynthesisHandler interface {
    OnMessage([]byte)
    OnTimestamp(timestamp SentenceTimestamp)
}
```

## Supported Providers

### ASR (Speech Recognition)
- QCloud (腾讯云)
- Google Cloud
- FunASR (阿里云)
- Volcengine (火山引擎)
- Deepgram
- AWS Transcribe
- Baidu (百度)
- Whisper
- Gladia
- Local

### TTS (Text-to-Speech)
- OpenAI
- ElevenLabs
- Azure
- Google Cloud
- AWS Polly
- Volcengine (火山引擎)
- Baidu (百度)
- Qiniu (七牛云)
- Xunfei (讯飞)
- FishSpeech
- FishAudio
- Coqui
- Minimax
- Local

## Example: Complete Audio+LLM Pipeline

See `cmd/avflow-asr-tts-demo/main.go` for a complete working example.

## Troubleshooting

### ASR Not Recognizing Audio
1. Check audio format (sample rate, bit depth)
2. Verify VAD threshold is appropriate
3. Enable logging to see partial results

### TTS Audio Quality Issues
1. Check audio format matches TTS provider requirements
2. Verify text encoding (UTF-8)
3. Adjust voice parameters (speed, pitch)

### Barge-in Not Working
1. Ensure VAD component is connected to TTS control_in
2. Check VAD threshold is detecting user speech
3. Verify TTS respects control signals

## Next Steps

1. Read `docs/10-avflow-asr-tts-integration.md` for detailed integration guide
2. Check `docs/11-framework-completion-summary.md` for architecture overview
3. Explore provider-specific configuration in `pkg/recognizer/` and `pkg/synthesizer/`
4. Implement custom components for specialized processing
