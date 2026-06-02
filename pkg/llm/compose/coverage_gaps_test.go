package compose_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestGraphInterrupt_ImmediateAndTimeout(t *testing.T) {
	ready := make(chan struct{})
	halt := make(chan struct{})
	g := compose.NewGraph("ext-int")
	_ = g.AddLambdaNode("a", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["step"] = "a"
		close(ready)
		<-halt
		return nil
	})
	_ = g.AddLambdaNode("b", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["step"] = "b"
		return nil
	})
	_ = g.AddEdge(compose.START, "a")
	_ = g.AddEdge("a", "b")
	_ = g.AddEdge("b", compose.END)
	cg, err := g.Compile(compose.WithCheckPointStore(compose.NewMemoryCheckPointStore()))
	if err != nil {
		t.Fatal(err)
	}

	ctx, interrupt := compose.WithGraphInterrupt(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := cg.Invoke(ctx, nil, compose.WithCheckPointID("ext-imm"))
		done <- err
	}()
	<-ready
	interrupt()
	close(halt)
	if err := <-done; !compose.IsInterrupt(err) {
		t.Fatalf("expected interrupt err=%v", err)
	}

	ready2 := make(chan struct{})
	g2 := compose.NewGraph("ext-int2")
	_ = g2.AddLambdaNode("a", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["step"] = "a"
		close(ready2)
		return nil
	})
	_ = g2.AddLambdaNode("b", func(ctx context.Context, _ *compose.GraphState) error {
		<-ctx.Done()
		return ctx.Err()
	})
	_ = g2.AddEdge(compose.START, "a")
	_ = g2.AddEdge("a", "b")
	_ = g2.AddEdge("b", compose.END)
	cg2, err := g2.Compile(compose.WithCheckPointStore(compose.NewMemoryCheckPointStore()))
	if err != nil {
		t.Fatal(err)
	}
	ctx2, interrupt2 := compose.WithGraphInterrupt(context.Background())
	go func() {
		<-ready2
		interrupt2(compose.WithGraphInterruptTimeout(20 * time.Millisecond))
	}()
	_, _, err = cg2.Invoke(ctx2, nil, compose.WithCheckPointID("ext-timeout"))
	if err == nil {
		t.Fatal("expected cancel during blocked node b")
	}
}

