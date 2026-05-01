// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/llm"
	"github.com/LingByte/LingVoice/pkg/utils/base"
	"github.com/LingByte/LingVoice/pkg/utils/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func newSpeechUsageRowID() string {
	if base.SnowflakeUtil != nil {
		return base.SnowflakeUtil.GenID()
	}
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}

func speechUsageSnapJSON(h gin.H) string {
	if h == nil {
		return "{}"
	}
	b, err := json.Marshal(h)
	if err != nil {
		return "{}"
	}
	return llm.ClipRelayUsageBody(string(b))
}

func speechUsageClipAnyJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return llm.ClipRelayUsageBody(string(b))
}

func speechRedactJSONKey(k string) bool {
	lk := strings.ToLower(strings.TrimSpace(k))
	for _, s := range []string{"secret", "password", "token", "api_key", "apikey", "private_key", "privatekey", "authorization", "sk-", "ak"} {
		if strings.Contains(lk, s) {
			return true
		}
	}
	return false
}

// speechRedactMapNested returns a JSON-safe snapshot of merged TTS/ASR options with secrets stripped (shallow + one nested map).
func speechRedactMapNested(m map[string]interface{}, depth int) map[string]interface{} {
	if m == nil || depth > 3 {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if speechRedactJSONKey(k) {
			out[k] = "<redacted>"
			continue
		}
		switch t := v.(type) {
		case map[string]interface{}:
			out[k] = speechRedactMapNested(t, depth+1)
		default:
			out[k] = v
		}
	}
	return out
}

func summarizeASRRequestForUsage(body *models.SpeechASRTranscribeReq) gin.H {
	if body == nil {
		return gin.H{}
	}
	out := gin.H{
		"group":    strings.TrimSpace(body.Group),
		"format":   strings.TrimSpace(body.Format),
		"language": strings.TrimSpace(body.Language),
	}
	if raw := strings.TrimSpace(body.AudioURL); raw != "" {
		out["has_audio_url"] = true
		if u, err := url.Parse(raw); err == nil {
			out["audio_url_scheme"] = u.Scheme
			out["audio_url_host"] = u.Host
			out["audio_url_path_len"] = len(u.Path)
			out["audio_url_query_len"] = len(u.RawQuery)
		} else {
			out["audio_url_parse_error"] = true
		}
	}
	if b64 := strings.TrimSpace(body.AudioBase64); b64 != "" {
		out["audio_base64_chars"] = len(b64)
	}
	if body.Extra != nil {
		out["extra"] = speechUsageClipAnyJSON(body.Extra)
	}
	return out
}

func summarizeTTSRequestForUsage(body *models.SpeechTTSSynthesizeReq, merged map[string]interface{}) gin.H {
	if body == nil {
		return gin.H{}
	}
	text := strings.TrimSpace(body.Text)
	preview := text
	if utf8.RuneCountInString(preview) > 240 {
		preview = string([]rune(preview)[:240]) + "…"
	}
	out := gin.H{
		"group":                    strings.TrimSpace(body.Group),
		"voice":                    strings.TrimSpace(body.Voice),
		"response_type":            models.NormalizeRelayTTSResponseType(body.ResponseType, body.Output),
		"response_type_raw":        strings.TrimSpace(body.ResponseType),
		"output_raw":               strings.TrimSpace(body.Output),
		"audio_format":             strings.TrimSpace(body.AudioFormat),
		"sample_rate":              body.SampleRate,
		"text_rune_count":          utf8.RuneCountInString(text),
		"text_byte_len":            len(text),
		"text_preview":             preview,
		"has_tts_options":          body.TTSOptions != nil && len(body.TTSOptions) > 0,
		"tts_options":              speechUsageClipAnyJSON(body.TTSOptions),
		"upload_bucket":            strings.TrimSpace(body.UploadBucket),
		"upload_key":               strings.TrimSpace(body.UploadKey),
		"upload_filename":          strings.TrimSpace(body.UploadFilename),
		"upload_bucket_or_default": strings.TrimSpace(body.UploadBucket) != "",
	}
	if body.Extra != nil {
		out["extra"] = speechUsageClipAnyJSON(body.Extra)
	}
	if merged != nil {
		safe := speechRedactMapNested(merged, 0)
		out["effective_merge_redacted"] = speechUsageClipAnyJSON(safe)
	}
	return out
}

