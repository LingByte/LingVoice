package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type pushDelivery struct {
	client       *http.Client
	onlyTerminal bool
	maxRetries   int
	backoff      time.Duration
	deadLetter   *pushDeadLetterQueue
}

func newPushDelivery(client *http.Client, onlyTerminal bool, retry PushRetryConfig) *pushDelivery {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	maxRetries, backoff, deadLetterMax := resolvePushRetry(retry)
	return &pushDelivery{
		client:       client,
		onlyTerminal: onlyTerminal,
		maxRetries:   maxRetries,
		backoff:      backoff,
		deadLetter:   newPushDeadLetterQueue(deadLetterMax),
	}
}

func (d *pushDelivery) deliver(cfg PushNotificationConfig, ev TaskStatusUpdateEvent) {
	if d == nil || cfg.URL == "" {
		return
	}
	go d.deliverWithRetry(cfg, ev)
}

func (d *pushDelivery) deliverWithRetry(cfg PushNotificationConfig, ev TaskStatusUpdateEvent) {
	if d == nil {
		return
	}
	var lastErr error
	delay := d.backoff
	attempts := d.maxRetries + 1
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			time.Sleep(delay)
			delay *= 2
		}
		timeout := d.client.Timeout
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		lastErr = d.deliverSync(ctx, cfg, ev)
		cancel()
		if lastErr == nil {
			return
		}
	}
	if d.deadLetter != nil {
		d.deadLetter.add(PushDeadLetter{
			TaskID:    cfg.TaskID,
			URL:       cfg.URL,
			Event:     ev,
			Attempts:  attempts,
			LastError: lastErr.Error(),
			CreatedAt: nowRFC3339(),
		})
	}
}

func (d *pushDelivery) deliverSync(ctx context.Context, cfg PushNotificationConfig, ev TaskStatusUpdateEvent) error {
	if d == nil || cfg.URL == "" {
		return fmt.Errorf("a2a: empty push url")
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("a2a: push status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (h *Handler) onTaskUpdate(task *Task, prev *Task, hadPrev bool) {
	if h == nil || task == nil || h.push == nil || !h.card.Capabilities.PushNotifications {
		return
	}
	cfg, ok := h.tasks.getPush(task.ID)
	if !ok {
		return
	}
	final := task.Status.State.Terminal()
	if h.push.onlyTerminal {
		if !final {
			return
		}
		if hadPrev && prev != nil && prev.Status.State.Terminal() {
			return
		}
	} else if hadPrev && prev != nil && prev.Status.State == task.Status.State {
		return
	}
	h.push.deliver(cfg, TaskStatusUpdateEvent{
		Kind:      "status-update",
		TaskID:    task.ID,
		ContextID: task.ContextID,
		Status:    task.Status,
		Final:     final,
	})
}

// ListPushDeadLetters returns push deliveries that failed after all retries.
func (h *Handler) ListPushDeadLetters() []PushDeadLetter {
	if h == nil || h.push == nil || h.push.deadLetter == nil {
		return nil
	}
	return h.push.deadLetter.list()
}
