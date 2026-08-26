// Package mock provides mock implementations of LingVoice plugin interfaces.
//
// 这些实现用于测试和演示，不依赖外部服务：
//   - MockASR: 回显固定文本
//   - MockTTS: 生成正弦波音频
//   - MockLLM: 回显输入（可加前缀）
//   - EnergyVAD: 基于能量的语音活动检测
package mock

import (
	"context"
	"fmt"
	"math"
	"sync"

	"github.com/LingByte/LingVoice/pkg/plugin"
)

// ─── MockASR ──────────────────────────────────────────────────────────────────

// MockASR 语音识别 mock 实现，始终返回固定文本。
type MockASR struct {
	mu   sync.Mutex
	text string
}

// NewMockASR 创建一个 MockASR，返回固定文本 text。
func NewMockASR(text string) *MockASR {
	return &MockASR{text: text}
}

func (m *MockASR) Name() string                  { return "mock-asr" }
func (m *MockASR) Type() plugin.PluginType       { return plugin.PluginTypeASR }
func (m *MockASR) Init(config plugin.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := config.Params["text"]; ok {
		if s, ok := v.(string); ok {
			m.text = s
		}
	}
	return nil
}
func (m *MockASR) Close() error { return nil }

// Recognize 忽略音频内容，返回固定文本。
func (m *MockASR) Recognize(_ context.Context, _ []float32) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.text, nil
}

// ─── MockTTS ──────────────────────────────────────────────────────────────────

// MockTTS 语音合成 mock 实现，生成正弦波音频。
type MockTTS struct {
	mu        sync.Mutex
	frequency float64 // 正弦波频率 (Hz)
	duration  float64 // 持续时间 (秒)
	sampleRate uint32  // 采样率
}

// NewMockTTS 创建一个 MockTTS。
func NewMockTTS(frequency float64, duration float64, sampleRate uint32) *MockTTS {
	if frequency <= 0 {
		frequency = 440.0
	}
	if duration <= 0 {
		duration = 1.0
	}
	if sampleRate == 0 {
		sampleRate = 16000
	}
	return &MockTTS{
		frequency:  frequency,
		duration:   duration,
		sampleRate: sampleRate,
	}
}

func (m *MockTTS) Name() string                  { return "mock-tts" }
func (m *MockTTS) Type() plugin.PluginType       { return plugin.PluginTypeTTS }
func (m *MockTTS) Init(config plugin.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := config.Params["frequency"]; ok {
		if f, ok := toFloat64(v); ok {
			m.frequency = f
		}
	}
	if v, ok := config.Params["duration"]; ok {
		if d, ok := toFloat64(v); ok {
			m.duration = d
		}
	}
	if v, ok := config.Params["sampleRate"]; ok {
		if sr, ok := toUint32(v); ok {
			m.sampleRate = sr
		}
	}
	return nil
}
func (m *MockTTS) Close() error { return nil }

// Synthesize 生成一段正弦波音频。
// 文本长度影响持续时间：每个字符增加 0.1 秒，但不超过配置的 duration。
func (m *MockTTS) Synthesize(_ context.Context, text string) ([]float32, error) {
	m.mu.Lock()
	freq := m.frequency
	baseDuration := m.duration
	sr := m.sampleRate
	m.mu.Unlock()

	// 根据文本长度调整持续时间（模拟真实 TTS 的时长差异）
	charDuration := float64(len([]rune(text))) * 0.08
	duration := baseDuration + charDuration
	if duration > 10.0 {
		duration = 10.0
	}

	numSamples := int(duration * float64(sr))
	audio := make([]float32, numSamples)

	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sr)
		// 正弦波 + 淡入淡出包络（避免爆音）
		amp := 0.3
		envelope := 1.0
		fadeSamples := int(sr / 50) // 20ms 淡入淡出
		if i < fadeSamples {
			envelope = float64(i) / float64(fadeSamples)
		} else if i > numSamples-fadeSamples {
			envelope = float64(numSamples-i) / float64(fadeSamples)
		}
		audio[i] = float32(amp * envelope * math.Sin(2*math.Pi*freq*t))
	}
	return audio, nil
}

// ─── MockLLM ──────────────────────────────────────────────────────────────────

// MockLLM 大语言模型 mock 实现，回显输入文本（可加前缀）。
type MockLLM struct {
	mu     sync.Mutex
	prefix string
}

// NewMockLLM 创建一个 MockLLM。
func NewMockLLM(prefix string) *MockLLM {
	return &MockLLM{prefix: prefix}
}

