package mock

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/plugin"
)

// ─── 编译时接口实现检查 ──────────────────────────────────────────────────────────

var _ plugin.ASR = (*MockASR)(nil)
var _ plugin.TTS = (*MockTTS)(nil)
var _ plugin.LLM = (*MockLLM)(nil)
var _ plugin.Detector = (*EnergyVAD)(nil)

var _ plugin.Plugin = (*MockASR)(nil)
var _ plugin.Plugin = (*MockTTS)(nil)
var _ plugin.Plugin = (*MockLLM)(nil)
var _ plugin.Plugin = (*EnergyVAD)(nil)

// ─── MockASR ──────────────────────────────────────────────────────────────────

func TestMockASR_Recognize(t *testing.T) {
	want := "你好世界"
	asr := NewMockASR(want)
	got, err := asr.Recognize(context.Background(), []float32{0.1, 0.2, 0.3})
	if err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}
	if got != want {
		t.Errorf("Recognize text = %q, want %q", got, want)
	}
}

func TestMockASR_RecognizeEmptyAudio(t *testing.T) {
	asr := NewMockASR("固定文本")
	got, err := asr.Recognize(context.Background(), nil)
	if err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}
	if got != "固定文本" {
		t.Errorf("Recognize text = %q, want %q", got, "固定文本")
	}
}

func TestMockASR_NameAndType(t *testing.T) {
	asr := NewMockASR("test")
	if asr.Name() != "mock-asr" {
		t.Errorf("Name() = %q, want %q", asr.Name(), "mock-asr")
	}
	if asr.Type() != plugin.PluginTypeASR {
		t.Errorf("Type() = %q, want %q", asr.Type(), plugin.PluginTypeASR)
	}
}

