package adk_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/adk"
	"github.com/LingByte/LingVoice/pkg/llm/adk/prebuilt/deep"
	"github.com/LingByte/LingVoice/pkg/llm/adk/prebuilt/planexecute"
	"github.com/LingByte/LingVoice/pkg/llm/adk/prebuilt/supervisor"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type stubGen struct{ name, reply string }

func (s stubGen) Generate(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
	return schema.AssistantMessage(s.reply, nil), nil
}

type transferRoot struct{ target string }

func (r transferRoot) Name(_ context.Context) string  { return "root" }
func (r transferRoot) Description(_ context.Context) string { return "" }
func (r transferRoot) Run(_ context.Context, _ *adk.AgentInput, _ ...adk.RunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	it := adk.NewAsyncIterator[*adk.AgentEvent](2)
	it.Send(adk.MakeTransferEvent(r.target))
	it.Send(&adk.AgentEvent{Kind: adk.EventDone})
	it.Close()
	return it
}

func TestGenerateAdapter(t *testing.T) {
	ag := adk.NewGenerateAdapter("x", "desc", stubGen{name: "x", reply: "hi"})
	msg, err := adk.RunToMessage(context.Background(), ag, &adk.AgentInput{
		Messages: []*schema.Message{schema.UserMessage("q")},
	})
	if err != nil || msg.Content != "hi" {
		t.Fatalf("msg=%+v err=%v", msg, err)
	}
}

func TestDeterministicTransfer(t *testing.T) {
	root := transferRoot{target: "worker"}
	workers := map[string]adk.Agent{
		"worker": adk.NewGenerateAdapter("worker", "", stubGen{reply: "done"}),
	}
	dt, err := adk.NewDeterministicTransferAgent(root, workers, 4)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := adk.RunToMessage(context.Background(), dt, &adk.AgentInput{Messages: []*schema.Message{schema.UserMessage("go")}})
	if err != nil || msg.Content != "done" {
		t.Fatalf("msg=%+v err=%v", msg, err)
	}
}

