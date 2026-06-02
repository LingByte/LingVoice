package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/LingByte/LingVoice/pkg/llm/adk"
	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// HostComposeState is typed local state for compose-based host routing (Eino host compose subset).
type HostComposeState struct {
	Messages          []*schema.Message
	SelectedAgent     string
	IsMultipleIntents bool
}

// HostComposeConfig configures a compose-graph host agent.
type HostComposeConfig struct {
	Name          string
	Agents        map[string]Agent
	Route         func(ctx context.Context, messages []*schema.Message) (string, bool, error)
	Default       string
	GraphName     string
}

// HostComposeAgent routes via a compiled compose graph with ProcessState.
type HostComposeAgent struct {
	name    string
	graph   *compose.CompiledGraph
	agents  map[string]Agent
	defaultName string
}

// NewHostComposeAgent builds a compose-graph multi-agent host.
func NewHostComposeAgent(cfg HostComposeConfig) (*HostComposeAgent, error) {
	if len(cfg.Agents) == 0 {
		return nil, composeErr("host compose requires at least one agent")
	}
	name := cfg.Name
	if name == "" {
		name = "host-compose"
	}
	graphName := cfg.GraphName
	if graphName == "" {
		graphName = name + "-graph"
	}
	def := cfg.Default
	if def == "" {
		for k := range cfg.Agents {
			def = k
			break
		}
	}
	route := cfg.Route
	if route == nil {
		route = hostKeywordRoute(def)
	}

	g := compose.NewGraphWithOptions(graphName, compose.WithGenLocalState(func(_ context.Context) *HostComposeState {
		return &HostComposeState{}
	}))

	if err := g.AddLambdaNode("classify", func(ctx context.Context, st *compose.GraphState) error {
		return compose.ProcessState(ctx, func(ctx context.Context, hs *HostComposeState) error {
			hs.Messages = append([]*schema.Message(nil), st.Messages...)
			key, multi, err := route(ctx, hs.Messages)
			if err != nil {
				return err
			}
			hs.SelectedAgent = key
			hs.IsMultipleIntents = multi
			if st.Vars == nil {
				st.Vars = map[string]any{}
			}
			st.Vars["selected_agent"] = key
			st.Vars["multiple_intents"] = multi
			return nil
		})
	}); err != nil {
		return nil, err
	}

	allowed := map[string]bool{}
	for agentName := range cfg.Agents {
		nodeName := "agent:" + agentName
		allowed[nodeName] = true
		ag := cfg.Agents[agentName]
		if err := g.AddLambdaNode(nodeName, hostAgentNode(ag)); err != nil {
			return nil, err
		}
		if err := g.AddEdge(nodeName, compose.END); err != nil {
			return nil, err
		}
	}

	if err := g.AddBranch("classify", func(ctx context.Context, st *compose.GraphState) (string, error) {
		key, _ := st.Vars["selected_agent"].(string)
		if key == "" {
			key = def
		}
		if _, ok := cfg.Agents[key]; !ok {
			key = def
		}
		return "agent:" + key, nil
	}, allowed); err != nil {
		return nil, err
	}
	if err := g.AddEdge(compose.START, "classify"); err != nil {
		return nil, err
	}

	cg, err := g.Compile()
	if err != nil {
		return nil, err
	}
	return &HostComposeAgent{
		name:        name,
		graph:       cg,
		agents:      cfg.Agents,
		defaultName: def,
	}, nil
}

func hostAgentNode(ag Agent) compose.NodeFunc {
	return func(ctx context.Context, st *compose.GraphState) error {
		if ag == nil {
			return fmt.Errorf("host compose: nil sub-agent")
		}
		out, err := ag.Generate(ctx, st.Messages)
		if err != nil {
			return err
		}
		st.LastOutput = out
		if out != nil {
			st.Messages = append(st.Messages, out)
		}
		return compose.ProcessState(ctx, func(_ context.Context, hs *HostComposeState) error {
			if out != nil {
				hs.Messages = append(hs.Messages, out)
			}
			return nil
		})
	}
}

func hostKeywordRoute(defaultAgent string) func(context.Context, []*schema.Message) (string, bool, error) {
	return func(_ context.Context, messages []*schema.Message) (string, bool, error) {
		text := lastUserText(messages)
		lower := strings.ToLower(text)
		multi := strings.Contains(lower, " and ") || strings.Contains(text, "并且")
		switch {
		case strings.Contains(lower, "math") || strings.Contains(lower, "计算") || strings.Contains(lower, "add"):
			return "calculator", multi, nil
		case strings.Contains(lower, "translate") || strings.Contains(lower, "翻译"):
			return "translator", multi, nil
		default:
			return defaultAgent, multi, nil
		}
	}
}

// Generate runs the compose host graph.
func (h *HostComposeAgent) Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	if h == nil || h.graph == nil {
		return nil, nil
	}
	st, _, err := h.graph.Invoke(ctx, messages)
	if err != nil {
		return nil, err
	}
	return compose.FinalMessage(st), nil
}

// AsADK wraps the host as an ADK agent.
func (h *HostComposeAgent) AsADK() adk.Agent {
	return adk.NewGenerateAdapter(h.name, "compose-graph host", h)
}

// Graph returns the compiled graph for introspection or checkpoint runs.
func (h *HostComposeAgent) Graph() *compose.CompiledGraph {
	if h == nil {
		return nil
	}
	return h.graph
}
