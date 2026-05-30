// Command workflow-demo: Workflow AddBranch + FieldMapping (local mock).
//
//	go run ./cmd/workflow-demo
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func main() {
	fmt.Fprintln(os.Stderr, "demo: workflow-demo | Workflow branch + field mapping")

	wf := compose.NewWorkflow("workflow-demo")
	_ = wf.AddInputNode("input")
	_ = wf.AddLambdaStep("classify", func(_ context.Context, st *compose.GraphState) error {
		text, _ := st.Vars["input"].(string)
		mode := "chat"
		if len(text) > 0 && text[0] == '{' {
			mode = "json"
		}
		st.Vars["mode"] = mode
		return nil
	})
	branch := compose.BranchStep{
		Select: func(_ context.Context, st *compose.State) (string, error) {
			return st.Vars["mode"].(string), nil
		},
		Steps: map[string]compose.Step{
			"chat": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
				st.LastOutput = schema.AssistantMessage("chat:"+st.Vars["input"].(string), nil)
				return nil
			}},
			"json": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
				st.LastOutput = schema.AssistantMessage(`{"kind":"json"}`, nil)
				return nil
			}},
		},
	}
	if err := wf.AddBranch("classify", branch); err != nil {
		fail(err)
	}
	_ = wf.AddLambdaStep("summarize", func(_ context.Context, st *compose.GraphState) error {
		in, _ := st.Vars["input:summarize"].(map[string]any)
		if out, ok := in["last_output"].(*schema.Message); ok && out != nil {
			st.LastOutput = schema.AssistantMessage("summary:"+out.PlainText(), nil)
		}
		return nil
	})
	wf.Node("summarize").JoinAnyPredecessor()
	_ = wf.Node("summarize").AddInput("classify_branch_chat", compose.FromNodeField("classify_branch_chat", "last_output").ToField("last_output"))
	_ = wf.Node("summarize").AddInput("classify_branch_json", compose.FromNodeField("classify_branch_json", "last_output").ToField("last_output"))

	_ = wf.AddEdge(compose.START, "input")
	_ = wf.AddEdge("input", "classify")
	_ = wf.AddEdge("classify_branch_chat", "summarize")
	_ = wf.AddEdge("classify_branch_json", "summarize")
	_ = wf.AddEdge("summarize", compose.END)

	cg, err := wf.Compile()
	if err != nil {
		fail(err)
	}

	for _, msg := range []string{"hello", "{}"} {
		st, _, err := cg.Invoke(context.Background(), []*schema.Message{schema.UserMessage(msg)})
		if err != nil {
			fail(err)
		}
		fmt.Printf("in=%q → %s\n", msg, st.LastOutput.PlainText())
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