func (h *Handlers) recordOpenAPIASRUsage(c *gin.Context, started time.Time, cred *models.Credential, ch *models.ASRChannel, httpCode int, success bool, errMsg string, body *models.SpeechASRTranscribeReq, respPayload gin.H, audioInBytes int64, audioSrc string) {
	if h.db == nil || cred == nil || started.IsZero() {
		return
	}
	provider, grp := "", ""
	chid := 0
	if ch != nil {
		provider = ch.Provider
		grp = ch.Group
		chid = int(ch.ID)
	}
	reqH := summarizeASRRequestForUsage(body)
	reqH["credential_id"] = cred.Id
	reqH["credential_kind"] = strings.TrimSpace(cred.Kind)
	reqH["credential_group"] = strings.TrimSpace(cred.Group)
	reqH["credential_name"] = strings.TrimSpace(cred.Name)
	if audioSrc != "" {
		reqH["audio_source"] = audioSrc
	}
	if audioInBytes > 0 {
		reqH["audio_bytes"] = audioInBytes
	}
	if ch != nil {
		reqH["channel_id"] = ch.ID
		reqH["channel_group"] = strings.TrimSpace(ch.Group)
		reqH["channel_provider"] = strings.TrimSpace(ch.Provider)
	}
	latMs := time.Since(started).Milliseconds()
	cfg := speechQuotaCfg()
	bill := speechBillableSeconds(latMs, audioInBytes, cfg.ASRInputBytesPerSec)
	delta := speechOpenAPIQuotaDelta(success, cred.Group, bill, cfg.ASRUnitsPerBillableSecond, cfg.MinDeltaOnSuccess)
	row := models.SpeechUsage{
		ID:              newSpeechUsageRowID(),
		RequestID:       newSpeechUsageRowID(),
		CredentialID:    cred.Id,
		UserID:          models.CredentialUserIDString(cred.UserId),
		Kind:            models.SpeechUsageKindASR,
		Provider:        provider,
		ChannelID:       chid,
		Group:           grp,
		RequestType:     "openapi_asr_transcribe",
		RequestContent:  speechUsageSnapJSON(reqH),
		ResponseContent: speechUsageSnapJSON(respPayload),
		LatencyMs:       latMs,
		StatusCode:      httpCode,
		Success:         success,
		ErrorMessage:    strings.TrimSpace(errMsg),
		AudioInputBytes: audioInBytes,
		QuotaDelta:      delta,
		UserAgent:       c.Request.UserAgent(),
		IPAddress:       c.ClientIP(),
		RequestedAt:     started,
		CompletedAt:     time.Now(),
	}
	if err := h.db.Create(&row).Error; err != nil {
		return
	}
	if delta >= 1 {
		models.DecrementCredentialAndUserQuota(h.db, cred, delta)
	}
}

func (h *Handlers) recordOpenAPITTSUsage(c *gin.Context, started time.Time, cred *models.Credential, ch *models.TTSChannel, httpCode int, success bool, errMsg string, body *models.SpeechTTSSynthesizeReq, merged map[string]interface{}, respPayload gin.H, audioOutBytes int64, textChars int) {
	if h.db == nil || cred == nil || started.IsZero() {
		return
	}
	provider, grp := "", ""
	chid := 0
	if ch != nil {
		provider = ch.Provider
		grp = ch.Group
		chid = int(ch.ID)
	}
	latMs := time.Since(started).Milliseconds()
	cfg := speechQuotaCfg()
	bill := speechBillableSeconds(latMs, audioOutBytes, cfg.TTSOutputBytesPerSec)
	delta := speechOpenAPIQuotaDelta(success, cred.Group, bill, cfg.TTSUnitsPerBillableSecond, cfg.MinDeltaOnSuccess)
	reqH := summarizeTTSRequestForUsage(body, merged)
	reqH["credential_id"] = cred.Id
	reqH["credential_kind"] = strings.TrimSpace(cred.Kind)
	reqH["credential_group"] = strings.TrimSpace(cred.Group)
	reqH["credential_name"] = strings.TrimSpace(cred.Name)
	if ch != nil {
		reqH["channel_id"] = ch.ID
		reqH["channel_group"] = strings.TrimSpace(ch.Group)
		reqH["channel_provider"] = strings.TrimSpace(ch.Provider)
	}
	row := models.SpeechUsage{
		ID:               newSpeechUsageRowID(),
		RequestID:        newSpeechUsageRowID(),
		CredentialID:     cred.Id,
		UserID:           models.CredentialUserIDString(cred.UserId),
		Kind:             models.SpeechUsageKindTTS,
		Provider:         provider,
		ChannelID:        chid,
		Group:            grp,
		RequestType:      "openapi_tts_synthesize",
		RequestContent:   speechUsageSnapJSON(reqH),
		ResponseContent:  speechUsageSnapJSON(respPayload),
		LatencyMs:        latMs,
		StatusCode:       httpCode,
		Success:          success,
		ErrorMessage:     strings.TrimSpace(errMsg),
		AudioOutputBytes: audioOutBytes,
		TextInputChars:   textChars,
		QuotaDelta:       delta,
		UserAgent:        c.Request.UserAgent(),
		IPAddress:        c.ClientIP(),
		RequestedAt:      started,
		CompletedAt:      time.Now(),
	}
	if err := h.db.Create(&row).Error; err != nil {
		return
	}
	if delta >= 1 {
		models.DecrementCredentialAndUserQuota(h.db, cred, delta)
	}
}

