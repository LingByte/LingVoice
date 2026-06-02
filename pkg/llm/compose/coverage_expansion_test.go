package compose_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestBuilderAPIs_Wrappers(t *testing.T) {
	model := llm.NewFuncModel("m", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("ok", nil), nil
	}, nil)
	tpl := prompt.FromMessages(schema.FormatFString, schema.SystemMessage("x"))
	loop, err := compose.NewToolLoop(context.Background(), compose.ToolLoopConfig{
		Model: &mockToolModel{},
		Tools: []tool.InvokableTool{tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
			return `{}`, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}

	branch := compose.NewChainBranch(func(_ context.Context, _ *compose.State) (string, error) {
		return "a", nil
	}).AddPrompt("a", schema.User, "p").AddTemplate("b", tpl).AddToolLoop("c", loop).
		AddLambda("d", func(_ context.Context, st *compose.State) error {
			st.LastOutput = schema.AssistantMessage("lambda", nil)
			return nil
		}).Default("a")

	par := compose.NewChainParallel().AddPrompt("a", schema.User, "p").AddTemplate("b", tpl).
		AddToolLoop("c", loop).AddLambda("d", func(_ context.Context, _ *compose.State) error { return nil }).
		AddChatModel("e", model)

	mb := compose.NewChainMultiBranch(func(_ context.Context, _ *compose.State) ([]string, error) {
		return []string{"a"}, nil
	}).Default("a").AddPrompt("a", schema.User, "p").AddTemplate("b", tpl).
		AddToolLoop("c", loop).AddLambda("d", func(_ context.Context, _ *compose.State) error { return nil })

	sb := compose.NewStreamChainBranch(func(_ context.Context, _ *compose.State) (string, error) {
		return "a", nil
	}).Default("a").AddPrompt("a", schema.User, "p").AddTemplate("b", tpl).
		AddStep("c", compose.ToolLoopStreamStage{Loop: loop, Stage: "c"}).
		AddLambda("d", func(_ context.Context, _ *compose.State) error { return nil })

	smb := compose.NewStreamChainMultiBranch(func(_ context.Context, _ *compose.State) ([]string, error) {
		return []string{"a"}, nil
	}).Default("a").AddPrompt("a", schema.User, "p").AddTemplate("b", tpl).
		AddStep("c", compose.ToolLoopStreamStage{Loop: loop, Stage: "c"}).
		AddLambda("d", func(_ context.Context, _ *compose.State) error { return nil })

	_ = par.Step()
	_ = mb.Step()
	_ = smb.Step()
	_ = branch.Step()

	_, err = compose.NewChainBuilder("par").
		AppendChainParallel(par).
		AppendChainMultiBranch(mb).
		AppendStreamChainMultiBranch(smb).
		CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	g, err := compose.NewChainBuilder("apis").
		AppendTemplate(tpl).
		AppendToolLoop(loop).
		AppendStreamChainBranch(sb).
		CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if g.Describe().Name != "apis" {
		t.Fatal("compiled")
	}
	chain := compose.NewChainBuilder("c").AppendPrompt(schema.User, "hi").BuildChain()
	if chain == nil {
		t.Fatal("build chain")
	}
	cr, err := compose.NewChainBuilder("cr").AppendChatModel(model).CompileGraphRunnable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cr.Invoke(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestMultiBranchStep_Run(t *testing.T) {
	ms := compose.MultiBranchStep{
		MultiSelect: func(_ context.Context, _ *compose.State) ([]string, error) {
			return []string{"a", "missing"}, nil
		},
		Default: []string{"b"},
		Steps: map[string]compose.Step{
			"a": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
				st.Vars = map[string]any{"a": true}
				return nil
			}},
			"b": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
				st.Vars = map[string]any{"b": true}
				return nil
			}},
		},
	}
	st := &compose.State{Vars: map[string]any{}}
	if err := ms.Run(context.Background(), st); err != nil {
		t.Fatal(err)
	}
}

