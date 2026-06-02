package llm

import (
	"context"
	"fmt"
	"testing"
)

func TestChain_AddStep(t *testing.T) {
	chain := NewChain("test-chain")
	if chain.GetName() != "test-chain" {
		t.Errorf("expected chain name 'test-chain', got %s", chain.GetName())
	}

	step := NewSimpleStep(
		"step1",
		[]string{"input"},
		[]string{"output"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{"output": "result"}, nil
		},
	)

	chain.AddStep(step)
	if len(chain.steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(chain.steps))
	}
}

func TestChain_Execute_SingleStep(t *testing.T) {
	chain := NewChain("test-chain")
	step := NewSimpleStep(
		"step1",
		[]string{"input"},
		[]string{"output"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			val := input["input"].(string)
			return map[string]interface{}{"output": val + "-processed"}, nil
		},
	)

	chain.AddStep(step)

	result, err := chain.Execute(context.Background(), map[string]interface{}{"input": "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result["output"] != "test-processed" {
		t.Errorf("expected 'test-processed', got %v", result["output"])
	}
}

func TestChain_Execute_MultipleSteps(t *testing.T) {
	chain := NewChain("test-chain")

	step1 := NewSimpleStep(
		"step1",
		[]string{"input"},
		[]string{"intermediate"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			val := input["input"].(string)
			return map[string]interface{}{"intermediate": val + "-1"}, nil
		},
	)

	step2 := NewSimpleStep(
		"step2",
		[]string{"intermediate"},
		[]string{"output"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			val := input["intermediate"].(string)
			return map[string]interface{}{"output": val + "-2"}, nil
		},
	)

	chain.AddStep(step1).AddStep(step2)

	result, err := chain.Execute(context.Background(), map[string]interface{}{"input": "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result["output"] != "test-1-2" {
		t.Errorf("expected 'test-1-2', got %v", result["output"])
	}
}

func TestChain_Execute_MissingInput(t *testing.T) {
	chain := NewChain("test-chain")
	step := NewSimpleStep(
		"step1",
		[]string{"required_input"},
		[]string{"output"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{"output": "result"}, nil
		},
	)

	chain.AddStep(step)

	_, err := chain.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing input")
	}
}

func TestChain_Execute_StepError(t *testing.T) {
	chain := NewChain("test-chain")
	step := NewSimpleStep(
		"step1",
		[]string{"input"},
		[]string{"output"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			return nil, fmt.Errorf("step error")
		},
	)

	chain.AddStep(step)

	_, err := chain.Execute(context.Background(), map[string]interface{}{"input": "test"})
	if err == nil {
		t.Fatal("expected error from step")
	}
}

func TestSimpleStep_GetInputKeys(t *testing.T) {
	step := NewSimpleStep(
		"test",
		[]string{"key1", "key2"},
		[]string{"output"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			return nil, nil
		},
	)

	keys := step.GetInputKeys()
	if len(keys) != 2 {
		t.Errorf("expected 2 input keys, got %d", len(keys))
	}
}

func TestSimpleStep_GetOutputKeys(t *testing.T) {
	step := NewSimpleStep(
		"test",
		[]string{"input"},
		[]string{"out1", "out2"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			return nil, nil
		},
	)

	keys := step.GetOutputKeys()
	if len(keys) != 2 {
		t.Errorf("expected 2 output keys, got %d", len(keys))
	}
}

func TestConditionalStep_TrueBranch(t *testing.T) {
	trueStep := NewSimpleStep(
		"true-step",
		[]string{},
		[]string{"result"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{"result": "true-branch"}, nil
		},
	)

	falseStep := NewSimpleStep(
		"false-step",
		[]string{},
		[]string{"result"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{"result": "false-branch"}, nil
		},
	)

	condStep := NewConditionalStep(
		"cond",
		func(ctx context.Context, input map[string]interface{}) (bool, error) {
			return true, nil
		},
		trueStep,
		falseStep,
	)

	result, err := condStep.Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result["result"] != "true-branch" {
		t.Errorf("expected 'true-branch', got %v", result["result"])
	}
}

func TestConditionalStep_FalseBranch(t *testing.T) {
	trueStep := NewSimpleStep(
		"true-step",
		[]string{},
		[]string{"result"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{"result": "true-branch"}, nil
		},
	)

	falseStep := NewSimpleStep(
		"false-step",
		[]string{},
		[]string{"result"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{"result": "false-branch"}, nil
		},
	)

	condStep := NewConditionalStep(
		"cond",
		func(ctx context.Context, input map[string]interface{}) (bool, error) {
			return false, nil
		},
		trueStep,
		falseStep,
	)

	result, err := condStep.Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result["result"] != "false-branch" {
		t.Errorf("expected 'false-branch', got %v", result["result"])
	}
}

func TestLoopStep_Execute(t *testing.T) {
	counter := 0
	step := NewSimpleStep(
		"loop-step",
		[]string{"count"},
		[]string{"count"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			count := input["count"].(int)
			return map[string]interface{}{"count": count + 1}, nil
		},
	)

	loopStep := NewLoopStep(
		"loop",
		step,
		func(ctx context.Context, input map[string]interface{}) (bool, error) {
			count := input["count"].(int)
			counter++
			return count < 3, nil
		},
		10,
	)

	result, err := loopStep.Execute(context.Background(), map[string]interface{}{"count": 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result["count"] != 3 {
		t.Errorf("expected count=3, got %v", result["count"])
	}

	// Counter should be 4: check at 0 (true), 1 (true), 2 (true), 3 (false)
	if counter != 4 {
		t.Errorf("expected 4 condition checks, got %d", counter)
	}
}
