package control

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/plugin"
	"go.uber.org/zap"
)

// SessionState 表示 AgentSession 的状态。
type SessionState int

const (
	StateIdle       SessionState = iota // 空闲
	StateListening                      // 监听用户说话
	StateThinking                       // ASR/LLM 处理中
	StateSpeaking                       // 播放 TTS 音频
)

func (s SessionState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateListening:
		return "listening"
	case StateThinking:
		return "thinking"
	case StateSpeaking:
		return "speaking"
	default:
		return "unknown"
	}
}

// SessionConfig AgentSession 配置。
type SessionConfig struct {
	// SampleRate 音频采样率（如 16000）。
	SampleRate uint32
	// Channels 音频声道数（通常为 1）。
	Channels uint16
	// FrameMs 每帧时长（毫秒，如 20）。
	FrameMs uint16
	// TurnConfig TurnManager 配置。
	TurnConfig TurnConfig
}

// DefaultSessionConfig 返回默认的会话配置。
func DefaultSessionConfig() SessionConfig {
	return SessionConfig{
		SampleRate: 16000,
		Channels:   1,
		FrameMs:    20,
		TurnConfig: DefaultTurnConfig(),
	}
}

// AudioSink 是音频输出接口，用于发送 TTS 音频到客户端。
// AgentSession 通过此接口将合成的音频帧发送给协议层。
type AudioSink interface {
	// SendAudio 发送一帧 float32 PCM 音频到客户端。
	SendAudio(audio []float32) error
	// SendEvent 发送一个事件消息到客户端（如状态变更、ASR 文本等）。
	SendEvent(eventType string, data map[string]any) error
}

// AgentSession 管理一个语音对话会话的完整生命周期。
// 它持有 ASR/TTS/LLM/Detector 插件实例，协调 AgentLoop 的运行。
//
// 生命周期：
//  1. Create → 创建会话，初始化插件和 AgentLoop
//  2. StartAudio → 开始接收音频帧
//  3. FeedAudio → 逐帧喂入用户音频
//  4. Stop/Destroy → 停止会话，释放资源
type AgentSession struct {
	mu     sync.Mutex
	id     string
	config SessionConfig
	log    *zap.Logger

	// 插件实例
	asr      plugin.ASR
	tts      plugin.TTS
	llm      plugin.LLM
	detector plugin.Detector

	// 组件
	turn  *TurnManager
	loop  *AgentLoop
	sink  AudioSink

	// 状态
	state    SessionState
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	closed   bool
	createdAt time.Time

	// 音频累积缓冲（用户说话期间的音频）
	audioBuffer []float32
}

// NewAgentSession 创建一个新的 AgentSession。
//
// 参数：
//   - id: 会话 ID
//   - config: 会话配置
//   - plugins: 插件加载器（需已加载 ASR/TTS/LLM/Detector）
//   - sink: 音频输出接口
//   - log: 日志
func NewAgentSession(
	id string,
	config SessionConfig,
	plugins *plugin.Loader,
	sink AudioSink,
	log *zap.Logger,
) (*AgentSession, error) {
	if log == nil {
		log = zap.NewNop()
	}
	log = log.With(zap.String("component", "agent-session"), zap.String("session", id))

	// 获取插件实例
	asr, ok := plugins.ASR()
	if !ok {
		return nil, fmt.Errorf("ASR plugin not loaded")
	}
	tts, ok := plugins.TTS()
	if !ok {
		return nil, fmt.Errorf("TTS plugin not loaded")
	}
	llm, ok := plugins.LLM()
	if !ok {
		return nil, fmt.Errorf("LLM plugin not loaded")
	}
	detector, ok := plugins.Detector()
	if !ok {
		return nil, fmt.Errorf("Detector plugin not loaded")
	}

	sess := &AgentSession{
		id:        id,
		config:    config,
		log:       log,
		asr:       asr,
		tts:       tts,
		llm:       llm,
		detector:  detector,
		sink:      sink,
		state:     StateIdle,
		createdAt: time.Now(),
	}

	// 创建 TurnManager
	sess.turn = NewTurnManager(config.TurnConfig, func(event TurnEvent) {
		sess.onTurnEvent(event)
	})

	// 创建 AgentLoop
	sess.loop = NewAgentLoop(sess, log)

	return sess, nil
}

// ID 返回会话 ID。
func (s *AgentSession) ID() string {
	return s.id
}

// State 返回当前会话状态。
func (s *AgentSession) State() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// setState 更新会话状态并通知客户端。
func (s *AgentSession) setState(newState SessionState) {
	s.mu.Lock()
	old := s.state
	s.state = newState
	s.mu.Unlock()

	if old != newState {
		s.log.Debug("state changed",
			zap.String("from", old.String()),
			zap.String("to", newState.String()))
		if s.sink != nil {
			_ = s.sink.SendEvent("state", map[string]any{
				"state": newState.String(),
				"from":  old.String(),
			})
		}
	}
}

// Start 启动会话的 AgentLoop。
func (s *AgentSession) Start() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session is closed")
	}
	if s.cancel != nil {
		s.mu.Unlock()
		return fmt.Errorf("session already started")
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()

	s.setState(StateListening)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.loop.Run(ctx)
	}()

	s.log.Info("agent session started")
	return nil
}

// FeedAudio 喂入一帧用户音频（float32 PCM）。
// 这是 AgentLoop 接收用户语音的入口。
func (s *AgentSession) FeedAudio(audio []float32) {
	if len(audio) == 0 {
		return
	}
	s.loop.FeedAudio(audio)
}

// Stop 停止会话，等待 AgentLoop 退出。
func (s *AgentSession) Stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.mu.Unlock()

	s.wg.Wait()
	s.setState(StateIdle)
	s.log.Info("agent session stopped")
}

// Destroy 销毁会话，释放所有资源。
func (s *AgentSession) Destroy() {
	s.Stop()

	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()

	s.log.Info("agent session destroyed",
		zap.Duration("uptime", time.Since(s.createdAt)))
}

// ─── 内部方法 ─────────────────────────────────────────────────────────────────

// onTurnEvent 处理 TurnManager 的事件。
func (s *AgentSession) onTurnEvent(event TurnEvent) {
	switch event {
	case TurnEventSpeechStart:
		s.log.Debug("speech start detected")
		// 如果正在说话（TTS 播放），触发 barge-in
		if s.State() == StateSpeaking {
			s.log.Info("barge-in: user started speaking during TTS playback")
			s.loop.BargeIn()
			if s.sink != nil {
				_ = s.sink.SendEvent("barge-in", map[string]any{
					"reason": "user-interrupt",
				})
			}
		}

	case TurnEventSpeechEnd:
		s.log.Debug("speech end detected (endpoint)")
		// 通知 AgentLoop 处理这一轮对话
		s.loop.OnSpeechEnd()

	case TurnEventTurnTimeout:
		s.log.Warn("turn timeout")
		s.loop.OnSpeechEnd()
	}
}

// appendAudio 将音频追加到累积缓冲。
func (s *AgentSession) appendAudio(audio []float32) {
	s.mu.Lock()
	s.audioBuffer = append(s.audioBuffer, audio...)
	s.mu.Unlock()
}

// drainAudio 取出并清空累积的音频缓冲。
func (s *AgentSession) drainAudio() []float32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	audio := s.audioBuffer
	s.audioBuffer = nil
	return audio
}

// resetAudioBuffer 清空音频缓冲。
func (s *AgentSession) resetAudioBuffer() {
	s.mu.Lock()
	s.audioBuffer = nil
	s.mu.Unlock()
}
