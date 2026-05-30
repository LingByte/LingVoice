package compose_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestGraphRunnable_StreamPaths(t *testing.T) {
	model := llm.NewFuncModel("m", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("invoke-out", nil), nil
	}, nil)
	g, err := compose.NewChainBuilder("gr").AppendChatModel(model).CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gr := &compose.GraphRunnable{Graph: g}
	if out, err := gr.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")}); err != nil || out.Content != "invoke-out" {
		t.Fatalf("invoke err=%v out=%v", err, out)
	}
	var nilGR *compose.GraphRunnable
	if out, err := nilGR.Invoke(context.Background(), nil); err != nil || out != nil {
		t.Fatalf("nil gr out=%v err=%v", out, err)
	}

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
	srLoop, err := gr.Stream(context.Background(), loop, []*schema.Message{schema.UserMessage("stream")})
	if err != nil {
		t.Fatal(err)
	}
	srLoop.Close()

	srPipe, err := gr.Stream(context.Background(), nil, []*schema.Message{schema.UserMessage("pipe")})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := schema.CollectMessages(srPipe)
	if err != nil || msg.Content != "invoke-out" {
		t.Fatalf("pipe stream err=%v msg=%v", err, msg)
	}
}

func TestMessageGenericChain_FinalMessagePath(t *testing.T) {
	mgc := compose.NewMessageGenericChain("mgc-final",
		compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
			st.Messages = append(st.Messages, schema.AssistantMessage("via-messages", nil))
			return nil
		}},
	)
	msg, err := mgc.Invoke(context.Background(), []*schema.Message{schema.UserMessage("q")})
	if err != nil || msg == nil || msg.Content != "via-messages" {
		t.Fatalf("err=%v msg=%v", err, msg)
	}
	if _, err := (*compose.MessageGenericChain)(nil).Invoke(context.Background(), nil); err == nil {
		t.Fatal("expected nil message generic chain error")
	}
}

func TestGenericChain_IOAndErrors(t *testing.T) {
	ioChain := compose.NewGenericChainIO[int]("io").
		Append(func(_ context.Context, in int) (int, error) { return in * 2, nil })
	out, err := ioChain.Invoke(context.Background(), 3)
	if err != nil || out != 6 {
		t.Fatalf("io err=%v out=%d", err, out)
	}

	bad := compose.NewGenericChain[int, string]("bad", nil)
	if _, err := bad.Invoke(context.Background(), 1); err == nil {
		t.Fatal("expected missing mapper error")
	}

	stepErr := compose.NewGenericChain("steps", func(v int) string { return "x" }).
		Append(func(_ context.Context, _ int) (int, error) { return 0, errors.New("step fail") })
	if _, err := stepErr.Invoke(context.Background(), 1); err == nil {
		t.Fatal("expected step error")
	}
}

func TestMappedChain_StringChain(t *testing.T) {
	ok := compose.NewStringMessageChain("str",
		compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
			st.LastOutput = schema.AssistantMessage("hello", nil)
			return nil
		}},
	)
	text, err := ok.Invoke(context.Background(), "prompt")
	if err != nil || text != "hello" {
		t.Fatalf("err=%v text=%q", err, text)
	}

	empty := compose.NewStringMessageChain("empty",
		compose.FuncStep{Fn: func(_ context.Context, _ *compose.State) error { return nil }},
	)
	if _, err := empty.Invoke(context.Background(), "x"); err == nil {
		t.Fatal("expected empty output error")
	}

	mc := compose.NewMappedChain("mc",
		func(_ context.Context, n int) (*compose.State, error) {
			return &compose.State{Vars: map[string]any{"n": n}}, nil
		},
		func(st *compose.State) (int, error) {
			return st.Vars["n"].(int), nil
		},
		compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
			st.Vars["n"] = st.Vars["n"].(int) + 1
			return nil
		}},
		nil,
	)
	if _, err := mc.Invoke(context.Background(), 1); err == nil {
		t.Fatal("expected nil step error")
	}
}

func TestTransform_MultiChunkPlainGraph(t *testing.T) {
	model := llm.NewFuncModel("m", func(_ context.Context, in []*schema.Message, _ llm.Options) (*schema.Message, error) {
		if len(in) > 0 && in[0].Content != "" {
			return schema.AssistantMessage("out:"+in[0].Content, nil), nil
		}
		return schema.AssistantMessage("empty", nil), nil
	}, nil)
	plain, err := compose.NewChainBuilder("multi-tr").AppendChatModel(model).CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	inSR := schema.StreamReaderFromSlice([]*schema.Message{
		schema.UserMessage("one"),
		schema.UserMessage("two"),
	})
	outSR, err := plain.Transform(context.Background(), inSR)
	if err != nil {
		t.Fatal(err)
	}
	defer outSR.Close()
	var contents []string
	for {
		m, err := outSR.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if m != nil {
			contents = append(contents, m.Content)
		}
	}
	if len(contents) != 2 || contents[0] != "out:one" || contents[1] != "out:two" {
		t.Fatalf("contents=%v", contents)
	}
}

func TestStreamMessages_NonReActGraph(t *testing.T) {
	model := llm.NewFuncModel("m", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("sm", nil), nil
	}, nil)
	g, err := compose.NewChainBuilder("sm").AppendChatModel(model).CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sr, err := g.StreamMessages(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := schema.CollectMessages(sr)
	if err != nil || msg.Content != "sm" {
		t.Fatalf("err=%v msg=%v", err, msg)
	}
}

func TestPregelCheckpointFromVars(t *testing.T) {
	store := compose.NewMemoryCheckPointStore()
	g := compose.NewGraph("pg-vars")
	_ = g.AddLambdaNode("a", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["tick"] = true
		return nil
	})
	_ = g.AddFanOutEdges(compose.START, "a")
	_ = g.AddEdge("a", compose.END)
	cg, err := g.Compile(
		compose.WithGraphRunMode(compose.RunModePregel),
		compose.WithCheckPointStore(store),
	)
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), nil,
		compose.WithCheckPointID("pg-vars"),
		compose.WithPregelCheckpoint(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if st.Vars["tick"] != true {
		t.Fatalf("vars=%v", st.Vars)
	}
}
