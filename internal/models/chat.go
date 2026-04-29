package models

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ChatSession 聊天会话表
type ChatSession struct {
	ID           string     `json:"id" gorm:"primaryKey;type:varchar(64)"` // 雪花算法生成的ID
	UserID       string     `json:"user_id" gorm:"type:varchar(64);not null;index"`
	Title        string     `json:"title" gorm:"type:varchar(255)"`
	Provider     string     `json:"provider" gorm:"type:varchar(50);not null"` // LLM提供商
	Model        string     `json:"model" gorm:"type:varchar(100);not null"`
	SystemPrompt string     `json:"system_prompt" gorm:"type:text"`
	Status       string     `json:"status" gorm:"type:varchar(20);default:'active'"` // active, archived, deleted
	CreatedAt    time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt    time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty" gorm:"index"`
}

// ChatMessage 聊天消息表
type ChatMessage struct {
	ID         string     `json:"id" gorm:"primaryKey;type:varchar(64)"` // 雪花算法生成的ID
	SessionID  string     `json:"session_id" gorm:"type:varchar(64);not null;index"`
	Role       string     `json:"role" gorm:"type:varchar(20);not null"` // user, assistant, system
	Content    string     `json:"content" gorm:"type:text;not null"`
	TokenCount int        `json:"token_count" gorm:"default:0"`
	Model      string     `json:"model" gorm:"type:varchar(100);not null"`
	Provider   string     `json:"provider" gorm:"type:varchar(50);not null"`
	RequestID  string     `json:"request_id" gorm:"type:varchar(64);index"` // 关联到LLMUsage
	CreatedAt  time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt  time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty" gorm:"index"`
}

