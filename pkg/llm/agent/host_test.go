package agent_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/agent"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type stubAgent struct {
	name string
}

func (s stubAgent) Generate(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
	return schema.AssistantMessage("from:"+s.name, nil), nil
}

func TestHostAgent_Route(t *testing.T) {
	host, err := agent.NewHostAgent(agent.HostConfig{
		Agents: map[string]agent.Agent{
			"calculator": stubAgent{name: "calculator"},
			"translator": stubAgent{name: "translator"},
		},
		Default: "calculator",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := host.Generate(context.Background(), []*schema.Message{
		schema.UserMessage("please 计算 1+2"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || out.Content != "from:calculator" {
		t.Fatalf("out=%+v", out)
	}
}

func TestHostAgent_TranslatorAndDefault(t *testing.T) {
	host, err := agent.NewHostAgent(agent.HostConfig{
		Agents: map[string]agent.Agent{
			"calculator": stubAgent{name: "calculator"},
			"translator": stubAgent{name: "translator"},
		},
		Default: "calculator",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := host.Generate(context.Background(), []*schema.Message{schema.UserMessage("please 翻译 hello")})
	if err != nil || out.Content != "from:translator" {
		t.Fatalf("err=%v out=%+v", err, out)
	}
	out, err = host.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")})
	if err != nil || out.Content != "from:calculator" {
		t.Fatalf("default err=%v out=%+v", err, out)
	}
}

func TestHostAgent_CustomRouteAndErrors(t *testing.T) {
	if _, err := agent.NewHostAgent(agent.HostConfig{}); err == nil {
		t.Fatal("expected no agents error")
	}
	host, err := agent.NewHostAgent(agent.HostConfig{
		Agents: map[string]agent.Agent{"only": stubAgent{name: "only"}},
		Route: func(_ context.Context, _ []*schema.Message) (string, error) {
			return "", context.Canceled
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Generate(context.Background(), nil); err == nil {
		t.Fatal("expected route error")
	}
	host2, err := agent.NewHostAgent(agent.HostConfig{
		Agents: map[string]agent.Agent{"only": stubAgent{name: "only"}},
		Route:  func(_ context.Context, _ []*schema.Message) (string, error) { return "missing", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := host2.Generate(context.Background(), []*schema.Message{schema.UserMessage("x")})
	if err != nil || out.Content != "from:only" {
		t.Fatalf("fallback err=%v out=%+v", err, out)
	}
	var nilHost *agent.HostAgent
	if out, err := nilHost.Generate(context.Background(), nil); err != nil || out != nil {
		t.Fatalf("nil host out=%v err=%v", out, err)
	}
}
