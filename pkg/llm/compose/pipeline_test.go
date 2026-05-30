package compose_test

import (
	"context"
	"io"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestPipeline_Branch(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{"sum":2}`, nil
	})
	loop, err := compose.NewToolLoop(context.Background(), compose.ToolLoopConfig{
		Model: &mockToolModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := compose.NewPipeline(compose.PipelineConfig{
		Branch: &compose.BranchStep{
			Select: func(_ context.Context, st *compose.State) (string, error) {
				return st.Vars["path"].(string), nil
			},
			Steps: map[string]compose.Step{
				"loop": compose.ToolLoopStage{Loop: loop},
				"skip": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
					st.LastOutput = schema.AssistantMessage("skipped", nil)
					return nil
				}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := pipe.Run(context.Background(), nil, map[string]any{"path": "loop"})
	if err != nil {
		t.Fatal(err)
	}
	if st.LastOutput == nil {
		t.Fatal("nil output")
	}
}

func TestPipeline_StreamFrames(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{"sum":2}`, nil
	})
	loop, err := compose.NewToolLoop(context.Background(), compose.ToolLoopConfig{
		Model: &mockToolModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := compose.NewPipeline(compose.PipelineConfig{
		Name: "stream-pipe",
		Branch: &compose.BranchStep{
			Select: func(_ context.Context, st *compose.State) (string, error) {
				return st.Vars["path"].(string), nil
			},
			Steps: map[string]compose.Step{
				"loop": compose.ToolLoopStreamStage{Loop: loop, Stage: "react"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := pipe.StreamFrames(context.Background(), nil, map[string]any{"path": "loop"})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	var stages []string
	for {
		f, err := sr.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if f != nil && f.Stage != "" {
			stages = append(stages, f.Stage)
		}
		if f != nil && f.Done && f.Stage == "stream-pipe" {
			break
		}
	}
	if len(stages) < 2 {
		t.Fatalf("stages=%v", stages)
	}
}
