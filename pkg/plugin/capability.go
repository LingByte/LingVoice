// Package plugin defines the plugin system for LingVoice's AI capabilities.
//
// This file defines the core capability interfaces (ASR/TTS/LLM/Recorder/Detector)
// that plugins implement. The registry and loader handle plugin lifecycle.
package plugin

import "context"

// PluginType 标识插件的能力类型。
type PluginType string

const (
	PluginTypeASR      PluginType = "asr"
	PluginTypeTTS      PluginType = "tts"
	PluginTypeLLM      PluginType = "llm"
	PluginTypeDetector PluginType = "detector"
	PluginTypeRecorder PluginType = "recorder"
)

// ─── 能力接口 ───────────────────────────────────────────────────────────────

// ASR (Automatic Speech Recognition) 语音识别接口。
// 接收 float32 PCM 音频样本，返回识别文本。
type ASR interface {
	// Recognize 对给定的音频进行语音识别。
	// audio 为 float32 PCM 样本（-1.0 ~ 1.0），单声道。
	Recognize(ctx context.Context, audio []float32) (text string, err error)
}

// TTS (Text To Speech) 语音合成接口。
// 接收文本，返回 float32 PCM 音频样本。
type TTS interface {
	// Synthesize 将文本合成为语音。
	// 返回 float32 PCM 样本（-1.0 ~ 1.0），单声道。
	Synthesize(ctx context.Context, text string) (audio []float32, err error)
}

// LLM (Large Language Model) 大语言模型接口。
// 接收提示文本，返回生成的响应文本。
type LLM interface {
	// Generate 根据提示文本生成响应。
	Generate(ctx context.Context, prompt string) (response string, err error)
}

// Detector 语音活动检测（VAD）/ 端点检测接口。
// 用于判断音频中是否包含语音。
type Detector interface {
	// Process 分析一段音频，返回是否检测到语音活动。
	// audio 为 float32 PCM 样本。
	// 返回 speaking=true 表示当前帧有语音，energy 为能量值（RMS）。
	Process(ctx context.Context, audio []float32) (speaking bool, energy float64, err error)
}

// Recorder 音频录制接口。
// 从音频源（麦克风/文件/网络）采集音频帧。
type Recorder interface {
	// Start 开始录制。
	Start(ctx context.Context) error
	// Stop 停止录制。
	Stop() error
	// Frames 返回音频帧通道，每帧为 float32 PCM 样本。
	Frames() <-chan []float32
	// Close 释放资源。
	Close() error
}

// ─── 插件基础接口 ─────────────────────────────────────────────────────────────

// Plugin 是所有插件必须实现的基础接口。
// 各能力接口（ASR/TTS/LLM 等）通过类型断言获取：
//
//	if asr, ok := p.(ASR); ok { text, _ := asr.Recognize(ctx, audio) }
type Plugin interface {
	// Name 返回插件名称（如 "mock-asr"）。
	Name() string
	// Type 返回插件能力类型。
	Type() PluginType
	// Init 用配置初始化插件。
	Init(config Config) error
	// Close 释放插件资源。
	Close() error
}

// Config 是传递给插件 Init 的配置。
// 这是一个通用的 key-value 配置，插件自行解析所需字段。
type Config struct {
	// Name 插件实例名称。
	Name string
	// Type 插件能力类型。
	Type PluginType
	// Params 插件特定参数。
	Params map[string]any
}

// PluginFactory 是插件工厂函数，创建一个新的插件实例。
type PluginFactory func() Plugin

// ─── 辅助函数 ─────────────────────────────────────────────────────────────────

// AsASR 将插件断言为 ASR 接口。
func AsASR(p Plugin) (ASR, bool) {
	asr, ok := p.(ASR)
	return asr, ok
}

// AsTTS 将插件断言为 TTS 接口。
func AsTTS(p Plugin) (TTS, bool) {
	tts, ok := p.(TTS)
	return tts, ok
}

// AsLLM 将插件断言为 LLM 接口。
func AsLLM(p Plugin) (LLM, bool) {
	llm, ok := p.(LLM)
	return llm, ok
}

// AsDetector 将插件断言为 Detector 接口。
func AsDetector(p Plugin) (Detector, bool) {
	det, ok := p.(Detector)
	return det, ok
}

// AsRecorder 将插件断言为 Recorder 接口。
func AsRecorder(p Plugin) (Recorder, bool) {
	rec, ok := p.(Recorder)
	return rec, ok
}
