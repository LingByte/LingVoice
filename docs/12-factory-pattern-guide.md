---
description: Factory Pattern Guide for ASR/TTS Provider Selection
---

# Factory Pattern Guide: ASR/TTS Provider Selection

## Overview

The LingVoice framework uses the **Factory Pattern** to provide a clean, extensible way to instantiate ASR and TTS engines. This guide explains how to use the factories to create engines with provider-specific configurations.

## Key Principles

1. **Type Safety**: Each provider has its own configuration struct (e.g., `QCloudASROption`, `OpenAIConfig`)
2. **Extensibility**: New providers can be registered without modifying existing code
3. **Consistency**: Both ASR and TTS follow the same factory pattern
4. **Thread Safety**: Global factories use mutex locks for concurrent access

## ASR Factory Pattern

### Configuration Interface

```go
type TranscriberConfig interface {
    GetVendor() Vendor
}
```

Each ASR provider implements this interface with its own config struct:

```go
type QCloudASROption struct {
    // QCloud-specific fields
    AppID     string
    SecretID  string
    SecretKey string
    // ... other fields
}

func (q *QCloudASROption) GetVendor() Vendor {
    return VendorQCloud
}
```

### Factory Interface

```go
type TranscriberFactory interface {
    CreateTranscriber(config TranscriberConfig) (SpeechRecognitionEngine, error)
    GetSupportedVendors() []Vendor
    IsVendorSupported(vendor Vendor) bool
}
```

### Usage Example: QCloud ASR

```go
import (
    "github.com/LingByte/LingVoice/pkg/recognizer"
)

// Create QCloud-specific configuration
qcloudConfig := &recognizer.QCloudASROption{
    AppID:     "your-app-id",
    SecretID:  "your-secret-id",
    SecretKey: "your-secret-key",
    Language:  "zh-CN",
    // ... other QCloud-specific options
}

// Get the global factory
factory := recognizer.GetGlobalFactory()

// Create the ASR engine
engine, err := factory.CreateTranscriber(qcloudConfig)
if err != nil {
    log.Fatal(err)
}

// Use the engine
asr := engine.(recognizer.SpeechRecognitionEngine)
```

### Usage Example: Google ASR

```go
// Create Google-specific configuration
googleConfig := &recognizer.GoogleASROption{
    ProjectID:   "your-project-id",
    CredentialsJSON: []byte{...},
    Language:    "en-US",
    // ... other Google-specific options
}

// Create the ASR engine
engine, err := factory.CreateTranscriber(googleConfig)
if err != nil {
    log.Fatal(err)
}
```

### Supported ASR Vendors

```go
const (
    VendorQCloud        Vendor = "qcloud"      // 腾讯云
    VendorGoogle        Vendor = "google"      // Google Cloud
    VendorAliyun        Vendor = "aliyun"      // 阿里云
    VendorFunASR        Vendor = "funasr"      // FunASR
    VendorVolcengine    Vendor = "volcengine"  // 火山引擎
    VendorXfyunMul      Vendor = "xfyun_mul"   // 科大讯飞
    VendorGladia        Vendor = "gladia"      // Gladia
    VendorFunASRRealtime Vendor = "funasr_realtime" // FunASR实时
    VendorWhisper       Vendor = "whisper"     // Whisper
    VendorDeepgram      Vendor = "deepgram"    // Deepgram
    VendorAWS           Vendor = "aws"         // AWS
    VendorBaidu         Vendor = "baidu"       // 百度
    VendorLocal         Vendor = "local"       // 本地
)
```

## TTS Factory Pattern

### Configuration Interface

```go
type SynthesisConfig interface {
    GetProvider() TTSProvider
}
```

Each TTS provider implements this interface:

```go
type OpenAIConfig struct {
    APIKey    string
    BaseURL   string
    Model     string
    Voice     string
    Speed     float64
    // ... other fields
}

func (o *OpenAIConfig) GetProvider() TTSProvider {
    return ProviderOpenAI
}
```

### Factory Interface

```go
type SynthesisFactory interface {
    CreateEngine(config SynthesisConfig) (AudioSynthesisEngine, error)
    GetSupportedProviders() []TTSProvider
    IsProviderSupported(provider TTSProvider) bool
    RegisterCreator(provider TTSProvider, creator func(SynthesisConfig) (AudioSynthesisEngine, error))
}
```

### Usage Example: OpenAI TTS

