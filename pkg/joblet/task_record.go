package joblet

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TaskRecord is a persistent representation of a joblet task.
//
// NOTE:
// - We intentionally keep Params/Result out of the core model to avoid generic serialization contracts.
// - Put business identifiers (doc_id, file_path, user_id, etc.) into Task.Meta.
type TaskRecord struct {
	ID          string     `json:"id" gorm:"primaryKey;type:varchar(64)"`
	OrgID       uint       `json:"org_id" gorm:"index"`
	DocID       uint       `json:"doc_id" gorm:"index"`
	Namespace   string     `json:"namespace,omitempty" gorm:"type:varchar(128);index"`
	Name        string     `json:"name" gorm:"type:varchar(255);index"`
	Status      string     `json:"status" gorm:"type:varchar(32);index"`
	Stage       string     `json:"stage" gorm:"type:varchar(32);index"`
	Priority    int        `json:"priority" gorm:"index"`
	Attempt     int        `json:"attempt" gorm:"default:0"`
	Message     string     `json:"message,omitempty" gorm:"type:text"`
	Error       string     `json:"error,omitempty" gorm:"type:text"`
	MetaJSON    string     `json:"meta_json,omitempty" gorm:"type:text"`
	SubmittedAt *time.Time `json:"submitted_at,omitempty" gorm:"index"`
	EnqueuedAt  *time.Time `json:"enqueued_at,omitempty" gorm:"index"`
	StartedAt   *time.Time `json:"started_at,omitempty" gorm:"index"`
	FinishedAt  *time.Time `json:"finished_at,omitempty" gorm:"index"`

	LastEventAt time.Time `json:"last_event_at" gorm:"index"`
	CreatedAt   time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (TaskRecord) TableName() string { return "joblet_tasks" }

// UpsertTerminalFailure persists a terminal failed task when the pool never ran the handler
// (nil pool, submit rejected, pool closed). Meta keys org_id, doc_id, namespace are parsed like DBTaskLogger.
func UpsertTerminalFailure(ctx context.Context, db *gorm.DB, id, name, stage, message string, err error, meta map[string]string) error {
	if db == nil {
		return errors.New("joblet: db is nil")
	}
	if id == "" {
		return errors.New("joblet: id is empty")
	}
	writeCtx := ctx
	if writeCtx == nil || writeCtx.Err() != nil {
		writeCtx = context.Background()
	}
	now := time.Now()
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	metaJSON := ""
	if len(meta) > 0 {
		if b, e := json.Marshal(meta); e == nil {
			metaJSON = string(b)
		}
	}
	rec := TaskRecord{
		ID:          id,
		Name:        name,
		Status:      TaskStatusFailed.String(),
		Stage:       stage,
		Message:     message,
		Error:       errStr,
		MetaJSON:    metaJSON,
		SubmittedAt: &now,
		LastEventAt: now,
		FinishedAt:  &now,
	}
	if len(meta) > 0 {
		if v := meta["org_id"]; v != "" {
			if n, e := strconv.ParseUint(v, 10, 64); e == nil {
				rec.OrgID = uint(n)
			}
		}
		if v := meta["doc_id"]; v != "" {
			if n, e := strconv.ParseUint(v, 10, 64); e == nil {
				rec.DocID = uint(n)
			}
		}
		rec.Namespace = meta["namespace"]
	}

	updates := map[string]any{
		"org_id":        rec.OrgID,
		"doc_id":        rec.DocID,
		"namespace":     rec.Namespace,
		"name":          rec.Name,
		"status":        rec.Status,
		"stage":         rec.Stage,
		"message":       rec.Message,
		"error":         rec.Error,
		"meta_json":     rec.MetaJSON,
		"last_event_at": rec.LastEventAt,
		"finished_at":   rec.FinishedAt,
		"updated_at":    now,
	}
	if rec.SubmittedAt != nil {
		updates["submitted_at"] = rec.SubmittedAt
	}
	return db.WithContext(writeCtx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(updates),
	}).Create(&rec).Error
}

type TaskRecordFilters struct {
	OrgID     *uint
	DocID     *uint
	Namespace string
	Status    string
	Stage     string
	NameLike  string

	From *time.Time
	To   *time.Time

	Page     int
	PageSize int
}

type TaskRecordListResult struct {
	List      []TaskRecord `json:"list"`
	Total     int64        `json:"total"`
	Page      int          `json:"page"`
	PageSize  int          `json:"pageSize"`
	TotalPage int          `json:"totalPage"`
}

func GetTaskRecord(db *gorm.DB, id string) (*TaskRecord, error) {
	if db == nil {
		return nil, errors.New("joblet: db is nil")
	}
	if id == "" {
		return nil, errors.New("joblet: id is empty")
	}
	var row TaskRecord
	if err := db.First(&row, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func DeleteTaskRecord(db *gorm.DB, id string) error {
	if db == nil {
		return errors.New("joblet: db is nil")
	}
	if id == "" {
		return errors.New("joblet: id is empty")
	}
	return db.Delete(&TaskRecord{}, "id = ?", id).Error
}

func ListTaskRecords(db *gorm.DB, f TaskRecordFilters) (*TaskRecordListResult, error) {
	if db == nil {
		return nil, errors.New("joblet: db is nil")
	}
	page := f.Page
	if page <= 0 {
		page = 1
	}
	pageSize := f.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	q := db.Model(&TaskRecord{})
	if f.OrgID != nil {
		q = q.Where("org_id = ?", *f.OrgID)
	}
	if f.DocID != nil {
		q = q.Where("doc_id = ?", *f.DocID)
	}
	if f.Namespace != "" {
		q = q.Where("namespace = ?", f.Namespace)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Stage != "" {
		q = q.Where("stage = ?", f.Stage)
	}
	if f.NameLike != "" {
		q = q.Where("name LIKE ?", "%"+f.NameLike+"%")
	}
	if f.From != nil {
		q = q.Where("last_event_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("last_event_at <= ?", *f.To)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	var list []TaskRecord
	if err := q.Order("last_event_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, err
	}
	totalPage := 0
	if total > 0 {
		totalPage = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	return &TaskRecordListResult{
		List:      list,
		Total:     total,
		Page:      page,
		PageSize:  pageSize,
		TotalPage: totalPage,
	}, nil
}
