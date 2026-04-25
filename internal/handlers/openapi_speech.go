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

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/config"
	"github.com/LingByte/LingVoice/pkg/middleware"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/LingByte/LingVoice/pkg/synthesizer"
	"github.com/LingByte/LingVoice/pkg/utils"
	"github.com/gin-gonic/gin"
	lingstorage "github.com/LingByte/lingstorage-sdk-go"
	"gorm.io/gorm"
)

const maxOpenAPIAudioFetchBytes = 32 << 20 // 32 MiB

type openAPIASRTranscribeBody struct {
	Group       string `json:"group"`
	AudioBase64 string `json:"audio_base64"`
	AudioURL    string `json:"audio_url"`
	Format      string `json:"format"`
	Language    string `json:"language"`
	Extra       any    `json:"extra"`
}

type openAPITTSSynthesizeBody struct {
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

func mergeTTSOpenAPIRequestOptions(merged map[string]interface{}, provider string, body *openAPITTSSynthesizeBody) {
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

// openAPIAudioURLProtection 拉取用户音频 URL：黑名单域名模式 + 禁止私网 IP（含解析结果）。
func openAPIAudioURLProtection() *utils.SSRFProtection {
	return &utils.SSRFProtection{
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

func resolveASRAudioInput(body *openAPIASRTranscribeBody) (source string, err error) {
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

// openAPIASRTranscribe POST /api/openapi/v1/speech/asr/transcribe
func (h *Handlers) openAPIASRTranscribe(c *gin.Context) {
	cred, ok := middleware.OpenAPISpeechCredentialFromContext(c)
	if !ok || cred == nil {
		response.FailWithCode(c, 401, "未授权", nil)
		return
	}
	started := time.Now()
	var body openAPIASRTranscribeBody
	if err := c.ShouldBindJSON(&body); err != nil {
		h.recordOpenAPIASRUsage(c, started, cred, nil, 400, false, err.Error(), nil, gin.H{"error": err.Error()}, 0, "")
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	src, err := resolveASRAudioInput(&body)
	if err != nil {
		h.recordOpenAPIASRUsage(c, started, cred, nil, 400, false, err.Error(), &body, gin.H{"error": err.Error()}, 0, "")
		response.FailWithCode(c, 400, err.Error(), nil)
		return
	}
	audioLen := 0
	if src == "base64" {
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(body.AudioBase64))
		audioLen = len(raw)
	} else {
		data, ferr := fetchAudioBytesFromURL(c.Request.Context(), strings.TrimSpace(body.AudioURL))
		if ferr != nil {
			h.recordOpenAPIASRUsage(c, started, cred, nil, 400, false, ferr.Error(), &body, gin.H{"error": ferr.Error()}, 0, src)
			response.FailWithCode(c, 400, "拉取 audio_url 失败", gin.H{"error": ferr.Error()})
			return
		}
		audioLen = len(data)
	}
	ch, err := pickASRChannel(h.db, cred, body.Group)
	if err != nil {
		h.recordOpenAPIASRUsage(c, started, cred, nil, 503, false, err.Error(), &body, gin.H{"error": err.Error()}, int64(audioLen), src)
		response.FailWithCode(c, 503, "无可用 ASR 渠道", gin.H{"error": err.Error()})
		return
	}
	out := gin.H{
		"text":         "",
		"segments":     []any{},
		"provider":     ch.Provider,
		"channel_id":   ch.ID,
		"group":        ch.Group,
		"audio_source": src,
		"audio_bytes":  audioLen,
		"message":      "HTTP 单次 ASR 已选路并完成音频入参校验；转写需接入 recognizer 流水线。",
	}
	h.recordOpenAPIASRUsage(c, started, cred, ch, 200, true, "", &body, out, int64(audioLen), src)
	response.Success(c, "ok", out)
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

// openAPITTSSynthesize POST /api/openapi/v1/speech/tts/synthesize
func (h *Handlers) openAPITTSSynthesize(c *gin.Context) {
	cred, ok := middleware.OpenAPISpeechCredentialFromContext(c)
	if !ok || cred == nil {
		response.FailWithCode(c, 401, "未授权", nil)
		return
	}
	started := time.Now()
	var body openAPITTSSynthesizeBody
	if err := c.ShouldBindJSON(&body); err != nil {
		h.recordOpenAPITTSUsage(c, started, cred, nil, 400, false, err.Error(), nil, gin.H{"error": err.Error()}, 0, 0)
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		h.recordOpenAPITTSUsage(c, started, cred, nil, 400, false, "text 不能为空", &body, gin.H{"error": "text 不能为空"}, 0, 0)
		response.FailWithCode(c, 400, "text 不能为空", nil)
		return
	}
	textChars := utf8.RuneCountInString(text)
	outMode := normalizeTTSResponseType(body.ResponseType, body.Output)
	ch, err := pickTTSChannel(h.db, cred, body.Group)
	if err != nil {
		h.recordOpenAPITTSUsage(c, started, cred, nil, 503, false, err.Error(), &body, gin.H{"error": err.Error()}, 0, textChars)
		response.FailWithCode(c, 503, "无可用 TTS 渠道", gin.H{"error": err.Error()})
		return
	}
	merged := map[string]interface{}{
		"provider": strings.ToLower(strings.TrimSpace(ch.Provider)),
	}
	if strings.TrimSpace(ch.ConfigJSON) != "" {
		var extra map[string]interface{}
		if err := json.Unmarshal([]byte(ch.ConfigJSON), &extra); err != nil {
			h.recordOpenAPITTSUsage(c, started, cred, ch, 500, false, err.Error(), &body, gin.H{"error": err.Error()}, 0, textChars)
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
		h.recordOpenAPITTSUsage(c, started, cred, ch, 400, false, err.Error(), &body, gin.H{"error": err.Error()}, 0, textChars)
		response.FailWithCode(c, 400, "TTS 配置无法构建服务", gin.H{"error": err.Error()})
		return
	}
	handler := &ttsBufferHandler{}
	ctx := context.Background()
	if err := svc.Synthesize(ctx, handler, text); err != nil {
		_ = svc.Close()
		h.recordOpenAPITTSUsage(c, started, cred, ch, 502, false, err.Error(), &body, gin.H{"error": err.Error()}, 0, textChars)
		response.FailWithCode(c, 502, "合成失败", gin.H{"error": err.Error()})
		return
	}
	_ = svc.Close()
	if len(handler.buf) == 0 {
		h.recordOpenAPITTSUsage(c, started, cred, ch, 502, false, "未收到音频数据", &body, nil, 0, textChars)
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
			h.recordOpenAPITTSUsage(c, started, cred, ch, 503, false, "对象存储未初始化", &body, gin.H{"response_type": outMode}, audioOut, textChars)
			response.FailWithCode(c, 503, "对象存储未初始化（LINGSTORAGE_*）", nil)
			return
		}
		bucket := strings.TrimSpace(body.UploadBucket)
		if bucket == "" {
			bucket = config.GlobalConfig.Services.Storage.Bucket
		}
		if bucket == "" {
			h.recordOpenAPITTSUsage(c, started, cred, ch, 503, false, "未配置存储 bucket", &body, gin.H{"response_type": outMode}, audioOut, textChars)
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
			h.recordOpenAPITTSUsage(c, started, cred, ch, 502, false, upErr.Error(), &body, gin.H{"response_type": outMode, "error": upErr.Error()}, audioOut, textChars)
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
			"channel_id":     base["channel_id"],
			"group":         base["group"],
			"size":          up.Size,
			"key":           up.Key,
			"bucket":        up.Bucket,
			"filename":      up.Filename,
		}
		h.recordOpenAPITTSUsage(c, started, cred, ch, 200, true, "", &body, respSnap, audioOut, textChars)
		response.Success(c, "ok", base)
		return
	}

	b64 := base64.StdEncoding.EncodeToString(handler.buf)
	base["audio_base64"] = b64
	respSnap := gin.H{
		"response_type": outMode,
		"format":        base["format"],
		"provider":      base["provider"],
		"channel_id":    base["channel_id"],
		"group":         base["group"],
		"audio_bytes":   len(handler.buf),
		"audio_base64_in_response": true,
	}
	h.recordOpenAPITTSUsage(c, started, cred, ch, 200, true, "", &body, respSnap, audioOut, textChars)
	response.Success(c, "ok", base)
}