```go
import (
    "github.com/LingByte/LingVoice/pkg/synthesizer"
)

// Create OpenAI-specific configuration
openaiConfig := &synthesizer.OpenAIConfig{
    APIKey:  "sk-...",
    Model:   "tts-1",
    Voice:   "nova",
    Speed:   1.0,
    Codec:   "mp3",
}

// Get the global factory
factory := synthesizer.GetGlobalSynthesisFactory()

// Create the TTS engine
engine, err := factory.CreateEngine(openaiConfig)
if err != nil {
    log.Fatal(err)
}

// Use the engine
tts := engine.(synthesizer.AudioSynthesisEngine)
```

### Usage Example: QCloud TTS

```go
// Create QCloud-specific configuration
qcloudConfig := &synthesizer.QCloudTTSConfig{
    AppID:     "your-app-id",
    SecretID:  "your-secret-id",
    SecretKey: "your-secret-key",
    VoiceType: 601002, // 云智天声-女声
    Codec:     "pcm",
    SampleRate: 16000,
}

// Create the TTS engine
engine, err := factory.CreateEngine(qcloudConfig)
if err != nil {
    log.Fatal(err)
}
```

### Supported TTS Providers

```go
const (
    ProviderQiniu          TTSProvider = "qiniu"           // 七牛云
    ProviderXunfei         TTSProvider = "xunfei"          // 讯飞
    ProviderAliyun         TTSProvider = "aliyun"          // 阿里云
    ProviderTencent        TTSProvider = "qcloud"          // 腾讯云
    ProviderBaidu          TTSProvider = "baidu"           // 百度
    ProviderAzure          TTSProvider = "azure"           // Azure
    ProviderGoogle         TTSProvider = "google"          // Google Cloud
    ProviderAWS            TTSProvider = "aws"             // AWS Polly
    ProviderOpenAI         TTSProvider = "openai"          // OpenAI
    ProviderElevenLabs     TTSProvider = "elevenlabs"      // ElevenLabs
    ProviderLocal          TTSProvider = "local"           // 本地
    ProviderLocalGoSpeech  TTSProvider = "local_gospeech"  // 本地go-speech
    ProviderFishSpeech     TTSProvider = "fishspeech"      // FishSpeech
    ProviderFishAudio      TTSProvider = "fishaudio"       // Fish Audio
    ProviderCoqui          TTSProvider = "coqui"           // Coqui
    ProviderVolcengine     TTSProvider = "volcengine"      // 火山引擎
    ProviderMinimax        TTSProvider = "minimax"         // Minimax
)
```

## Complete Example: Building an Audio+LLM Pipeline

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/LingByte/LingVoice/pkg/avflow"
    "github.com/LingByte/LingVoice/pkg/recognizer"
    "github.com/LingByte/LingVoice/pkg/runtime/av"
    "github.com/LingByte/LingVoice/pkg/synthesizer"
)

func main() {
    // Step 1: Create ASR engine using factory
    asrFactory := recognizer.GetGlobalFactory()
    asrConfig := &recognizer.QCloudASROption{
        AppID:     "your-app-id",
        SecretID:  "your-secret-id",
        SecretKey: "your-secret-key",
        Language:  "zh-CN",
    }
    asrEngine, err := asrFactory.CreateTranscriber(asrConfig)
    if err != nil {
        log.Fatalf("Failed to create ASR engine: %v", err)
    }

    // Step 2: Create TTS engine using factory
    ttsFactory := synthesizer.GetGlobalSynthesisFactory()
    ttsConfig := &synthesizer.OpenAIConfig{
        APIKey: "sk-...",
        Model:  "tts-1",
        Voice:  "nova",
        Speed:  1.0,
    }
    ttsEngine, err := ttsFactory.CreateEngine(ttsConfig)
    if err != nil {
        log.Fatalf("Failed to create TTS engine: %v", err)
    }

    // Step 3: Create transport
    transport := av.NewChanTransport("demo", "duplex", 64)

    // Step 4: Create AVFlow components
    mic := avflow.NewMicComponent("mic", transport)
    vad := avflow.NewVADComponent("vad", 0.5)
    asr := avflow.NewRealASRComponent("asr", asrEngine)
    llm := avflow.NewLLMComponent("llm", "Assistant: ", func(ctx context.Context, prompt string) (<-chan string, error) {
        // Your LLM logic here
        ch := make(chan string, 1)
        ch <- "Response to: " + prompt
        close(ch)
        return ch, nil
    })
    tts := avflow.NewRealTTSComponent("tts", ttsEngine)
    speaker := avflow.NewSpeakerComponent("speaker", transport)

    // Step 5: Build the graph
    g := avflow.NewGraph("audio-llm-pipeline")
    g.AddComponent(mic)
    g.AddComponent(vad)
    g.AddComponent(asr)
    g.AddComponent(llm)
    g.AddComponent(tts)
    g.AddComponent(speaker)

    // Connect ports
    g.Connect("mic", "audio_out", "vad", "audio_in")
    g.Connect("vad", "audio_out", "asr", "audio_in")
    g.Connect("vad", "control_out", "tts", "control_in")
    g.Connect("asr", "text_out", "llm", "text_in")
    g.Connect("llm", "text_out", "tts", "text_in")
    g.Connect("tts", "audio_out", "speaker", "audio_in")

    // Step 6: Run the graph
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    if err := g.Run(ctx); err != nil {
        log.Fatalf("Graph execution failed: %v", err)
    }

    log.Println("Pipeline completed successfully!")
}
```

## Custom Provider Registration

You can register custom providers with the factory:

```go
// Custom ASR provider
type CustomASRConfig struct {
    APIKey string
}

