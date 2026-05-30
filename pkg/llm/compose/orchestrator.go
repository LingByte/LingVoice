package compose

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/llm/session"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// OrchestratorConfig wires template + ReAct loop into one pipeline.
type OrchestratorConfig struct {
	Name      string
	Template  *prompt.ChatTemplate
	Loop      *ToolLoop
	Session   *session.Conversation
	Vars      map[string]any
}

// Orchestrator runs Prompt → ToolLoop over session history.
type Orchestrator struct {
	name     string
	template *prompt.ChatTemplate
	loop     *ToolLoop
	sess     *session.Conversation
}

// NewOrchestrator builds a pipeline from config.
func NewOrchestrator(cfg OrchestratorConfig) (*Orchestrator, error) {
	if cfg.Loop == nil {
		return nil, fmt.Errorf("compose: orchestrator requires ToolLoop")
	}
	name := cfg.Name
	if name == "" {
		name = "orchestrator"
	}
	sess := cfg.Session
	if sess == nil {
		sess = &session.Conversation{Vars: map[string]any{}}
	}
	if sess.Vars == nil {
		sess.Vars = map[string]any{}
	}
	for k, v := range cfg.Vars {
		sess.Vars[k] = v
	}
	return &Orchestrator{
		name:     name,
		template: cfg.Template,
		loop:     cfg.Loop,
		sess:     sess,
	}, nil
}

// OrchestratorResult is one pipeline run output.
type OrchestratorResult struct {
	Message *schema.Message
	Trace   *LoopTrace
	Delta   []*schema.Message
	Session *session.Conversation
}

// Run appends user input, renders template vars, executes ToolLoop, updates session.
func (o *Orchestrator) Run(ctx context.Context, userText string) (*OrchestratorResult, error) {
	if o == nil || o.loop == nil {
		return nil, fmt.Errorf("compose: nil orchestrator")
	}
	ctx = callback.InitRun(ctx, &callback.RunInfo{Name: o.name, Component: callback.ComponentChain})

	o.sess.AppendUser(userText)
	o.sess.Vars["history"] = o.sess.Messages

	msgs := append([]*schema.Message(nil), o.sess.Messages...)
	if o.template != nil {
		extra, err := o.template.Format(ctx, o.sess.Vars)
		if err != nil {
			return nil, err
		}
		msgs = append(extra, msgs...)
	}

	inputLen := len(msgs)
	out, delta, trace, err := o.loop.RunWithTrace(ctx, msgs)
	result := &OrchestratorResult{
		Message: out,
		Trace:   trace,
		Delta:   delta,
		Session: o.sess,
	}
	if err != nil {
		return result, err
	}
	if out != nil {
		o.sess.AppendAssistant(out)
	}
	_ = inputLen // template may prepend messages; delta already computed by loop
	return result, nil
}

// Session returns the live conversation handle.
func (o *Orchestrator) Session() *session.Conversation {
	if o == nil {
		return nil
	}
	return o.sess
}

// Messages returns current session messages.
func (o *Orchestrator) Messages() []*schema.Message {
	if o == nil || o.sess == nil {
		return nil
	}
	return append([]*schema.Message(nil), o.sess.Messages...)
}
