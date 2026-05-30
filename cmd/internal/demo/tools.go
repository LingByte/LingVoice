package demo

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/tool"
)

// CalculatorTools returns demo arithmetic tools (InferTool).
func CalculatorTools() ([]tool.InvokableTool, error) {
	add, err := tool.InferTool("add", "Add two numbers and return sum.", func(_ context.Context, in struct {
		A float64 `json:"a" desc:"first number"`
		B float64 `json:"b" desc:"second number"`
	}) (map[string]float64, error) {
		return map[string]float64{"sum": in.A + in.B}, nil
	})
	if err != nil {
		return nil, err
	}
	mul, err := tool.InferTool("multiply", "Multiply two numbers.", func(_ context.Context, in struct {
		A float64 `json:"a"`
		B float64 `json:"b"`
	}) (map[string]float64, error) {
		return map[string]float64{"product": in.A * in.B}, nil
	})
	if err != nil {
		return nil, err
	}
	sqrt, err := tool.InferTool("sqrt", "Square root of a non-negative number.", func(_ context.Context, in struct {
		X float64 `json:"x" desc:"non-negative value"`
	}) (map[string]float64, error) {
		if in.X < 0 {
			return nil, fmt.Errorf("sqrt: negative input %v", in.X)
		}
		return map[string]float64{"sqrt": math.Sqrt(in.X)}, nil
	})
	if err != nil {
		return nil, err
	}
	now, err := tool.InferTool("now", "Return current UTC time in RFC3339.", func(_ context.Context, _ struct{}) (map[string]string, error) {
		return map[string]string{"time": time.Now().UTC().Format(time.RFC3339)}, nil
	})
	if err != nil {
		return nil, err
	}
	return []tool.InvokableTool{add, mul, sqrt, now}, nil
}
