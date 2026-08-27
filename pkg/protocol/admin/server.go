// Package admin implements a web admin panel for LingVoice.
//
// 提供:
//   - 仪表盘: 会话数、流数、协议状态概览
//   - 会话管理: 查看所有活跃会话, 可关闭/发送命令
//   - 流管理: 查看所有流, 可启动/停止录制
//   - 协议监控: 各协议连接状态
//   - 系统设置: 鉴权配置、集群配置
//
// 前端为嵌入式单页 HTML (无需外部依赖), 通过 REST API 与后端交互。
package admin

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

// Config 管理面板配置
type Config struct {
	Addr     string // 监听地址
	Path     string // URL 路径前缀
	Username string // 基本认证用户名 (空=不认证)
	Password string // 基本认证密码
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		Addr: ":8088",
		Path: "/admin",
	}
}

// SessionManager 会话管理接口
type SessionManager interface {
	GetSession(id string) (common.ProtocolSession, bool)
	ListSessions() []common.ProtocolSession
	SendCommand(sessionID string, cmd common.ProtocolCommand) error
}

// Server 管理面板服务
type Server struct {
	config  Config
	manager SessionManager
	log     *zap.Logger
}

// NewServer 创建管理面板
func NewServer(config Config, manager SessionManager, log *zap.Logger) *Server {
	if log == nil {
		if logger.Lg != nil {
			log = logger.Lg
		} else {
			log = zap.NewNop()
		}
	}
	return &Server{
		config:  config,
		manager: manager,
		log:     log.With(zap.String("component", "admin-panel")),
	}
}

// Handler 返回 HTTP Handler
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	base := s.config.Path

	// API endpoints
	mux.HandleFunc(base+"/api/dashboard", s.authMiddleware(s.handleDashboard))
	mux.HandleFunc(base+"/api/sessions", s.authMiddleware(s.handleSessions))
	mux.HandleFunc(base+"/api/sessions/", s.authMiddleware(s.handleSessionAction))

	// Serve embedded HTML
	mux.HandleFunc(base+"/", s.authMiddleware(s.handleUI))

	return mux
}

// Start 启动管理面板
func (s *Server) Start() error {
	s.log.Info("admin panel starting", zap.String("addr", s.config.Addr), zap.String("path", s.config.Path))
	return http.ListenAndServe(s.config.Addr, s.Handler())
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.config.Username != "" || s.config.Password != "" {
			user, pass, ok := r.BasicAuth()
			if !ok || user != s.config.Username || pass != s.config.Password {
				w.Header().Set("WWW-Authenticate", `Basic realm="LingVoice Admin"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

// --- API Handlers ---

type dashboardData struct {
	TotalSessions int             `json:"totalSessions"`
	ByProtocol    map[string]int  `json:"byProtocol"`
	Sessions      []sessionBrief  `json:"sessions"`
	Timestamp     time.Time       `json:"timestamp"`
}

type sessionBrief struct {
	ID       string `json:"id"`
	Protocol string `json:"protocol"`
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	sessions := s.manager.ListSessions()
	byProto := make(map[string]int)
	briefs := make([]sessionBrief, 0, len(sessions))
	for _, sess := range sessions {
		proto := string(sess.Protocol())
		byProto[proto]++
		briefs = append(briefs, sessionBrief{
			ID:       sess.ID(),
			Protocol: proto,
		})
	}

	s.writeJSON(w, http.StatusOK, dashboardData{
		TotalSessions: len(sessions),
		ByProtocol:    byProto,
		Sessions:      briefs,
		Timestamp:     time.Now(),
	})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET"})
		return
	}

	sessions := s.manager.ListSessions()
	list := make([]sessionBrief, 0, len(sessions))
	for _, sess := range sessions {
		list = append(list, sessionBrief{
			ID:       sess.ID(),
			Protocol: string(sess.Protocol()),
		})
	}
	s.writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleSessionAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, s.config.Path+"/api/sessions/")
	parts := strings.SplitN(path, "/", 2)
	sessionID := parts[0]

	if sessionID == "" {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing session id"})
		return
	}

	sess, ok := s.manager.GetSession(sessionID)
	if !ok {
		s.writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}

	if len(parts) == 1 {
		// GET session details or DELETE session
		switch r.Method {
		case http.MethodGet:
			s.writeJSON(w, http.StatusOK, sessionBrief{
				ID:       sess.ID(),
				Protocol: string(sess.Protocol()),
			})
		case http.MethodDelete:
			_ = s.manager.SendCommand(sessionID, common.ProtocolCommand{Type: common.CmdHangup})
			s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
		default:
			s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
		return
	}

	action := parts[1]
	switch action {
	case "hangup":
		_ = s.manager.SendCommand(sessionID, common.ProtocolCommand{Type: common.CmdHangup})
		s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	case "reject":
		_ = s.manager.SendCommand(sessionID, common.ProtocolCommand{Type: common.CmdReject, Reason: "admin"})
		s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	default:
		s.writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown action: " + action})
	}
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	// Serve the embedded admin HTML page
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(adminHTML))
}

func (s *Server) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func init() {
	_ = fmt.Sprintf // keep import if needed
}
