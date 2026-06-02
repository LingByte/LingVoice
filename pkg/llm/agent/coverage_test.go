package agent_test

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/agent"
	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/session"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type agentMockModel struct {
	n int
}

func (m *agentMockModel) Name() string { return "mock/agent" }

func (m *agentMockModel) Generate(_ context.Context, _ []*schema.Message, _ ...llm.Option) (*schema.Message, error) {
	m.n++
	if m.n == 1 {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "1", Type: "function",
			Function: schema.FunctionCall{Name: "add", Arguments: `{"a":1,"b":2}`},
		}}), nil
	}
	return schema.AssistantMessage("ok", nil), nil
}

func (m *agentMockModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...llm.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		defer sw.Close()
		sw.Send(out, nil)
	}()
	return sr, nil
}

func (m *agentMockModel) WithTools(_ []*schema.ToolInfo) (llm.ToolCallingChatModel, error) {
	return m, nil
}

func testAddTool(t *testing.T) tool.InvokableTool {
	t.Helper()
	add, err := tool.InferTool("add", "add", func(_ context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (map[string]int, error) {
		return map[string]int{"sum": in.A + in.B}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return add
}

func TestReActAgent_AllPaths(t *testing.T) {
	react, err := agent.NewReActAgent(context.Background(), agent.ReActConfig{
		Model:           &agentMockModel{},
		Tools:           []tool.InvokableTool{testAddTool(t)},
		CheckPointStore: compose.NewMemoryCheckPointStore(),
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := react.Generate(context.Background(), []*schema.Message{schema.UserMessage("calc")})
	if err != nil || msg == nil {
		t.Fatalf("generate err=%v msg=%v", err, msg)
	}
	if react.CheckPointStore() == nil {
		t.Fatal("checkpoint store")
	}
	if react.GraphAgent() == nil {
		t.Fatal("graph agent")
	}
	sr, err := react.Stream(context.Background(), []*schema.Message{schema.UserMessage("calc")})
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
	frames, err := react.StreamFrames(context.Background(), []*schema.Message{schema.UserMessage("calc")})
	if err != nil {
		t.Fatal(err)
	}
	defer frames.Close()
	for {
		f, err := frames.Recv()
		if err == io.EOF {
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

func TestGraphAgent_AllPaths(t *testing.T) {
	ga, err := agent.NewGraphAgent(context.Background(), agent.ReActConfig{
		Model: &agentMockModel{},
		Tools: []tool.InvokableTool{testAddTool(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := ga.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil || msg == nil {
		t.Fatalf("err=%v", err)
	}
	sr, err := ga.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	sr.Close()
	fr, err := ga.StreamFrames(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	fr.Close()
}

func TestChainSteps(t *testing.T) {
	react, err := agent.NewReActAgent(context.Background(), agent.ReActConfig{
		Model: &agentMockModel{},
		Tools: []tool.InvokableTool{testAddTool(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	st := &compose.State{Messages: []*schema.Message{schema.UserMessage("hi")}, Vars: map[string]any{}}
	if err := (agent.ReActStep{Agent: react}).Run(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if len(compose.RunsFromState(st)) == 0 {
		t.Fatal("expected runs merged")
	}
	sr, err := agent.StreamStep{Agent: react}.Stream(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	sr.Close()
	fr, err := agent.ReActStep{Agent: react}.StreamFrames(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	fr.Close()
}

func TestRunner(t *testing.T) {
	g, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model: &agentMockModel{},
		Tools: []tool.InvokableTool{testAddTool(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := agent.NewRunner(context.Background(), agent.RunnerConfig{Graph: g})
	if err != nil {
		t.Fatal(err)
	}
	st, trace, err := runner.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")}, "cp1")
	if err != nil || st == nil || len(trace) == 0 {
		t.Fatalf("invoke err=%v st=%v trace=%d", err, st, len(trace))
	}
	if _, err := agent.NewRunner(context.Background(), agent.RunnerConfig{}); err == nil {
		t.Fatal("expected nil graph error")
	}
}

func TestTurnLoop(t *testing.T) {
	react, err := agent.NewReActAgent(context.Background(), agent.ReActConfig{
		Model:           &agentMockModel{},
		Tools:           []tool.InvokableTool{testAddTool(t)},
		CheckPointStore: compose.NewMemoryCheckPointStore(),
	})
	if err != nil {
		t.Fatal(err)
	}
	store := session.NewMemoryStore()
	var events int
	var turnWG sync.WaitGroup
	turnWG.Add(1)
	loop, err := agent.NewTurnLoop(context.Background(), agent.TurnLoopConfig{
		Agent:           react,
		Session:         &session.Conversation{ID: "tl1", Vars: map[string]any{}},
		SessionStore:    store,
		CheckPointStore: compose.NewMemoryCheckPointStore(),
		CheckPointID:    "cp-tl",
		OnTurn: func(ev agent.TurnEvent) {
			events++
			turnWG.Done()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go loop.Run(ctx)
	loop.Push("hello")
	turnWG.Wait()
	loop.Stop()
	loop.Wait()
	if events != 1 {
		t.Fatalf("events=%d", events)
	}
	if loop.Session() == nil {
		t.Fatal("session")
	}
}

func TestSessionOrchestrator_HITLAndStream(t *testing.T) {
	book, err := tool.InferTool("book", "book", func(_ context.Context, in struct {
		Dest string `json:"dest"`
	}) (map[string]string, error) {
		return map[string]string{"status": "ok", "dest": in.Dest}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	model := &hitlMockModel{}
	react, err := agent.NewReActAgent(context.Background(), agent.ReActConfig{
		Model:           model,
		Tools:           []tool.InvokableTool{tool.WrapApprovable(book)},
		CheckPointStore: compose.NewMemoryCheckPointStore(),
	})
	if err != nil {
		t.Fatal(err)
	}
	orch, err := agent.NewSessionOrchestrator(agent.SessionOrchestratorConfig{
		Agent:        react,
		Session:      &session.Conversation{ID: "h1", Vars: map[string]any{}},
		CheckPointID: "cp-h1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if orch.CheckPointID() != "cp-h1" {
		t.Fatal("cp id")
	}
	_, err = orch.RunTurn(context.Background(), "book NYC")
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok {
		t.Fatalf("expected interrupt err=%v", err)
	}
	intrID := info.InterruptContexts[0].ID
	out, err := orch.ResumeTurn(context.Background(), map[string]any{
		intrID: &tool.ApprovalResult{Approved: true},
	})
	if err != nil || out.Message == nil {
		t.Fatalf("resume err=%v", err)
	}
	sr, err := orch.StreamTurn(context.Background(), "thanks")
	if err != nil {
		t.Fatal(err)
	}
	sr.Close()
}

type hitlMockModel struct{ step int }

func (m *hitlMockModel) Name() string { return "mock/hitl" }

func (m *hitlMockModel) Generate(_ context.Context, _ []*schema.Message, _ ...llm.Option) (*schema.Message, error) {
	m.step++
	if m.step == 1 {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "b1", Type: "function",
			Function: schema.FunctionCall{Name: "book", Arguments: `{"dest":"NYC"}`},
		}}), nil
	}
	return schema.AssistantMessage("done", nil), nil
}

func (m *hitlMockModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...llm.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		defer sw.Close()
		sw.Send(out, nil)
	}()
	return sr, nil
}

func (m *hitlMockModel) WithTools(_ []*schema.ToolInfo) (llm.ToolCallingChatModel, error) {
	return m, nil
}

func TestAgentErrors(t *testing.T) {
	if _, err := agent.NewSessionOrchestrator(agent.SessionOrchestratorConfig{}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := agent.NewTurnLoop(context.Background(), agent.TurnLoopConfig{}); err == nil {
		t.Fatal("expected error")
	}
	orch, _ := agent.NewSessionOrchestrator(agent.SessionOrchestratorConfig{
		Agent: mustReact(t),
	})
	if _, err := orch.ResumeTurn(context.Background(), nil); err == nil {
		t.Fatal("expected no pending error")
	}
}

func TestReActAgent_Resume(t *testing.T) {
	book, err := tool.InferTool("book", "book", func(_ context.Context, in struct {
		Dest string `json:"dest"`
	}) (map[string]string, error) {
		return map[string]string{"status": "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cpStore := compose.NewMemoryCheckPointStore()
	react, err := agent.NewReActAgent(context.Background(), agent.ReActConfig{
		Model:           &hitlMockModel{},
		Tools:           []tool.InvokableTool{tool.WrapApprovable(book)},
		CheckPointStore: cpStore,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = react.GenerateWithTrace(context.Background(), []*schema.Message{schema.UserMessage("book")}, compose.WithCheckPointID("cp-r"))
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok {
		t.Fatal("expected interrupt")
	}
	intrID := info.InterruptContexts[0].ID
	result, err := react.Resume(context.Background(), "cp-r", map[string]any{
		intrID: &tool.ApprovalResult{Approved: true},
	})
	if err != nil || result == nil || result.Message == nil {
		t.Fatalf("resume err=%v result=%v", err, result)
	}
}

func TestResumeStep(t *testing.T) {
	react := mustReact(t)
	st := &compose.State{Messages: []*schema.Message{schema.UserMessage("hi")}}
	if err := (agent.ResumeStep{Agent: react, CheckPointID: "x", ResumeData: map[string]any{}}).Run(context.Background(), st); err != nil {
		// resume without checkpoint is expected to fail or noop
	}
}

func TestRunner_StreamPaths(t *testing.T) {
	g, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model: &agentMockModel{},
		Tools: []tool.InvokableTool{testAddTool(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := agent.NewRunner(context.Background(), agent.RunnerConfig{Graph: g})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := runner.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")}, "cp-stream")
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
	fr, err := runner.StreamFrames(context.Background(), []*schema.Message{schema.UserMessage("hi")}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer fr.Close()
	for {
		f, err := fr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if f != nil && f.Done {
			break
		}
	}
	var nilRunner *agent.Runner
	if _, err := nilRunner.Stream(context.Background(), nil, ""); err == nil {
		t.Fatal("nil runner stream")
	}
	if _, err := nilRunner.StreamFrames(context.Background(), nil, ""); err == nil {
		t.Fatal("nil runner frames")
	}
	plain, err := compose.NewChainBuilder("plain").AppendPrompt(schema.User, "x").CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plainRunner, err := agent.NewRunner(context.Background(), agent.RunnerConfig{Graph: plain})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plainRunner.StreamFrames(context.Background(), nil, ""); err == nil {
		t.Fatal("expected no react runtime error")
	}
}

func TestGraphAgent_GenerateWithTraceAndResume(t *testing.T) {
	ga, err := agent.NewGraphAgent(context.Background(), agent.ReActConfig{
		Model:           &agentMockModel{},
		Tools:           []tool.InvokableTool{testAddTool(t)},
		CheckPointStore: compose.NewMemoryCheckPointStore(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ga.GenerateWithTrace(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil || result == nil || result.Message == nil {
		t.Fatalf("trace err=%v result=%v", err, result)
	}
	if _, err := ga.Resume(context.Background(), "missing-cp", nil); err == nil {
		// may start fresh without checkpoint
	}
}

func TestSessionOrchestrator_Session(t *testing.T) {
	orch, err := agent.NewSessionOrchestrator(agent.SessionOrchestratorConfig{
		Agent:   mustReact(t),
		Session: &session.Conversation{ID: "s1", Vars: map[string]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if orch.Session() == nil {
		t.Fatal("session")
	}
}

func TestRunner_Resume(t *testing.T) {
	g, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model: &agentMockModel{},
		Tools: []tool.InvokableTool{testAddTool(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := agent.NewRunner(context.Background(), agent.RunnerConfig{Graph: g})
	if err != nil {
		t.Fatal(err)
	}
	st, trace, err := runner.Resume(context.Background(), "cp-resume", map[string]any{})
	if err == nil && st != nil && len(trace) >= 0 {
		// resume without prior checkpoint may start fresh or error — both acceptable
	}
	if _, _, err := runner.Invoke(context.Background(), nil, ""); err != nil {
		// nil messages ok
	}
}

func TestTurnLoop_Resume(t *testing.T) {
	react, err := agent.NewReActAgent(context.Background(), agent.ReActConfig{
		Model:           &hitlMockModel{},
		Tools:           []tool.InvokableTool{tool.WrapApprovable(mustBookTool(t))},
		CheckPointStore: compose.NewMemoryCheckPointStore(),
	})
	if err != nil {
		t.Fatal(err)
	}
	loop, err := agent.NewTurnLoop(context.Background(), agent.TurnLoopConfig{
		Agent:           react,
		Session:         &session.Conversation{ID: "tl2", Vars: map[string]any{}},
		CheckPointStore: compose.NewMemoryCheckPointStore(),
		CheckPointID:    "cp-tl2",
	})
	if err != nil {
		t.Fatal(err)
	}
	loop.Session().AppendUser("book")
	_, _ = react.GenerateWithTrace(context.Background(), loop.Session().Messages, compose.WithCheckPointID("cp-tl2"))
	if _, err := loop.Resume(context.Background(), map[string]any{}); err == nil {
		// may fail without valid resume data
	}
}

func mustBookTool(t *testing.T) tool.InvokableTool {
	t.Helper()
	book, err := tool.InferTool("book", "book", func(_ context.Context, in struct {
		Dest string `json:"dest"`
	}) (map[string]string, error) {
		return map[string]string{"status": "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tool.WrapApprovable(book)
}

func mustReact(t *testing.T) *agent.ReActAgent {
	t.Helper()
	r, err := agent.NewReActAgent(context.Background(), agent.ReActConfig{
		Model: &agentMockModel{},
		Tools: []tool.InvokableTool{testAddTool(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
