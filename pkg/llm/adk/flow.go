package adk

import (
	"context"
	"fmt"
)

// SubAgentsConfig registers named sub-agents on a parent (Eino adk.SetSubAgents subset).
type SubAgentsConfig struct {
	Parent      Agent
	SubAgents   map[string]Agent
	Descriptions map[string]string
}

// SetSubAgents validates and returns a map of sub-agents keyed by name.
func SetSubAgents(cfg SubAgentsConfig) (map[string]Agent, error) {
	if cfg.Parent == nil {
		return nil, fmt.Errorf("adk: nil parent agent")
	}
	if len(cfg.SubAgents) == 0 {
		return nil, fmt.Errorf("adk: no sub-agents")
	}
	out := make(map[string]Agent, len(cfg.SubAgents))
	for name, ag := range cfg.SubAgents {
		if name == "" {
			return nil, fmt.Errorf("adk: empty sub-agent name")
		}
		if ag == nil {
			return nil, fmt.Errorf("adk: nil sub-agent %q", name)
		}
		out[name] = ag
	}
	return out, nil
}

// SubAgentNames returns sorted-stable names from a sub-agent map.
func SubAgentNames(m map[string]Agent) []string {
	if len(m) == 0 {
		return nil
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	return names
}

// LookupSubAgent finds a sub-agent by name with optional default.
func LookupSubAgent(m map[string]Agent, name, defaultName string) Agent {
	if ag, ok := m[name]; ok && ag != nil {
		return ag
	}
	if ag, ok := m[defaultName]; ok {
		return ag
	}
	for _, ag := range m {
		return ag
	}
	return nil
}

// ParentName returns the parent agent name.
func ParentName(ctx context.Context, parent Agent) string {
	if parent == nil {
		return ""
	}
	return parent.Name(ctx)
}
