// Package api implements HTTP REST API for session management and control.
//
// REST API 是业务方/控制面对接协议层的入口：
//   GET    /api/sessions           列出所有会话
//   GET    /api/sessions/{id}      查看会话详情
//   POST   /api/sessions/{id}/cmd  向会话下发指令 (answer/reject/hangup/transfer)
//   POST   /api/sessions/{id}/media 向会话发送媒体帧（测试用）
//   DELETE /api/sessions/{id}      关闭会话
//   GET    /api/health             健康检查
//   GET    /api/protocols          列出已启用的协议
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

// Config REST API 配置
type Config struct {
	Addr string // 监听地址
	Path string // API 前缀
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{Addr: ":8090", Path: "/api"}
}

// Server REST API 服务
type Server struct {
	config              Config
	manager             SessionManager
	log                 *zap.Logger
	protocols           []common.ProtocolType // 已启用的协议列表
	recordingController RecordingController
	transcodeController TranscodeController
}

// SessionManager 会话管理接口（由 protocol.Manager 实现）
type SessionManager interface {
	GetSession(id string) (common.ProtocolSession, bool)
	ListSessions() []common.ProtocolSession
	SendCommand(sessionID string, cmd common.ProtocolCommand) error
	SendMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error
}

// RecordingController 录制控制接口（由 rustbridge.Client 实现）
type RecordingController interface {
	StartRecording(ctx context.Context, sessionID, format, path string, channels uint8) (string, error)
	StopRecording(ctx context.Context, sessionID, recordingID string) (any, error)
}

// TranscodeController 转码控制接口
type TranscodeController interface {
	// SetTranscodeConfig 配置会话的转码规则
	SetTranscodeConfig(ctx context.Context, sessionID string, srcCodec, dstCodec string) error
	// GetTranscodeConfig 查询会话的转码配置
	GetTranscodeConfig(ctx context.Context, sessionID string) (map[string]string, error)
}

// NewServer 创建 REST API 服务
func NewServer(config Config, manager SessionManager, log *zap.Logger) *Server {
	if log == nil {
		log = logger.Lg
	}
	return &Server{
		config:  config,
		manager: manager,
		log:     log.With(zap.String("component", "api-server")),
	}
}

// SetRecordingController 设置录制控制器
func (s *Server) SetRecordingController(rc RecordingController) {
	s.recordingController = rc
}

// SetTranscodeController 设置转码控制器
func (s *Server) SetTranscodeController(tc TranscodeController) {
	s.transcodeController = tc
}

// SetProtocols 设置已启用的协议列表
func (s *Server) SetProtocols(protocols []common.ProtocolType) {
	s.protocols = protocols
}

// Handler 返回 HTTP Handler
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	base := s.config.Path
	mux.HandleFunc(base+"/health", s.handleHealth)
	mux.HandleFunc(base+"/protocols", s.handleProtocols)
	mux.HandleFunc(base+"/sessions", s.handleSessions)
	mux.HandleFunc(base+"/sessions/", s.handleSessionByID)
	mux.HandleFunc(base+"/recordings", s.handleRecordings)
	mux.HandleFunc(base+"/recordings/", s.handleRecordingByID)
	mux.HandleFunc(base+"/transcode", s.handleTranscode)
	mux.HandleFunc(base+"/transcode/", s.handleTranscodeByID)
	mux.HandleFunc(base+"/streams", s.handleStreams)
	return mux
}

// Start 启动服务
func (s *Server) Start() error {
	s.log.Info("rest api server starting", zap.String("addr", s.config.Addr), zap.String("path", s.config.Path))
	return http.ListenAndServe(s.config.Addr, s.Handler())
}

// --- 响应类型 ---

type apiResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Data    any    `json:"data,omitempty"`
}

type sessionInfo struct {
	ID       string `json:"id"`
	Protocol string `json:"protocol"`
}

type commandRequest struct {
	Type    string `json:"type"`    // answer/reject/hangup/transfer/startMedia/stopMedia
	Reason  string `json:"reason"`  // reject 时
	Target  string `json:"target"`  // transfer 时
}

type mediaRequest struct {
	TrackID   string `json:"trackId"`   // 轨道 ID
	Type      string `json:"type"`      // audio/video
	Codec     string `json:"codec"`     // opus/pcmu/pcma/pcm16/h264/vp8
	Payload   []byte `json:"payload"`   // base64 encoded
	Timestamp uint32 `json:"timestamp"`
	Sequence  uint16 `json:"sequence"`
}

// --- 处理函数 ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, apiResponse{
		Success: true,
		Data: map[string]any{
			"status":   "ok",
			"uptime":   time.Since(time.Now()).String(), // 简化
			"protocols": s.protocols,
		},
	})
}

