// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	lingstorage "github.com/LingByte/lingstorage-sdk-go"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const maxOpenAPIAudioFetchBytes = 32 << 20 // 32 MiB

type OpenapiASRTranscribeReq struct {
	Group       string `json:"group"`
	AudioBase64 string `json:"audio_base64"`
	AudioURL    string `json:"audio_url"`
	Format      string `json:"format"`
	Language    string `json:"language"`
	Extra       any    `json:"extra"`
}

type OpenapiTTSSynthesizeReq struct {
	Group          string `json:"group"`
	Text           string `json:"text" binding:"required"`
	Voice          string `json:"voice"`
	Extra          any    `json:"extra"`
	ResponseType   string `json:"response_type"` // audio_base64（默认）| url；兼容 output
	Output         string `json:"output"`        // 与 response_type 二选一，取值同义
	UploadBucket   string `json:"upload_bucket"`
	UploadKey      string `json:"upload_key"`
	UploadFilename string `json:"upload_filename"`
	// AudioFormat 输出编码简写：mp3 / pcm / wav / opus 等；具体含义随厂商（见 applyOpenAPITTSFormatHint）。
	// 与渠道 configJson 或 tts_options 中已有 codec/format/encoding 并存时，tts_options 优先，本字段仅补空。
	AudioFormat string `json:"audio_format"`
	// SampleRate 可选采样率（Hz），写入 sample_rate / sampleRate 供 synthesizer 读取。
	SampleRate int `json:"sample_rate"`
	// TTSOptions 透传到厂商凭据 map（覆盖渠道 configJson），如 {"format":"mp3","mp3_bitrate":128,"encoding":"mp3"}。
	TTSOptions map[string]interface{} `json:"tts_options"`
}

