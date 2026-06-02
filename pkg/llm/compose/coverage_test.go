package compose_test

import (
	"context"
	"io"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestRuns_MergeAndGraph(t *testing.T) {
	st := &compose.State{Vars: map[string]any{}}
	compose.AppendRun(st, compose.RunEntry{Step: "a", Model: "m1"})
	compose.AppendRun(st, compose.RunEntry{Step: "b", Model: "m2"})
	if len(compose.RunsFromState(st)) != 2 {
		t.Fatal("runs count")
	}
	src := &compose.State{Vars: map[string]any{}}
	compose.AppendRun(src, compose.RunEntry{Step: "x"})
	compose.MergeRunsInto(st, src)
	if len(compose.RunsFromState(st)) != 3 {
		t.Fatalf("merged=%d", len(compose.RunsFromState(st)))
	}
	compose.AppendRun(nil, compose.RunEntry{})
	compose.MergeRunsInto(nil, src)
}

func TestChain_EdgeCases(t *testing.T) {
	_, err := compose.NewChain("c").Invoke(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (*compose.Chain)(nil).Invoke(context.Background(), nil)
	if err == nil {
		t.Fatal("expected nil chain error")
	}
	step := &compose.ChatModelStep{Model: nil}
	st := &compose.State{Messages: nil}
	if err := step.Run(context.Background(), st); err == nil {
		t.Fatal("expected nil model error")
	}
	model := llm.NewFuncModel("m", func(ctx context.Context, input []*schema.Message, opts llm.Options) (*schema.Message, error) {
		return nil, context.Canceled
	}, nil)
	step = &compose.ChatModelStep{Model: model}
	if err := step.Run(context.Background(), st); err == nil {
		t.Fatal("expected generate error")
	}
	if len(compose.RunsFromState(st)) != 1 {
		t.Fatal("expected run recorded on error")
	}
	compose.ChatModelStepOption(step, llm.WithMaxTokens(10))
	if len(step.Opts) == 0 {
		t.Fatal("expected opts")
	}
	_ = compose.PromptStep{Role: schema.System, Content: ""}.Run(context.Background(), st)
}

func TestBranch_DefaultAndStream(t *testing.T) {
	st := &compose.State{Vars: map[string]any{"path": "missing"}}
	err := compose.BranchStep{
		Select: func(_ context.Context, st *compose.State) (string, error) {
			return st.Vars["path"].(string), nil
		},
		Default: "skip",
		Steps: map[string]compose.Step{
			"skip": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
				st.LastOutput = schema.AssistantMessage("skipped", nil)
				return nil
			}},
		},
	}.Run(context.Background(), st)
	if err != nil || st.LastOutput.Content != "skipped" {
		t.Fatalf("err=%v out=%v", err, st.LastOutput)
	}
	sr, err := compose.BranchStep{
		Select: func(_ context.Context, _ *compose.State) (string, error) { return "skip", nil },
		Steps: map[string]compose.Step{
			"skip": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
				st.LastOutput = schema.AssistantMessage("x", nil)
				return nil
			}},
		},
	}.StreamFrames(context.Background(), &compose.State{})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	f, _ := sr.Recv()
	if f == nil || !f.Done {
		t.Fatalf("frame=%+v", f)
	}
}

func TestToolLoop_StreamAndStep(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{"sum":2}`, nil
	})
	loop, err := compose.NewToolLoop(context.Background(), compose.ToolLoopConfig{
		Model: &mockStreamModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := loop.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	for {
		_, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	n, err := drainFrames(mustStreamFrames(t, loop))
	if err != nil || n < 2 {
		t.Fatalf("frames=%d err=%v", n, err)
	}
	st := &compose.State{Messages: []*schema.Message{schema.UserMessage("hi")}}
	if err := (compose.ToolLoopStep{Loop: loop}).Run(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if st.LastOutput == nil {
		t.Fatal("nil output")
	}
}

func mustStreamFrames(t *testing.T, loop *compose.ToolLoop) *compose.StreamFrameReader {
	t.Helper()
	sr, err := loop.StreamFrames(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	return sr
}

func TestGraph_StreamNative(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{"sum":2}`, nil
	})
	g, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model: &mockStreamModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := g.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	for {
		_, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = (*compose.CompiledGraph)(nil).StreamFrames(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil react graph")
	}
}

