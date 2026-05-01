// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package synthesizer

import "strings"

func relayMergeIfAbsent(m map[string]interface{}, key string, val interface{}) {
	if val == nil || m == nil {
		return
	}
	if s, ok := val.(string); ok && strings.TrimSpace(s) == "" {
		return
	}
	if _, exists := m[key]; exists {
		return
	}
	m[key] = val
}

// ApplyRelayTTSFormatHint maps short audio_format to keys read by NewSynthesisServiceFromCredential for each provider string.
func ApplyRelayTTSFormatHint(provider, audioFormat string, m map[string]interface{}) {
	af := strings.ToLower(strings.TrimSpace(audioFormat))
	if af == "" || m == nil {
		return
	}
	prov := strings.ToLower(strings.TrimSpace(provider))
	switch prov {
	case "openai":
		relayMergeIfAbsent(m, "codec", af)
	case "qcloud", "tencent":
		relayMergeIfAbsent(m, "codec", af)
	case "volcengine":
		relayMergeIfAbsent(m, "encoding", af)
	case "minimax", "fishaudio":
		relayMergeIfAbsent(m, "format", af)
	case "fishspeech":
		relayMergeIfAbsent(m, "codec", af)
	case "azure":
		switch af {
		case "mp3":
			relayMergeIfAbsent(m, "codec", "audio-24khz-48kbitrate-mono-mp3")
		case "opus":
			relayMergeIfAbsent(m, "codec", "webm-16khz-16bit-mono-opus")
		case "pcm", "wav":
			relayMergeIfAbsent(m, "codec", "riff-16khz-16bit-mono-pcm")
		default:
			relayMergeIfAbsent(m, "codec", af)
		}
	case "local":
		relayMergeIfAbsent(m, "codec", af)
	case "local_gospeech":
		relayMergeIfAbsent(m, "codec", af)
	default:
		// qiniu, baidu, xunfei, google, aws, coqui, elevenlabs, unknown
		relayMergeIfAbsent(m, "format", af)
	}
}

// MergeRelayTTSRequestOptions merges tts_options, sample_rate, and format hint (later keys do not override existing).
func MergeRelayTTSRequestOptions(merged map[string]interface{}, provider string, audioFormat string, sampleRate int, ttsOptions map[string]interface{}) {
	if merged == nil {
		return
	}
	if ttsOptions != nil {
		for k, v := range ttsOptions {
			k = strings.TrimSpace(k)
			if k == "" || strings.EqualFold(k, "provider") {
				continue
			}
			merged[k] = v
		}
	}
	if sampleRate > 0 {
		sr := int64(sampleRate)
		relayMergeIfAbsent(merged, "sample_rate", sr)
		relayMergeIfAbsent(merged, "sampleRate", sr)
	}
	ApplyRelayTTSFormatHint(provider, audioFormat, merged)
}