func ttsMergeIfAbsent(m map[string]interface{}, key string, val interface{}) {
	if val == nil {
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

// applyOpenAPITTSFormatHint 将简短 audio_format 映射到各厂商在 NewSynthesisServiceFromCredential 中识别的键。
func applyOpenAPITTSFormatHint(m map[string]interface{}, provider, audioFormat string) {
	af := strings.ToLower(strings.TrimSpace(audioFormat))
	if af == "" {
		return
	}
	prov := strings.ToLower(strings.TrimSpace(provider))
	switch prov {
	case "openai":
		// OpenAI /v1/audio/speech 的 response_format：mp3、opus、aac、flac、wav、pcm
		ttsMergeIfAbsent(m, "codec", af)
	case "qcloud", "tencent":
		ttsMergeIfAbsent(m, "codec", af)
	case "volcengine":
		ttsMergeIfAbsent(m, "encoding", af)
	case "minimax", "fishaudio":
		ttsMergeIfAbsent(m, "format", af)
	case "fishspeech":
		ttsMergeIfAbsent(m, "codec", af)
	case "azure":
		switch af {
		case "mp3":
			ttsMergeIfAbsent(m, "codec", "audio-24khz-48kbitrate-mono-mp3")
		case "opus":
			ttsMergeIfAbsent(m, "codec", "webm-16khz-16bit-mono-opus")
		case "pcm", "wav":
			ttsMergeIfAbsent(m, "codec", "riff-16khz-16bit-mono-pcm")
		default:
			ttsMergeIfAbsent(m, "codec", af)
		}
	case "local":
		ttsMergeIfAbsent(m, "codec", af)
	default:
		ttsMergeIfAbsent(m, "format", af)
	}
}

func mergeTTSOpenAPIRequestOptions(merged map[string]interface{}, provider string, body *OpenapiTTSSynthesizeReq) {
	if body.TTSOptions != nil {
		for k, v := range body.TTSOptions {
			k = strings.TrimSpace(k)
			if k == "" || strings.EqualFold(k, "provider") {
				continue
			}
			merged[k] = v
		}
	}
	if body.SampleRate > 0 {
		sr := int64(body.SampleRate)
		ttsMergeIfAbsent(merged, "sample_rate", sr)
		ttsMergeIfAbsent(merged, "sampleRate", sr)
	}
	applyOpenAPITTSFormatHint(merged, provider, body.AudioFormat)
}

func pickASRChannel(db *gorm.DB, cred *models.Credential, groupOverride string) (*models.ASRChannel, error) {
	g := strings.TrimSpace(groupOverride)
	if g == "" {
		g = strings.TrimSpace(cred.Group)
	}
	q := db.Model(&models.ASRChannel{}).Where("enabled = ?", true)
	if g != "" {
		q = q.Where("`group` = ?", g)
	}
	var ch models.ASRChannel
	if err := q.Order("sort_order ASC, id ASC").First(&ch).Error; err != nil {
		return nil, err
	}
	return &ch, nil
}

func pickTTSChannel(db *gorm.DB, cred *models.Credential, groupOverride string) (*models.TTSChannel, error) {
	g := strings.TrimSpace(groupOverride)
	if g == "" {
		g = strings.TrimSpace(cred.Group)
	}
	q := db.Model(&models.TTSChannel{}).Where("enabled = ?", true)
	if g != "" {
		q = q.Where("`group` = ?", g)
	}
	var ch models.TTSChannel
	if err := q.Order("sort_order ASC, id ASC").First(&ch).Error; err != nil {
		return nil, err
	}
	return &ch, nil
}

func noChannelErrDetail(err error, kind, group string) string {
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
	body := io.LimitReader(resp.Body, maxOpenAPIAudioFetchBytes+1)
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	if len(data) > maxOpenAPIAudioFetchBytes {
		return nil, fmt.Errorf("audio 超过大小限制 %d 字节", maxOpenAPIAudioFetchBytes)
	}
	return data, nil
}

func mergeASRTranscribeOptions(merged map[string]interface{}, body *OpenapiASRTranscribeReq) {
	if body == nil {
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
	// 避免 LLM 等场景下的 "model" 误入腾讯云 engine_model_type。
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

func bindAndLoadOpenAPIASRAudio(c *gin.Context) (OpenapiASRTranscribeReq, []byte, string, error) {
	ct := strings.ToLower(strings.TrimSpace(c.ContentType()))
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := c.Request.ParseMultipartForm(int64(maxOpenAPIAudioFetchBytes)); err != nil {
			return OpenapiASRTranscribeReq{}, nil, "", fmt.Errorf("解析 multipart 失败: %w", err)
		}
		var body OpenapiASRTranscribeReq
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
			data, err := io.ReadAll(io.LimitReader(f, maxOpenAPIAudioFetchBytes+1))
			if err != nil {
				return body, nil, "", err
			}
			if len(data) > maxOpenAPIAudioFetchBytes {
				return body, nil, "", fmt.Errorf("audio 超过大小限制 %d 字节", maxOpenAPIAudioFetchBytes)
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
			if len(raw) > maxOpenAPIAudioFetchBytes {
				return body, nil, "", fmt.Errorf("audio 超过大小限制 %d 字节", maxOpenAPIAudioFetchBytes)
			}
			body.AudioBase64 = b64
			return body, raw, "base64", nil
		}
		return body, nil, "", fmt.Errorf("请上传表单文件字段 audio，或提供 audio_url / audio_base64")
	}

	var body OpenapiASRTranscribeReq
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
		if len(raw) > maxOpenAPIAudioFetchBytes {
			return body, nil, "", fmt.Errorf("audio 超过大小限制 %d 字节", maxOpenAPIAudioFetchBytes)
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

func resolveASRAudioInput(body *OpenapiASRTranscribeReq) (source string, err error) {
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
		if len(raw) > maxOpenAPIAudioFetchBytes {
			return "", fmt.Errorf("audio 超过大小限制 %d 字节", maxOpenAPIAudioFetchBytes)
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
	ch, err := pickASRChannel(h.db, cred, body.Group)
	if err != nil {
		errDetail := noChannelErrDetail(err, "ASR", body.Group)
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
	mergeASRTranscribeOptions(merged, &body)

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

func nilIfASRBodyEmpty(body OpenapiASRTranscribeReq) *OpenapiASRTranscribeReq {
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

func normalizeTTSResponseType(responseType, output string) string {
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
	var body OpenapiTTSSynthesizeReq
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
	outMode := normalizeTTSResponseType(body.ResponseType, body.Output)
	ch, err := pickTTSChannel(h.db, cred, body.Group)
	if err != nil {
		errDetail := noChannelErrDetail(err, "TTS", body.Group)
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
		switch merged["provider"] {
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
	mergeTTSOpenAPIRequestOptions(merged, strings.ToLower(strings.TrimSpace(ch.Provider)), &body)

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
