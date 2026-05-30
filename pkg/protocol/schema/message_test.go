package schema

import (
	"encoding/json"
	"testing"
)

func TestMessageBuilders(t *testing.T) {
	sys := SystemMessage("you are helpful")
	if sys.Role != System || sys.Content != "you are helpful" {
		t.Fatalf("system message: %+v", sys)
	}
	tool := ToolMessage(`{"ok":true}`, "call-1", WithToolName("lookup"))
	if tool.Role != Tool || tool.ToolCallID != "call-1" || tool.ToolName != "lookup" {
		t.Fatalf("tool message: %+v", tool)
	}
}

func TestConcatMessages_streamingText(t *testing.T) {
	msgs := []*Message{
		{Role: Assistant, Content: "hel"},
		{Role: Assistant, Content: "lo"},
	}
	out, err := ConcatMessages(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "hello" {
		t.Fatalf("content=%q", out.Content)
	}
}

func TestConcatMessages_toolCalls(t *testing.T) {
	idx0, idx1 := 0, 1
	msgs := []*Message{
		{Role: Assistant, ToolCalls: []ToolCall{{Index: &idx0, ID: "a", Type: "function", Function: FunctionCall{Name: "f", Arguments: `{"x":`}}}},
		{Role: Assistant, ToolCalls: []ToolCall{{Index: &idx0, Function: FunctionCall{Arguments: `1}`}}}},
		{Role: Assistant, ToolCalls: []ToolCall{{Index: &idx1, ID: "b", Type: "function", Function: FunctionCall{Name: "g", Arguments: `{}`}}}},
	}
	out, err := ConcatMessages(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 2 {
		t.Fatalf("tool calls=%d", len(out.ToolCalls))
	}
	if out.ToolCalls[0].Function.Arguments != `{"x":1}` {
		t.Fatalf("args0=%q", out.ToolCalls[0].Function.Arguments)
	}
}

func TestToolInfo_ParamsJSONSchema(t *testing.T) {
	info := &ToolInfo{
		Name: "weather",
		Desc: "lookup weather",
		Params: &ToolParams{
			Fields: map[string]*ParameterInfo{
				"city": {Type: String, Desc: "city name", Required: true},
			},
		},
	}
	raw, err := info.ParamsJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "object" {
		t.Fatalf("schema type=%v", m["type"])
	}
}

func TestCollectMessages(t *testing.T) {
	sr := StreamReaderFromSlice([]*Message{
		AssistantMessage("a", nil),
		AssistantMessage("b", nil),
	})
	out, err := CollectMessages(sr)
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "ab" {
		t.Fatalf("content=%q", out.Content)
	}
}

func TestRoleValid(t *testing.T) {
	if !User.Valid() || Assistant.Valid() == false {
		t.Fatal("roles should be valid")
	}
	if RoleType("nope").Valid() {
		t.Fatal("unknown role should be invalid")
	}
}