func (s *Server) handleProtocols(w http.ResponseWriter, r *http.Request) {
	protoStrs := make([]string, len(s.protocols))
	for i, p := range s.protocols {
		protoStrs[i] = string(p)
	}
	s.writeJSON(w, http.StatusOK, apiResponse{
		Success: true,
		Data:    protoStrs,
	})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	// GET /api/sessions — 列出所有会话
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Error: "use GET"})
		return
	}

	sessions := s.manager.ListSessions()
	list := make([]sessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		list = append(list, sessionInfo{
			ID:       sess.ID(),
			Protocol: string(sess.Protocol()),
		})
	}
	s.writeJSON(w, http.StatusOK, apiResponse{
		Success: true,
		Data:    list,
	})
}

func (s *Server) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	// /api/sessions/{id} 或 /api/sessions/{id}/cmd 或 /api/sessions/{id}/media
	path := strings.TrimPrefix(r.URL.Path, s.config.Path+"/sessions/")
	parts := strings.SplitN(path, "/", 2)
	sessionID := parts[0]

	if sessionID == "" {
		s.writeJSON(w, http.StatusBadRequest, apiResponse{Error: "missing session id"})
		return
	}

	sess, ok := s.manager.GetSession(sessionID)
	if !ok {
		s.writeJSON(w, http.StatusNotFound, apiResponse{Error: "session not found"})
		return
	}

	if len(parts) == 1 {
		// /api/sessions/{id}
		switch r.Method {
		case http.MethodGet:
			s.writeJSON(w, http.StatusOK, apiResponse{
				Success: true,
				Data: sessionInfo{
					ID:       sess.ID(),
					Protocol: string(sess.Protocol()),
				},
			})
		case http.MethodDelete:
			_ = s.manager.SendCommand(sessionID, common.ProtocolCommand{
				Type: common.CmdHangup,
			})
			s.writeJSON(w, http.StatusOK, apiResponse{Success: true})
		default:
			s.writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		}
		return
	}

	action := parts[1]
	switch action {
	case "cmd":
		s.handleCommand(w, r, sessionID)
	case "media":
		s.handleMedia(w, r, sessionID)
	default:
		s.writeJSON(w, http.StatusNotFound, apiResponse{Error: "unknown action: " + action})
	}
}

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Error: "use POST"})
		return
	}

	var req commandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, apiResponse{Error: "invalid JSON: " + err.Error()})
		return
	}

	cmd, err := parseCommand(req)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, apiResponse{Error: err.Error()})
		return
	}

	if err := s.manager.SendCommand(sessionID, cmd); err != nil {
		s.writeJSON(w, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}

	s.writeJSON(w, http.StatusOK, apiResponse{Success: true})
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Error: "use POST"})
		return
	}

	var req mediaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, apiResponse{Error: "invalid JSON: " + err.Error()})
		return
	}

	frameType := common.FrameAudio
	if req.Type == "video" {
		frameType = common.FrameVideo
	}

	codec, err := common.CodecFromString(req.Codec)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, apiResponse{Error: err.Error()})
		return
	}

	frame := common.MediaFrame{
		Type:      frameType,
		Codec:     codec,
		Payload:   req.Payload,
		Timestamp: req.Timestamp,
		Sequence:  req.Sequence,
	}

	if err := s.manager.SendMediaFrame(sessionID, common.TrackID(req.TrackID), frame); err != nil {
		s.writeJSON(w, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}

	s.writeJSON(w, http.StatusOK, apiResponse{Success: true})
}

// --- 辅助 ---

func parseCommand(req commandRequest) (common.ProtocolCommand, error) {
	switch req.Type {
	case "answer":
		return common.ProtocolCommand{Type: common.CmdAnswer}, nil
	case "reject":
		return common.ProtocolCommand{Type: common.CmdReject, Reason: req.Reason}, nil
	case "hangup":
		return common.ProtocolCommand{Type: common.CmdHangup}, nil
	case "transfer":
		return common.ProtocolCommand{Type: common.CmdTransfer, Target: req.Target}, nil
	case "startMedia":
		return common.ProtocolCommand{Type: common.CmdStartMedia}, nil
	case "stopMedia":
		return common.ProtocolCommand{Type: common.CmdStopMedia}, nil
	default:
		return common.ProtocolCommand{}, fmt.Errorf("unknown command type: %s", req.Type)
	}
}

func (s *Server) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// ============================================================================
// 录制控制 API
// ============================================================================
//
//	POST   /api/recordings              开始录制 {sessionId, format, path, channels}
//	DELETE /api/recordings/{id}         停止录制 ?sessionId=xxx
//	GET    /api/recordings              列出活跃录制（需 controller 支持）

type recordingRequest struct {
	SessionID string `json:"sessionId"`
	Format    string `json:"format"`  // wav/opus/pcap/mp4
	Path      string `json:"path"`    // 输出路径
	Channels  uint8  `json:"channels"`
}

type recordingResponse struct {
	RecordingID string `json:"recordingId"`
}

