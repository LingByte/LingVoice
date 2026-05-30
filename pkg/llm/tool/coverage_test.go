package tool_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/internal/core"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestCollectInfos(t *testing.T) {
	raw := tool.NewFuncTool(&schema.ToolInfo{Name: "ping", Desc: "ping"}, func(_ context.Context, _ string) (string, error) {
		return "pong", nil
	})
	infos, err := tool.CollectInfos(context.Background(), []tool.InvokableTool{raw})
	if err != nil || len(infos) != 1 || infos[0].Name != "ping" {
		t.Fatalf("err=%v infos=%v", err, infos)
	}
	_, err = tool.CollectInfos(context.Background(), []tool.InvokableTool{nil})
	if err == nil {
		t.Fatal("expected nil tool error")
	}
}

func TestInferEnhancedTool_Basic(t *testing.T) {
	type in struct {
		City string `json:"city"`
	}
	et, err := tool.InferEnhancedTool("weather", "forecast", func(_ context.Context, input in) (*schema.ToolResult, error) {
		return &schema.ToolResult{Text: input.City}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := et.Info(context.Background())
	if err != nil || info.Name != "weather" {
		t.Fatalf("info err=%v name=%q", err, info.Name)
	}
	r, err := et.EnhancedInvokableRun(context.Background(), `{"city":"Paris"}`)
	if err != nil || r.Text != "Paris" {
		t.Fatalf("err=%v r=%v", err, r)
	}
	inv := tool.AsInvokable(et)
	out, err := inv.InvokableRun(context.Background(), `{"city":"Paris"}`)
	if err != nil || out != "Paris" {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

func TestInferEnhancedTool_OutputVariants(t *testing.T) {
	strTool, err := tool.InferEnhancedTool("s", "", func(_ context.Context, _ struct{}) (string, error) {
		return "plain", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := strTool.EnhancedInvokableRun(context.Background(), `{}`)
	if err != nil || r.Text != "plain" {
		t.Fatalf("err=%v r=%v", err, r)
	}

	type payload struct {
		N int `json:"n"`
	}
	jsonTool, err := tool.InferEnhancedTool("j", "", func(_ context.Context, _ struct{}) (payload, error) {
		return payload{N: 7}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := jsonTool.EnhancedInvokableRun(context.Background(), `{}`)
	if err != nil || r2.Text != `{"n":7}` {
		t.Fatalf("err=%v r=%v", err, r2)
	}
}

func TestStructToToolParams_RichTypes(t *testing.T) {
	type nested struct {
		Flag bool `json:"flag"`
	}
	type rich struct {
		Tags   []string          `json:"tags"`
		Meta   nested            `json:"meta"`
		Score  float64           `json:"score"`
		Labels map[string]string `json:"labels"`
	}
	params, err := tool.StructToToolParams[rich]()
	if err != nil || params == nil || len(params.JSONSchema) == 0 {
		t.Fatalf("err=%v params=%v", err, params)
	}
}

func TestToolInterruptHelpers(t *testing.T) {
	ctx := context.Background()
	if err := tool.Interrupt(ctx, "pause"); !core.IsInterrupt(err) {
		t.Fatalf("interrupt err=%v", err)
	}
	ctx = core.WithResumeData(ctx, map[string]any{"id1": "go"})
	ctx = tool.SyncResumeFromCompose(ctx)
	was, _, state := tool.GetInterruptState[string](tool.WithToolResume(ctx, "id1", "info", "saved"))
	if !was || state != "saved" {
		t.Fatalf("was=%v state=%q", was, state)
	}
	target, has, data := tool.GetResumeContext[string](ctx, "id1")
	if !target || !has || data != "go" {
		t.Fatalf("target=%v has=%v data=%q", target, has, data)
	}
	if tool.ActiveInterruptID(tool.WithToolResume(ctx, "id1", nil, nil)) != "id1" {
		t.Fatal("active interrupt id")
	}
}

func TestMiddleware_InfoAndWrapper(t *testing.T) {
	raw := tool.NewFuncTool(&schema.ToolInfo{Name: "x", Desc: "x"}, func(_ context.Context, _ string) (string, error) {
		return "ok", nil
	})
	wrapped := tool.Chain(raw,
		tool.WithInvokableWrapper(func(ctx context.Context, inner tool.InvokableTool, args string) (string, error) {
			info, err := inner.Info(ctx)
			if err != nil || info.Name != "x" {
				return "", errors.New("bad info")
			}
			return inner.InvokableRun(ctx, args)
		}),
	)
	info, err := wrapped.Info(context.Background())
	if err != nil || info.Name != "x" {
		t.Fatalf("err=%v info=%v", err, info)
	}
	out, err := wrapped.InvokableRun(context.Background(), `{}`)
	if err != nil || out != "ok" {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

func TestEnhancedNilGuards(t *testing.T) {
	var et *tool.EnhancedFuncTool
	if _, err := et.Info(context.Background()); err == nil {
		t.Fatal("expected nil enhanced info error")
	}
	if _, err := et.EnhancedInvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("expected nil enhanced run error")
	}
	if tool.AsInvokable(nil) != nil {
		t.Fatal("expected nil adapter")
	}
}
