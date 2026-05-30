package anthropic

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

const (
	defaultBaseURL    = "https://api.anthropic.com/v1"
	defaultAPIVersion = "2023-06-01"
)

var errMissingAPIKey = errors.New("anthropic: api key is required")

// Config configures an Anthropic Messages API client.
type Config struct {
	APIKey       string
	BaseURL      string
	Model        string
	APIVersion   string
	HTTPClient   *http.Client
	DefaultTools []*schema.ToolInfo
	// MaxTokens is the default completion budget when a call omits WithMaxTokens.
	MaxTokens int
}

// ChatModel calls Anthropic /v1/messages .
type ChatModel struct {
	apiKey     string
	baseURL    string
	model      string
	apiVersion string
	maxTokens  int
	client     *http.Client
	tools      []*schema.ToolInfo
}

// NewChatModel creates a client.
func NewChatModel(cfg Config) (*ChatModel, error) {
	if cfg.APIKey == "" {
		return nil, errMissingAPIKey
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = defaultBaseURL
	}
	ver := cfg.APIVersion
	if ver == "" {
		ver = defaultAPIVersion
	}
	model := cfg.Model
	if model == "" {
		model = "claude-3-5-haiku-latest"
	}
	maxTok := cfg.MaxTokens
	if maxTok <= 0 {
		maxTok = 1024
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	return &ChatModel{
		apiKey:     cfg.APIKey,
		baseURL:    base,
		model:      model,
		apiVersion: ver,
		maxTokens:  maxTok,
		client:     client,
		tools:      cfg.DefaultTools,
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