func (c *CustomASRConfig) GetVendor() recognizer.Vendor {
    return "custom"
}

// Register with factory
factory := recognizer.GetGlobalFactory()
factory.RegisterCreator("custom", func(config recognizer.TranscriberConfig) (recognizer.SpeechRecognitionEngine, error) {
    customConfig, ok := config.(*CustomASRConfig)
    if !ok {
        return nil, fmt.Errorf("invalid config type")
    }
    return NewCustomASR(customConfig.APIKey), nil
})

// Use it
customConfig := &CustomASRConfig{APIKey: "..."}
engine, err := factory.CreateTranscriber(customConfig)
```

## Best Practices

### 1. Use Type-Specific Configs

```go
// ✅ Good: Type-specific configuration
qcloudConfig := &recognizer.QCloudASROption{
    AppID:     "...",
    SecretID:  "...",
    SecretKey: "...",
}

// ❌ Bad: Generic map-based configuration
config := map[string]interface{}{
    "appId":     "...",
    "secretId":  "...",
    "secretKey": "...",
}
```

### 2. Check Provider Support

```go
factory := recognizer.GetGlobalFactory()

// Check if provider is supported
if !factory.IsVendorSupported(recognizer.VendorQCloud) {
    log.Fatal("QCloud ASR not supported")
}

// List all supported providers
vendors := factory.GetSupportedVendors()
for _, vendor := range vendors {
    log.Printf("Supported: %s", vendor)
}
```

### 3. Handle Configuration Errors

```go
config := &recognizer.QCloudASROption{
    // Missing required fields
}

engine, err := factory.CreateTranscriber(config)
if err != nil {
    // Handle configuration error
    log.Printf("Configuration error: %v", err)
}
```

### 4. Use Global Factory Singleton

```go
// Reuse the global factory instance
factory := recognizer.GetGlobalFactory()

// Create multiple engines with different configs
asr1, _ := factory.CreateTranscriber(config1)
asr2, _ := factory.CreateTranscriber(config2)
```

## Migration from JSON Config

If you're migrating from JSON-based configuration:

### Before (JSON-based)

```go
configJSON := `{
    "provider": "qcloud",
    "appId": "...",
    "secretId": "...",
    "secretKey": "..."
}`

engine, err := synthesizer.NewAudioSynthesisEngineFromCredential(
    parseJSON(configJSON),
)
```

### After (Type-safe factory)

```go
config := &synthesizer.QCloudTTSConfig{
    AppID:     "...",
    SecretID:  "...",
    SecretKey: "...",
}

factory := synthesizer.GetGlobalSynthesisFactory()
engine, err := factory.CreateEngine(config)
```

## Benefits

✅ **Type Safety**: Compile-time checking of configuration fields  
✅ **IDE Support**: Auto-completion for provider-specific options  
✅ **Error Prevention**: Missing required fields caught at compile time  
✅ **Documentation**: Config structs serve as self-documenting API  
✅ **Extensibility**: Easy to add new providers without modifying core code  
✅ **Testability**: Mock factories for unit testing  
✅ **Performance**: No JSON marshaling/unmarshaling overhead  

## Next Steps

1. Update your configuration loading to use type-specific structs
2. Replace JSON-based configuration with factory pattern
3. Register custom providers using the factory interface
4. Add configuration validation in your config structs
5. Create configuration builders for complex setups
