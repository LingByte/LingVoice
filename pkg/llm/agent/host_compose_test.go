package agent_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/agent"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestHostComposeAgent_Route(t *testing.T) {
	host, err := agent.NewHostComposeAgent(agent.HostComposeConfig{
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

	out, err = host.Generate(context.Background(), []*schema.Message{schema.UserMessage("please 翻译 hello")})
	if err != nil || out.Content != "from:translator" {
		t.Fatalf("translator out=%+v err=%v", out, err)
	}

	if host.Graph() == nil {
		t.Fatal("expected compiled graph")
	}
}
