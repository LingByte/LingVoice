// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// LLMUsageChannelAttempt 多渠道路由/重试时单次走向（失败后再换渠道成功时形成数组）。
type LLMUsageChannelAttempt struct {
	Order        int    `json:"order"` // 从 1 递增
	ChannelID    int    `json:"channel_id"`
	BaseURL      string `json:"base_url,omitempty"`
	Success      bool   `json:"success"`
	StatusCode   int    `json:"status_code,omitempty"`
	LatencyMs    int64  `json:"latency_ms,omitempty"` // 该次上游往返耗时
	TTFTMs       int64  `json:"ttft_ms,omitempty"`    // 非流式时常与 LatencyMs 同量级；流式场景可填首包
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// LLMUsageChannelAttempts JSON 列类型。
type LLMUsageChannelAttempts []LLMUsageChannelAttempt

func (a LLMUsageChannelAttempts) Value() (driver.Value, error) {
	if len(a) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(a)
}

func (a *LLMUsageChannelAttempts) Scan(value interface{}) error {
	if value == nil {
		*a = nil
		return nil
	}
	var raw []byte
	switch v := value.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("LLMUsageChannelAttempts: unsupported scan type %T", value)
	}
	if len(raw) == 0 || string(raw) == "null" {
		*a = nil
		return nil
	}
	var sl []LLMUsageChannelAttempt
	if err := json.Unmarshal(raw, &sl); err != nil {
		return err
	}
	*a = LLMUsageChannelAttempts(sl)
	return nil
}
