// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LingByte/LingVoice/internal/config"
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/middleware"
	"github.com/LingByte/LingVoice/pkg/recognizer"
	"github.com/LingByte/LingVoice/pkg/synthesizer"
	"github.com/LingByte/LingVoice/pkg/utils/response"
	"github.com/LingByte/LingVoice/pkg/utils/system"
	"github.com/LingByte/lingstorage-sdk-go"
	"github.com/gin-gonic/gin"
)

// Relay v1 speech: ASR 走 pkg/recognizer（与渠道 provider 能力一致），TTS 走 pkg/synthesizer。

// openAPIAudioURLProtection 拉取用户音频 URL：黑名单域名模式 + 禁止私网 IP（含解析结果）。
func openAPIAudioURLProtection() *system.SSRFProtection {
	return &system.SSRFProtection{
		AllowPrivateIp:         false,
		DomainFilterMode:       false,
		DomainList:             nil,
		IpFilterMode:           false,
		IpList:                 nil,
		AllowedPorts:           nil,
		ApplyIPFilterForDomain: true,
	}
}

func fetchAudioBytesFromURL(ctx context.Context, rawURL string) ([]byte, error) {
	if err := openAPIAudioURLProtection().ValidateURL(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body := io.LimitReader(resp.Body, models.MaxRelayAudioFetchBytes+1)
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	if len(data) > models.MaxRelayAudioFetchBytes {
		return nil, fmt.Errorf("audio 超过大小限制 %d 字节", models.MaxRelayAudioFetchBytes)
	}
	return data, nil
}

func bindAndLoadOpenAPIASRAudio(c *gin.Context) (models.SpeechASRTranscribeReq, []byte, string, error) {
	ct := strings.ToLower(strings.TrimSpace(c.ContentType()))
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := c.Request.ParseMultipartForm(int64(models.MaxRelayAudioFetchBytes)); err != nil {
			return models.SpeechASRTranscribeReq{}, nil, "", fmt.Errorf("解析 multipart 失败: %w", err)
		}
		var body models.SpeechASRTranscribeReq
		body.Group = strings.TrimSpace(c.PostForm("group"))
		body.Format = strings.TrimSpace(c.PostForm("format"))
		body.Language = strings.TrimSpace(c.PostForm("language"))
		if ex := strings.TrimSpace(c.PostForm("extra")); ex != "" {
			var raw any
			if err := json.Unmarshal([]byte(ex), &raw); err == nil {
				body.Extra = raw
			}
		}
		file, ferr := c.FormFile("audio")
		url := strings.TrimSpace(c.PostForm("audio_url"))
		b64 := strings.TrimSpace(c.PostForm("audio_base64"))
		n := 0
		if ferr == nil && file != nil {
			n++
		}
		if url != "" {
			n++
		}
		if b64 != "" {
			n++
		}
		if n > 1 {
			return body, nil, "", fmt.Errorf("音频来源请只选一种：表单文件 audio、audio_url 或 audio_base64")
		}
		if ferr == nil && file != nil {
			f, err := file.Open()
			if err != nil {
				return body, nil, "", err
			}
			defer f.Close()
			data, err := io.ReadAll(io.LimitReader(f, models.MaxRelayAudioFetchBytes+1))
			if err != nil {
				return body, nil, "", err
			}
			if len(data) > models.MaxRelayAudioFetchBytes {
				return body, nil, "", fmt.Errorf("audio 超过大小限制 %d 字节", models.MaxRelayAudioFetchBytes)
			}
			return body, data, "upload", nil
		}
		if url != "" {
			body.AudioURL = url
			data, err := fetchAudioBytesFromURL(c.Request.Context(), url)
			if err != nil {
				return body, nil, "url", err
			}
			return body, data, "url", nil
		}
		if b64 != "" {
			raw, decErr := base64.StdEncoding.DecodeString(b64)
			if decErr != nil {
				return body, nil, "", fmt.Errorf("audio_base64 解码失败: %w", decErr)
			}
			if len(raw) > models.MaxRelayAudioFetchBytes {
				return body, nil, "", fmt.Errorf("audio 超过大小限制 %d 字节", models.MaxRelayAudioFetchBytes)
			}
			body.AudioBase64 = b64
			return body, raw, "base64", nil
		}
		return body, nil, "", fmt.Errorf("请上传表单文件字段 audio，或提供 audio_url / audio_base64")
	}

	var body models.SpeechASRTranscribeReq
	if err := c.ShouldBindJSON(&body); err != nil {
		return body, nil, "", err
	}
	src, err := resolveASRAudioInput(&body)
	if err != nil {
		return body, nil, "", err
	}
	var audio []byte
	if src == "base64" {
		raw, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(body.AudioBase64))
		if decErr != nil {
			return body, nil, "", fmt.Errorf("audio_base64 解码失败: %w", decErr)
		}
		if len(raw) > models.MaxRelayAudioFetchBytes {
			return body, nil, "", fmt.Errorf("audio 超过大小限制 %d 字节", models.MaxRelayAudioFetchBytes)
		}
		audio = raw
	} else {
		data, ferr := fetchAudioBytesFromURL(c.Request.Context(), strings.TrimSpace(body.AudioURL))
		if ferr != nil {
			return body, nil, src, ferr
		}
		audio = data
	}
	return body, audio, src, nil
}

