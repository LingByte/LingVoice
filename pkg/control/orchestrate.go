package control

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// AgentLoop 是语音对话的核心编排循环。
//
// 循环流程：
//  1. Listen: 接收用户音频帧，送入 VAD（Detector）
//  2. VAD → TurnManager: 端点检测（语音开始/结束）
//  3. 语音结束 → ASR: 识别用户说的内容
//  4. ASR 文本 → LLM: 生成回复
//  5. LLM 回复 → TTS: 合成语音
//  6. TTS 音频 → Speak: 逐帧发送给客户端播放
//  7. 播放期间持续监听 VAD，如果检测到用户说话 → Barge-in: 中断播放，回到 Listen
type AgentLoop struct {
	session *AgentSession
	log     *zap.Logger

	// 音频输入通道
	audioIn chan []float32

	// 对话处理信号
	speechEndCh chan struct{} // 语音结束信号
	bargeInCh   chan struct{} // barge-in 信号

	// barge-in 标志（原子操作）
	bargeInFlag atomic.Bool

	// 当前播放控制
	speakMu     sync.Mutex
	speakCancel context.CancelFunc
}

// NewAgentLoop 创建一个新的 AgentLoop。
func NewAgentLoop(session *AgentSession, log *zap.Logger) *AgentLoop {
	return &AgentLoop{
		session:     session,
		log:         log.With(zap.String("component", "agent-loop")),
		audioIn:     make(chan []float32, 256),
		speechEndCh: make(chan struct{}, 1),
		bargeInCh:   make(chan struct{}, 1),
	}
}

// FeedAudio 喂入一帧用户音频。
// 非阻塞：如果缓冲区满则丢弃（避免反压影响实时性）。
func (l *AgentLoop) FeedAudio(audio []float32) {
	select {
	case l.audioIn <- audio:
	default:
		// 缓冲区满，丢弃这帧（实时音频不应阻塞）
		l.log.Debug("audio buffer full, dropping frame")
	}
}

// OnSpeechEnd 通知 AgentLoop 用户语音结束（由 TurnManager 触发）。
func (l *AgentLoop) OnSpeechEnd() {
	select {
	case l.speechEndCh <- struct{}{}:
	default:
		// 已有待处理的语音结束信号，跳过
	}
}

// BargeIn 触发 barge-in，中断当前 TTS 播放。
func (l *AgentLoop) BargeIn() {
	l.bargeInFlag.Store(true)
	select {
	case l.bargeInCh <- struct{}{}:
	default:
	}
}

// Run 运行 AgentLoop 主循环，直到 ctx 被取消。
func (l *AgentLoop) Run(ctx context.Context) {
	l.log.Info("agent loop started")
	defer l.log.Info("agent loop stopped")

	for {
		select {
		case <-ctx.Done():
			l.cancelSpeaking()
			return

		case audio := <-l.audioIn:
			l.handleAudioFrame(ctx, audio)

		case <-l.speechEndCh:
			l.handleTurn(ctx)

		case <-l.bargeInCh:
			// barge-in 信号在 handleAudioFrame 中已处理
			// 这里只是消费信号避免堆积
		}
	}
}

// handleAudioFrame 处理一帧用户音频。
func (l *AgentLoop) handleAudioFrame(ctx context.Context, audio []float32) {
	// 1. VAD 检测
	speaking, energy, err := l.session.detector.Process(ctx, audio)
	if err != nil {
		l.log.Error("VAD error", zap.Error(err))
		return
	}

	// 2. 如果正在说话（TTS 播放），检查 barge-in
	if l.session.State() == StateSpeaking {
		if speaking && energy > 0.02 {
			// 用户在 TTS 播放期间说话，触发 barge-in
			l.log.Info("barge-in detected during playback",
				zap.Float64("energy", energy))
			l.BargeIn()
			return
		}
		// 播放期间不累积音频、不送 TurnManager
		return
	}

	// 3. 累积音频（只在非播放状态）
	l.session.appendAudio(audio)

	// 4. 送入 TurnManager 进行端点检测
	l.session.turn.Process(speaking)
}

