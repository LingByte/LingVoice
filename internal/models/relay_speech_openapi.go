// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// MaxRelayAudioFetchBytes limits inbound audio for relay ASR (multipart / URL / base64).
const MaxRelayAudioFetchBytes = 32 << 20 // 32 MiB

// SpeechASRTranscribeReq is the JSON / form body for POST /v1/speech/asr/transcribe.
type SpeechASRTranscribeReq struct {
	Group       string `json:"group"`
	AudioBase64 string `json:"audio_base64"`
	AudioURL    string `json:"audio_url"`
	Format      string `json:"format"`
	Language    string `json:"language"`
	Extra       any    `json:"extra"`
}

// SpeechTTSSynthesizeReq is the JSON body for POST /v1/speech/tts/synthesize.
type SpeechTTSSynthesizeReq struct {
	Group          string `json:"group"`
	Text           string `json:"text" binding:"required"`
	Voice          string `json:"voice"`
	Extra          any    `json:"extra"`
	ResponseType   string `json:"response_type"`
	Output         string `json:"output"`
	UploadBucket   string `json:"upload_bucket"`
	UploadKey      string `json:"upload_key"`
	UploadFilename string `json:"upload_filename"`
	AudioFormat    string `json:"audio_format"`
	SampleRate     int    `json:"sample_rate"`
	TTSOptions     map[string]interface{} `json:"tts_options"`
}

// MergeASRTranscribeOptions merges request extra/format into merged provider map (caller supplies base map).
func MergeASRTranscribeOptions(merged map[string]interface{}, body *SpeechASRTranscribeReq) {
	if body == nil || merged == nil {
		return
	}
	if body.Extra != nil {
		if m, ok := body.Extra.(map[string]interface{}); ok {
			for k, v := range m {
				k = strings.TrimSpace(k)
				if k == "" || strings.EqualFold(k, "provider") {
					continue
				}
				merged[k] = v
			}
		}
	}
	delete(merged, "model")
	f := strings.TrimSpace(body.Format)
	if f != "" {
		if _, ok := merged["format"]; !ok {
			if _, ok2 := merged["voiceFormat"]; !ok2 {
				merged["format"] = f
			}
		}
	}
}

// NormalizeRelayTTSResponseType returns audio_base64 | url from response_type / output aliases.
func NormalizeRelayTTSResponseType(responseType, output string) string {
	s := strings.TrimSpace(responseType)
	if s == "" {
		s = strings.TrimSpace(output)
	}
	switch strings.ToLower(s) {
	case "url", "audio_url":
		return "url"
	case "audio_base64", "base64", "audio_data", "data", "":
		return "audio_base64"
	default:
		return "audio_base64"
	}
}

// PickASRChannel selects first enabled ASR channel for group (or credential group when override empty).
func PickASRChannel(db *gorm.DB, cred *Credential, groupOverride string) (*ASRChannel, error) {
	if db == nil {
		return nil, errors.New("db nil")
	}
	g := strings.TrimSpace(groupOverride)
	if g == "" && cred != nil {
		g = strings.TrimSpace(cred.Group)
	}
	q := db.Model(&ASRChannel{}).Where("enabled = ?", true)
	if g != "" {
		q = q.Where("`group` = ?", g)
	}
	var ch ASRChannel
	if err := q.Order("sort_order ASC, id ASC").First(&ch).Error; err != nil {
		return nil, err
	}
	return &ch, nil
}

// PickTTSChannel selects first enabled TTS channel for group (or credential group when override empty).
func PickTTSChannel(db *gorm.DB, cred *Credential, groupOverride string) (*TTSChannel, error) {
	if db == nil {
		return nil, errors.New("db nil")
	}
	g := strings.TrimSpace(groupOverride)
	if g == "" && cred != nil {
		g = strings.TrimSpace(cred.Group)
	}
	q := db.Model(&TTSChannel{}).Where("enabled = ?", true)
	if g != "" {
		q = q.Where("`group` = ?", g)
	}
	var ch TTSChannel
	if err := q.Order("sort_order ASC, id ASC").First(&ch).Error; err != nil {
		return nil, err
	}
	return &ch, nil
}

// RelayNoSpeechChannelDetail formats pick-channel errors for API responses.
func RelayNoSpeechChannelDetail(err error, kind, group string) string {
	if err == nil {
		return ""
	}
	g := strings.TrimSpace(group)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if g != "" {
			return fmt.Sprintf("未找到可用 %s 渠道（group=%s）", kind, g)
		}
		return fmt.Sprintf("未找到可用 %s 渠道", kind)
	}
	if g != "" {
		return fmt.Sprintf("选择 %s 渠道失败（group=%s）：%s", kind, g, err.Error())
	}
	return fmt.Sprintf("选择 %s 渠道失败：%s", kind, err.Error())
}

// ApplyTTSVoiceToMergedMap maps HTTP "voice" to provider-specific keys expected by pkg/synthesizer.
func ApplyTTSVoiceToMergedMap(provider, voice string, merged map[string]interface{}) {
	if merged == nil || strings.TrimSpace(voice) == "" {
		return
	}
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
	case "azure":
		merged["voice"] = voice
	case "qcloud", "tencent":
		merged["voiceType"] = voice
	case "minimax":
		merged["voiceId"] = voice
	case "elevenlabs":
		merged["voiceId"] = voice
	default:
		merged["voice"] = voice
	}
}
