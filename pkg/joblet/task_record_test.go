package joblet

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTaskRecordTableName(t *testing.T) {
	var tr TaskRecord
	if tr.TableName() != "joblet_tasks" {
		t.Fatalf("unexpected table name")
	}
}

func TestTaskRecordCRUD(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TaskRecord{}); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	seed := TaskRecord{
		ID:          "tk_100",
		OrgID:       1,
		DocID:       2,
		Namespace:   "ns",
		Name:        "knowledge.upload",
		Status:      "scheduled",
		Stage:       "enqueue",
		Priority:    0,
		Attempt:     0,
		LastEventAt: now,
	}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatal(err)
	}

	got, err := GetTaskRecord(db, "tk_100")
	if err != nil {
		t.Fatal(err)
	}
	if got.OrgID != 1 || got.DocID != 2 || got.Namespace != "ns" {
		t.Fatalf("unexpected record: %+v", got)
	}

	// list filter by org/doc
	docID := uint(2)
	out, err := ListTaskRecords(db, TaskRecordFilters{
		OrgID:     ptrUint(1),
		DocID:     &docID,
		Namespace: "ns",
		Page:      1,
		PageSize:  10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 1 || len(out.List) != 1 {
		t.Fatalf("unexpected list: %+v", out)
	}

	if err := DeleteTaskRecord(db, "tk_100"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetTaskRecord(db, "tk_100"); err == nil {
		t.Fatalf("expected not found after delete")
	}
}

func ptrUint(v uint) *uint { return &v }

func TestUpsertTerminalFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TaskRecord{}); err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{
		"org_id":    "7",
		"doc_id":    "9",
		"namespace": "n1",
	}
	submitErr := errors.New("queue full")
	if err := UpsertTerminalFailure(context.Background(), db, "tk_x", "knowledge.upload", "submit_reject", "not accepted", submitErr, meta); err != nil {
		t.Fatal(err)
	}
	got, err := GetTaskRecord(db, "tk_x")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != TaskStatusFailed.String() || got.Stage != "submit_reject" {
		t.Fatalf("unexpected row: %+v", got)
	}
	if got.Error != submitErr.Error() || got.OrgID != 7 || got.DocID != 9 || got.Namespace != "n1" {
		t.Fatalf("unexpected fields: %+v", got)
	}
}
