package openai

import (
	"net/http"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

const defaultBaseURL = "https://api.openai.com/v1"

// Config configures an OpenAI-compatible Chat Completions client.
type Config struct {
	APIKey       string
	BaseURL      string
	Model        string
	HTTPClient   *http.Client
	DefaultTools []*schema.ToolInfo
}

// ChatModel calls OpenAI-compatible /chat/completions endpoints.
type ChatModel struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
	tools   []*schema.ToolInfo
}

// NewChatModel creates a client. BaseURL defaults to https://api.openai.com/v1 .
func NewChatModel(cfg Config) (*ChatModel, error) {
	if cfg.APIKey == "" {
		return nil, errMissingAPIKey
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = defaultBaseURL
	}
	model := cfg.Model
	if model == "" {
		model = "gpt-4o-mini"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	return &ChatModel{
		apiKey:  cfg.APIKey,
		baseURL: base,
		model:   model,
		client:  client,
		tools:   cfg.DefaultTools,
	}, nil
}

func (m *ChatModel) clone() *ChatModel {
	if m == nil {
		return nil
	}
	c := *m
	if len(m.tools) > 0 {
		c.tools = append([]*schema.ToolInfo(nil), m.tools...)
	}
	return &c
}