func TestMockASR_InitOverridesText(t *testing.T) {
	asr := NewMockASR("初始文本")
	if err := asr.Init(plugin.Config{
		Name: "mock",
		Type: plugin.PluginTypeASR,
		Params: map[string]any{
			"text": "覆盖后的文本",
		},
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	got, err := asr.Recognize(context.Background(), []float32{0.1})
	if err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}
	if got != "覆盖后的文本" {
		t.Errorf("after Init, Recognize = %q, want %q", got, "覆盖后的文本")
	}
}

func TestMockASR_Close(t *testing.T) {
	asr := NewMockASR("test")
	if err := asr.Close(); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

// ─── MockTTS ──────────────────────────────────────────────────────────────────

func TestMockTTS_Synthesize(t *testing.T) {
	tts := NewMockTTS(440.0, 0.5, 16000)
	audio, err := tts.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	if len(audio) == 0 {
		t.Fatal("Synthesize returned empty audio")
	}
	// 0.5s + 5 chars * 0.08 = 0.9s -> 0.9 * 16000 = 14400 samples
	wantSamples := int((0.5 + float64(len([]rune("hello")))*0.08) * 16000)
	if len(audio) != wantSamples {
		t.Errorf("audio samples = %d, want %d", len(audio), wantSamples)
	}
}

func TestMockTTS_SynthesizeEmptyText(t *testing.T) {
	tts := NewMockTTS(440.0, 1.0, 16000)
	audio, err := tts.Synthesize(context.Background(), "")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	// 1.0s base duration, 0 chars
	wantSamples := int(1.0 * 16000)
	if len(audio) != wantSamples {
		t.Errorf("audio samples = %d, want %d", len(audio), wantSamples)
	}
}

func TestMockTTS_NameAndType(t *testing.T) {
	tts := NewMockTTS(440.0, 1.0, 16000)
	if tts.Name() != "mock-tts" {
		t.Errorf("Name() = %q, want %q", tts.Name(), "mock-tts")
	}
	if tts.Type() != plugin.PluginTypeTTS {
		t.Errorf("Type() = %q, want %q", tts.Type(), plugin.PluginTypeTTS)
	}
}

func TestMockTTS_InitOverridesParams(t *testing.T) {
	tts := NewMockTTS(100, 0.1, 8000)
	if err := tts.Init(plugin.Config{
		Name: "mock",
		Type: plugin.PluginTypeTTS,
		Params: map[string]any{
			"frequency":  float64(880.0),
			"duration":   float64(2.0),
			"sampleRate": 24000,
		},
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	audio, err := tts.Synthesize(context.Background(), "")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	// 2.0s @ 24000Hz = 48000 samples
	wantSamples := int(2.0 * 24000)
	if len(audio) != wantSamples {
		t.Errorf("after Init, audio samples = %d, want %d", len(audio), wantSamples)
	}
}

func TestMockTTS_Defaults(t *testing.T) {
	tts := NewMockTTS(0, 0, 0)
	audio, err := tts.Synthesize(context.Background(), "")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	// defaults: freq=440, duration=1.0, sampleRate=16000
	wantSamples := int(1.0 * 16000)
	if len(audio) != wantSamples {
		t.Errorf("default audio samples = %d, want %d", len(audio), wantSamples)
	}
}

func TestMockTTS_Close(t *testing.T) {
	tts := NewMockTTS(440.0, 1.0, 16000)
	if err := tts.Close(); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

// ─── MockLLM ──────────────────────────────────────────────────────────────────

func TestMockLLM_GenerateNoPrefix(t *testing.T) {
	llm := NewMockLLM("")
	got, err := llm.Generate(context.Background(), "你好")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if got != "你好" {
		t.Errorf("Generate = %q, want %q", got, "你好")
	}
}

func TestMockLLM_GenerateWithPrefix(t *testing.T) {
	llm := NewMockLLM("你说的是：")
	got, err := llm.Generate(context.Background(), "你好")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	want := "你说的是：你好"
	if got != want {
		t.Errorf("Generate = %q, want %q", got, want)
	}
}

func TestMockLLM_NameAndType(t *testing.T) {
	llm := NewMockLLM("prefix")
	if llm.Name() != "mock-llm" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "mock-llm")
	}
	if llm.Type() != plugin.PluginTypeLLM {
		t.Errorf("Type() = %q, want %q", llm.Type(), plugin.PluginTypeLLM)
	}
}

func TestMockLLM_InitOverridesPrefix(t *testing.T) {
	llm := NewMockLLM("初始前缀")
	if err := llm.Init(plugin.Config{
		Name: "mock",
		Type: plugin.PluginTypeLLM,
		Params: map[string]any{
			"prefix": "新前缀：",
		},
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	got, err := llm.Generate(context.Background(), "测试")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if got != "新前缀：测试" {
		t.Errorf("after Init, Generate = %q, want %q", got, "新前缀：测试")
	}
}

func TestMockLLM_Close(t *testing.T) {
	llm := NewMockLLM("prefix")
	if err := llm.Close(); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

// ─── EnergyVAD ────────────────────────────────────────────────────────────────

func TestEnergyVAD_ProcessSilence(t *testing.T) {
	vad := NewEnergyVAD(0.01)
	// 全零音频 → 能量为 0 → 无语音
	speaking, energy, err := vad.Process(context.Background(), []float32{0, 0, 0, 0})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if speaking {
		t.Error("expected speaking=false for silence, got true")
	}
	if energy != 0 {
		t.Errorf("energy = %v, want 0", energy)
	}
}

func TestEnergyVAD_ProcessSpeech(t *testing.T) {
	vad := NewEnergyVAD(0.01)
	// 高振幅音频 → 能量超过阈值 → 有语音
	audio := make([]float32, 100)
	for i := range audio {
		audio[i] = 0.5
	}
	speaking, energy, err := vad.Process(context.Background(), audio)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if !speaking {
		t.Error("expected speaking=true for loud audio, got false")
	}
	if energy <= 0 {
		t.Errorf("energy = %v, want > 0", energy)
	}
}

func TestEnergyVAD_ProcessEmptyAudio(t *testing.T) {
	vad := NewEnergyVAD(0.01)
	speaking, energy, err := vad.Process(context.Background(), nil)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if speaking {
		t.Error("expected speaking=false for empty audio, got true")
	}
	if energy != 0 {
		t.Errorf("energy = %v, want 0", energy)
	}
}

func TestEnergyVAD_NameAndType(t *testing.T) {
	vad := NewEnergyVAD(0.01)
	if vad.Name() != "energy-vad" {
		t.Errorf("Name() = %q, want %q", vad.Name(), "energy-vad")
	}
	if vad.Type() != plugin.PluginTypeDetector {
		t.Errorf("Type() = %q, want %q", vad.Type(), plugin.PluginTypeDetector)
	}
}

func TestEnergyVAD_InitOverridesThreshold(t *testing.T) {
	vad := NewEnergyVAD(0.01)
	if err := vad.Init(plugin.Config{
		Name: "energy",
		Type: plugin.PluginTypeDetector,
		Params: map[string]any{
			"threshold": float64(0.9),
		},
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	// 低能量音频，阈值提高到 0.9 后应判为无语音
	audio := []float32{0.1, 0.1, 0.1}
	speaking, _, err := vad.Process(context.Background(), audio)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if speaking {
		t.Error("expected speaking=false with high threshold, got true")
	}
}

func TestEnergyVAD_DefaultThreshold(t *testing.T) {
	vad := NewEnergyVAD(0)
	// 阈值 0 → 默认 0.01
	audio := []float32{0.5, 0.5, 0.5}
	speaking, _, err := vad.Process(context.Background(), audio)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if !speaking {
		t.Error("expected speaking=true with default threshold, got false")
	}
}

func TestEnergyVAD_Close(t *testing.T) {
	vad := NewEnergyVAD(0.01)
	if err := vad.Close(); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

// ─── 辅助函数 ──────────────────────────────────────────────────────────────────

func TestFormatAudioInfo(t *testing.T) {
	if got := FormatAudioInfo(nil); got != "empty" {
		t.Errorf("FormatAudioInfo(nil) = %q, want %q", got, "empty")
	}
	if got := FormatAudioInfo([]float32{}); got != "empty" {
		t.Errorf("FormatAudioInfo([]) = %q, want %q", got, "empty")
	}
	got := FormatAudioInfo([]float32{0.5, -0.5, 0.3})
	if got == "empty" {
		t.Error("FormatAudioInfo with data returned 'empty'")
	}
}

// ─── 工厂注册 ──────────────────────────────────────────────────────────────────

func TestRegister(t *testing.T) {
	r := plugin.NewRegistry()
	if err := Register(r); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	// 验证各类型插件可创建
	asr, err := r.Create(plugin.PluginTypeASR, "mock")
	if err != nil {
		t.Fatalf("Create ASR mock failed: %v", err)
	}
	if asr.Name() != "mock-asr" {
		t.Errorf("ASR plugin name = %q, want %q", asr.Name(), "mock-asr")
	}

	tts, err := r.Create(plugin.PluginTypeTTS, "mock")
	if err != nil {
		t.Fatalf("Create TTS mock failed: %v", err)
	}
	if tts.Name() != "mock-tts" {
		t.Errorf("TTS plugin name = %q, want %q", tts.Name(), "mock-tts")
	}

	llm, err := r.Create(plugin.PluginTypeLLM, "mock")
	if err != nil {
		t.Fatalf("Create LLM mock failed: %v", err)
	}
	if llm.Name() != "mock-llm" {
		t.Errorf("LLM plugin name = %q, want %q", llm.Name(), "mock-llm")
	}

	det, err := r.Create(plugin.PluginTypeDetector, "energy")
	if err != nil {
		t.Fatalf("Create Detector energy failed: %v", err)
	}
	if det.Name() != "energy-vad" {
		t.Errorf("Detector plugin name = %q, want %q", det.Name(), "energy-vad")
	}
}

func TestRegisterDuplicate(t *testing.T) {
	r := plugin.NewRegistry()
	if err := Register(r); err != nil {
		t.Fatalf("first Register returned error: %v", err)
	}
	// 重复注册应返回错误
	if err := Register(r); err == nil {
		t.Error("expected error on duplicate Register, got nil")
	}
}