func resolveASRAudioInput(body *models.SpeechASRTranscribeReq) (source string, err error) {
	b64 := strings.TrimSpace(body.AudioBase64)
	u := strings.TrimSpace(body.AudioURL)
	if b64 != "" && u != "" {
		return "", fmt.Errorf("audio_base64 与 audio_url 请二选一")
	}
	if b64 == "" && u == "" {
		return "", fmt.Errorf("请提供 audio_base64 或 audio_url 之一")
	}
	if b64 != "" {
		raw, decErr := base64.StdEncoding.DecodeString(b64)
		if decErr != nil {
			return "", fmt.Errorf("audio_base64 解码失败: %w", decErr)
		}
		if len(raw) > models.MaxRelayAudioFetchBytes {
			return "", fmt.Errorf("audio 超过大小限制 %d 字节", models.MaxRelayAudioFetchBytes)
		}
		return "base64", nil
	}
	if err := openAPIAudioURLProtection().ValidateURL(u); err != nil {
		return "", err
	}
	return "url", nil
}

// openAPIASRTranscribeHandler POST /v1/speech/asr/transcribe
func (h *Handlers) openAPIASRTranscribeHandler(c *gin.Context) {
	cred, ok := middleware.OpenAPISpeechCredentialFromContext(c)
	if !ok || cred == nil {
		response.FailWithCode(c, 401, "未授权", nil)
		return
	}
	started := time.Now()
	body, audio, src, err := bindAndLoadOpenAPIASRAudio(c)
	if err != nil {
		h.recordOpenAPIASRUsage(c, started, cred, nil, 400, false, err.Error(), nilIfASRBodyEmpty(body), gin.H{"error": err.Error()}, 0, "")
		response.FailWithCode(c, 400, err.Error(), gin.H{"error": err.Error()})
		return
	}
	audioLen := len(audio)
	if audioLen == 0 {
		msg := "音频内容为空"
		h.recordOpenAPIASRUsage(c, started, cred, nil, 400, false, msg, &body, gin.H{"error": msg}, 0, src)
		response.FailWithCode(c, 400, msg, nil)
		return
	}
	ch, err := models.PickASRChannel(h.db, cred, body.Group)
	if err != nil {
		errDetail := models.RelayNoSpeechChannelDetail(err, "ASR", body.Group)
		h.recordOpenAPIASRUsage(c, started, cred, nil, 503, false, errDetail, &body, gin.H{"error": errDetail}, int64(audioLen), src)
		response.FailWithCode(c, 503, "无可用 ASR 渠道", gin.H{"error": errDetail})
		return
	}
	merged := map[string]interface{}{
		"provider": strings.ToLower(strings.TrimSpace(ch.Provider)),
	}
	if strings.TrimSpace(ch.ConfigJSON) != "" {
		var extra map[string]interface{}
		if err := json.Unmarshal([]byte(ch.ConfigJSON), &extra); err != nil {
			h.recordOpenAPIASRUsage(c, started, cred, ch, 500, false, err.Error(), &body, gin.H{"error": err.Error()}, int64(audioLen), src)
			response.FailWithCode(c, 500, "渠道 configJson 无效", gin.H{"error": err.Error()})
			return
		}
		for k, v := range extra {
			merged[k] = v
		}
	}
	models.MergeASRTranscribeOptions(merged, &body)

	lang := strings.TrimSpace(body.Language)
	text, recErr := recognizer.RecognizeOpenAPIOnce(c.Request.Context(), ch.Provider, merged, audio, lang)
	if recErr != nil {
		errMsg := recErr.Error()
		h.recordOpenAPIASRUsage(c, started, cred, ch, 502, false, errMsg, &body, gin.H{"error": errMsg}, int64(audioLen), src)
		publicMsg := strings.TrimSpace(errMsg)
		if publicMsg == "" {
			publicMsg = "语音识别失败"
		}
		response.FailWithCode(c, 502, publicMsg, gin.H{"error": errMsg})
		return
	}

	out := gin.H{
		"text":         text,
		"segments":     []any{},
		"provider":     ch.Provider,
		"channel_id":   ch.ID,
		"group":        ch.Group,
		"audio_source": src,
		"audio_bytes":  audioLen,
	}
	if strings.TrimSpace(text) == "" {
		out["message"] = "未识别到有效文本（可检查音频格式与渠道 configJson 中的 model/format 是否与内容一致）"
	} else {
		out["message"] = ""
	}
	h.recordOpenAPIASRUsage(c, started, cred, ch, 200, true, "", &body, out, int64(audioLen), src)
	response.Success(c, "ok", out)
}