func TestPipeline_TemplateAndMerge(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{"sum":2}`, nil
	})
	loop, _ := compose.NewToolLoop(context.Background(), compose.ToolLoopConfig{
		Model: &mockToolModel{},
		Tools: []tool.InvokableTool{add},
	})
	tpl := prompt.FromMessages(schema.FormatFString, schema.SystemMessage("mode={mode}"))
	pipe, err := compose.NewPipeline(compose.PipelineConfig{
		Name:     "p",
		Template: tpl,
		Steps:    []compose.Step{compose.ToolLoopStage{Loop: loop}},
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := pipe.Run(context.Background(), []*schema.Message{schema.UserMessage("q")}, map[string]any{"mode": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Messages) < 2 {
		t.Fatalf("msgs=%d", len(st.Messages))
	}
	_, err = compose.NewPipeline(compose.PipelineConfig{})
	if err == nil {
		t.Fatal("expected empty pipeline error")
	}
}

func TestRunnableAndFieldMapping(t *testing.T) {
	src := map[string]any{"user": map[string]any{"name": "a"}}
	dst := map[string]any{}
	if err := compose.MapFields(src, dst, compose.FromField("user.name").ToField("n")); err != nil {
		t.Fatal(err)
	}
	if dst["n"] != "a" {
		t.Fatalf("dst=%v", dst)
	}
	type payload struct {
		X int `json:"x"`
	}
	m := compose.MapStructToMap(payload{X: 3})
	if m["x"] != 3 {
		t.Fatalf("m=%v", m)
	}
	g, _ := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model: &mockToolModel{},
		Tools: []tool.InvokableTool{tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
			return `{}`, nil
		})},
	})
	gr := &compose.GraphRunnable{Graph: g}
	msg, err := gr.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil || msg == nil {
		t.Fatalf("err=%v msg=%v", err, msg)
	}
	sr, err := gr.Stream(context.Background(), nil, []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	sr.Close()
}

func TestTrace_MessageDelta(t *testing.T) {
	all := []*schema.Message{schema.UserMessage("a"), schema.AssistantMessage("b", nil)}
	delta := compose.MessageDelta(all, 1)
	if len(delta) != 1 {
		t.Fatalf("delta=%d", len(delta))
	}
	trace := compose.LoopTraceFromGraph(nil, nil, 0)
	if trace == nil || len(trace.Steps) != 0 {
		t.Fatal("expected empty trace")
	}
}

func TestTemplateStep(t *testing.T) {
	st := &compose.State{Vars: map[string]any{"n": "v"}}
	tpl := prompt.FromMessages(schema.FormatFString, schema.SystemMessage("{n}"))
	if err := (compose.TemplateStep{Template: tpl}).Run(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if err := (compose.TemplateStep{}).Run(context.Background(), st); err != nil {
		t.Fatal(err)
	}
}

func TestStreamFrame_Close(t *testing.T) {
	var r compose.StreamFrameReader
	if _, err := r.Recv(); err == nil {
		t.Fatal("expected closed error")
	}
	r.Close()
}

func TestFinalMessage(t *testing.T) {
	if compose.FinalMessage(nil) != nil {
		t.Fatal("nil state")
	}
	st := &compose.GraphState{
		Messages: []*schema.Message{
			schema.UserMessage("q"),
			{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "1"}}},
			schema.AssistantMessage("answer", nil),
		},
		LastOutput: &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "1"}}},
	}
	if m := compose.FinalMessage(st); m == nil || m.Content != "answer" {
		t.Fatalf("msg=%+v", m)
	}
}

func TestCompositeInterruptHelpers(t *testing.T) {
	ctx := compose.WithToolCallID(context.Background(), "tc1")
	if compose.ToolCallID(ctx) != "tc1" {
		t.Fatal("tool call id")
	}
	ctx = compose.AppendAddressSegment(ctx, "seg")
	if len(compose.CurrentAddress(ctx)) == 0 {
		t.Fatal("address")
	}
	if !compose.IsInterrupt(compose.Interrupt(ctx, "x")) {
		t.Fatal("is interrupt")
	}
}

func TestGraphCompile_Validation(t *testing.T) {
	g := compose.NewGraph("g")
	_, err := g.Compile()
	if err == nil {
		t.Fatal("expected missing START edge")
	}
	_ = g.AddLambdaNode("n", func(_ context.Context, st *compose.GraphState) error { return nil })
	_ = g.AddEdge(compose.START, "n")
	_ = g.AddEdge("n", compose.END)
	cg, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if cg.HasReActRuntime() {
		t.Fatal("plain graph should not have react runtime")
	}
}

func TestReActGraph_LegacyExecutor(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{"sum":2}`, nil
	})
	var steps int
	rg, err := compose.NewReActGraph(context.Background(), compose.ReActGraphConfig{
		Model:  &mockToolModel{},
		Tools:  []tool.InvokableTool{add},
		OnStep: func(_ compose.GraphStep) { steps++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	out, trace, err := rg.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil || out == nil || len(trace) < 2 {
		t.Fatalf("err=%v out=%v trace=%d steps=%d", err, out, len(trace), steps)
	}
	sr, err := rg.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	sr.Close()
}

func TestGraphMaxStepsExceeded(t *testing.T) {
	g := compose.NewGraph("max").WithMaxSteps(1)
	_ = g.AddLambdaNode("a", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["a"] = true
		return nil
	})
	_ = g.AddLambdaNode("b", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["b"] = true
		return nil
	})
	_ = g.AddEdge(compose.START, "a")
	_ = g.AddEdge("a", "b")
	_ = g.AddEdge("b", compose.END)
	cg, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = cg.Invoke(context.Background(), nil)
	if err == nil {
		t.Fatal("expected max steps error")
	}
}