func TestSubGraphAndPassthroughSteps(t *testing.T) {
	inner := compose.NewGraph("inner-step")
	_ = inner.AddLambdaNode("n", func(_ context.Context, st *compose.GraphState) error {
		st.LastOutput = schema.AssistantMessage("sub", nil)
		return nil
	})
	_ = inner.AddEdge(compose.START, "n")
	_ = inner.AddEdge("n", compose.END)

	st := &compose.State{Messages: []*schema.Message{schema.UserMessage("hi")}, Vars: map[string]any{}}
	if err := (compose.SubGraphStep{Name: "s", Graph: inner}).Run(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if st.LastOutput == nil || st.LastOutput.Content != "sub" {
		t.Fatalf("out=%v", st.LastOutput)
	}
	if err := (compose.PassthroughStep{}).Run(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if err := (compose.SubGraphStep{Graph: nil}).Run(context.Background(), st); err == nil {
		t.Fatal("expected nil graph error")
	}
}

func TestRunnable_TransformAndCollect(t *testing.T) {
	model := llm.NewFuncModel("m", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("plain", nil), nil
	}, nil)
	plain, err := compose.NewChainBuilder("plain").AppendChatModel(model).CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	inSR := schema.StreamReaderFromSlice([]*schema.Message{schema.UserMessage("a"), schema.UserMessage("b")})
	outSR, err := plain.Transform(context.Background(), inSR)
	if err != nil {
		t.Fatal(err)
	}
	defer outSR.Close()
	if _, err := outSR.Recv(); err != nil {
		t.Fatal(err)
	}

	cr := compose.NewChainRunnable(plain)
	collectSR := schema.StreamReaderFromSlice([]*schema.Message{schema.UserMessage("collect")})
	msg, err := cr.Collect(context.Background(), collectSR)
	if err != nil || msg == nil {
		t.Fatalf("collect err=%v", err)
	}
	trSR := schema.StreamReaderFromSlice([]*schema.Message{schema.UserMessage("tr")})
	trOut, err := cr.Transform(context.Background(), trSR)
	if err != nil {
		t.Fatal(err)
	}
	trOut.Close()

	react, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model: &mockStreamModel{},
		Tools: []tool.InvokableTool{tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
			return `{}`, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	reactSR := schema.StreamReaderFromSlice([]*schema.Message{schema.UserMessage("react")})
	rOut, err := react.Transform(context.Background(), reactSR)
	if err != nil {
		t.Fatal(err)
	}
	rOut.Close()

	if _, err := (*compose.ChainRunnable)(nil).Invoke(context.Background(), nil); err == nil {
		t.Fatal("nil chain runnable")
	}
	if _, err := (*compose.CompiledGraph)(nil).Collect(context.Background(), nil); err == nil {
		t.Fatal("nil collect")
	}
}

func TestValuesMerge_AllTypes(t *testing.T) {
	out, err := compose.MergeValues([]any{
		map[string]any{"a": 1},
		map[string]any{"b": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["a"] != 1 || m["b"] != 2 {
		t.Fatalf("m=%v", m)
	}
	out, err = compose.MergeValues([]any{
		map[string]string{"x": "1"},
		map[string]string{"y": "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sm := out.(map[string]string)
	if sm["x"] != "1" {
		t.Fatal(sm)
	}
	out, err = compose.MergeValues([]any{
		[]*schema.Message{schema.UserMessage("a")},
		[]*schema.Message{schema.UserMessage("a"), schema.UserMessage("b")},
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs := out.([]*schema.Message)
	if len(msgs) != 2 {
		t.Fatalf("len=%d", len(msgs))
	}
	_, err = compose.MergeValues([]any{map[string]any{"dup": 1}, map[string]any{"dup": 2}})
	if err == nil {
		t.Fatal("expected dup key error")
	}
	compose.RegisterValuesMergeFunc(func(items []int) (int, error) {
		sum := 0
		for _, v := range items {
			sum += v
		}
		return sum, nil
	})
	out, err = compose.MergeValues([]any{1, 2, 3})
	if err != nil || out.(int) != 6 {
		t.Fatalf("custom merge err=%v out=%v", err, out)
	}
}

func TestStreamUtil_Collect(t *testing.T) {
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
	sr, err := loop.StreamFrames(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	frames, err := compose.CollectStreamFrames(sr)
	if err != nil || len(frames) == 0 {
		t.Fatalf("frames=%d err=%v", len(frames), err)
	}
	if _, err := compose.CollectStreamFrames(nil); err == nil {
		t.Fatal("nil reader")
	}
	msr, msw := schema.Pipe[*schema.Message](1)
	go func() {
		defer msw.Close()
		msw.Send(schema.AssistantMessage("chunk", nil), nil)
	}()
	msg, err := compose.CollectMessageStream(msr)
	if err != nil || msg == nil {
		t.Fatalf("msg err=%v", err)
	}
	if _, err := compose.CollectMessageStream(nil); err == nil {
		t.Fatal("nil message stream")
	}
}

func TestInterrupt_ResumeWithData(t *testing.T) {
	ctx := compose.ResumeWithData(context.Background(), "id1", map[string]any{"k": "v"})
	isTarget, hasData, data := compose.GetResumeContext[map[string]any](ctx, "id1")
	if !isTarget || !hasData || data["k"] != "v" {
		t.Fatalf("target=%v has=%v data=%v", isTarget, hasData, data)
	}
}

func TestCompileCallback_Error(t *testing.T) {
	g := compose.NewGraph("cb-err")
	_ = g.AddLambdaNode("n", func(_ context.Context, _ *compose.GraphState) error { return nil })
	_ = g.AddEdge(compose.START, "n")
	_ = g.AddEdge("n", compose.END)
	_, err := g.Compile(compose.WithCompileCallback(func(_ context.Context, _ compose.GraphInfo) error {
		return errors.New("boom")
	}))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err=%v", err)
	}
}

func TestWorkflow_AllNodeTypes(t *testing.T) {
	model := llm.NewFuncModel("m", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("wf-model", nil), nil
	}, nil)
	tpl := prompt.FromMessages(schema.FormatFString, schema.SystemMessage("sys"))
	wf := compose.NewWorkflow("wf-all")
	if err := wf.Node("").AddInput("input"); err == nil {
		t.Fatal("expected nil node error")
	}
	_ = wf.AddInputNode("input")
	if err := wf.AddChatModelNode("model", model); err != nil {
		t.Fatal(err)
	}
	if err := wf.AddTemplateNode("tpl", tpl); err != nil {
		t.Fatal(err)
	}
	wf.Node("model").SetStaticValue("mode", "test")
	_ = wf.AddEdge(compose.START, "input")
	_ = wf.AddEdge("input", "model")
	_ = wf.AddEnd("model")
	cg, err := wf.Compile()
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")}, compose.WithGraphChatModelOption(llm.WithMaxTokens(16)))
	if err != nil {
		t.Fatal(err)
	}
	if st.LastOutput == nil || st.LastOutput.Content != "wf-model" {
		t.Fatalf("out=%v", st.LastOutput)
	}
	info := cg.Describe()
	if info.NodeKinds["input"] != compose.NodeKindInput {
		t.Fatalf("kinds=%v", info.NodeKinds)
	}

	wf2 := compose.NewWorkflow("wf-tpl")
	_ = wf2.AddInputNode("input")
	if err := wf2.AddTemplateNode("tpl", tpl); err != nil {
		t.Fatal(err)
	}
	_ = wf2.AddEdge(compose.START, "input")
	_ = wf2.AddEdge("input", "tpl")
	_ = wf2.AddEnd("tpl")
	cg2, err := wf2.Compile()
	if err != nil {
		t.Fatal(err)
	}
	st2, _, err := cg2.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil || len(st2.Messages) < 2 {
		t.Fatalf("tpl workflow err=%v msgs=%d", err, len(st2.Messages))
	}
}

func TestBatch_WithItems(t *testing.T) {
	var ran int
	err := (&compose.Batch{
		Name: "b",
		Items: []compose.BatchItem{
			{Run: func(_ context.Context) error { ran++; return nil }},
			{ID: "x", Run: func(_ context.Context) error { ran++; return compose.Interrupt(context.Background(), "pause") }},
		},
	}).Run(context.Background())
	if err == nil {
		t.Fatal("expected composite interrupt")
	}
	if ran != 2 {
		t.Fatalf("ran=%d", ran)
	}
}

func TestGenericChain_Full(t *testing.T) {
	outMapper := func(v int) string { return strings.Repeat("x", v) }
	chain := compose.NewGenericChain("g", outMapper).
		Append(func(_ context.Context, in int) (int, error) { return in + 1, nil })
	out, err := chain.Invoke(context.Background(), 2)
	if err != nil || out != "xxx" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	mgc := compose.NewMessageGenericChain("mgc", &compose.ChatModelStep{
		Model: llm.NewFuncModel("m", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
			return schema.AssistantMessage("mgc", nil), nil
		}, nil),
	})
	msg, err := mgc.Invoke(context.Background(), []*schema.Message{schema.UserMessage("q")})
	if err != nil || msg.Content != "mgc" {
		t.Fatalf("msg=%v err=%v", msg, err)
	}
	if _, err := (*compose.GenericChain[int, string])(nil).Invoke(context.Background(), 0); err == nil {
		t.Fatal("nil generic chain")
	}
}

func TestPregelCheckpoint_SaveOnInvoke(t *testing.T) {
	store := compose.NewMemoryCheckPointStore()
	g := compose.NewGraph("pg-save")
	_ = g.AddLambdaNode("a", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["done"] = true
		return nil
	})
	_ = g.AddFanOutEdges(compose.START, "a")
	_ = g.AddEdge("a", compose.END)
	cg, err := g.Compile(compose.WithGraphRunMode(compose.RunModePregel), compose.WithCheckPointStore(store))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = cg.Invoke(context.Background(), nil,
		compose.WithCheckPointID("pg-save"),
		compose.WithPregelCheckpoint(),
	)
	if err != nil {
		t.Fatal(err)
	}
	b, ok, _ := store.Get(context.Background(), "pg-save")
	if !ok || len(b) == 0 {
		t.Fatal("checkpoint not saved")
	}
}

func TestSubGraph_InterruptBefore(t *testing.T) {
	inner := compose.NewGraph("inner-int")
	_ = inner.AddLambdaNode("n", func(_ context.Context, _ *compose.GraphState) error { return nil })
	_ = inner.AddEdge(compose.START, "n")
	_ = inner.AddEdge("n", compose.END)
	outer := compose.NewGraph("outer-int")
	_ = outer.AddGraphNode("sub", inner, compose.WithSubGraphInterruptBefore("n"))
	_ = outer.AddEdge(compose.START, "sub")
	_ = outer.AddEdge("sub", compose.END)
	cg, err := outer.Compile(compose.WithInterruptBeforeNodes("sub"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = cg.Invoke(context.Background(), nil, compose.WithCheckPointID("sg-int"))
	if !compose.IsInterrupt(err) {
		t.Fatalf("expected interrupt err=%v", err)
	}
}

func TestToolLoop_ReturnDirectly(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{"sum":3}`, nil
	})
	g, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model:              &mockToolModel{},
		Tools:              []tool.InvokableTool{add},
		ToolReturnDirectly: map[string]struct{}{"add": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := g.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if st.LastOutput == nil {
		t.Fatal("nil output")
	}
}

func TestStreamResume_LoadCheckpoint(t *testing.T) {
	store := compose.NewMemoryCheckPointStore()
	g, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model:           &mockStreamModel{},
		Tools:           []tool.InvokableTool{tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
			return `{}`, nil
		})},
		CheckPointStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := g.StreamFrames(context.Background(), []*schema.Message{schema.UserMessage("hi")},
		compose.WithCheckPointID("sr-cp"),
		compose.WithStreamCheckpoint(),
	)
	if err != nil {
		t.Fatal(err)
	}
	for {
		f, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		if f != nil && f.Done {
			break
		}
	}
	sr2, err := g.StreamFrames(context.Background(), nil, compose.WithCheckPointID("sr-cp"))
	if err != nil {
		t.Fatal(err)
	}
	sr2.Close()
}

func TestGraph_AddToolCallingModelNode(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{}`, nil
	})
	g := compose.NewGraph("tcm")
	if err := g.AddToolCallingModelNode(context.Background(), "model", &mockToolModel{}, []tool.InvokableTool{add}); err != nil {
		t.Fatal(err)
	}
	_ = g.AddEdge(compose.START, "model")
	_ = g.AddEdge("model", compose.END)
	cg, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	info := cg.Describe()
	if info.NodeKinds["model"] != compose.NodeKindChatModel {
		t.Fatalf("kinds=%v", info.NodeKinds)
	}
}

func TestPipeline_ToolLoopStreamStageRun(t *testing.T) {
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
	stage := compose.ToolLoopStreamStage{Loop: loop, Stage: "react"}
	st := &compose.State{Messages: []*schema.Message{schema.UserMessage("hi")}}
	if err := stage.Run(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if st.LastOutput == nil {
		t.Fatal("nil output")
	}
}

func TestCheckpoint_MarshalErrors(t *testing.T) {
	if _, err := compose.MarshalCheckpointForTest(nil); err != nil {
		t.Fatal(err)
	}
	store := compose.NewMemoryCheckPointStore()
	if err := store.Set(context.Background(), "k", []byte(`{`)); err != nil {
		t.Fatal(err)
	}
	g := compose.NewGraph("cp")
	_ = g.AddLambdaNode("n", func(_ context.Context, _ *compose.GraphState) error { return nil })
	_ = g.AddEdge(compose.START, "n")
	_ = g.AddEdge("n", compose.END)
	cg, err := g.Compile(compose.WithCheckPointStore(store))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = cg.Invoke(context.Background(), nil, compose.WithCheckPointID("k"))
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestChainCompile_ParallelAndBranch(t *testing.T) {
	g, err := compose.NewChainBuilder("mix").
		AppendChainParallel(compose.NewChainParallel().
			AddLambda("a", func(_ context.Context, st *compose.State) error {
				st.Vars = map[string]any{"a": 1}
				return nil
			}).
			AddLambda("b", func(_ context.Context, st *compose.State) error {
				st.Vars = map[string]any{"b": 2}
				return nil
			})).
		CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, trace, err := g.Invoke(context.Background(), nil)
	if err != nil || len(trace) < 2 {
		t.Fatalf("trace=%d err=%v", len(trace), err)
	}
}

func TestMappedChain_ErrorPaths(t *testing.T) {
	mc := compose.NewMappedChain("mc",
		func(_ context.Context, _ string) (*compose.State, error) { return nil, context.Canceled },
		func(_ *compose.State) (string, error) { return "", nil },
	)
	if _, err := mc.Invoke(context.Background(), "x"); err == nil {
		t.Fatal("expected fromInput error")
	}
	mc2 := compose.NewMappedChain("mc2",
		func(_ context.Context, _ string) (*compose.State, error) {
			return &compose.State{Vars: map[string]any{}}, nil
		},
		func(_ *compose.State) (string, error) { return "", context.Canceled },
		compose.FuncStep{Fn: func(_ context.Context, _ *compose.State) error { return nil }},
	)
	if _, err := mc2.Invoke(context.Background(), "x"); err == nil {
		t.Fatal("expected toOutput error")
	}
}

func TestChainBuilder_AppendParallelAndBranch(t *testing.T) {
	model := llm.NewFuncModel("m", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("branch-out", nil), nil
	}, nil)
	par := compose.NewParallel(map[string]compose.Step{
		"a": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
			if st.Vars == nil {
				st.Vars = map[string]any{}
			}
			st.Vars["a"] = 1
			return nil
		}},
		"b": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
			if st.Vars == nil {
				st.Vars = map[string]any{}
			}
			st.Vars["b"] = 2
			return nil
		}},
	})
	branch := compose.BranchStep{
		Select: func(_ context.Context, _ *compose.State) (string, error) { return "fast", nil },
		Steps: map[string]compose.Step{
			"fast": &compose.ChatModelStep{Model: model},
			"slow": &compose.ChatModelStep{Model: model},
		},
	}
	g, err := compose.NewChainBuilder("pb").
		AppendParallel(par).
		AppendBranch(branch).
		CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := g.Invoke(context.Background(), nil)
	if err != nil || st.LastOutput == nil {
		t.Fatalf("err=%v out=%v", err, st.LastOutput)
	}
}

func TestPregel_InterruptAfter(t *testing.T) {
	store := compose.NewMemoryCheckPointStore()
	g := compose.NewGraph("pg-after")
	_ = g.AddLambdaNode("a", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["a"] = 1
		return nil
	})
	_ = g.AddLambdaNode("b", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["b"] = 2
		return nil
	})
	_ = g.AddFanOutEdges(compose.START, "a", "b")
	_ = g.AddEdge("a", compose.END)
	_ = g.AddEdge("b", compose.END)
	cg, err := g.Compile(
		compose.WithGraphRunMode(compose.RunModePregel),
		compose.WithCheckPointStore(store),
		compose.WithInterruptAfterNodes("a"),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = cg.Invoke(context.Background(), nil, compose.WithCheckPointID("pg-after"))
	if !compose.IsInterrupt(err) {
		t.Fatalf("expected interrupt err=%v", err)
	}
	_, _, err = cg.Invoke(context.Background(), nil, compose.WithCheckPointID("pg-after"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestClearCheckpointOnComplete(t *testing.T) {
	store := compose.NewMemoryCheckPointStore()
	g := compose.NewGraph("clear")
	_ = g.AddLambdaNode("n", func(_ context.Context, _ *compose.GraphState) error { return nil })
	_ = g.AddEdge(compose.START, "n")
	_ = g.AddEdge("n", compose.END)
	cg, err := g.Compile(compose.WithCheckPointStore(store), compose.WithClearCheckpointOnCompleteCompile())
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = cg.Invoke(context.Background(), nil,
		compose.WithCheckPointID("clear-1"),
		compose.WithClearCheckpointOnComplete(),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, ok, _ := store.Get(context.Background(), "clear-1")
	if ok {
		t.Fatal("checkpoint should be cleared")
	}
}

func TestWorkflow_AddFanOutEdges(t *testing.T) {
	wf := compose.NewWorkflow("wf-fan")
	_ = wf.AddInputNode("input")
	_ = wf.AddLambdaStep("a", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["a"] = true
		return nil
	})
	_ = wf.AddLambdaStep("b", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["b"] = true
		return nil
	})
	_ = wf.AddEdge(compose.START, "input")
	if err := wf.AddFanOutEdges("input", "a", "b"); err != nil {
		t.Fatal(err)
	}
	_ = wf.AddEdge("a", compose.END)
	_ = wf.AddEdge("b", compose.END)
	cg, err := wf.Compile(compose.WithGraphRunMode(compose.RunModePregel))
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), []*schema.Message{schema.UserMessage("x")})
	if err != nil {
		t.Fatal(err)
	}
	if st.Vars["a"] != true || st.Vars["b"] != true {
		t.Fatalf("vars=%v", st.Vars)
	}
}

func TestRunnable_NilErrors(t *testing.T) {
	if _, err := (*compose.ChainRunnable)(nil).Collect(context.Background(), nil); err == nil {
		t.Fatal("nil chain runnable collect")
	}
	if _, err := (*compose.ChainRunnable)(nil).Transform(context.Background(), nil); err == nil {
		t.Fatal("nil chain runnable transform")
	}
	if _, err := (*compose.CompiledGraph)(nil).StreamMessages(context.Background(), nil); err == nil {
		t.Fatal("nil stream messages")
	}
	if _, err := (*compose.CompiledGraph)(nil).Transform(context.Background(), nil); err == nil {
		t.Fatal("nil transform")
	}
}

func TestStatefulInterrupt_InNode(t *testing.T) {
	g := compose.NewGraph("stateful")
	_ = g.AddLambdaNode("n", func(ctx context.Context, st *compose.GraphState) error {
		was, _, saved := compose.GetInterruptState[string](ctx)
		if !was {
			return compose.StatefulInterrupt(ctx, "pause", "saved-state")
		}
		st.Vars["resumed"] = saved
		return nil
	})
	_ = g.AddEdge(compose.START, "n")
	_ = g.AddEdge("n", compose.END)
	cg, err := g.Compile(compose.WithCheckPointStore(compose.NewMemoryCheckPointStore()))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = cg.Invoke(context.Background(), nil, compose.WithCheckPointID("st-int"))
	if !compose.IsInterrupt(err) {
		t.Fatalf("expected interrupt err=%v", err)
	}
	st, _, err := cg.Invoke(context.Background(), nil, compose.WithCheckPointID("st-int"))
	if err != nil || st.Vars["resumed"] != "saved-state" {
		t.Fatalf("err=%v vars=%v", err, st.Vars)
	}
}

func TestPregel_ExternalInterrupt(t *testing.T) {
	ready := make(chan struct{})
	halt := make(chan struct{})
	g := compose.NewGraph("pg-ext")
	_ = g.AddLambdaNode("a", func(_ context.Context, st *compose.GraphState) error {
		close(ready)
		<-halt
		st.Vars["a"] = 1
		return nil
	})
	_ = g.AddEdge(compose.START, "a")
	_ = g.AddEdge("a", compose.END)
	cg, err := g.Compile(
		compose.WithGraphRunMode(compose.RunModePregel),
		compose.WithCheckPointStore(compose.NewMemoryCheckPointStore()),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, interrupt := compose.WithGraphInterrupt(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := cg.Invoke(ctx, nil, compose.WithCheckPointID("pg-ext"))
		done <- err
	}()
	<-ready
	interrupt()
	close(halt)
	if err := <-done; !compose.IsInterrupt(err) {
		t.Fatalf("expected interrupt err=%v", err)
	}
}

func TestGraph_InterruptAfterDAG(t *testing.T) {
	store := compose.NewMemoryCheckPointStore()
	g := compose.NewGraph("dag-after")
	_ = g.AddLambdaNode("n", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["done"] = true
		return nil
	})
	_ = g.AddEdge(compose.START, "n")
	_ = g.AddEdge("n", compose.END)
	cg, err := g.Compile(compose.WithCheckPointStore(store), compose.WithInterruptAfterNodes("n"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = cg.Invoke(context.Background(), nil, compose.WithCheckPointID("dag-after"))
	if !compose.IsInterrupt(err) {
		t.Fatalf("expected interrupt err=%v", err)
	}
	st, _, err := cg.Invoke(context.Background(), nil, compose.WithCheckPointID("dag-after"))
	if err != nil || st.Vars["done"] != true {
		t.Fatalf("err=%v vars=%v", err, st.Vars)
	}
}