// speechUsageListHandler 分页查询语音用量（管理员）。
func (h *Handlers) speechUsageListHandler(c *gin.Context) {
	page := models.ParseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := models.ClampPageSize(models.ParseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	q := h.db.Model(&models.SpeechUsage{})
	if s := strings.TrimSpace(c.Query("user_id")); s != "" {
		q = q.Where("user_id = ?", s)
	}
	if s := strings.TrimSpace(c.Query("kind")); s != "" {
		q = q.Where("kind = ?", s)
	}
	if s := strings.TrimSpace(c.Query("credential_id")); s != "" {
		if id, err := strconv.Atoi(s); err == nil && id > 0 {
			q = q.Where("credential_id = ?", id)
		}
	}
	if s := strings.TrimSpace(c.Query("channel_id")); s != "" {
		if cid, err := strconv.Atoi(s); err == nil && cid > 0 {
			q = q.Where("channel_id = ?", cid)
		}
	}
	if s := strings.TrimSpace(c.Query("request_id")); s != "" {
		q = q.Where("request_id = ?", s)
	}
	if s := strings.TrimSpace(c.Query("provider")); s != "" {
		q = q.Where("provider = ?", s)
	}
	if s := strings.TrimSpace(c.Query("request_type")); s != "" {
		q = q.Where("request_type = ?", s)
	}
	if v, ok := models.ParseQueryBool(c, "success"); ok {
		q = q.Where("success = ?", v)
	}
	if t, ok := models.ParseQueryTime(c, "from"); ok {
		q = q.Where("completed_at >= ?", t)
	}
	if t, ok := models.ParseQueryTime(c, "to"); ok {
		q = q.Where("completed_at <= ?", t)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}

	listQ := h.db.Model(&models.SpeechUsage{})
	if s := strings.TrimSpace(c.Query("user_id")); s != "" {
		listQ = listQ.Where("user_id = ?", s)
	}
	if s := strings.TrimSpace(c.Query("kind")); s != "" {
		listQ = listQ.Where("kind = ?", s)
	}
	if s := strings.TrimSpace(c.Query("credential_id")); s != "" {
		if id, err := strconv.Atoi(s); err == nil && id > 0 {
			listQ = listQ.Where("credential_id = ?", id)
		}
	}
	if s := strings.TrimSpace(c.Query("channel_id")); s != "" {
		if cid, err := strconv.Atoi(s); err == nil && cid > 0 {
			listQ = listQ.Where("channel_id = ?", cid)
		}
	}
	if s := strings.TrimSpace(c.Query("request_id")); s != "" {
		listQ = listQ.Where("request_id = ?", s)
	}
	if s := strings.TrimSpace(c.Query("provider")); s != "" {
		listQ = listQ.Where("provider = ?", s)
	}
	if s := strings.TrimSpace(c.Query("request_type")); s != "" {
		listQ = listQ.Where("request_type = ?", s)
	}
	if v, ok := models.ParseQueryBool(c, "success"); ok {
		listQ = listQ.Where("success = ?", v)
	}
	if t, ok := models.ParseQueryTime(c, "from"); ok {
		listQ = listQ.Where("completed_at >= ?", t)
	}
	if t, ok := models.ParseQueryTime(c, "to"); ok {
		listQ = listQ.Where("completed_at <= ?", t)
	}

	var list []models.SpeechUsage
	if err := listQ.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	totalPage := int(total) / pageSize
	if int(total)%pageSize != 0 {
		totalPage++
	}
	response.Success(c, "ok", gin.H{
		"list":      list,
		"total":     total,
		"page":      page,
		"pageSize":  pageSize,
		"totalPage": totalPage,
	})
}

// speechUsageDetailHandler 按主键 id 查询单条语音用量（管理员）。
func (h *Handlers) speechUsageDetailHandler(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var row models.SpeechUsage
	if err := h.db.Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "记录不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{"usage": row})
}
