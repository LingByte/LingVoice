package joblet

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LingByte/LingVoice/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var globalDBLogger atomic.Pointer[DBTaskLogger]

func SetGlobalDBTaskLogger(l *DBTaskLogger) {
	if l == nil {
		return
	}
	globalDBLogger.Store(l)
}

func GlobalDBTaskLoggerHealth() (DBTaskLoggerHealth, bool) {
	l := globalDBLogger.Load()
	if l == nil {
		return DBTaskLoggerHealth{}, false
	}
	return l.Health(), true
}

// GlobalDBTaskLogger returns the global DB task logger, if configured.
func GlobalDBTaskLogger() *DBTaskLogger {
	return globalDBLogger.Load()
}

// DBTaskLogger persists task lifecycle events into the joblet_tasks table.
// It is safe for concurrent use.
type DBTaskLogger struct {
	db        *gorm.DB
	events    atomic.Uint64
	failed    atomic.Uint64
	lastErr   atomic.Value // string
	mu        sync.Mutex
	startTime map[string]time.Time
}

func NewDBTaskLogger(db *gorm.DB) (*DBTaskLogger, error) {
	if db == nil {
		return nil, errors.New("joblet: db is nil")
	}
	// Ensure table exists and schema is up to date.
	return &DBTaskLogger{
		db:        db,
		startTime: make(map[string]time.Time),
	}, nil
}

func (l *DBTaskLogger) OnTaskEvent(ctx context.Context, e TaskLogEvent) {
	if l == nil || l.db == nil {
		return
	}
	l.events.Add(1)

	// Detach persistence from request cancellation.
	// If ctx is already canceled, use Background so we still can persist the event.
	writeCtx := ctx
	if writeCtx == nil || writeCtx.Err() != nil {
		writeCtx = context.Background()
	}

	metaJSON := ""
	if len(e.Meta) > 0 {
		if b, err := json.Marshal(e.Meta); err == nil {
			metaJSON = string(b)
		}
	}

	rec := TaskRecord{
		ID:          e.TaskID,
		Name:        e.TaskName,
		Status:      e.Status.String(),
		Stage:       string(e.Stage),
		Priority:    e.Priority,
		Attempt:     e.Attempt,
		Message:     e.Message,
		LastEventAt: e.At,
		MetaJSON:    metaJSON,
	}
	if v := stringsGet(e.Meta, "org_id"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			rec.OrgID = uint(n)
		}
	}
	if v := stringsGet(e.Meta, "doc_id"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			rec.DocID = uint(n)
		}
	}
	rec.Namespace = stringsGet(e.Meta, "namespace")
	if e.Err != nil {
		rec.Error = e.Err.Error()
	}

	// Stage timestamps (best effort).
	switch e.Stage {
	case TaskStageSubmit:
		rec.SubmittedAt = ptrTime(e.At)
	case TaskStageEnqueue:
		rec.EnqueuedAt = ptrTime(e.At)
	case TaskStageStart:
		rec.StartedAt = ptrTime(e.At)
		l.mu.Lock()
		l.startTime[e.TaskID] = e.At
		l.mu.Unlock()
	case TaskStageFinish:
		rec.FinishedAt = ptrTime(e.At)
		l.mu.Lock()
		delete(l.startTime, e.TaskID)
		l.mu.Unlock()
	}

	updates := map[string]any{
		"org_id":        rec.OrgID,
		"doc_id":        rec.DocID,
		"namespace":     rec.Namespace,
		"name":          rec.Name,
		"status":        rec.Status,
		"stage":         rec.Stage,
		"priority":      rec.Priority,
		"attempt":       rec.Attempt,
		"message":       rec.Message,
		"error":         rec.Error,
		"meta_json":     rec.MetaJSON,
		"last_event_at": rec.LastEventAt,
		"updated_at":    e.At,
	}
	if rec.SubmittedAt != nil {
		updates["submitted_at"] = rec.SubmittedAt
	}
	if rec.EnqueuedAt != nil {
		updates["enqueued_at"] = rec.EnqueuedAt
	}
	if rec.StartedAt != nil {
		updates["started_at"] = rec.StartedAt
	}
	if rec.FinishedAt != nil {
		updates["finished_at"] = rec.FinishedAt
	}

	// Upsert for idempotency.
	if err := l.db.WithContext(writeCtx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(updates),
	}).Create(&rec).Error; err != nil {
		l.failed.Add(1)
		l.lastErr.Store(err.Error())
		logger.Error("joblet.db_logger.upsert_failed",
			zap.String("task_id", e.TaskID),
			zap.String("stage", string(e.Stage)),
			zap.String("status", e.Status.String()),
			zap.Error(err),
		)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func stringsGet(m map[string]string, k string) string {
	if len(m) == 0 {
		return ""
	}
	return m[k]
}

type DBTaskLoggerHealth struct {
	Events  uint64 `json:"events"`
	Failed  uint64 `json:"failed"`
	LastErr string `json:"last_err,omitempty"`
}

func (l *DBTaskLogger) Health() DBTaskLoggerHealth {
	if l == nil {
		return DBTaskLoggerHealth{}
	}
	out := DBTaskLoggerHealth{
		Events: l.events.Load(),
		Failed: l.failed.Load(),
	}
	if v := l.lastErr.Load(); v != nil {
		if s, ok := v.(string); ok {
			out.LastErr = s
		}
	}
	return out
}
