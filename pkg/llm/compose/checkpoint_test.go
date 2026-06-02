package compose_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
)

func TestCheckpoint_StaticInterruptResume(t *testing.T) {
	store := compose.NewMemoryCheckPointStore()
	g := compose.NewGraph("hitl")
	_ = g.AddLambdaNode("prepare", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["action"] = "delete prod db"
		return nil
	})
	_ = g.AddLambdaNode("execute", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["status"] = "executed"
		return nil
	})
	_ = g.AddEdge(compose.START, "prepare")
	_ = g.AddEdge("prepare", "execute")
	_ = g.AddEdge("execute", compose.END)

	runner, err := g.Compile(
		compose.WithCheckPointStore(store),
		compose.WithInterruptAfterNodes("prepare"),
	)
	if err != nil {
		t.Fatal(err)
	}

	cpID := "cp-test-1"
	st, _, err := runner.Invoke(context.Background(), nil, compose.WithCheckPointID(cpID))
	info, interrupted := compose.ExtractInterruptInfo(err)
	if !interrupted {
		t.Fatalf("expected interrupt, err=%v st=%+v", err, st)
	}
	if info == nil || len(info.AfterNodes) != 1 || info.AfterNodes[0] != "prepare" {
		t.Fatalf("info=%+v", info)
	}

	st, trace, err := runner.Invoke(context.Background(), nil, compose.WithCheckPointID(cpID))
	if err != nil {
		t.Fatal(err)
	}
	if st.Vars["status"] != "executed" {
		t.Fatalf("vars=%v trace=%v", st.Vars, trace)
	}
}

func TestCheckpoint_StatefulInterruptResume(t *testing.T) {
	store := compose.NewMemoryCheckPointStore()
	g := compose.NewGraph("approval")
	_ = g.AddLambdaNode("approve", func(ctx context.Context, st *compose.GraphState) error {
		was, info, payload := compose.GetInterruptState[string](ctx)
		if !was {
			return compose.StatefulInterrupt(ctx, map[string]string{"need": "approval"}, "book-flight-NY")
		}
		target, has, decision := compose.GetResumeContext[string](ctx, compose.ActiveInterruptID(ctx))
		_ = info
		if target && has && decision == "approved" {
			st.Vars["result"] = "booked:" + payload
			return nil
		}
		return compose.StatefulInterrupt(ctx, "still waiting", payload)
	})
	_ = g.AddEdge(compose.START, "approve")
	_ = g.AddEdge("approve", compose.END)

	runner, err := g.Compile(compose.WithCheckPointStore(store))
	if err != nil {
		t.Fatal(err)
	}

	cpID := "cp-approval"
	_, _, err = runner.Invoke(context.Background(), nil, compose.WithCheckPointID(cpID))
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok || info == nil || len(info.InterruptContexts) == 0 {
		t.Fatalf("interrupt=%v err=%v", info, err)
	}
	intrID := info.InterruptContexts[0].ID

	st, _, err := runner.Invoke(context.Background(), nil,
		compose.WithCheckPointID(cpID),
		compose.WithResumeData(map[string]any{intrID: "approved"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if st.Vars["result"] != "booked:book-flight-NY" {
		t.Fatalf("vars=%v", st.Vars)
	}
}

func TestBranchStep(t *testing.T) {
	st := &compose.State{Vars: map[string]any{"mode": "b"}}
	err := compose.BranchStep{
		Select: func(_ context.Context, st *compose.State) (string, error) {
			return st.Vars["mode"].(string), nil
		},
		Steps: map[string]compose.Step{
			"b": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
				st.Vars["path"] = "b"
				return nil
			}},
		},
	}.Run(context.Background(), st)
	if err != nil || st.Vars["path"] != "b" {
		t.Fatalf("err=%v vars=%v", err, st.Vars)
	}
}

func TestMemoryCheckPointStore_RoundTrip(t *testing.T) {
	store := compose.NewMemoryCheckPointStore()
	cp := &compose.Checkpoint{GraphName: "g", NextNode: "n1", State: &compose.GraphState{Vars: map[string]any{"x": 1}}}
	b, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "k", b); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(context.Background(), "k")
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	if string(got) != string(b) {
		t.Fatalf("got=%s", got)
	}
}
