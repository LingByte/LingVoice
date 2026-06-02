package agent

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/llm/session"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// SessionOrchestrator runs multi-turn ReAct with template, session, and checkpoint/HITL.
type SessionOrchestrator struct {
	name         string
	agent        *ReActAgent
	template     *prompt.ChatTemplate
	sess         *session.Conversation
	store        session.Store
	checkpointID string
}

// SessionOrchestratorConfig configures a session-backed orchestrator.
type SessionOrchestratorConfig struct {
	Name         string
	Agent        *ReActAgent
	Template     *prompt.ChatTemplate
	Session      *session.Conversation
	SessionStore session.Store
	CheckPointID string
}

// NewSessionOrchestrator builds an orchestrator.
func NewSessionOrchestrator(cfg SessionOrchestratorConfig) (*SessionOrchestrator, error) {
	if cfg.Agent == nil {
		return nil, composeErr("session orchestrator requires ReActAgent")
	}
	name := cfg.Name
	if name == "" {
		name = "session-orch"
	}
	sess := cfg.Session
	if sess == nil {
		sess = &session.Conversation{Vars: map[string]any{}}
	}
	if sess.Vars == nil {
		sess.Vars = map[string]any{}
	}
	cpID := cfg.CheckPointID
	if cpID == "" {
		cpID = sess.ID
	}
	if cpID == "" {
		cpID = "default-cp"
	}
	return &SessionOrchestrator{
		name:         name,
		agent:        cfg.Agent,
		template:     cfg.Template,
		sess:         sess,
		store:        cfg.SessionStore,
		checkpointID: cpID,
	}, nil
}

// SessionTurnResult is one orchestrated turn.
type SessionTurnResult struct {
	Message   *schema.Message
	Trace     *compose.LoopTrace
	Session   *session.Conversation
	Interrupt *compose.InterruptInfo
}

// RunTurn appends user text, runs ReAct with checkpoint, updates session.
func (o *SessionOrchestrator) RunTurn(ctx context.Context, userText string) (*SessionTurnResult, error) {
	if o == nil || o.agent == nil {
		return nil, composeErr("nil session orchestrator")
	}
	o.sess.AppendUser(userText)
	msgs, err := o.renderMessages(ctx)
	if err != nil {
		return nil, err
	}
	result, err := o.agent.GenerateWithTrace(ctx, msgs, compose.WithCheckPointID(o.checkpointID))
	out := &SessionTurnResult{Session: o.sess}
	if result != nil {
		out.Message = result.Message
		out.Trace = result.Trace
	}
	if err != nil {
		if info, ok := compose.ExtractInterruptInfo(err); ok {
			out.Interrupt = info
			o.sess.SetPending(o.checkpointID, info)
			o.persist(ctx)
			return out, err
		}
		return out, err
	}
	o.sess.ClearPending()
	if result.Message != nil {
		o.sess.AppendAssistant(result.Message)
	}
	o.persist(ctx)
	return out, nil
}

// ResumeTurn continues after HITL with resume payloads keyed by interrupt id.
func (o *SessionOrchestrator) ResumeTurn(ctx context.Context, resume map[string]any) (*SessionTurnResult, error) {
	if o == nil || o.agent == nil {
		return nil, composeErr("nil session orchestrator")
	}
	if !o.sess.HasPending() {
		return nil, fmt.Errorf("agent: session has no pending interrupt")
	}
	result, err := o.agent.Resume(ctx, o.checkpointID, resume)
	out := &SessionTurnResult{Session: o.sess}
	if result != nil {
		out.Message = result.Message
		out.Trace = result.Trace
	}
	if err != nil {
		if info, ok := compose.ExtractInterruptInfo(err); ok {
			out.Interrupt = info
			o.sess.SetPending(o.checkpointID, info)
			o.persist(ctx)
			return out, err
		}
		return out, err
	}
	o.sess.ClearPending()
	if result.Message != nil {
		o.sess.AppendAssistant(result.Message)
	}
	o.persist(ctx)
	return out, nil
}

// StreamTurn streams one user turn with frame metadata.
func (o *SessionOrchestrator) StreamTurn(ctx context.Context, userText string) (*compose.StreamFrameReader, error) {
	if o == nil || o.agent == nil {
		return nil, composeErr("nil session orchestrator")
	}
	o.sess.AppendUser(userText)
	msgs, err := o.renderMessages(ctx)
	if err != nil {
		return nil, err
	}
	return o.agent.StreamFrames(ctx, msgs)
}

func (o *SessionOrchestrator) renderMessages(ctx context.Context) ([]*schema.Message, error) {
	o.sess.Vars["history"] = o.sess.Messages
	msgs := append([]*schema.Message(nil), o.sess.Messages...)
	if o.template != nil {
		extra, err := o.template.Format(ctx, o.sess.Vars)
		if err != nil {
			return nil, err
		}
		msgs = append(extra, msgs...)
	}
	return msgs, nil
}

func (o *SessionOrchestrator) persist(ctx context.Context) {
	if o.store != nil && o.sess.ID != "" {
		_ = o.store.Save(ctx, o.sess)
	}
}

// Session returns the live conversation.
func (o *SessionOrchestrator) Session() *session.Conversation {
	if o == nil {
		return nil
	}
	return o.sess
}

// CheckPointID returns the active checkpoint key.
func (o *SessionOrchestrator) CheckPointID() string {
	if o == nil {
		return ""
	}
	return o.checkpointID
}