func nilIfASRBodyEmpty(body models.SpeechASRTranscribeReq) *models.SpeechASRTranscribeReq {
	if strings.TrimSpace(body.Group) == "" && strings.TrimSpace(body.Format) == "" && strings.TrimSpace(body.Language) == "" &&
		strings.TrimSpace(body.AudioURL) == "" && strings.TrimSpace(body.AudioBase64) == "" && body.Extra == nil {
		return nil
	}
	return &body
}

type ttsBufferHandler struct {
	buf []byte
}

func (t *ttsBufferHandler) OnMessage(data []byte) {
	t.buf = append(t.buf, data...)
}

func (t *ttsBufferHandler) OnTimestamp(timestamp synthesizer.SentenceTimestamp) {}

// openAPITTSSynthesizeHandler POST /v1/speech/tts/synthesize
func (h *Handlers) openAPITTSSynthesizeHandler(c *gin.Context) {
	cred, ok := middleware.OpenAPISpeechCredentialFromContext(c)
	if !ok || cred == nil {
		response.FailWithCode(c, 401, "未授权", nil)
		return
	}
	// Defense-in-depth: middleware 已校验一次，这里再做一次数据库级别校验，避免并发/脏上下文放行。
	var freshCred models.Credential
	if err := h.db.Where("id = ? AND status = ? AND kind = ?", cred.Id, 1, models.CredentialKindTTS).First(&freshCred).Error; err != nil {
		response.FailWithCode(c, 401, "无效或已禁用的 TTS 凭证", nil)
		return
	}
	if !models.CredentialHasRemainingQuota(&freshCred) {
		response.FailWithCode(c, 403, "该 TTS 凭证剩余额度不足", gin.H{"reason": models.OpenAPIQuotaReasonCredentialExhausted})
		return
	}
	if freshCred.UserId > 0 {
		okUser, err := models.UserHasSpendableQuota(h.db, uint(freshCred.UserId))
		if err != nil {
			response.FailWithCode(c, 500, "校验用户额度失败", gin.H{"error": err.Error()})
			return
		}
		if !okUser {
			response.FailWithCode(c, 403, "所属用户账户剩余额度不足", gin.H{"reason": models.OpenAPIQuotaReasonUserExhausted})
			return
		}
	}
	cred = &freshCred
	started := time.Now()
	var body models.SpeechTTSSynthesizeReq
	if err := c.ShouldBindJSON(&body); err != nil {
		h.recordOpenAPITTSUsage(c, started, cred, nil, 400, false, err.Error(), nil, nil, gin.H{"error": err.Error()}, 0, 0)
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		h.recordOpenAPITTSUsage(c, started, cred, nil, 400, false, "text 不能为空", &body, nil, gin.H{"error": "text 不能为空"}, 0, 0)
		response.FailWithCode(c, 400, "text 不能为空", nil)
		return
	}
	textChars := utf8.RuneCountInString(text)
	outMode := models.NormalizeRelayTTSResponseType(body.ResponseType, body.Output)
	ch, err := models.PickTTSChannel(h.db, cred, body.Group)
	if err != nil {
		errDetail := models.RelayNoSpeechChannelDetail(err, "TTS", body.Group)
		h.recordOpenAPITTSUsage(c, started, cred, nil, 503, false, errDetail, &body, nil, gin.H{"error": errDetail}, 0, textChars)
		response.FailWithCode(c, 503, "无可用 TTS 渠道", gin.H{"error": errDetail})
		return
	}
	merged := map[string]interface{}{
		"provider": strings.ToLower(strings.TrimSpace(ch.Provider)),
	}
	if strings.TrimSpace(ch.ConfigJSON) != "" {
		var extra map[string]interface{}
		if err := json.Unmarshal([]byte(ch.ConfigJSON), &extra); err != nil {
			h.recordOpenAPITTSUsage(c, started, cred, ch, 500, false, err.Error(), &body, merged, gin.H{"error": err.Error()}, 0, textChars)
			response.FailWithCode(c, 500, "渠道 configJson 无效", gin.H{"error": err.Error()})
			return
		}
		for k, v := range extra {
			merged[k] = v
		}
	}
	voice := strings.TrimSpace(body.Voice)
	if voice != "" {
		models.ApplyTTSVoiceToMergedMap(ch.Provider, voice, merged)
	}
	synthesizer.MergeRelayTTSRequestOptions(merged, strings.ToLower(strings.TrimSpace(ch.Provider)), body.AudioFormat, body.SampleRate, body.TTSOptions)

	svc, err := synthesizer.NewSynthesisServiceFromCredential(synthesizer.TTSCredentialConfig(merged))
	if err != nil {
		h.recordOpenAPITTSUsage(c, started, cred, ch, 400, false, err.Error(), &body, merged, gin.H{"error": err.Error()}, 0, textChars)
		response.FailWithCode(c, 400, "TTS 配置无法构建服务", gin.H{"error": err.Error()})
		return
	}
	handler := &ttsBufferHandler{}
	ctx := context.Background()
	if err := svc.Synthesize(ctx, handler, text); err != nil {
		_ = svc.Close()
		h.recordOpenAPITTSUsage(c, started, cred, ch, 502, false, err.Error(), &body, merged, gin.H{"error": err.Error()}, 0, textChars)
		response.FailWithCode(c, 502, "合成失败", gin.H{"error": err.Error()})
		return
	}
	_ = svc.Close()
	if len(handler.buf) == 0 {
		h.recordOpenAPITTSUsage(c, started, cred, ch, 502, false, "未收到音频数据", &body, merged, nil, 0, textChars)
		response.FailWithCode(c, 502, "未收到音频数据", nil)
		return
	}
	audioOut := int64(len(handler.buf))

	base := gin.H{
		"response_type": outMode,
		"format":        svc.Format(),
		"provider":      ch.Provider,
		"channel_id":    ch.ID,
		"group":         ch.Group,
	}
	for _, k := range []string{"codec", "format", "encoding", "response_format"} {
		if v, ok := merged[k]; ok && v != nil && fmt.Sprint(v) != "" {
			base[k] = v
		}
	}

	if outMode == "url" {
		if config.GlobalStore == nil {
			h.recordOpenAPITTSUsage(c, started, cred, ch, 503, false, "对象存储未初始化", &body, merged, gin.H{"response_type": outMode}, audioOut, textChars)
			response.FailWithCode(c, 503, "对象存储未初始化（LINGSTORAGE_*）", nil)
			return
		}
		bucket := strings.TrimSpace(body.UploadBucket)
		if bucket == "" {
			bucket = config.GlobalConfig.Services.Storage.Bucket
		}
		if bucket == "" {
			h.recordOpenAPITTSUsage(c, started, cred, ch, 503, false, "未配置存储 bucket", &body, merged, gin.H{"response_type": outMode}, audioOut, textChars)
			response.FailWithCode(c, 503, "未配置存储 bucket（LINGSTORAGE_BUCKET）", nil)
			return
		}
		fname := strings.TrimSpace(body.UploadFilename)
		if fname == "" {
			fname = fmt.Sprintf("tts-%d.bin", time.Now().UnixNano())
		}
		fname = filepath.Base(fname)
		if fname == "." || fname == "" {
			fname = fmt.Sprintf("tts-%d.bin", time.Now().UnixNano())
		}
		key := strings.TrimSpace(body.UploadKey)
		if key == "" {
			key = fmt.Sprintf("openapi/tts/%s/%d-%s", strings.TrimPrefix(ch.Group, "/"), time.Now().UnixNano(), fname)
		}
		up, upErr := config.GlobalStore.UploadBytes(&lingstorage.UploadBytesRequest{
			Data:     handler.buf,
			Filename: fname,
			Bucket:   bucket,
			Key:      key,
		})
		if upErr != nil {
			h.recordOpenAPITTSUsage(c, started, cred, ch, 502, false, upErr.Error(), &body, merged, gin.H{"response_type": outMode, "error": upErr.Error()}, audioOut, textChars)
			response.FailWithCode(c, 502, "上传存储失败", gin.H{"error": upErr.Error()})
			return
		}
		base["url"] = up.URL
		base["key"] = up.Key
		base["bucket"] = up.Bucket
		base["filename"] = up.Filename
		base["size"] = up.Size
		respSnap := gin.H{
			"response_type": outMode,
			"format":        base["format"],
			"provider":      base["provider"],
			"channel_id":    base["channel_id"],
			"group":         base["group"],
			"size":          up.Size,
			"key":           up.Key,
			"bucket":        up.Bucket,
			"filename":      up.Filename,
		}
		h.recordOpenAPITTSUsage(c, started, cred, ch, 200, true, "", &body, merged, respSnap, audioOut, textChars)
		response.Success(c, "ok", base)
		return
	}

	b64 := base64.StdEncoding.EncodeToString(handler.buf)
	base["audio_base64"] = b64
	respSnap := gin.H{
		"response_type":            outMode,
		"format":                   base["format"],
		"provider":                 base["provider"],
		"channel_id":               base["channel_id"],
		"group":                    base["group"],
		"audio_bytes":              len(handler.buf),
		"audio_base64_in_response": true,
	}
	h.recordOpenAPITTSUsage(c, started, cred, ch, 200, true, "", &body, merged, respSnap, audioOut, textChars)
	response.Success(c, "ok", base)
}
