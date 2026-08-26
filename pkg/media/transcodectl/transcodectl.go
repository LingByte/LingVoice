// Package transcodectl provides an in-memory transcode configuration store.
//
// 用于 REST API 的转码控制：
//
//	POST /api/transcode/{sessionId}  {srcCodec, dstCodec} → 设置转码规则
//	GET  /api/transcode/{sessionId}  → 查询转码配置
//
// 实际转码在 Rust 媒体节点执行，这里只存储配置，
// 由 push_rtp / bridge_sessions 调用时读取。
package transcodectl

import (
	"context"
	"sync"
)

// Controller 转码配置存储（实现 api.TranscodeController）
type Controller struct {
	mu      sync.RWMutex
	configs map[string]TranscodeConfig // sessionID → config
}

// TranscodeConfig 转码配置
type TranscodeConfig struct {
	SrcCodec string `json:"srcCodec"`
	DstCodec string `json:"dstCodec"`
}

// New 创建转码控制器
func New() *Controller {
	return &Controller{
		configs: make(map[string]TranscodeConfig),
	}
}

// SetTranscodeConfig 设置会话的转码配置
func (c *Controller) SetTranscodeConfig(_ context.Context, sessionID, srcCodec, dstCodec string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.configs[sessionID] = TranscodeConfig{
		SrcCodec: srcCodec,
		DstCodec: dstCodec,
	}
	return nil
}

// GetTranscodeConfig 查询会话的转码配置
func (c *Controller) GetTranscodeConfig(_ context.Context, sessionID string) (map[string]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cfg, ok := c.configs[sessionID]
	if !ok {
		return map[string]string{}, nil
	}
	return map[string]string{
		"srcCodec": cfg.SrcCodec,
		"dstCodec": cfg.DstCodec,
	}, nil
}

// GetConfig 获取转码配置（内部使用，不加 context）
func (c *Controller) GetConfig(sessionID string) (TranscodeConfig, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cfg, ok := c.configs[sessionID]
	return cfg, ok
}

// RemoveConfig 移除转码配置
func (c *Controller) RemoveConfig(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.configs, sessionID)
}
