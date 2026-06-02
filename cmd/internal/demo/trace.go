package demo

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// PrintLoopTrace prints ReAct steps to stderr (human-readable).
func PrintLoopTrace(trace *compose.LoopTrace) {
	if trace == nil || len(trace.Steps) == 0 {
		fmt.Fprintln(os.Stderr, "--- react trace: (empty) ---")
		return
	}
	fmt.Fprintf(os.Stderr, "--- react trace (%d steps, used_tools=%v) ---\n",
		len(trace.Steps), trace.UsedTools)
	for i, step := range trace.Steps {
		switch step.Phase {
		case compose.PhaseModel:
			fmt.Fprintf(os.Stderr, "  [%d] round=%d model (%s)\n", i, step.Round, step.Duration)
			printAssistantStep(step.Message)
		case compose.PhaseTools:
			fmt.Fprintf(os.Stderr, "  [%d] round=%d tools (%s)\n", i, step.Round, step.Duration)
			for _, m := range step.ToolMsgs {
				if m == nil {
					continue
				}
				fmt.Fprintf(os.Stderr, "       → tool[%s]: %s\n", m.ToolName, m.Content)
			}
		}
	}
	if b, err := json.MarshalIndent(trace, "", "  "); err == nil {
		fmt.Fprintf(os.Stderr, "--- react trace json ---\n%s\n", b)
	}
}

func printAssistantStep(msg *schema.Message) {
	if msg == nil {
		return
	}
	if msg.Content != "" {
		fmt.Fprintf(os.Stderr, "       content: %s\n", msg.Content)
	}
	if len(msg.ToolCalls) == 0 {
		fmt.Fprintln(os.Stderr, "       (no tool calls — model answered directly)")
		return
	}
	for _, tc := range msg.ToolCalls {
		fmt.Fprintf(os.Stderr, "       → call %s(%s)\n", tc.Function.Name, tc.Function.Arguments)
	}
}

// PrintLoopTraceWarning warns when model skipped tools.
func PrintLoopTraceWarning(trace *compose.LoopTrace) {
	if trace == nil || trace.UsedTools {
		return
	}
	fmt.Fprintln(os.Stderr, "hint: model did not invoke tools (try -force-tools or a harder prompt)")
}
