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
	config    Config
	manager   SessionManager
	log       *zap.Logger
	protocols []common.ProtocolType // 已启用的协议列表
}

// SessionManager 会话管理接口（由 protocol.Manager 实现）
type SessionManager interface {
	GetSession(id string) (common.ProtocolSession, bool)
	SendCommand(sessionID string, cmd common.ProtocolCommand) error
	SendMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error
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
	// 注意：这里需要 manager 提供列表接口，暂返回空列表
	// 后续在 Manager 中加 ListSessions 方法
	s.writeJSON(w, http.StatusOK, apiResponse{
		Success: true,
		Data:    []sessionInfo{},
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
