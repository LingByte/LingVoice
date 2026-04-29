// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"math"

	"github.com/LingByte/LingVoice/internal/config"
)

// speechBillableSeconds 计费秒数：墙钟（识别/合成耗时）与按字节估算的播放/输入时长取较大值，避免极短墙钟但长音频时少计。
func speechBillableSeconds(wallMs int64, payloadBytes int64, bytesPerSec int64) float64 {
	wall := float64(wallMs) / 1000.0
	if wall < 0 {
		wall = 0
	}
	if bytesPerSec <= 0 || payloadBytes <= 0 {
		return wall
	}
	est := float64(payloadBytes) / float64(bytesPerSec)
	if est > wall {
		return est
	}
	return wall
}

// speechOpenAPIQuotaDelta 仅成功调用计费；额度单位为整数，与 LLM OpenAPI 一致；含分组倍率（OPENAPI_QUOTA_GROUP_RATIOS）。
func speechOpenAPIQuotaDelta(
	success bool,
	group string,
	billableSec float64,
	unitsPerSec float64,
	minOnSuccess int,
) int {
	if !success {
		return 0
	}
	if minOnSuccess < 0 {
		minOnSuccess = 0
	}
	n := 0
	if unitsPerSec > 0 {
		n = int(math.Round(billableSec * unitsPerSec))
	} else {
		n = minOnSuccess
	}
	if n < minOnSuccess {
		n = minOnSuccess
	}
	gr := 1.0
	if config.GlobalConfig != nil {
		gr = config.GlobalConfig.OpenAPIQuotaGroupRatio(group)
	}
	adj := int(math.Round(float64(n) * gr))
	if adj < 1 && n > 0 {
		adj = 1
	}
	if adj < minOnSuccess {
		adj = minOnSuccess
	}
	return adj
}

func speechQuotaCfg() config.SpeechQuotaConfig {
	if config.GlobalConfig == nil {
		return config.SpeechQuotaConfig{
			ASRUnitsPerBillableSecond: 1,
			TTSUnitsPerBillableSecond: 1,
			ASRInputBytesPerSec:       32000,
			TTSOutputBytesPerSec:      16000,
			MinDeltaOnSuccess:         1,
		}
	}
	return config.GlobalConfig.Services.SpeechQuota
}
