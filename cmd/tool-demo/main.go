// Command tool-demo: ToolNode executes a single round of tool calls (mock assistant message).
//
//	go run ./cmd/tool-demo
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/cmd/internal/demo"
	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func main() {
	fmt.Fprintln(os.Stderr, "demo: tool-demo | local InferTool, no API key")

	tools, err := demo.CalculatorTools()
	demo.Fatal(err)

	node, err := compose.NewToolNode(context.Background(), &compose.ToolNodeConfig{
		Tools:        tools,
		ErrorHandler: tool.DefaultErrorHandler,
	})
	demo.Fatal(err)

	// Simulate model requesting add(12, 30) and sqrt(144).
	assistant := schema.AssistantMessage("", []schema.ToolCall{
		{ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "add", Arguments: `{"a":12,"b":30}`}},
		{ID: "c2", Type: "function", Function: schema.FunctionCall{Name: "sqrt", Arguments: `{"x":144}`}},
	})

	msgs, err := node.Invoke(context.Background(), assistant)
	demo.Fatal(err)

	for _, m := range msgs {
		var pretty map[string]any
		_ = json.Unmarshal([]byte(m.Content), &pretty)
		b, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Printf("tool[%s] %s\n", m.ToolName, string(b))
	}
}
