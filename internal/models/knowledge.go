// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: AGPL-3.0
package models

import (
	"errors"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/utils/base"
	"gorm.io/gorm"
)

const (
	KnowledgeStatusActive     = "active"
	KnowledgeStatusDeleted    = "deleted"
	KnowledgeStatusProcessing = "processing"
	KnowledgeStatusFailed     = "failed"

	// KnowledgeVectorProviderQdrant 自建 / 托管 Qdrant，namespace 对应 collection 名。
	KnowledgeVectorProviderQdrant = "qdrant"
	// KnowledgeVectorProviderMilvus Milvus 向量库；namespace 对应 collection 名。
	KnowledgeVectorProviderMilvus = "milvus"
)

// NormalizeVectorProvider returns KnowledgeVectorProviderQdrant or KnowledgeVectorProviderMilvus.
func NormalizeVectorProvider(s string) string {
	v := strings.TrimSpace(strings.ToLower(s))
	switch v {
	case "", KnowledgeVectorProviderQdrant:
		return KnowledgeVectorProviderQdrant
	case KnowledgeVectorProviderMilvus:
		return KnowledgeVectorProviderMilvus
	default:
		return v
	}
}

type KnowledgeNamespace struct {
	ID uint `json:"id,string" gorm:"primaryKey;autoIncrement:false"`

	OrgID uint `json:"orgId" gorm:"uniqueIndex:idx_knowledge_org_namespace;not null;default:0;comment:tenant organization id"`

	Namespace   string `json:"namespace" gorm:"type:varchar(128);uniqueIndex:idx_knowledge_org_namespace;not null;comment:Qdrant/Milvus collection"`
	Name        string `json:"name" gorm:"type:varchar(255);not null;comment:知识库名称（中文名）"`
	Description string `json:"description" gorm:"type:text;comment:描述"`

	VectorProvider string `json:"vector_provider" gorm:"type:varchar(32);not null;default:'qdrant';index;comment:向量后端 qdrant|milvus"`

	EmbedModel string `json:"embed_model" gorm:"type:varchar(64);not null;comment:向量模型（bge/m3e...）"`
	VectorDim  int    `json:"vector_dim" gorm:"not null;comment:向量维度"`

	Status string `json:"status" gorm:"type:varchar(20);index;not null;default:'active'"`

	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (KnowledgeNamespace) TableName() string { return "knowledge_namespaces" }

func (m *KnowledgeNamespace) BeforeCreate(tx *gorm.DB) error {
	if m.ID == 0 {
		m.ID = base.GenUintID()
	}
	if strings.TrimSpace(m.VectorProvider) == "" {
		m.VectorProvider = KnowledgeVectorProviderQdrant
	}
	return nil
}

// IsMilvusVectorBackend reports whether vectors are hosted on Milvus.
func (m *KnowledgeNamespace) IsMilvusVectorBackend() bool {
	if m == nil {
		return false
	}
	return NormalizeVectorProvider(m.VectorProvider) == KnowledgeVectorProviderMilvus
}

type KnowledgeNamespaceListResult struct {
	List      []KnowledgeNamespace `json:"list"`
	Total     int64                `json:"total"`
	Page      int                  `json:"page"`
	PageSize  int                  `json:"pageSize"`
	TotalPage int                  `json:"totalPage"`
}

func ListKnowledgeNamespaces(db *gorm.DB, orgID uint, status string, page, pageSize int) (*KnowledgeNamespaceListResult, error) {
	if db == nil {
		return nil, errors.New("nil db")
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	q := db.Model(&KnowledgeNamespace{})
	q = q.Where("org_id = ?", orgID)
	if s := strings.TrimSpace(status); s != "" {
		q = q.Where("status = ?", s)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}

	var list []KnowledgeNamespace
	if err := q.Order("id DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, err
	}

	totalPage := int(total) / pageSize
	if int(total)%pageSize != 0 {
		totalPage++
	}
	return &KnowledgeNamespaceListResult{
		List:      list,
		Total:     total,
		Page:      page,
		PageSize:  pageSize,
		TotalPage: totalPage,
	}, nil
}

func GetKnowledgeNamespace(db *gorm.DB, orgID uint, id uint) (*KnowledgeNamespace, error) {
	if db == nil {
		return nil, errors.New("nil db")
	}
	var row KnowledgeNamespace
	if err := db.Where("org_id = ?", orgID).First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// GetKnowledgeNamespaceByOrgAndNamespace loads a knowledge namespace by org and namespace string (collection / IndexId).
func GetKnowledgeNamespaceByOrgAndNamespace(db *gorm.DB, orgID uint, namespace string) (*KnowledgeNamespace, error) {
	if db == nil {
		return nil, errors.New("nil db")
	}
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return nil, errors.New("namespace is required")
	}
	var row KnowledgeNamespace
	if err := db.Where("org_id = ? AND namespace = ?", orgID, ns).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

type KnowledgeNamespaceCreateUpdate struct {
	Namespace      string
	Name           string
	Description    string
	VectorProvider string
	EmbedModel     string
	VectorDim      int
	Status         string
}

func UpsertKnowledgeNamespace(db *gorm.DB, orgID uint, id uint, req *KnowledgeNamespaceCreateUpdate) (*KnowledgeNamespace, error) {
	if db == nil {
		return nil, errors.New("nil db")
	}
	if req == nil {
		return nil, errors.New("nil req")
	}
	namespace := strings.TrimSpace(req.Namespace)
	if namespace == "" {
		return nil, errors.New("namespace is required")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	vp := NormalizeVectorProvider(req.VectorProvider)
	if vp != KnowledgeVectorProviderQdrant && vp != KnowledgeVectorProviderMilvus {
		return nil, errors.New("vector_provider must be qdrant or milvus")
	}

	embedModel := strings.TrimSpace(req.EmbedModel)
	if embedModel == "" {
		return nil, errors.New("embed_model is required")
	}
	if req.VectorDim <= 0 {
		return nil, errors.New("vector_dim must be > 0")
	}

	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = KnowledgeStatusActive
	}

	if id == 0 {
		// Dedup by (org_id, namespace).
		var existing KnowledgeNamespace
		err := db.Where("org_id = ? AND namespace = ?", orgID, namespace).First(&existing).Error
		if err == nil {
			existing.Name = name
			existing.Description = strings.TrimSpace(req.Description)
			existing.VectorProvider = vp
			existing.EmbedModel = embedModel
			existing.VectorDim = req.VectorDim
			existing.Status = status
			if err := db.Save(&existing).Error; err != nil {
				return nil, err
			}
			return &existing, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}

		row := KnowledgeNamespace{
			OrgID:          orgID,
			Namespace:      namespace,
			Name:           name,
			Description:    strings.TrimSpace(req.Description),
			VectorProvider: vp,
			EmbedModel:     embedModel,
			VectorDim:      req.VectorDim,
			Status:         status,
		}
		if err := db.Create(&row).Error; err != nil {
			return nil, err
		}
		return &row, nil
	}

	var row KnowledgeNamespace
	if err := db.Where("org_id = ?", orgID).First(&row, id).Error; err != nil {
		return nil, err
	}
	row.OrgID = orgID
	row.Namespace = namespace
	row.Name = name
	row.Description = strings.TrimSpace(req.Description)
	row.VectorProvider = vp
	row.EmbedModel = embedModel
	row.VectorDim = req.VectorDim
	row.Status = status
	if err := db.Save(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func SoftDeleteKnowledgeNamespace(db *gorm.DB, orgID uint, id uint) error {
	if db == nil {
		return errors.New("nil db")
	}
	var row KnowledgeNamespace
	if err := db.Where("org_id = ?", orgID).First(&row, id).Error; err != nil {
		return err
	}
	row.Status = KnowledgeStatusDeleted
	return db.Save(&row).Error
}

// KnowledgeDocument 代表用户上传的一份知识材料（对应一批 Qdrant points）。
type KnowledgeDocument struct {
	ID uint `json:"id,string" gorm:"primaryKey;autoIncrement:false"`

	OrgID uint `json:"orgId" gorm:"uniqueIndex:idx_knowledge_org_ns_filehash;not null;default:0;comment:tenant organization id"`

	// Namespace: 所属知识库（= Qdrant collection）
	Namespace string `json:"namespace" gorm:"type:varchar(128);index;not null;uniqueIndex:idx_knowledge_org_ns_filehash"`

	Title    string `json:"title" gorm:"type:varchar(255);not null;comment:文件名"`
	Source   string `json:"source" gorm:"type:varchar(128);comment:来源（file/url/api...）"`
	FileHash string `json:"file_hash" gorm:"type:varchar(64);index;not null;comment:文件MD5（去重）;uniqueIndex:idx_knowledge_org_ns_filehash"`

	// TextURL: extracted markdown content stored in object storage.
	TextURL string `json:"text_url,omitempty" gorm:"type:text;comment:解析后的 markdown 文本 URL"`

	// RecordIDs: 关联向量 ID（逗号分隔或 JSON 字符串）
	RecordIDs string `json:"record_ids" gorm:"type:text;comment:关联的向量ID（逗号分隔/JSON）"`

	Status string `json:"status" gorm:"type:varchar(20);index;not null;default:'active'"`

	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (KnowledgeDocument) TableName() string { return "knowledge_documents" }

func (m *KnowledgeDocument) BeforeCreate(tx *gorm.DB) error {
	if m.ID == 0 {
		m.ID = base.GenUintID()
	}
	return nil
}

type KnowledgeDocumentListResult struct {
	List      []KnowledgeDocument `json:"list"`
	Total     int64               `json:"total"`
	Page      int                 `json:"page"`
	PageSize  int                 `json:"pageSize"`
	TotalPage int                 `json:"totalPage"`
}

func ListKnowledgeDocuments(db *gorm.DB, orgID uint, namespace string, status string, page, pageSize int) (*KnowledgeDocumentListResult, error) {
	if db == nil {
		return nil, errors.New("nil db")
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	q := db.Model(&KnowledgeDocument{})
	q = q.Where("org_id = ?", orgID)
	ns := strings.TrimSpace(namespace)
	if ns != "" {
		q = q.Where("namespace = ?", ns)
	}
	s := strings.TrimSpace(status)
	if s != "" {
		q = q.Where("status = ?", s)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}

	var list []KnowledgeDocument
	if err := q.Order("id DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, err
	}

	totalPage := int(total) / pageSize
	if int(total)%pageSize != 0 {
		totalPage++
	}
	return &KnowledgeDocumentListResult{
		List:      list,
		Total:     total,
		Page:      page,
		PageSize:  pageSize,
		TotalPage: totalPage,
	}, nil
}

func GetKnowledgeDocument(db *gorm.DB, orgID uint, id uint) (*KnowledgeDocument, error) {
	if db == nil {
		return nil, errors.New("nil db")
	}
	var row KnowledgeDocument
	if err := db.Where("org_id = ?", orgID).First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

type KnowledgeDocumentUpsertReq struct {
	Namespace string
	Title     string
	Source    string
	FileHash  string
	RecordIDs string
	TextURL   string
	Status    string
}

func UpsertKnowledgeDocument(db *gorm.DB, orgID uint, id uint, req *KnowledgeDocumentUpsertReq) (*KnowledgeDocument, error) {
	if db == nil {
		return nil, errors.New("nil db")
	}
	if req == nil {
		return nil, errors.New("nil req")
	}
	ns := strings.TrimSpace(req.Namespace)
	if ns == "" {
		return nil, errors.New("namespace is required")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, errors.New("title is required")
	}
	fileHash := strings.TrimSpace(req.FileHash)
	if fileHash == "" {
		return nil, errors.New("file_hash is required")
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = KnowledgeStatusActive
	}

	if id == 0 {
		// Dedup by (namespace, file_hash)
		var existing KnowledgeDocument
		err := db.Where("org_id = ? AND namespace = ? AND file_hash = ?", orgID, ns, fileHash).First(&existing).Error
		if err == nil {
			existing.Title = title
			existing.Source = strings.TrimSpace(req.Source)
			existing.RecordIDs = strings.TrimSpace(req.RecordIDs)
			existing.TextURL = strings.TrimSpace(req.TextURL)
			existing.Status = status
			if err := db.Save(&existing).Error; err != nil {
				return nil, err
			}
			return &existing, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}

		row := KnowledgeDocument{
			OrgID:     orgID,
			Namespace: ns,
			Title:     title,
			Source:    strings.TrimSpace(req.Source),
			FileHash:  fileHash,
			RecordIDs: strings.TrimSpace(req.RecordIDs),
			TextURL:   strings.TrimSpace(req.TextURL),
			Status:    status,
		}
		if err := db.Create(&row).Error; err != nil {
			return nil, err
		}
		return &row, nil
	}

	var row KnowledgeDocument
	if err := db.Where("org_id = ?", orgID).First(&row, id).Error; err != nil {
		return nil, err
	}
	row.OrgID = orgID
	row.Namespace = ns
	row.Title = title
	row.Source = strings.TrimSpace(req.Source)
	row.FileHash = fileHash
	row.RecordIDs = strings.TrimSpace(req.RecordIDs)
	row.TextURL = strings.TrimSpace(req.TextURL)
	row.Status = status
	if err := db.Save(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func SoftDeleteKnowledgeDocument(db *gorm.DB, orgID uint, id uint) error {
	if db == nil {
		return errors.New("nil db")
	}
	var row KnowledgeDocument
	if err := db.Where("org_id = ?", orgID).First(&row, id).Error; err != nil {
		return err
	}
	row.Status = KnowledgeStatusDeleted
	return db.Save(&row).Error
}
