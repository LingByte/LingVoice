package control

import (
	"sync"
	"time"
)

// TurnState 表示一轮对话的状态。
type TurnState int

const (
	TurnIdle       TurnState = iota // 空闲，等待用户说话
	TurnListening                   // 检测到语音，正在录音
	TurnEnding                      // 语音结束，准备触发 ASR
)

func (s TurnState) String() string {
	switch s {
	case TurnIdle:
		return "idle"
	case TurnListening:
		return "listening"
	case TurnEnding:
		return "ending"
	default:
		return "unknown"
	}
}

// TurnConfig TurnManager 配置。
type TurnConfig struct {
	// SpeechStartFrames 检测到语音开始所需的连续语音帧数。
	// 每帧通常为 20ms，默认 3 帧 = 60ms。
	SpeechStartFrames int
	// SilenceEndFrames 检测到语音结束所需的连续静音帧数。
	// 每帧通常为 20ms，默认 25 帧 = 500ms。
	SilenceEndFrames int
	// MinTurnDuration 最小轮次时长（毫秒），短于此时间的语音被视为噪声。
	MinTurnDuration time.Duration
	// MaxTurnDuration 最大轮次时长（毫秒），超过则强制结束。
	MaxTurnDuration time.Duration
}

// DefaultTurnConfig 返回默认的 TurnManager 配置。
func DefaultTurnConfig() TurnConfig {
	return TurnConfig{
		SpeechStartFrames: 3,     // 60ms @ 20ms/frame
		SilenceEndFrames:  25,    // 500ms @ 20ms/frame
		MinTurnDuration:   200 * time.Millisecond,
		MaxTurnDuration:   30 * time.Second,
	}
}

// TurnEvent 表示 TurnManager 产生的事件。
type TurnEvent int

const (
	TurnEventSpeechStart TurnEvent = iota // 语音开始
	TurnEventSpeechEnd                    // 语音结束（端点检测）
	TurnEventTurnTimeout                  // 轮次超时
)

func (e TurnEvent) String() string {
	switch e {
	case TurnEventSpeechStart:
		return "speech-start"
	case TurnEventSpeechEnd:
		return "speech-end"
	case TurnEventTurnTimeout:
		return "turn-timeout"
	default:
		return "unknown"
	}
}

// TurnCallback 是 TurnManager 事件的回调函数。
type TurnCallback func(event TurnEvent)

// TurnManager 管理单轮对话的端点检测。
// 它基于 VAD（语音活动检测）结果，判断用户何时开始说话、何时结束说话。
//
// 状态机：
//
//	Idle ──(连续 N 帧语音)──→ Listening ──(连续 M 帧静音)──→ Ending ──→ Idle
//	                                    └──(超时)──→ Ending ──→ Idle
type TurnManager struct {
	mu     sync.Mutex
	config TurnConfig

	state       TurnState
	speechFrames int // 连续语音帧计数
	silenceFrames int // 连续静音帧计数
	turnStart   time.Time
	lastActive  time.Time

	callback TurnCallback
}

// NewTurnManager 创建一个新的 TurnManager。
func NewTurnManager(config TurnConfig, callback TurnCallback) *TurnManager {
	if config.SpeechStartFrames <= 0 {
		config.SpeechStartFrames = 3
	}
	if config.SilenceEndFrames <= 0 {
		config.SilenceEndFrames = 25
	}
	if config.MinTurnDuration <= 0 {
		config.MinTurnDuration = 200 * time.Millisecond
	}
	if config.MaxTurnDuration <= 0 {
		config.MaxTurnDuration = 30 * time.Second
	}
	return &TurnManager{
		config:   config,
		callback: callback,
		state:    TurnIdle,
	}
}

// State 返回当前状态。
func (tm *TurnManager) State() TurnState {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.state
}

// Reset 重置到空闲状态。
func (tm *TurnManager) Reset() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.state = TurnIdle
	tm.speechFrames = 0
	tm.silenceFrames = 0
	tm.turnStart = time.Time{}
	tm.lastActive = time.Time{}
}

// Process 处理一帧 VAD 结果，返回产生的事件列表（可能为空）。
// speaking 为 VAD 检测结果（是否检测到语音）。
// 如果设置了回调，事件会在锁释放后同步触发。
func (tm *TurnManager) Process(speaking bool) []TurnEvent {
	tm.mu.Lock()
	events := tm.processLocked(speaking)
	tm.mu.Unlock()

	// 在锁外触发回调，避免回调中访问 TurnManager 导致死锁
	if tm.callback != nil {
		for _, e := range events {
			tm.callback(e)
		}
	}
	return events
}

// processLocked 在持有锁的情况下处理 VAD 结果。
func (tm *TurnManager) processLocked(speaking bool) []TurnEvent {
	var events []TurnEvent
	now := time.Now()

	switch tm.state {
	case TurnIdle:
		if speaking {
			tm.speechFrames++
			tm.silenceFrames = 0
			if tm.speechFrames >= tm.config.SpeechStartFrames {
				tm.state = TurnListening
				tm.turnStart = now
				tm.lastActive = now
				tm.speechFrames = 0
				events = append(events, TurnEventSpeechStart)
			}
		} else {
			tm.speechFrames = 0
		}

	case TurnListening:
		if speaking {
			tm.silenceFrames = 0
			tm.lastActive = now
		} else {
			tm.silenceFrames++
			if tm.silenceFrames >= tm.config.SilenceEndFrames {
				turnDuration := now.Sub(tm.turnStart)
				if turnDuration >= tm.config.MinTurnDuration {
					events = append(events, TurnEventSpeechEnd)
				}
				// 无论是否达到最小时长，都回到 Idle
				tm.state = TurnIdle
				tm.speechFrames = 0
				tm.silenceFrames = 0
			}
		}

		// 超时检查
		if tm.state == TurnListening && now.Sub(tm.turnStart) >= tm.config.MaxTurnDuration {
			tm.state = TurnIdle
			tm.speechFrames = 0
			tm.silenceFrames = 0
			events = append(events, TurnEventTurnTimeout)
		}
	}

	return events
}

// TurnDuration 返回当前轮次的持续时间（如果在 Listening 状态）。
func (tm *TurnManager) TurnDuration() time.Duration {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.state == TurnListening && !tm.turnStart.IsZero() {
		return time.Since(tm.turnStart)
	}
	return 0
}

// IsListening 返回是否正在监听用户说话。
func (tm *TurnManager) IsListening() bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.state == TurnListening
}
