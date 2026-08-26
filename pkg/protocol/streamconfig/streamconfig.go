// Package streamconfig provides static stream push/pull configuration and pull-on-demand management.
//
// 功能：
//   - 静态推流配置：启动时自动从外部源拉流并注册
//   - 静态拉流配置：启动时自动向外部目标推流
//   - 按需拉流：有观众请求时才从源拉流，无观众时停止
//
// 配置文件格式 (YAML):
//
//	streams:
//	  - id: "camera-01"
//	    source: "rtsp://192.168.1.100/stream"
//	    protocol: "rtsp"
//	    pull_on_demand: true
//	    outputs:
//	      - protocol: "hls"
//	        path: "/hls/camera-01"
//	      - protocol: "webrtc"
//	        path: "/whep/camera-01"
//	  - id: "feed-external"
//	    source: "rtmp://external-server/live/feed"
//	    protocol: "rtmp"
//	    pull_on_demand: false
package streamconfig

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 流配置文件根
type Config struct {
	Streams []StreamConfig `yaml:"streams"`
}

// StreamConfig 单个流配置
type StreamConfig struct {
	ID            string         `yaml:"id"`
	Source        string         `yaml:"source"`         // 源 URL
	Protocol      string         `yaml:"protocol"`       // 源协议: rtsp/rtmp/srt/whip
	PullOnDemand  bool           `yaml:"pull_on_demand"` // 按需拉流
	AutoStart     bool           `yaml:"auto_start"`     // 启动时自动拉流
	Outputs       []OutputConfig `yaml:"outputs"`        // 输出配置
}

// OutputConfig 输出配置
type OutputConfig struct {
	Protocol string `yaml:"protocol"` // hls/flv/webrtc/rtmp/rtsp/srt
	Path     string `yaml:"path"`     // 输出路径
}

// LoadFromFile 从 YAML 文件加载配置
func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	return &cfg, nil
}

// ============================================================================
// PullOnDemandManager — 按需拉流管理器
// ============================================================================

// PullStarter 拉流启动函数（由协议层提供）
type PullStarter func(ctx context.Context, streamID, sourceURL, protocol string) error

// PullStopper 停止拉流函数
type PullStopper func(streamID string) error

// PullOnDemandManager 按需拉流管理器
//
// 当第一个观众请求流时启动拉流，当所有观众离开时停止拉流。
type PullOnDemandManager struct {
	mu         sync.Mutex
	streams    map[string]*pullState
	starters   map[string]PullStarter // protocol → starter
	stoppers   map[string]PullStopper // protocol → stopper
	stopTimers map[string]*time.Timer  // streamID → 延迟停止 timer
	stopDelay  time.Duration
}

type pullState struct {
	streamID   string
	source     string
	protocol   string
	viewers    int
	active     bool
	cancel     context.CancelFunc
}

// NewPullOnDemandManager 创建按需拉流管理器
func NewPullOnDemandManager() *PullOnDemandManager {
	return &PullOnDemandManager{
		streams:    make(map[string]*pullState),
		starters:   make(map[string]PullStarter),
		stoppers:   make(map[string]PullStopper),
		stopTimers: make(map[string]*time.Timer),
		stopDelay:  30 * time.Second, // 无观众后 30s 停止
	}
}

// RegisterPullStarter 注册拉流启动器
func (m *PullOnDemandManager) RegisterPullStarter(protocol string, starter PullStarter, stopper PullStopper) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.starters[protocol] = starter
	m.stoppers[protocol] = stopper
}

// RegisterStream 注册一个可按需拉流的流
func (m *PullOnDemandManager) RegisterStream(streamID, source, protocol string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streams[streamID] = &pullState{
		streamID: streamID,
		source:   source,
		protocol: protocol,
	}
}

// AddViewer 观众请求流（触发拉流如果未活跃）
func (m *PullOnDemandManager) AddViewer(streamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.streams[streamID]
	if !ok {
		return fmt.Errorf("stream %s not registered", streamID)
	}

	// 取消延迟停止 timer
	if timer, ok := m.stopTimers[streamID]; ok {
		timer.Stop()
		delete(m.stopTimers, streamID)
	}

	state.viewers++

	// 如果未活跃，启动拉流
	if !state.active {
		starter, ok := m.starters[state.protocol]
		if !ok {
			return fmt.Errorf("no pull starter for protocol %s", state.protocol)
		}

		ctx, cancel := context.WithCancel(context.Background())
		state.cancel = cancel
		state.active = true

		go func() {
			if err := starter(ctx, streamID, state.source, state.protocol); err != nil {
				m.mu.Lock()
				state.active = false
				m.mu.Unlock()
			}
		}()
	}

	return nil
}

// RemoveViewer 观众离开（可能触发延迟停止）
func (m *PullOnDemandManager) RemoveViewer(streamID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.streams[streamID]
	if !ok {
		return
	}

	state.viewers--
	if state.viewers < 0 {
		state.viewers = 0
	}

	// 无观众，延迟停止
	if state.viewers == 0 && state.active {
		timer := time.AfterFunc(m.stopDelay, func() {
			m.stopStream(streamID)
		})
		m.stopTimers[streamID] = timer
	}
}

// stopStream 停止拉流（内部，已持锁或 timer 回调）
func (m *PullOnDemandManager) stopStream(streamID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.streams[streamID]
	if !ok || !state.active {
		return
	}

	if state.cancel != nil {
		state.cancel()
		state.cancel = nil
	}

	if stopper, ok := m.stoppers[state.protocol]; ok {
		_ = stopper(streamID)
	}

	state.active = false
	delete(m.stopTimers, streamID)
}

// IsActive 检查流是否活跃
func (m *PullOnDemandManager) IsActive(streamID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.streams[streamID]
	return ok && state.active
}

// ViewerCount 获取观众数
func (m *PullOnDemandManager) ViewerCount(streamID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.streams[streamID]
	if !ok {
		return 0
	}
	return state.viewers
}

// ActiveStreams 列出活跃流
func (m *PullOnDemandManager) ActiveStreams() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []string
	for id, state := range m.streams {
		if state.active {
			list = append(list, id)
		}
	}
	return list
}

// StartAutoStart 启动所有 auto_start=true 的流
func (m *PullOnDemandManager) StartAutoStart(cfg *Config) {
	for _, sc := range cfg.Streams {
		if sc.AutoStart {
			m.RegisterStream(sc.ID, sc.Source, sc.Protocol)
			_ = m.AddViewer(sc.ID)
		} else if sc.PullOnDemand {
			m.RegisterStream(sc.ID, sc.Source, sc.Protocol)
		}
	}
}

// StopAll 停止所有拉流
func (m *PullOnDemandManager) StopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.streams))
	for id := range m.streams {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	for _, id := range ids {
		m.stopStream(id)
	}
}
