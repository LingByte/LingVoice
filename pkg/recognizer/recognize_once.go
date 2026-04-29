// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package recognizer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tencentcloud/tencentcloud-speech-sdk-go/asr"
)

const (
	// openAPIASRChunkPCM 约 200ms@16kHz mono int16，与流式场景单包量级接近。
	openAPIASRChunkPCM = 6400
	// openAPIASRChunkEncoded 压缩格式按字节分块，配合 qcloudOpenAPISendPace 控制发送节奏。
	openAPIASRChunkEncoded = 4096
	openAPIASRWait         = 120 * time.Second
)

// qcloudOpenAPISendPace 返回发送 n 字节音频后应等待的墙钟时间。
// 腾讯云实时识别限制约为「1 秒内至多 3 秒音频」；按实时或略慢于实时发送可避免 error 4000。
func qcloudOpenAPISendPace(format int, modelType string, n int) time.Duration {
	if n <= 0 {
		return 0
	}
	mt := strings.ToLower(modelType)
	pcmBytesPerSec := 32000.0 // 16kHz mono int16
	if strings.Contains(mt, "8k") {
		pcmBytesPerSec = 16000.0
	}
	var audioSec float64
	switch format {
	case asr.AudioFormatPCM:
		audioSec = float64(n) / pcmBytesPerSec
	default:
		// 压缩流无法在未解码时得到精确 PCM 时长：按偏低码率估算，使墙钟略宽裕于实时。
		const assumedFileBps = 8000.0 // ≈64 kbps 量级，偏保守
		audioSec = float64(n) / assumedFileBps
	}
	out := time.Duration(audioSec * float64(time.Second))
	if out < 8*time.Millisecond {
		out = 8 * time.Millisecond
	}
	return out
}

// RecognizeOpenAPIOnce runs one-shot recognition for HTTP OpenAPI callers.
// Currently only Tencent Cloud realtime ASR (provider qcloud / tencent) is supported.
func RecognizeOpenAPIOnce(ctx context.Context, provider string, merged map[string]interface{}, audio []byte, language string) (text string, err error) {
	if len(audio) == 0 {
		return "", fmt.Errorf("audio is empty")
	}
	prov := strings.ToLower(strings.TrimSpace(provider))
	tc, err := NewTranscriberConfigFromMap(prov, merged, language)
	if err != nil {
		return "", err
	}
	if tc.GetVendor() != VendorQCloud {
		return "", fmt.Errorf("OpenAPI 单次 ASR 当前仅支持腾讯云（qcloud/tencent），渠道 provider 为 %q", provider)
	}

	factory := NewTranscriberFactory()
	svc, err := factory.CreateTranscriber(tc)
	if err != nil {
		return "", err
	}
	defer func() { _ = svc.StopConn() }()

	done := make(chan string, 1)
	errFatal := make(chan error, 1)
	var lastMu sync.Mutex
	var lastPartial string

	svc.Init(func(t string, isLast bool, _ time.Duration, _ string) {
		lastMu.Lock()
		if strings.TrimSpace(t) != "" {
			lastPartial = t
		}
		lastMu.Unlock()
		if isLast {
			select {
			case done <- strings.TrimSpace(t):
			default:
			}
		}
	}, func(e error, fatal bool) {
		if fatal && e != nil {
			select {
			case errFatal <- e:
			default:
			}
		}
	})

	if err := svc.ConnAndReceive("openapi"); err != nil {
		return "", err
	}

	format := asr.AudioFormatPCM
	modelType := "16k_zh"
	if qo, ok := tc.(*QCloudASROption); ok {
		format = qo.Format
		modelType = qo.ModelType
	}

	chunk := openAPIASRChunkPCM
	if format != asr.AudioFormatPCM {
		chunk = openAPIASRChunkEncoded
	}

	for i := 0; i < len(audio); i += chunk {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		end := i + chunk
		if end > len(audio) {
			end = len(audio)
		}
		sent := end - i
		if err := svc.SendAudioBytes(audio[i:end]); err != nil {
			return "", err
		}
		if end < len(audio) {
			delay := qcloudOpenAPISendPace(format, modelType, sent)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}
	}

	if err := svc.SendEnd(); err != nil {
		s := err.Error()
		if strings.Contains(s, "recognizer is not running") || strings.Contains(s, "会话未建立") {
			return "", fmt.Errorf("腾讯云 ASR 会话未就绪或已关闭（请核对渠道 modelType 与控制台「实时语音识别」是否开通、appId/secret 是否匹配）: %s", s)
		}
		return "", err
	}

	waitCtx, cancel := context.WithTimeout(ctx, openAPIASRWait)
	defer cancel()

	select {
	case t := <-done:
		if t != "" {
			return t, nil
		}
		lastMu.Lock()
		lp := strings.TrimSpace(lastPartial)
		lastMu.Unlock()
		if lp != "" {
			return lp, nil
		}
		return "", nil
	case e := <-errFatal:
		if e != nil {
			return "", e
		}
		return "", fmt.Errorf("ASR failed")
	case <-waitCtx.Done():
		return "", fmt.Errorf("ASR 等待结果超时或已取消: %w", waitCtx.Err())
	}
}
