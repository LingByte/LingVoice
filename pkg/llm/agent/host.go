package agent

import (
	"context"
	"strings"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// Agent is a minimal generate-capable agent (Eino adk.Agent subset).
type Agent interface {
	Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error)
}

// HostConfig routes messages to named sub-agents (Eino multi-agent host subset).
type HostConfig struct {
	Name    string
	Agents  map[string]Agent
	Route   func(ctx context.Context, messages []*schema.Message) (string, error)
	Default string
}

// HostAgent picks a sub-agent and delegates Generate.
type HostAgent struct {
	name    string
	agents  map[string]Agent
	route   func(ctx context.Context, messages []*schema.Message) (string, error)
	defaultName string
}

// NewHostAgent builds a routing host.
func NewHostAgent(cfg HostConfig) (*HostAgent, error) {
	if len(cfg.Agents) == 0 {
		return nil, composeErr("host requires at least one agent")
	}
	route := cfg.Route
	if route == nil {
		route = keywordRoute(cfg.Default)
	}
	name := cfg.Name
	if name == "" {
		name = "host"
	}
	def := cfg.Default
	if def == "" {
		for k := range cfg.Agents {
			def = k
			break
		}
	}
	return &HostAgent{
		name:        name,
		agents:      cfg.Agents,
		route:       route,
		defaultName: def,
	}, nil
}

// Generate routes to a sub-agent and returns its reply.
func (h *HostAgent) Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	if h == nil {
		return nil, nil
	}
	key, err := h.route(ctx, messages)
	if err != nil {
		return nil, err
	}
	ag, ok := h.agents[key]
	if !ok {
		ag = h.agents[h.defaultName]
	}
	if ag == nil {
		return schema.AssistantMessage("no agent matched", nil), nil
	}
	return ag.Generate(ctx, messages)
}

func keywordRoute(defaultAgent string) func(context.Context, []*schema.Message) (string, error) {
	return func(_ context.Context, messages []*schema.Message) (string, error) {
		text := lastUserText(messages)
		lower := strings.ToLower(text)
		switch {
		case strings.Contains(lower, "math") || strings.Contains(lower, "计算") || strings.Contains(lower, "add"):
			return "calculator", nil
		case strings.Contains(lower, "translate") || strings.Contains(lower, "翻译"):
			return "translator", nil
		default:
			return defaultAgent, nil
		}
	}
}

func lastUserText(messages []*schema.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m != nil && m.Role == schema.User {
			return m.PlainText()
		}
	}
	return ""
}