func TestToolNode_UnknownAliasSequential(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return "2", nil
	})
	node, err := compose.NewToolNode(context.Background(), &compose.ToolNodeConfig{
		Tools:               []tool.InvokableTool{add},
		NameAliases:         map[string]string{"plus": "add"},
		ExecuteSequentially: true,
		ToolArgumentsHandler: func(_ context.Context, name, input string) (string, error) {
			if name == "add" && input == "bad" {
				return "", errors.New("bad args")
			}
			return input, nil
		},
		UnknownToolHandler: func(_ context.Context, name, _ string) (string, error) {
			return "unknown:" + name, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	msgs, err := node.Invoke(context.Background(), schema.AssistantMessage("", []schema.ToolCall{
		{ID: "1", Function: schema.FunctionCall{Name: "plus", Arguments: `{"a":1,"b":1}`}},
		{ID: "2", Function: schema.FunctionCall{Name: "missing", Arguments: `{}`}},
	}))
	if err != nil || len(msgs) != 2 || msgs[0].Content != "2" || msgs[1].Content != "unknown:missing" {
		t.Fatalf("err=%v msgs=%+v", err, msgs)
	}

	_, err = node.Invoke(context.Background(), schema.AssistantMessage("", []schema.ToolCall{
		{ID: "3", Function: schema.FunctionCall{Name: "add", Arguments: "bad"}},
	}))
	if err == nil {
		t.Fatal("expected args handler error")
	}

	if _, err := compose.NewToolNode(context.Background(), nil); err == nil {
		t.Fatal("expected nil config error")
	}
	if _, err := compose.NewToolNode(context.Background(), &compose.ToolNodeConfig{
		Tools: []tool.InvokableTool{add, add},
	}); err == nil {
		t.Fatal("expected duplicate tool error")
	}
}

func TestToolNode_StatefulInterrupt(t *testing.T) {
	pause := tool.NewFuncTool(&schema.ToolInfo{Name: "pause", Desc: "pause"}, func(ctx context.Context, _ string) (string, error) {
		was, _, saved := tool.GetInterruptState[string](ctx)
		if !was {
			return "", tool.StatefulInterrupt(ctx, "need approval", "secret")
		}
		return saved, nil
	})
	node, err := compose.NewToolNode(context.Background(), &compose.ToolNodeConfig{Tools: []tool.InvokableTool{pause}})
	if err != nil {
		t.Fatal(err)
	}
	assistant := schema.AssistantMessage("", []schema.ToolCall{{
		ID: "c1", Function: schema.FunctionCall{Name: "pause", Arguments: `{}`},
	}})
	_, err = node.Invoke(context.Background(), assistant)
	if !compose.IsInterrupt(err) {
		t.Fatalf("expected interrupt err=%v", err)
	}
	resumeCtx := tool.WithToolResume(context.Background(), "intr-1", "need approval", "secret")
	msgs, err := node.Invoke(resumeCtx, assistant)
	if err != nil || len(msgs) != 1 || msgs[0].Content != "secret" {
		t.Fatalf("err=%v msgs=%+v", err, msgs)
	}
}

type alwaysToolModel struct {
	lastChoice llm.ToolChoice
	round      int
}

func (m *alwaysToolModel) Name() string { return "mock/always" }

func (m *alwaysToolModel) Generate(_ context.Context, _ []*schema.Message, opts ...llm.Option) (*schema.Message, error) {
	o := llm.ApplyOptions(opts...)
	m.lastChoice = o.ToolChoice
	m.round++
	return schema.AssistantMessage("", []schema.ToolCall{{
		ID: "c1", Function: schema.FunctionCall{Name: "add", Arguments: `{}`},
	}}), nil
}

func (m *alwaysToolModel) Stream(context.Context, []*schema.Message, ...llm.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, llm.ErrNotImplemented
}

func (m *alwaysToolModel) WithTools(_ []*schema.ToolInfo) (llm.ToolCallingChatModel, error) { return m, nil }

func TestToolLoop_ForceToolUseAndMaxRounds(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{}`, nil
	})
	model := &alwaysToolModel{}
	loop, err := compose.NewToolLoop(context.Background(), compose.ToolLoopConfig{
		Model:        model,
		Tools:        []tool.InvokableTool{add},
		ForceToolUse: true,
		MaxRounds:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = loop.Run(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err == nil {
		t.Fatal("expected max rounds error")
	}
	if model.lastChoice != llm.ToolChoiceForced {
		t.Fatalf("choice=%q", model.lastChoice)
	}

	loop2, err := compose.NewToolLoop(context.Background(), compose.ToolLoopConfig{
		Model:              &mockToolModel{},
		Tools:              []tool.InvokableTool{add},
		ToolReturnDirectly: map[string]struct{}{"add": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := loop2.Run(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil || out == nil || len(out.ToolCalls) == 0 {
		t.Fatalf("err=%v out=%v", err, out)
	}
}

func TestToolLoop_StreamMaxRounds(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{}`, nil
	})
	loop, err := compose.NewToolLoop(context.Background(), compose.ToolLoopConfig{
		Model:     &alwaysToolModel{},
		Tools:     []tool.InvokableTool{add},
		MaxRounds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := loop.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, err := sr.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return
		}
	}
}

func TestGraph_ParallelFanIn(t *testing.T) {
	g := compose.NewGraph("fanin")
	if err := g.AddParallelFanInNode("join", map[string]compose.NodeFunc{
		"a": func(_ context.Context, st *compose.GraphState) error {
			st.Vars["a"] = 1
			return nil
		},
		"b": func(_ context.Context, st *compose.GraphState) error {
			st.Vars["b"] = 2
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	_ = g.AddEdge(compose.START, "join")
	_ = g.AddEdge("join", compose.END)
	cg, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	aPart, _ := st.Vars["a"].(*compose.GraphState)
	bPart, _ := st.Vars["b"].(*compose.GraphState)
	if aPart == nil || bPart == nil || aPart.Vars["a"] != 1 || bPart.Vars["b"] != 2 {
		t.Fatalf("vars=%v a=%v b=%v", st.Vars, aPart, bPart)
	}
	if err := g.AddParallelFanInNode("", nil); err == nil {
		t.Fatal("expected empty branches error")
	}
}