func TestGraphInterruptContext(t *testing.T) {
	ctx, interrupt := compose.WithGraphInterrupt(context.Background())
	interrupt()
	select {
	case <-ctx.Done():
		if ctx.Err() != context.Canceled {
			t.Fatalf("err=%v", ctx.Err())
		}
	default:
		t.Fatal("expected canceled context")
	}
}

func TestParallel_ErrorPath(t *testing.T) {
	p := compose.NewParallel(map[string]compose.Step{
		"bad": compose.FuncStep{Fn: func(_ context.Context, _ *compose.State) error {
			return context.Canceled
		}},
	})
	st := &compose.State{Vars: map[string]any{}}
	if err := p.Run(context.Background(), st); err == nil {
		t.Fatal("expected parallel error")
	}
	if err := (compose.ParallelStep{Parallel: p}).Run(context.Background(), st); err == nil {
		t.Fatal("expected parallel step error")
	}
}

func TestOrchestrator_WithTemplate(t *testing.T) {
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
	tpl := prompt.FromMessages(schema.FormatFString, schema.SystemMessage("hello {name}"))
	orch, err := compose.NewOrchestrator(compose.OrchestratorConfig{
		Loop:     loop,
		Template: tpl,
		Vars:     map[string]any{"name": "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := orch.Run(context.Background(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	if r.Message == nil || orch.Session() == nil || len(orch.Messages()) == 0 {
		t.Fatalf("r=%+v", r)
	}
}

func TestWorkflow_FieldMapping(t *testing.T) {
	wf := compose.NewWorkflow("wf-map")
	_ = wf.AddInputNode("input")
	_ = wf.AddLambdaStep("work", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["copied"] = st.Vars["input"]
		return nil
	})
	_ = wf.AddEdge(compose.START, "input")
	_ = wf.AddEdge("input", "work")
	_ = wf.AddEdge("work", compose.END)
	cg, err := wf.Compile()
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), []*schema.Message{schema.UserMessage("payload")})
	if err != nil || st.Vars["copied"] != "payload" {
		t.Fatalf("err=%v vars=%v", err, st.Vars)
	}
}

func TestCompileReActGraph_Errors(t *testing.T) {
	_, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{})
	if err == nil {
		t.Fatal("expected nil model error")
	}
}

func TestGraphCompile_GraphAPIErrors(t *testing.T) {
	g := compose.NewGraph("err")
	if err := g.AddLambdaNode("", func(_ context.Context, _ *compose.GraphState) error { return nil }); err == nil {
		t.Fatal("empty name")
	}
	if err := g.AddLambdaNode("n", nil); err == nil {
		t.Fatal("nil fn")
	}
	_ = g.AddLambdaNode("n", func(_ context.Context, _ *compose.GraphState) error { return nil })
	if err := g.AddLambdaNode("n", func(_ context.Context, _ *compose.GraphState) error { return nil }); err == nil {
		t.Fatal("duplicate")
	}
	if err := g.AddEdge(compose.END, "n"); err == nil {
		t.Fatal("edge from END")
	}
}

func TestSubGraph_WithOptions(t *testing.T) {
	inner := compose.NewGraph("inner2")
	_ = inner.AddLambdaNode("n", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["ok"] = true
		return nil
	})
	_ = inner.AddEdge(compose.START, "n")
	_ = inner.AddEdge("n", compose.END)
	outer := compose.NewGraph("outer2")
	_ = outer.AddGraphNode("sub", inner, compose.WithSubGraphCheckPointStore(compose.NewMemoryCheckPointStore()))
	_ = outer.AddEdge(compose.START, "sub")
	_ = outer.AddEdge("sub", compose.END)
	cg, err := outer.Compile()
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), nil)
	if err != nil || st.Vars["ok"] != true {
		t.Fatalf("err=%v vars=%v", err, st.Vars)
	}
}

func TestBatch_Empty(t *testing.T) {
	if err := (*compose.Batch)(nil).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRuns_NilSafety(t *testing.T) {
	if compose.RunsFromState(nil) != nil {
		t.Fatal("nil state")
	}
	if compose.GraphRunsFromState(nil) != nil {
		t.Fatal("nil graph state")
	}
}

func TestRoundOptsCopy(t *testing.T) {
	// exercised via force tool use in graph compile
	g, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model:        &mockToolModel{},
		Tools:        []tool.InvokableTool{tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) { return `{}`, nil })},
		ForceToolUse: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = g.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
}