// LLMUsage LLM用量统计表
type LLMUsage struct {
	ID              string  `json:"id" gorm:"primaryKey;type:varchar(64)"`                   // 雪花算法生成的ID
	RequestID       string  `json:"request_id" gorm:"type:varchar(64);uniqueIndex;not null"` // 唯一请求ID（可与上游 id 对齐）
	UserID          string  `json:"user_id" gorm:"type:varchar(64);index"`
	Provider        string  `json:"provider" gorm:"type:varchar(50);not null;index"`
	Model           string  `json:"model" gorm:"type:varchar(100);not null;index"`
	BaseURL         string  `json:"base_url" gorm:"type:varchar(255)"`             // API基础URL
	RequestType     string  `json:"request_type" gorm:"type:varchar(20);not null"` // query, query_stream, rewrite, expand
	InputTokens     int     `json:"input_tokens" gorm:"default:0"`
	OutputTokens    int     `json:"output_tokens" gorm:"default:0"`
	TotalTokens     int     `json:"total_tokens" gorm:"default:0"`
	QuotaDelta      int     `json:"quota_delta" gorm:"default:0"`        // 本次从凭证扣除的额度单位（倍率/按次/按 token 汇总）
	LatencyMs       int64   `json:"latency_ms" gorm:"default:0"`         // 总延迟（毫秒）
	TTFTMs          int64   `json:"ttft_ms" gorm:"default:0"`            // Time To First Token（毫秒）
	TPS             float64 `json:"tps" gorm:"default:0"`                // Tokens Per Second
	QueueTimeMs     int64   `json:"queue_time_ms" gorm:"default:0"`      // 排队时间（毫秒）
	RequestContent  string  `json:"request_content" gorm:"type:text"`    // 请求内容（JSON格式）
	ResponseContent string  `json:"response_content" gorm:"type:text"`   // 响应内容（JSON格式）
	UserAgent       string  `json:"user_agent" gorm:"type:varchar(500)"` // 用户代理
	IPAddress       string  `json:"ip_address" gorm:"type:varchar(45)"`  // 客户端IP地址
	StatusCode      int     `json:"status_code" gorm:"default:200"`      // HTTP响应码
	Success         bool    `json:"success" gorm:"default:true"`
	ErrorCode       string  `json:"error_code" gorm:"type:varchar(50)"`
	ErrorMessage    string  `json:"error_message" gorm:"type:text"`
	// ChannelID 实际完成请求的上游 llm_channels.id（轮询/重试后命中的渠道）。
	ChannelID       int                     `json:"channel_id" gorm:"index;default:0"`
	ChannelAttempts LLMUsageChannelAttempts `json:"channel_attempts" gorm:"column:channel_attempts;type:json"`
	RequestedAt     time.Time               `json:"requested_at" gorm:"not null;index"` // 请求开始时间
	StartedAt       time.Time               `json:"started_at" gorm:"index"`            // 实际处理开始时间
	FirstTokenAt    time.Time               `json:"first_token_at" gorm:"index"`        // 首个token时间
	CompletedAt     time.Time               `json:"completed_at"`                       // 请求完成时间
	CreatedAt       time.Time               `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt       time.Time               `json:"updated_at" gorm:"autoUpdateTime"`
}

type AgentRun struct {
	ID            string    `json:"id" gorm:"primaryKey;type:varchar(64)"`
	SessionID     string    `json:"session_id" gorm:"type:varchar(64);index;not null"`
	UserID        string    `json:"user_id" gorm:"type:varchar(64);index;not null"`
	Goal          string    `json:"goal" gorm:"type:text;not null"`
	Status        string    `json:"status" gorm:"type:varchar(20);index;not null"` // queued/running/succeeded/failed/cancelled
	Phase         string    `json:"phase" gorm:"type:varchar(32);index"`           // planning/executing/reflecting
	PlanJSON      string    `json:"plan_json" gorm:"type:longtext"`
	ResultText    string    `json:"result_text" gorm:"type:longtext"`
	ErrorMessage  string    `json:"error_message" gorm:"type:text"`
	TotalSteps    int       `json:"total_steps" gorm:"default:0"`
	TotalTokens   int       `json:"total_tokens" gorm:"default:0"`
	MaxSteps      int       `json:"max_steps" gorm:"default:0"`
	MaxCostTokens int       `json:"max_cost_tokens" gorm:"default:0"`
	MaxDurationMs int64     `json:"max_duration_ms" gorm:"default:0"`
	StartedAt     time.Time `json:"started_at" gorm:"index"`
	CompletedAt   time.Time `json:"completed_at"`
	CreatedAt     time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt     time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

type AgentStep struct {
	ID           string    `json:"id" gorm:"primaryKey;type:varchar(64)"`
	RunID        string    `json:"run_id" gorm:"type:varchar(64);index;not null"`
	StepID       string    `json:"step_id" gorm:"type:varchar(64);index;not null"`
	TaskID       string    `json:"task_id" gorm:"type:varchar(64);index"`
	Title        string    `json:"title" gorm:"type:varchar(255)"`
	Instruction  string    `json:"instruction" gorm:"type:text"`
	Status       string    `json:"status" gorm:"type:varchar(20);index;not null"` // queued/running/waiting_tool/succeeded/failed/cancelled
	Model        string    `json:"model" gorm:"type:varchar(100)"`
	InputJSON    string    `json:"input_json" gorm:"type:longtext"`
	OutputText   string    `json:"output_text" gorm:"type:longtext"`
	ErrorMessage string    `json:"error_message" gorm:"type:text"`
	Feedback     string    `json:"feedback" gorm:"type:text"`
	Attempts     int       `json:"attempts" gorm:"default:0"`
	InputTokens  int       `json:"input_tokens" gorm:"default:0"`
	OutputTokens int       `json:"output_tokens" gorm:"default:0"`
	TotalTokens  int       `json:"total_tokens" gorm:"default:0"`
	LatencyMs    int64     `json:"latency_ms" gorm:"default:0"`
	StartedAt    time.Time `json:"started_at" gorm:"index"`
	CompletedAt  time.Time `json:"completed_at"`
	CreatedAt    time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt    time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// TableName 指定表名
func (ChatSession) TableName() string {
	return "chat_sessions"
}

func (ChatMessage) TableName() string {
	return "chat_messages"
}

func (LLMUsage) TableName() string {
	return "llm_usage"
}

func (AgentRun) TableName() string {
	return "agent_runs"
}

func (AgentStep) TableName() string {
	return "agent_steps"
}

// GetChatSessionOwned returns the session if it belongs to userID and is not soft-deleted.
func GetChatSessionOwned(db *gorm.DB, userID, sessionID string) (*ChatSession, error) {
	if db == nil {
		return nil, errNilDB
	}
	var row ChatSession
	err := db.Where("id = ? AND user_id = ? AND (deleted_at IS NULL)", sessionID, userID).First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ListChatSessionsForUser returns recent sessions for the user (non-deleted).
func ListChatSessionsForUser(db *gorm.DB, userID string, limit int) ([]ChatSession, error) {
	if db == nil {
		return nil, errNilDB
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var rows []ChatSession
	err := db.Where("user_id = ? AND (deleted_at IS NULL)", userID).
		Order("updated_at DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

// ChatSessionAPIRow matches web/src/api/chat.ts ChatSessionRow.
type ChatSessionAPIRow struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	SystemPrompt string `json:"system_prompt"`
	Status       string `json:"status"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// ChatSessionToAPIRow maps a DB row to the API list/detail shape.
func ChatSessionToAPIRow(s *ChatSession) ChatSessionAPIRow {
	if s == nil {
		return ChatSessionAPIRow{}
	}
	return ChatSessionAPIRow{
		ID:           s.ID,
		Title:        s.Title,
		Model:        s.Model,
		Provider:     s.Provider,
		SystemPrompt: s.SystemPrompt,
		Status:       s.Status,
		CreatedAt:    s.CreatedAt.UnixMilli(),
		UpdatedAt:    s.UpdatedAt.UnixMilli(),
	}
}

// CreateChatSession inserts a new session row.
func CreateChatSession(db *gorm.DB, row *ChatSession) error {
	if db == nil {
		return errNilDB
	}
	if row == nil {
		return errors.New("models: nil chat session")
	}
	return db.Create(row).Error
}

// UpdateChatSessionTitle updates title and bumps updated_at for an owned session.
func UpdateChatSessionTitle(db *gorm.DB, userID, sessionID, title string) error {
	if db == nil {
		return errNilDB
	}
	title = strings.TrimSpace(title)
	return db.Model(&ChatSession{}).Where("id = ? AND user_id = ?", sessionID, userID).
		Updates(map[string]interface{}{
			"title":      title,
			"updated_at": time.Now(),
		}).Error
}

// SoftDeleteChatSession marks a session deleted for an owner.
func SoftDeleteChatSession(db *gorm.DB, userID, sessionID string) error {
	if db == nil {
		return errNilDB
	}
	now := time.Now()
	return db.Model(&ChatSession{}).Where("id = ? AND user_id = ?", sessionID, userID).
		Updates(map[string]interface{}{
			"deleted_at": now,
			"status":     "deleted",
			"updated_at": now,
		}).Error
}

// ListChatMessagesForSession returns messages for a session (non-deleted), oldest first.
func ListChatMessagesForSession(db *gorm.DB, sessionID string, limit int) ([]ChatMessage, error) {
	if db == nil {
		return nil, errNilDB
	}
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	var rows []ChatMessage
	err := db.Where("session_id = ? AND (deleted_at IS NULL)", sessionID).
		Order("created_at ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

// ChatMessageAPIRow matches the chat messages API payload.
type ChatMessageAPIRow struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	Role       string `json:"role"`
	Content    string `json:"content"`
	TokenCount int    `json:"token_count"`
	Model      string `json:"model"`
	Provider   string `json:"provider"`
	RequestID  string `json:"request_id"`
	CreatedAt  int64  `json:"created_at"`
}

// ChatMessageToAPIRow maps a DB message to API JSON.
func ChatMessageToAPIRow(m *ChatMessage) ChatMessageAPIRow {
	if m == nil {
		return ChatMessageAPIRow{}
	}
	return ChatMessageAPIRow{
		ID:         m.ID,
		SessionID:  m.SessionID,
		Role:       m.Role,
		Content:    m.Content,
		TokenCount: m.TokenCount,
		Model:      m.Model,
		Provider:   m.Provider,
		RequestID:  m.RequestID,
		CreatedAt:  m.CreatedAt.UnixMilli(),
	}
}

// CreateChatMessage inserts a message and returns the created row (timestamps from DB).
func CreateChatMessage(db *gorm.DB, msg *ChatMessage) error {
	if db == nil {
		return errNilDB
	}
	if msg == nil {
		return errors.New("models: nil chat message")
	}
	return db.Create(msg).Error
}

// TouchChatSessionUpdatedAt bumps updated_at for a session (e.g. after new message).
func TouchChatSessionUpdatedAt(db *gorm.DB, sessionID string) error {
	if db == nil {
		return errNilDB
	}
	return db.Model(&ChatSession{}).Where("id = ?", sessionID).Update("updated_at", time.Now()).Error
}
