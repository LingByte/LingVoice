package joblet

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDBTaskLoggerUpsert(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TaskRecord{}); err != nil {
		t.Fatal(err)
	}

	l, err := NewDBTaskLogger(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewDBTaskLogger(nil); err == nil {
		t.Fatalf("expected error for nil db")
	}
	// nil logger should be safe
	var nl *DBTaskLogger
	nl.OnTaskEvent(context.Background(), TaskLogEvent{TaskID: "x"})

	now := time.Now()
	l.OnTaskEvent(context.Background(), TaskLogEvent{
		TaskID:   "tk_1",
		TaskName: "upload-doc",
		Stage:    TaskStageSubmit,
		Status:   TaskStatusPending,
		Priority: 3,
		Attempt:  0,
		At:       now,
		Message:  "upload-doc",
		Meta:     map[string]string{"doc_id": "d1"},
	})
	// non-marshalable meta should not panic
	l.OnTaskEvent(context.Background(), TaskLogEvent{
		TaskID:   "tk_2",
		TaskName: "bad-meta",
		Stage:    TaskStageSubmit,
		Status:   TaskStatusPending,
		Priority: 1,
		Attempt:  0,
		At:       now,
		Meta:     map[string]string{},
		Err:      errors.New("x"),
	})
	l.OnTaskEvent(context.Background(), TaskLogEvent{
		TaskID:   "tk_1",
		TaskName: "upload-doc",
		Stage:    TaskStageFinish,
		Status:   TaskStatusSuccess,
		Priority: 3,
		Attempt:  1,
		At:       now.Add(10 * time.Millisecond),
		Message:  "upload-doc",
	})

	var rec TaskRecord
	if err := db.First(&rec, "id = ?", "tk_1").Error; err != nil {
		t.Fatal(err)
	}
	if rec.Name != "upload-doc" || rec.Stage != string(TaskStageFinish) || rec.Status != "success" {
		t.Fatalf("unexpected record: %+v", rec)
	}
	if rec.SubmittedAt == nil || rec.FinishedAt == nil {
		t.Fatalf("expected submitted/finished timestamps")
	}
}

