package agent

import (
	"context"
	"sync"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/session"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// TurnEvent is emitted after each processed turn.
type TurnEvent struct {
	UserText    string
	Assistant   *schema.Message
	Interrupted bool
	Err         error
}

// TurnLoopConfig configures a push-based multi-turn agent loop.
type TurnLoopConfig struct {
	Agent           Agent
	Session         *session.Conversation
	SessionStore    session.Store
	CheckPointStore compose.CheckPointStore
	CheckPointID    string
	BufferSize      int
	OnTurn          func(TurnEvent)
}

// TurnLoop manages queued user turns against a session-backed agent.
type TurnLoop struct {
	agent    Agent
	react    *ReActAgent
	sess     *session.Conversation
	store    session.Store
	cpStore  compose.CheckPointStore
	cpID     string
	onTurn   func(TurnEvent)
	ch       chan string
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewTurnLoop creates a turn loop.
func NewTurnLoop(_ context.Context, cfg TurnLoopConfig) (*TurnLoop, error) {
	if cfg.Agent == nil {
		return nil, composeErr("nil agent")
	}
	buf := cfg.BufferSize
	if buf <= 0 {
		buf = 16
	}
	sess := cfg.Session
	if sess == nil {
		sess = &session.Conversation{ID: "default", Vars: map[string]any{}}
	}
	if sess.Vars == nil {
		sess.Vars = map[string]any{}
	}
	tl := &TurnLoop{
		agent:   cfg.Agent,
		sess:    sess,
		store:   cfg.SessionStore,
		cpStore: cfg.CheckPointStore,
		cpID:    cfg.CheckPointID,
		onTurn:  cfg.OnTurn,
		ch:      make(chan string, buf),
		stop:    make(chan struct{}),
	}
	if ra, ok := cfg.Agent.(*ReActAgent); ok {
		tl.react = ra
	}
	return tl, nil
}

// Push enqueues a user text turn.
func (t *TurnLoop) Push(userText string) {
	if t == nil {
		return
	}
	t.ch <- userText
}

// Run processes turns until Stop or ctx cancel.
func (t *TurnLoop) Run(ctx context.Context) {
	if t == nil {
		return
	}
	t.wg.Add(1)
	defer t.wg.Done()
	for {
		select {
		case <-t.stop:
			return
		case <-ctx.Done():
			return
		case text := <-t.ch:
			t.handleTurn(ctx, text)
		}
	}
}

func (t *TurnLoop) handleTurn(ctx context.Context, text string) {
	ev := TurnEvent{UserText: text}
	t.sess.AppendUser(text)
	msgs := append([]*schema.Message(nil), t.sess.Messages...)

	var (
		out *schema.Message
		err error
	)
	if t.react != nil && t.cpStore != nil && t.cpID != "" {
		var result *ReActResult
		result, err = t.react.GenerateWithTrace(ctx, msgs, compose.WithCheckPointID(t.cpID))
		if result != nil {
			out = result.Message
		}
		if err != nil {
			if _, ok := compose.ExtractInterruptInfo(err); ok {
				ev.Interrupted = true
			}
		}
	} else {
		out, err = t.agent.Generate(ctx, msgs)
	}

	ev.Assistant = out
	ev.Err = err
	if err == nil && out != nil {
		t.sess.AppendAssistant(out)
	}
	if t.store != nil && t.sess.ID != "" {
		_ = t.store.Save(ctx, t.sess)
	}
	if t.onTurn != nil {
		t.onTurn(ev)
	}
}

// Stop signals the loop to exit.
func (t *TurnLoop) Stop() {
	if t == nil {
		return
	}
	t.stopOnce.Do(func() { close(t.stop) })
}

// Wait blocks until Run returns.
func (t *TurnLoop) Wait() {
	if t != nil {
		t.wg.Wait()
	}
}

// Session returns the conversation handle.
func (t *TurnLoop) Session() *session.Conversation {
	if t == nil {
		return nil
	}
	return t.sess
}

// Resume continues an interrupted ReAct run (requires ReActAgent + checkpoint id).
func (t *TurnLoop) Resume(ctx context.Context, resume map[string]any) (*ReActResult, error) {
	if t == nil || t.react == nil || t.cpID == "" {
		return nil, composeErr("turn loop resume requires ReActAgent and checkpoint id")
	}
	result, err := t.react.Resume(ctx, t.cpID, resume)
	if err == nil && result != nil && result.Message != nil {
		t.sess.AppendAssistant(result.Message)
		if t.store != nil {
			_ = t.store.Save(ctx, t.sess)
		}
	}
	return result, err
}
