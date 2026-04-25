// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

import "time"

// OpenAPI 语音用量种类（与 speech_usage.kind 一致）。
const (
	SpeechUsageKindASR = "asr"
	SpeechUsageKindTTS = "tts"
)

// SpeechUsage 记录 OpenAPI 语音（ASR/TTS）单次调用，便于审计与统计；不落原始音频 base64。
type SpeechUsage struct {
	ID               string    `json:"id" gorm:"primaryKey;type:varchar(64)"`
	RequestID        string    `json:"request_id" gorm:"type:varchar(64);uniqueIndex;not null"`
	CredentialID     int       `json:"credential_id" gorm:"index"`
	UserID           string    `json:"user_id" gorm:"type:varchar(64);index"`
	Kind             string    `json:"kind" gorm:"type:varchar(16);not null;index"`
	Provider         string    `json:"provider" gorm:"type:varchar(64);index"`
	ChannelID        int       `json:"channel_id" gorm:"index"`
	Group            string    `json:"group" gorm:"type:varchar(128)"`
	RequestType      string    `json:"request_type" gorm:"type:varchar(64);not null;index"`
	RequestContent   string    `json:"request_content" gorm:"type:text"`
	ResponseContent  string    `json:"response_content" gorm:"type:text"`
	LatencyMs        int64     `json:"latency_ms"`
	StatusCode       int       `json:"status_code"`
	Success          bool      `json:"success"`
	ErrorMessage     string    `json:"error_message" gorm:"type:text"`
	AudioInputBytes  int64     `json:"audio_input_bytes"`
	AudioOutputBytes int64     `json:"audio_output_bytes"`
	TextInputChars   int       `json:"text_input_chars"`
	UserAgent        string    `json:"user_agent" gorm:"type:varchar(500)"`
	IPAddress        string    `json:"ip_address" gorm:"type:varchar(45)"`
	RequestedAt      time.Time `json:"requested_at" gorm:"not null;index"`
	CompletedAt      time.Time `json:"completed_at" gorm:"index"`
	CreatedAt        time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt        time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (SpeechUsage) TableName() string {
	return "speech_usage"
}