func TestSequentialAndLoop(t *testing.T) {
	seq, err := adk.NewSequentialAgent("seq",
		adk.NewGenerateAdapter("a", "", stubGen{reply: "1"}),
		adk.NewGenerateAdapter("b", "", stubGen{reply: "2"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := adk.RunToMessage(context.Background(), seq, &adk.AgentInput{Messages: []*schema.Message{schema.UserMessage("x")}})
	if err != nil || msg.Content != "2" {
		t.Fatalf("seq msg=%+v err=%v", msg, err)
	}

	n := 0
	loop, err := adk.NewLoopAgent("loop",
		adk.NewGenerateAdapter("body", "", stubGen{reply: "tick"}),
		3,
		func(_ context.Context, msgs []*schema.Message) bool {
			return len(msgs) >= 4
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = n
	it := loop.Run(context.Background(), &adk.AgentInput{Messages: []*schema.Message{schema.UserMessage("start")}})
	var count int
	for {
		ev, ok := it.Next()
		if !ok {
			break
		}
		if ev != nil && ev.Kind == adk.EventMessage {
			count++
		}
	}
	if count != 3 {
		t.Fatalf("loop count=%d", count)
	}
}

func TestSupervisorPlanDeepPrebuilt(t *testing.T) {
	sup, err := supervisor.New(supervisor.Config{
		Supervisor: transferRoot{target: "w1"},
		Workers: map[string]adk.Agent{
			"w1": adk.NewGenerateAdapter("w1", "", stubGen{reply: "worker"}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := sup.RunToMessage(context.Background(), []*schema.Message{schema.UserMessage("task")})
	if err != nil || msg.Content != "worker" {
		t.Fatalf("supervisor msg=%+v err=%v", msg, err)
	}

	pe, err := planexecute.New(planexecute.Config{
		Executor: stubExec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err = adk.RunToMessage(context.Background(), pe, &adk.AgentInput{
		Messages: []*schema.Message{schema.UserMessage("step1; step2")},
	})
	if err != nil || msg == nil {
		t.Fatalf("plan msg=%+v err=%v", msg, err)
	}

	da, err := deep.New(deep.Config{
		Lead: transferRoot{target: "sub"},
		SubAgents: map[string]adk.Agent{
			"sub": adk.NewGenerateAdapter("sub", "", stubGen{reply: "deep"}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err = adk.RunToMessage(context.Background(), da, &adk.AgentInput{Messages: []*schema.Message{schema.UserMessage("go")}})
	if err != nil || msg.Content != "deep" {
		t.Fatalf("deep msg=%+v err=%v", msg, err)
	}
}

type stubExec struct{}

func (stubExec) Execute(_ context.Context, step string, _ []*schema.Message) (*schema.Message, error) {
	return schema.AssistantMessage("exec:"+step, nil), nil
}

func TestAsyncIteratorAndFlowErrors(t *testing.T) {
	it := adk.NewAsyncIterator[int](1)
	it.Send(1)
	it.Close()
	it.Send(2)
	v, ok := it.Next()
	if !ok || v != 1 {
		t.Fatalf("v=%d ok=%v", v, ok)
	}
	if _, ok := it.Next(); ok {
		t.Fatal("expected closed")
	}
	if adk.FinalMessage(nil) != nil {
		t.Fatal("nil iterator")
	}

	if _, err := adk.SetSubAgents(adk.SubAgentsConfig{}); err == nil {
		t.Fatal("expected parent error")
	}
	if _, err := adk.SetSubAgents(adk.SubAgentsConfig{
		Parent:    adk.NewGenerateAdapter("p", "", stubGen{}),
		SubAgents: map[string]adk.Agent{"": adk.NewGenerateAdapter("x", "", stubGen{})},
	}); err == nil {
		t.Fatal("expected empty name error")
	}
	if _, err := adk.SetSubAgents(adk.SubAgentsConfig{
		Parent:    adk.NewGenerateAdapter("p", "", stubGen{}),
		SubAgents: map[string]adk.Agent{"bad": nil},
	}); err == nil {
		t.Fatal("expected nil sub-agent error")
	}
}

func TestPlanExecuteAndDeepEdgeCases(t *testing.T) {
	if _, err := planexecute.New(planexecute.Config{}); err == nil {
		t.Fatal("expected executor error")
	}
	pe, _ := planexecute.New(planexecute.Config{Executor: stubExec{}})
	it := pe.Run(context.Background(), &adk.AgentInput{Messages: nil})
	ev, ok := it.Next()
	if !ok || ev.Err == nil {
		t.Fatalf("expected empty input error ev=%+v", ev)
	}

	da, err := deep.New(deep.Config{
		Lead: adk.NewGenerateAdapter("lead", "", stubGen{reply: "lead-only"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := adk.RunToMessage(context.Background(), da, &adk.AgentInput{
		Messages: []*schema.Message{schema.UserMessage("x")},
	})
	if err != nil || msg.Content != "lead-only" {
		t.Fatalf("lead msg=%+v err=%v", msg, err)
	}

	da2, _ := deep.New(deep.Config{
		Lead: transferRoot{target: "sub"},
		SubAgents: map[string]adk.Agent{
			"sub": adk.NewGenerateAdapter("sub", "", stubGen{reply: "sub"}),
		},
	})
	if _, err := da2.DelegateTask(context.Background(), "sub", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := da2.DelegateTask(context.Background(), "missing", nil); err == nil {
		t.Fatal("expected unknown task")
	}
}

func TestSupervisorErrors(t *testing.T) {
	if _, err := supervisor.New(supervisor.Config{}); err == nil {
		t.Fatal("expected supervisor error")
	}
	sup, _ := supervisor.New(supervisor.Config{
		Supervisor: transferRoot{target: "missing"},
		Workers: map[string]adk.Agent{
			"w1": adk.NewGenerateAdapter("w1", "", stubGen{reply: "ok"}),
		},
	})
	it := sup.Run(context.Background(), &adk.AgentInput{Messages: []*schema.Message{schema.UserMessage("x")}})
	for {
		ev, ok := it.Next()
		if !ok {
			break
		}
		if ev != nil && ev.Err != nil {
			return
		}
	}
	t.Fatal("expected unknown worker error")
}

func TestTransferMaxHops(t *testing.T) {
	chain := map[string]adk.Agent{}
	root := transferRoot{target: "a"}
	chain["a"] = adk.Agent(transferAgent{target: "a"})
	dt, err := adk.NewDeterministicTransferAgent(root, chain, 2)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adk.RunToMessage(context.Background(), dt, &adk.AgentInput{
		Messages: []*schema.Message{schema.UserMessage("x")},
	})
	if err == nil {
		t.Fatal("expected max hops error")
	}
}

type transferAgent struct{ target string }

func (t transferAgent) Name(context.Context) string        { return t.target }
func (t transferAgent) Description(context.Context) string { return "" }
func (t transferAgent) Run(context.Context, *adk.AgentInput, ...adk.RunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	it := adk.NewAsyncIterator[*adk.AgentEvent](1)
	it.Send(adk.MakeTransferEvent(t.target))
	it.Close()
	return it
}