// handleTurn 处理一轮完整的对话：ASR → LLM → TTS → Speak。
func (l *AgentLoop) handleTurn(ctx context.Context) {
	// 取出累积的音频
	audio := l.session.drainAudio()
	if len(audio) == 0 {
		l.log.Debug("no audio to process for this turn")
		return
	}

	l.log.Info("processing turn", zap.Int("audioSamples", len(audio)))

	// 状态 → Thinking
	l.session.setState(StateThinking)

	// 1. ASR: 语音识别
	asrText, err := l.runASR(ctx, audio)
	if err != nil {
		l.log.Error("ASR failed", zap.Error(err))
		l.session.resetAudioBuffer()
		l.session.setState(StateListening)
		return
	}

	l.log.Info("ASR result", zap.String("text", asrText))
	if l.session.sink != nil {
		_ = l.session.sink.SendEvent("asr", map[string]any{
			"text": asrText,
		})
	}

	// 检查 barge-in（ASR 期间用户可能又开始说话）
	if l.bargeInFlag.Load() {
		l.log.Info("barge-in during ASR, aborting turn")
		l.bargeInFlag.Store(false)
		l.session.setState(StateListening)
		return
	}

	// 2. LLM: 生成回复
	llmResponse, err := l.runLLM(ctx, asrText)
	if err != nil {
		l.log.Error("LLM failed", zap.Error(err))
		l.session.setState(StateListening)
		return
	}

	l.log.Info("LLM response", zap.String("text", llmResponse))
	if l.session.sink != nil {
		_ = l.session.sink.SendEvent("llm", map[string]any{
			"text": llmResponse,
		})
	}

	// 检查 barge-in
	if l.bargeInFlag.Load() {
		l.log.Info("barge-in during LLM, aborting turn")
		l.bargeInFlag.Store(false)
		l.session.setState(StateListening)
		return
	}

	// 3. TTS: 语音合成
	ttsAudio, err := l.runTTS(ctx, llmResponse)
	if err != nil {
		l.log.Error("TTS failed", zap.Error(err))
		l.session.setState(StateListening)
		return
	}

	l.log.Info("TTS synthesized", zap.Int("audioSamples", len(ttsAudio)))

	// 4. Speak: 播放 TTS 音频（支持 barge-in 中断）
	l.session.setState(StateSpeaking)
	l.speak(ctx, ttsAudio)

	// 播放结束，回到监听状态
	l.bargeInFlag.Store(false)
	l.session.setState(StateListening)
}

// runASR 执行 ASR 识别。
func (l *AgentLoop) runASR(ctx context.Context, audio []float32) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return l.session.asr.Recognize(ctx, audio)
}

// runLLM 执行 LLM 生成。
func (l *AgentLoop) runLLM(ctx context.Context, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return l.session.llm.Generate(ctx, prompt)
}

// runTTS 执行 TTS 合成。
func (l *AgentLoop) runTTS(ctx context.Context, text string) ([]float32, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return l.session.tts.Synthesize(ctx, text)
}

// speak 逐帧播放 TTS 音频，支持 barge-in 中断。
// 音频按帧切分（每帧 frameMs 毫秒），逐帧发送给客户端。
// 在每帧之间检查 barge-in 信号，如果触发则停止播放。
func (l *AgentLoop) speak(ctx context.Context, audio []float32) {
	if len(audio) == 0 {
		return
	}

	// 创建可取消的播放上下文
	speakCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	l.speakMu.Lock()
	l.speakCancel = cancel
	l.speakMu.Unlock()

	defer func() {
		l.speakMu.Lock()
		l.speakCancel = nil
		l.speakMu.Unlock()
	}()

	// 计算每帧的样本数
	samplesPerFrame := int(float64(l.session.config.SampleRate) * float64(l.session.config.FrameMs) / 1000.0)
	if samplesPerFrame <= 0 {
		samplesPerFrame = 320 // 默认 20ms @ 16kHz
	}

	// 逐帧发送
	for offset := 0; offset < len(audio); offset += samplesPerFrame {
		// 检查 barge-in
		if l.bargeInFlag.Load() {
			l.log.Info("speak interrupted by barge-in",
				zap.Int("sentSamples", offset),
				zap.Int("totalSamples", len(audio)))
			return
		}

		// 检查上下文取消
		if speakCtx.Err() != nil {
			return
		}

		end := offset + samplesPerFrame
		if end > len(audio) {
			end = len(audio)
		}
		frame := audio[offset:end]

		// 发送音频帧给客户端
		if l.session.sink != nil {
			if err := l.session.sink.SendAudio(frame); err != nil {
				l.log.Error("send audio frame failed", zap.Error(err))
				return
			}
		}

		// 模拟实时播放速率（等待一帧的时间）
		frameDuration := time.Duration(l.session.config.FrameMs) * time.Millisecond
		select {
		case <-speakCtx.Done():
			return
		case <-l.bargeInCh:
			l.log.Info("speak interrupted by barge-in channel")
			return
		case <-time.After(frameDuration):
		}
	}

	l.log.Info("speak completed", zap.Int("totalSamples", len(audio)))
}

// cancelSpeaking 取消当前正在进行的 TTS 播放。
func (l *AgentLoop) cancelSpeaking() {
	l.speakMu.Lock()
	defer l.speakMu.Unlock()
	if l.speakCancel != nil {
		l.speakCancel()
		l.speakCancel = nil
	}
}
