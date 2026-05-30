package compose_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestPipeline_TemplateAndErrors(t *testing.T) {
	tpl := prompt.FromMessages(schema.FormatFString, schema.SystemMessage("sys {x}"), schema.UserMessage("{q}"))
	pipe, err := compose.NewPipeline(compose.PipelineConfig{
		Name:     "tpl-pipe",
		Template: tpl,
		Steps: []compose.Step{compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
			st.LastOutput = schema.AssistantMessage("done", nil)
			return nil
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := pipe.Run(context.Background(), nil, map[string]any{"x": "v", "q": "hi"})
	if err != nil || st.LastOutput == nil {
		t.Fatalf("err=%v st=%+v", err, st)
	}
	if _, err := compose.NewPipeline(compose.PipelineConfig{}); err == nil {
		t.Fatal("expected empty pipeline error")
	}
	var nilPipe *compose.Pipeline
	if _, err := nilPipe.Run(context.Background(), nil, nil); err == nil {
		t.Fatal("expected nil pipeline error")
	}
}

func TestPipeline_StreamFramesTemplateBranch(t *testing.T) {
	tpl := prompt.FromMessages(schema.FormatFString, schema.UserMessage("hello {name}"))
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{}`, nil
	})
	loop, err := compose.NewToolLoop(context.Background(), compose.ToolLoopConfig{
		Model: &mockStreamModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := compose.NewPipeline(compose.PipelineConfig{
		Name:     "full-stream",
		Template: tpl,
		Branch: &compose.BranchStep{
			Select: func(_ context.Context, st *compose.State) (string, error) {
				return st.Vars["path"].(string), nil
			},
			Steps: map[string]compose.Step{
				"react": compose.ToolLoopStreamStage{Loop: loop, Stage: "react"},
				"skip": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
					st.LastOutput = schema.AssistantMessage("skip", nil)
					return nil
				}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := pipe.StreamFrames(context.Background(), nil, map[string]any{"name": "world", "path": "react"})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	seen := map[string]bool{}
	for {
		f, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		if f != nil && f.Stage != "" {
			seen[f.Stage] = true
		}
		if f != nil && f.Done && f.Stage == "full-stream" {
			break
		}
	}
	for _, want := range []string{"template", "branch", "react", "full-stream"} {
		if !seen[want] {
			t.Fatalf("missing stage %q seen=%v", want, seen)
		}
	}

	skipSR, err := pipe.StreamFrames(context.Background(), nil, map[string]any{"name": "x", "path": "skip"})
	if err != nil {
		t.Fatal(err)
	}
	defer skipSR.Close()
	for {
		f, err := skipSR.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		if f != nil && f.Done && f.Stage == "full-stream" {
			break
		}
	}
}

func TestStreamChainBranch_AddToolLoop(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{}`, nil
	})
	loop, err := compose.NewToolLoop(context.Background(), compose.ToolLoopConfig{
		Model: &mockToolModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := compose.NewStreamChainBranch(func(_ context.Context, _ *compose.State) (string, error) {
		return "loop", nil
	}).AddToolLoop("loop", loop).Default("loop")
	step := b.Step()
	if step.Steps["loop"] == nil {
		t.Fatal("expected tool loop step")
	}
}

func TestTagStreamFrames_Nil(t *testing.T) {
	if _, err := compose.TagStreamFrames(nil, "s"); err == nil {
		t.Fatal("expected nil reader error")
	}
}
