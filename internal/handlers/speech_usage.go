// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/llm"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/LingByte/LingVoice/pkg/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func newSpeechUsageRowID() string {
	if utils.SnowflakeUtil != nil {
		return utils.SnowflakeUtil.GenID()
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
	return llm.ClipOpenAPIUsageBody(string(b))
}

func summarizeASRRequestForUsage(body *openAPIASRTranscribeBody) gin.H {
	if body == nil {
		return gin.H{}
	}
	out := gin.H{
		"group":    strings.TrimSpace(body.Group),
		"format":   strings.TrimSpace(body.Format),
		"language": strings.TrimSpace(body.Language),
	}
	if strings.TrimSpace(body.AudioURL) != "" {
		out["has_audio_url"] = true
	}
	if strings.TrimSpace(body.AudioBase64) != "" {
		out["audio_base64_chars"] = len(strings.TrimSpace(body.AudioBase64))
	}
	return out
}

func summarizeTTSRequestForUsage(body *openAPITTSSynthesizeBody) gin.H {
	if body == nil {
		return gin.H{}
	}
	text := strings.TrimSpace(body.Text)
	return gin.H{
		"group":                     strings.TrimSpace(body.Group),
		"voice":                     strings.TrimSpace(body.Voice),
		"response_type":             normalizeTTSResponseType(body.ResponseType, body.Output),
		"audio_format":              strings.TrimSpace(body.AudioFormat),
		"sample_rate":               body.SampleRate,
		"text_rune_count":           utf8.RuneCountInString(text),
		"has_tts_options":           body.TTSOptions != nil && len(body.TTSOptions) > 0,
		"upload_bucket_or_default":  strings.TrimSpace(body.UploadBucket) != "",
	}
}

func (h *Handlers) recordOpenAPIASRUsage(c *gin.Context, started time.Time, cred *models.Credential, ch *models.ASRChannel, httpCode int, success bool, errMsg string, body *openAPIASRTranscribeBody, respPayload gin.H, audioInBytes int64, audioSrc string) {
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
	if audioSrc != "" {
		reqH["audio_source"] = audioSrc
	}
	if audioInBytes > 0 {
		reqH["audio_bytes"] = audioInBytes
	}
	row := models.SpeechUsage{
		ID:               newSpeechUsageRowID(),
		RequestID:        newSpeechUsageRowID(),
		CredentialID:     cred.Id,
		UserID:           credentialUserIDString(cred.UserId),
		Kind:             models.SpeechUsageKindASR,
		Provider:         provider,
		ChannelID:        chid,
		Group:            grp,
		RequestType:      "openapi_asr_transcribe",
		RequestContent:   speechUsageSnapJSON(reqH),
		ResponseContent:  speechUsageSnapJSON(respPayload),
		LatencyMs:        time.Since(started).Milliseconds(),
		StatusCode:       httpCode,
		Success:          success,
		ErrorMessage:     strings.TrimSpace(errMsg),
		AudioInputBytes:  audioInBytes,
		UserAgent:        c.Request.UserAgent(),
		IPAddress:        c.ClientIP(),
		RequestedAt:      started,
		CompletedAt:      time.Now(),
	}
	_ = h.db.Create(&row).Error
}

func (h *Handlers) recordOpenAPITTSUsage(c *gin.Context, started time.Time, cred *models.Credential, ch *models.TTSChannel, httpCode int, success bool, errMsg string, body *openAPITTSSynthesizeBody, respPayload gin.H, audioOutBytes int64, textChars int) {
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
	row := models.SpeechUsage{
		ID:               newSpeechUsageRowID(),
		RequestID:        newSpeechUsageRowID(),
		CredentialID:     cred.Id,
		UserID:           credentialUserIDString(cred.UserId),
		Kind:             models.SpeechUsageKindTTS,
		Provider:         provider,
		ChannelID:        chid,
		Group:            grp,
		RequestType:      "openapi_tts_synthesize",
		RequestContent:   speechUsageSnapJSON(summarizeTTSRequestForUsage(body)),
		ResponseContent:  speechUsageSnapJSON(respPayload),
		LatencyMs:        time.Since(started).Milliseconds(),
		StatusCode:       httpCode,
		Success:          success,
		ErrorMessage:     strings.TrimSpace(errMsg),
		AudioOutputBytes: audioOutBytes,
		TextInputChars:   textChars,
		UserAgent:        c.Request.UserAgent(),
		IPAddress:        c.ClientIP(),
		RequestedAt:      started,
		CompletedAt:      time.Now(),
	}
	_ = h.db.Create(&row).Error
}

// listSpeechUsage 分页查询语音用量（管理员）。
func (h *Handlers) listSpeechUsage(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	page := parseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := clampPageSize(parseQueryInt(c, "pageSize", 20))
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
	if v, ok := parseQueryBool(c, "success"); ok {
		q = q.Where("success = ?", v)
	}
	if t, ok := parseQueryTime(c, "from"); ok {
		q = q.Where("completed_at >= ?", t)
	}
	if t, ok := parseQueryTime(c, "to"); ok {
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
	if v, ok := parseQueryBool(c, "success"); ok {
		listQ = listQ.Where("success = ?", v)
	}
	if t, ok := parseQueryTime(c, "from"); ok {
		listQ = listQ.Where("completed_at >= ?", t)
	}
	if t, ok := parseQueryTime(c, "to"); ok {
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

// getSpeechUsage 按主键 id 查询单条语音用量（管理员）。
func (h *Handlers) getSpeechUsage(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
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
