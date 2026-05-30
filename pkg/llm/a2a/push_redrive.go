package a2a

import (
	"context"
	"time"
)

func (d *pushDelivery) redriveEntry(entry PushDeadLetter) bool {
	if d == nil || entry.URL == "" {
		return false
	}
	cfg := PushNotificationConfig{TaskID: entry.TaskID, URL: entry.URL}
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
		lastErr = d.deliverSync(ctx, cfg, entry.Event)
		cancel()
		if lastErr == nil {
			return true
		}
	}
	if d.deadLetter != nil {
		entry.Attempts += attempts
		if lastErr != nil {
			entry.LastError = lastErr.Error()
		}
		entry.CreatedAt = nowRFC3339()
		d.deadLetter.requeue(entry)
	}
	return false
}

// RedrivePushDeadLetters retries dead-letter webhook deliveries.
// When taskID is empty, all entries are redriven.
func (h *Handler) RedrivePushDeadLetters(taskID string) PushDeadLetterRedriveResult {
	if h == nil || h.push == nil || h.push.deadLetter == nil {
		return PushDeadLetterRedriveResult{}
	}
	entries := h.push.deadLetter.take(taskID)
	var result PushDeadLetterRedriveResult
	for _, entry := range entries {
		if h.push.redriveEntry(entry) {
			result.Redriven++
		} else {
			result.Failed++
		}
	}
	return result
}
