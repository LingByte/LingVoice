package agent_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/agent"
	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/session"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestRunnerError_String(t *testing.T) {
	_, err := agent.NewRunner(context.Background(), agent.RunnerConfig{})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "agent: nil graph" {
		t.Fatalf("err=%q", err.Error())
	}
}

func TestNilAgents(t *testing.T) {
	var ga *agent.GraphAgent
	if msg, err := ga.Generate(context.Background(), nil); err != nil || msg != nil {
		t.Fatalf("graph agent generate msg=%v err=%v", msg, err)
	}
	if _, err := ga.GenerateWithTrace(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if sr, err := ga.Stream(context.Background(), nil); err != nil || sr != nil {
		t.Fatalf("stream sr=%v err=%v", sr, err)
	}
	if fr, err := ga.StreamFrames(context.Background(), nil); err != nil || fr != nil {
		t.Fatalf("frames fr=%v err=%v", fr, err)
	}

	var react *agent.ReActAgent
	if msg, err := react.Generate(context.Background(), nil); err != nil || msg != nil {
		t.Fatalf("react generate msg=%v err=%v", msg, err)
	}
	if _, err := react.GenerateWithTrace(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := react.Resume(context.Background(), "cp", nil); err != nil {
		t.Fatal(err)
	}
	if sr, err := react.Stream(context.Background(), nil); err != nil || sr != nil {
		t.Fatalf("react stream sr=%v err=%v", sr, err)
	}
	if fr, err := react.StreamFrames(context.Background(), nil); err != nil || fr != nil {
		t.Fatalf("react frames fr=%v err=%v", fr, err)
	}
	if react.CheckPointStore() != nil || react.GraphAgent() != nil {
		t.Fatal("nil react accessors")
	}
}

func TestChainSteps_ErrorsAndInterrupt(t *testing.T) {
	st := &compose.State{Messages: []*schema.Message{schema.UserMessage("hi")}, Vars: map[string]any{}}
	if err := (agent.ReActStep{}).Run(context.Background(), st); err == nil {
		t.Fatal("nil react step")
	}
	if _, err := (agent.StreamStep{}).Stream(context.Background(), st); err == nil {
		t.Fatal("nil stream step")
	}
	if _, err := (agent.ReActStep{}).StreamFrames(context.Background(), st); err == nil {
		t.Fatal("nil react stream frames")
	}
	if err := (agent.ResumeStep{}).Run(context.Background(), st); err == nil {
		t.Fatal("nil resume step")
	}

	book, err := tool.InferTool("book", "book", func(_ context.Context, in struct {
		Dest string `json:"dest"`
	}) (map[string]string, error) {
		return map[string]string{"ok": in.Dest}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	react, err := agent.NewReActAgent(context.Background(), agent.ReActConfig{
		Model:           &hitlMockModel{},
		Tools:           []tool.InvokableTool{tool.WrapApprovable(book)},
		CheckPointStore: compose.NewMemoryCheckPointStore(),
	})
	if err != nil {
		t.Fatal(err)
	}
	st2 := &compose.State{Messages: []*schema.Message{schema.UserMessage("book")}, Vars: map[string]any{}}
	err = (agent.ReActStep{Agent: react, CheckPointID: "cp-step"}).Run(context.Background(), st2)
	if !compose.IsInterrupt(err) {
		t.Fatalf("expected interrupt err=%v", err)
	}
	if st2.Vars["__interrupt"] == nil {
		t.Fatal("expected interrupt info in vars")
	}
}

type streamFailModel struct {
	agentMockModel
}

func (streamFailModel) Stream(_ context.Context, _ []*schema.Message, _ ...llm.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stream unavailable")
}

func TestReActAgent_StreamGenerateFallback(t *testing.T) {
	add := testAddTool(t)
	react, err := agent.NewReActAgent(context.Background(), agent.ReActConfig{
		Model: &streamFailModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := react.Stream(context.Background(), []*schema.Message{schema.UserMessage("calc")})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	msg, err := schema.CollectMessages(sr)
	if err != nil || msg == nil {
		t.Fatalf("err=%v msg=%v", err, msg)
	}
}

func TestTurnLoop_ResumeWithApproval(t *testing.T) {
	react, err := agent.NewReActAgent(context.Background(), agent.ReActConfig{
		Model:           &hitlMockModel{},
		Tools:           []tool.InvokableTool{tool.WrapApprovable(mustBookTool(t))},
		CheckPointStore: compose.NewMemoryCheckPointStore(),
	})
	if err != nil {
		t.Fatal(err)
	}
	cpStore := compose.NewMemoryCheckPointStore()
	loop, err := agent.NewTurnLoop(context.Background(), agent.TurnLoopConfig{
		Agent:           react,
		Session:         &session.Conversation{ID: "tl-resume", Vars: map[string]any{}},
		CheckPointStore: cpStore,
		CheckPointID:    "cp-tl-resume",
	})
	if err != nil {
		t.Fatal(err)
	}
	loop.Session().AppendUser("book NYC")
	_, err = react.GenerateWithTrace(context.Background(), loop.Session().Messages, compose.WithCheckPointID("cp-tl-resume"))
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok || len(info.InterruptContexts) == 0 {
		t.Fatalf("interrupt=%v err=%v", info, err)
	}
	out, err := loop.Resume(context.Background(), map[string]any{
		info.InterruptContexts[0].ID: &tool.ApprovalResult{Approved: true},
	})
	if err != nil || out == nil || out.Message == nil {
		t.Fatalf("resume err=%v out=%v", err, out)
	}
}

func TestSessionOrchestrator_StreamTurn(t *testing.T) {
	orch, err := agent.NewSessionOrchestrator(agent.SessionOrchestratorConfig{
		Agent:   mustReact(t),
		Session: &session.Conversation{ID: "st-stream", Vars: map[string]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := orch.StreamTurn(context.Background(), "hello stream")
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	for {
		f, err := sr.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if f != nil && f.Done {
			break
		}
	}
}
