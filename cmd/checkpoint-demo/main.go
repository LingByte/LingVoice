// Command checkpoint-demo: Graph checkpoint + HITL interrupt/resume (Eino subset).
//
//	go run ./cmd/checkpoint-demo
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/agent"
	"github.com/LingByte/LingVoice/pkg/llm/compose"
)

type approvalInfo struct {
	Action string `json:"action"`
}

func main() {
	fmt.Fprintln(os.Stderr, "demo: checkpoint-demo | local HITL, no API key")

	store := compose.NewMemoryCheckPointStore()
	g := compose.NewGraph("ticket-booking")

	_ = g.AddLambdaNode("prepare", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["action"] = "book flight SFO → NYC ($420)"
		return nil
	})
	_ = g.AddLambdaNode("approve", func(ctx context.Context, st *compose.GraphState) error {
		action, _ := st.Vars["action"].(string)
		was, _, stored := compose.GetInterruptState[string](ctx)
		if !was {
			return compose.StatefulInterrupt(ctx, approvalInfo{Action: action}, action)
		}
		intrID := compose.ActiveInterruptID(ctx)
		target, has, decision := compose.GetResumeContext[string](ctx, intrID)
		if !target || !has {
			return compose.StatefulInterrupt(ctx, approvalInfo{Action: stored}, stored)
		}
		if decision != "approved" {
			st.Vars["status"] = "rejected"
			return nil
		}
		st.Vars["status"] = "approved"
		st.Vars["ticket"] = stored
		return nil
	})
	_ = g.AddLambdaNode("confirm", func(_ context.Context, st *compose.GraphState) error {
		if st.Vars["status"] == "approved" {
			st.Vars["message"] = fmt.Sprintf("confirmed: %v", st.Vars["ticket"])
		} else {
			st.Vars["message"] = "booking cancelled"
		}
		return nil
	})
	_ = g.AddEdge(compose.START, "prepare")
	_ = g.AddEdge("prepare", "approve")
	_ = g.AddEdge("approve", "confirm")
	_ = g.AddEdge("confirm", compose.END)

	compiled, err := g.Compile(compose.WithCheckPointStore(store))
	if err != nil {
		fail(err)
	}

	runner, err := agent.NewRunner(context.Background(), agent.RunnerConfig{Graph: compiled, CheckPointStore: store})
	if err != nil {
		fail(err)
	}

	cpID := "demo-cp-001"
	fmt.Fprintln(os.Stderr, "--- phase 1: run until human approval ---")
	st, trace, err := runner.Invoke(context.Background(), nil, cpID)
	printTrace(trace)
	info, interrupted := compose.ExtractInterruptInfo(err)
	if !interrupted {
		fail(fmt.Errorf("expected interrupt, got err=%v vars=%v", err, st.Vars))
	}
	b, _ := json.MarshalIndent(info, "", "  ")
	fmt.Fprintf(os.Stderr, "interrupt:\n%s\n", b)
	fmt.Fprintln(os.Stderr, ">>> simulate human: approved")

	intrID := info.InterruptContexts[0].ID
	fmt.Fprintln(os.Stderr, "--- phase 2: resume with approval ---")
	st, trace, err = runner.Resume(context.Background(), cpID, map[string]any{intrID: "approved"})
	printTrace(trace)
	if err != nil {
		fail(err)
	}
	fmt.Printf("result: %v\n", st.Vars["message"])
}

func printTrace(trace []compose.GraphStep) {
	for i, s := range trace {
		fmt.Fprintf(os.Stderr, "  [%d] %s (%s)\n", i, s.Node, s.Duration)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