func (s *Server) handleRecordings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleRecordingStart(w, r)
		return
	}
	if r.Method == http.MethodGet {
		// 列出活跃录制（简化：返回空列表，需 controller 扩展）
		s.writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: []any{}})
		return
	}
	s.writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Error: "use POST or GET"})
}

func (s *Server) handleRecordingStart(w http.ResponseWriter, r *http.Request) {
	if s.recordingController == nil {
		s.writeJSON(w, http.StatusServiceUnavailable, apiResponse{Error: "recording controller not configured"})
		return
	}

	var req recordingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, apiResponse{Error: "invalid JSON: " + err.Error()})
		return
	}

	if req.SessionID == "" {
		s.writeJSON(w, http.StatusBadRequest, apiResponse{Error: "sessionId is required"})
		return
	}

	if req.Format == "" {
		req.Format = "wav"
	}
	if req.Channels == 0 {
		req.Channels = 1
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	recordingID, err := s.recordingController.StartRecording(ctx, req.SessionID, req.Format, req.Path, req.Channels)
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}

	s.writeJSON(w, http.StatusOK, apiResponse{
		Success: true,
		Data:    recordingResponse{RecordingID: recordingID},
	})
}

func (s *Server) handleRecordingByID(w http.ResponseWriter, r *http.Request) {
	// DELETE /api/recordings/{id}?sessionId=xxx
	if r.Method != http.MethodDelete {
		s.writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Error: "use DELETE"})
		return
	}

	if s.recordingController == nil {
		s.writeJSON(w, http.StatusServiceUnavailable, apiResponse{Error: "recording controller not configured"})
		return
	}

	recordingID := strings.TrimPrefix(r.URL.Path, s.config.Path+"/recordings/")
	recordingID = strings.TrimSuffix(recordingID, "/")
	if recordingID == "" {
		s.writeJSON(w, http.StatusBadRequest, apiResponse{Error: "missing recording id"})
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		s.writeJSON(w, http.StatusBadRequest, apiResponse{Error: "sessionId query param is required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if _, err := s.recordingController.StopRecording(ctx, sessionID, recordingID); err != nil {
		s.writeJSON(w, http.StatusInternalServerError, apiResponse{Error: err.Error()})
		return
	}

	s.writeJSON(w, http.StatusOK, apiResponse{Success: true})
}

// ============================================================================
// 转码控制 API
// ============================================================================
//
//	POST /api/transcode/{sessionId}   设置转码 {srcCodec, dstCodec}
//	GET  /api/transcode/{sessionId}   查询转码配置

type transcodeRequest struct {
	SrcCodec string `json:"srcCodec"` // vp8/h264/opus/pcmu...
	DstCodec string `json:"dstCodec"` // vp8/h264/opus/pcmu...
}

func (s *Server) handleTranscode(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: "use /api/transcode/{sessionId}"})
}

func (s *Server) handleTranscodeByID(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, s.config.Path+"/transcode/")
	sessionID = strings.TrimSuffix(sessionID, "/")
	if sessionID == "" {
		s.writeJSON(w, http.StatusBadRequest, apiResponse{Error: "missing session id"})
		return
	}

	if s.transcodeController == nil {
		s.writeJSON(w, http.StatusServiceUnavailable, apiResponse{Error: "transcode controller not configured"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	switch r.Method {
	case http.MethodPost:
		var req transcodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeJSON(w, http.StatusBadRequest, apiResponse{Error: "invalid JSON: " + err.Error()})
			return
		}
		if req.SrcCodec == "" || req.DstCodec == "" {
			s.writeJSON(w, http.StatusBadRequest, apiResponse{Error: "srcCodec and dstCodec are required"})
			return
		}
		if err := s.transcodeController.SetTranscodeConfig(ctx, sessionID, req.SrcCodec, req.DstCodec); err != nil {
			s.writeJSON(w, http.StatusInternalServerError, apiResponse{Error: err.Error()})
			return
		}
		s.writeJSON(w, http.StatusOK, apiResponse{Success: true})

	case http.MethodGet:
		config, err := s.transcodeController.GetTranscodeConfig(ctx, sessionID)
		if err != nil {
			s.writeJSON(w, http.StatusInternalServerError, apiResponse{Error: err.Error()})
			return
		}
		s.writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: config})

	default:
		s.writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Error: "use POST or GET"})
	}
}

// ============================================================================
// 流管理 API
// ============================================================================
//
//	GET /api/streams — 列出所有活跃流（等同于 sessions 的流视角）

func (s *Server) handleStreams(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Error: "use GET"})
		return
	}

	sessions := s.manager.ListSessions()
	streams := make([]map[string]any, 0, len(sessions))
	for _, sess := range sessions {
		streams = append(streams, map[string]any{
			"streamId": sess.ID(),
			"protocol": string(sess.Protocol()),
			"state":    "active",
		})
	}
	s.writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: streams})
}