func (m *MockLLM) Name() string                  { return "mock-llm" }
func (m *MockLLM) Type() plugin.PluginType       { return plugin.PluginTypeLLM }
func (m *MockLLM) Init(config plugin.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := config.Params["prefix"]; ok {
		if s, ok := v.(string); ok {
			m.prefix = s
		}
	}
	return nil
}
func (m *MockLLM) Close() error { return nil }

// Generate 回显输入文本，可加前缀。
func (m *MockLLM) Generate(_ context.Context, prompt string) (string, error) {
	m.mu.Lock()
	prefix := m.prefix
	m.mu.Unlock()

	if prefix != "" {
		return prefix + prompt, nil
	}
	return prompt, nil
}

// ─── EnergyVAD ────────────────────────────────────────────────────────────────

// EnergyVAD 基于能量的语音活动检测 mock 实现。
// 当音频 RMS 能量超过阈值时判定为有语音。
type EnergyVAD struct {
	mu        sync.Mutex
	threshold float64
}

// NewEnergyVAD 创建一个 EnergyVAD。
func NewEnergyVAD(threshold float64) *EnergyVAD {
	if threshold <= 0 {
		threshold = 0.01
	}
	return &EnergyVAD{threshold: threshold}
}

func (v *EnergyVAD) Name() string                  { return "energy-vad" }
func (v *EnergyVAD) Type() plugin.PluginType       { return plugin.PluginTypeDetector }
func (v *EnergyVAD) Init(config plugin.Config) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if val, ok := config.Params["threshold"]; ok {
		if t, ok := toFloat64(val); ok {
			v.threshold = t
		}
	}
	return nil
}
func (v *EnergyVAD) Close() error { return nil }

// Process 计算音频 RMS 能量，与阈值比较判断是否有语音。
func (v *EnergyVAD) Process(_ context.Context, audio []float32) (bool, float64, error) {
	if len(audio) == 0 {
		return false, 0, nil
	}

	var sum float64
	for _, s := range audio {
		sum += float64(s) * float64(s)
	}
	rms := math.Sqrt(sum / float64(len(audio)))

	v.mu.Lock()
	threshold := v.threshold
	v.mu.Unlock()

	return rms > threshold, rms, nil
}

// ─── 工厂注册 ─────────────────────────────────────────────────────────────────

// Register 将所有 mock 插件工厂注册到给定的 Registry。
func Register(r *plugin.Registry) error {
	if err := r.Register(plugin.PluginTypeASR, "mock", func() plugin.Plugin {
		return NewMockASR("你好，这是 mock ASR 的固定回复。")
	}); err != nil {
		return err
	}
	if err := r.Register(plugin.PluginTypeTTS, "mock", func() plugin.Plugin {
		return NewMockTTS(440.0, 0.5, 16000)
	}); err != nil {
		return err
	}
	if err := r.Register(plugin.PluginTypeLLM, "mock", func() plugin.Plugin {
		return NewMockLLM("你说的是：")
	}); err != nil {
		return err
	}
	if err := r.Register(plugin.PluginTypeDetector, "energy", func() plugin.Plugin {
		return NewEnergyVAD(0.01)
	}); err != nil {
		return err
	}
	return nil
}

// RegisterWithDefaults 使用 RegisterBuiltin 机制注册 mock 插件，
// 这样 plugin.RegisterBuiltins 会自动包含 mock 插件。
func init() {
	plugin.RegisterBuiltin(func(r *plugin.Registry) error {
		return Register(r)
	})
}

// ─── 辅助函数 ─────────────────────────────────────────────────────────────────

// toFloat64 尝试将 any 转换为 float64（支持 int/float64/float32）。
func toFloat64(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case int32:
		return float64(val), true
	default:
		return 0, false
	}
}

// toUint32 尝试将 any 转换为 uint32。
func toUint32(v any) (uint32, bool) {
	switch val := v.(type) {
	case float64:
		return uint32(val), true
	case float32:
		return uint32(val), true
	case int:
		return uint32(val), true
	case int64:
		return uint32(val), true
	case int32:
		return uint32(val), true
	default:
		return 0, false
	}
}

// FormatAudioInfo 返回音频信息的可读字符串（用于日志）。
func FormatAudioInfo(audio []float32) string {
	if len(audio) == 0 {
		return "empty"
	}
	var sum float64
	var max float32
	for _, s := range audio {
		abs := s
		if abs < 0 {
			abs = -abs
		}
		if abs > max {
			max = abs
		}
		sum += float64(s) * float64(s)
	}
	rms := math.Sqrt(sum / float64(len(audio)))
	return fmt.Sprintf("samples=%d rms=%.4f peak=%.4f", len(audio), rms, max)
}

