package compose_test

import (
	"context"
	"io"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestCompiledGraph_StreamFrames(t *testing.T) {
	add, err := tool.InferTool("add", "add", func(_ context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (map[string]int, error) {
		return map[string]int{"sum": in.A + in.B}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	g, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model: &mockToolModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !g.HasReActRuntime() {
		t.Fatal("expected react runtime")
	}
	sr, err := g.StreamFrames(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()

	var nodes []string
	for {
		f, err := sr.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if f != nil && f.Node != "" {
			nodes = append(nodes, f.Node)
		}
		if f != nil && f.Done {
			break
		}
	}
	if len(nodes) < 2 {
		t.Fatalf("nodes=%v", nodes)
	}
	if nodes[0] != compose.NodeChatModel {
		t.Fatalf("first node=%q", nodes[0])
	}
}

func TestCompiledGraph_RunsInState(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{"sum":2}`, nil
	})
	g, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model: &mockToolModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := g.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	runs := compose.GraphRunsFromState(st)
	if len(runs) < 2 {
		t.Fatalf("runs=%d", len(runs))
	}
}
