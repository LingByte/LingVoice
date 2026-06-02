package tools_test

import (
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/internal/tools"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestOpenAITool(t *testing.T) {
	if _, err := tools.OpenAITool(nil); err != nil {
		t.Fatal(err)
	}
	info := &schema.ToolInfo{
		Name: "add",
		Desc: "add numbers",
		Params: &schema.ToolParams{
			Fields: map[string]*schema.ParameterInfo{
				"a": {Type: schema.String, Desc: "first"},
			},
		},
	}
	out, err := tools.OpenAITool(info)
	if err != nil {
		t.Fatal(err)
	}
	if out["type"] != "function" {
		t.Fatalf("out=%v", out)
	}
	fn := out["function"].(map[string]any)
	if fn["name"] != "add" {
		t.Fatalf("fn=%v", fn)
	}
}

func TestOpenAITool_EmptyParams(t *testing.T) {
	out, err := tools.OpenAITool(&schema.ToolInfo{Name: "ping", Desc: "ping"})
	if err != nil {
		t.Fatal(err)
	}
	fn := out["function"].(map[string]any)
	params := fn["parameters"].(map[string]any)
	if params["type"] != "object" {
		t.Fatalf("params=%v", params)
	}
}

func TestAnthropicTool(t *testing.T) {
	if out, err := tools.AnthropicTool(nil); err != nil || out != nil {
		t.Fatalf("out=%v err=%v", out, err)
	}
	info := &schema.ToolInfo{Name: "search", Desc: "search web"}
	out, err := tools.AnthropicTool(info)
	if err != nil {
		t.Fatal(err)
	}
	if out["name"] != "search" {
		t.Fatalf("out=%v", out)
	}
}

func TestMergeTools_AndToolChoice(t *testing.T) {
	defaults := []*schema.ToolInfo{{Name: "a"}}
	perCall := []*schema.ToolInfo{{Name: "b"}}
	if got := tools.MergeTools(defaults, perCall); got[0].Name != "b" {
		t.Fatal("per-call wins")
	}
	if got := tools.MergeTools(defaults, nil); got[0].Name != "a" {
		t.Fatal("defaults")
	}

	if tools.OpenAIToolChoice(schema.ToolChoiceForbidden, true) != "none" {
		t.Fatal("openai forbidden")
	}
	if tools.OpenAIToolChoice(schema.ToolChoiceForced, true) != "required" {
		t.Fatal("openai forced")
	}
	if tools.OpenAIToolChoice(schema.ToolChoiceAllowed, true) != "auto" {
		t.Fatal("openai auto")
	}
	if tools.OpenAIToolChoice(schema.ToolChoiceAllowed, false) != nil {
		t.Fatal("no tools")
	}

	ac := tools.AnthropicToolChoice(schema.ToolChoiceForbidden, true)
	if ac["type"] != "none" {
		t.Fatalf("anthropic=%v", ac)
	}
	ac = tools.AnthropicToolChoice(schema.ToolChoiceForced, true)
	if ac["type"] != "any" {
		t.Fatalf("anthropic=%v", ac)
	}
}
