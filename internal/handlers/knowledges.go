// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: AGPL-3.0
package handlers

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/internal/config"
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/logger"
	"github.com/LingByte/LingVoice/pkg/knowledge"
	"github.com/LingByte/LingVoice/pkg/llm"
	"github.com/LingByte/LingVoice/pkg/utils/search"
	"github.com/LingByte/LingVoice/pkg/utils/base"
	knowledgeParser "github.com/LingByte/LingVoice/pkg/utils/parser"
	"github.com/LingByte/LingVoice/pkg/utils/response"
	lingstorage "github.com/LingByte/lingstorage-sdk-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func normalizeVec64InPlace(v []float64) {
	if len(v) == 0 {
		return
	}
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	if sum <= 0 {
		return
	}
	n := math.Sqrt(sum)
	if n == 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return
	}
	for i := range v {
		v[i] = v[i] / n
	}
}

func knowledgeProviderForLog(ns *models.KnowledgeNamespace) string {
	if ns == nil {
		return ""
	}
	p := strings.TrimSpace(strings.ToLower(ns.VectorProvider))
	if p == "" {
		p = models.KnowledgeVectorProviderQdrant
	}
	return p
}

var (
	knowledgeSearchOnce   sync.Once
	knowledgeSearchEngine search.Engine
	knowledgeSearchErr    error
)

func knowledgeSearchFromEnv() (search.Engine, error) {
	knowledgeSearchOnce.Do(func() {
		path := strings.TrimSpace(base.GetEnv("KNOWLEDGE_SEARCH_INDEX_PATH"))
		if path == "" {
			path = "./data/knowledge_search.bleve"
		}
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		eng, err := search.NewDefault(search.Config{
			IndexPath:           path,
			DefaultAnalyzer:     "standard",
			DefaultSearchFields: []string{"title", "content"},
			OpenTimeout:         5 * time.Second,
			QueryTimeout:        5 * time.Second,
			BatchSize:           200,
		})
		if err != nil {
			knowledgeSearchErr = err
			return
		}
		knowledgeSearchEngine = eng
	})
	return knowledgeSearchEngine, knowledgeSearchErr
}

// KnowledgeNamespaceUpsertReq 创建/更新知识库（namespace）定义。
type KnowledgeNamespaceUpsertReq struct {
	Namespace      string  `json:"namespace" binding:"required,max=128"`
	Name           string  `json:"name" binding:"required,max=255"`
	Description    string  `json:"description"`
	VectorProvider string  `json:"vector_provider"` // qdrant（默认）| milvus
	EmbedModel     string  `json:"embed_model" binding:"max=64"`
	VectorDim      int     `json:"vector_dim"`
	Status         *string `json:"status"` // active/deleted
}

type KnowledgeDocumentUpsertReq struct {
	Namespace string  `json:"namespace" binding:"required,max=128"`
	Title     string  `json:"title" binding:"required,max=255"`
	Source    string  `json:"source" binding:"max=128"`
	FileHash  string  `json:"file_hash" binding:"required,max=64"` // md5
	RecordIDs string  `json:"record_ids"`                          // comma separated / json string
	Status    *string `json:"status"`                              // active/deleted
}

func knowledgeHandlerForNS(ns *models.KnowledgeNamespace, embedder knowledge.Embedder) (knowledge.KnowledgeHandler, error) {
	if ns == nil {
		return nil, errors.New("nil namespace")
	}
	return knowledge.NewKnowledgeHandler(knowledge.HandlerFactoryParams{
		Provider:  ns.VectorProvider,
		Namespace: strings.TrimSpace(ns.Namespace),
		Embedder:  embedder,
	})
}

func embedderFromEnv() (knowledge.Embedder, error) {
	baseURL := strings.TrimSpace(base.GetEnv("EMBED_BASEURL"))
	apiKey := strings.TrimSpace(base.GetEnv("EMBED_API_KEY"))
	model := strings.TrimSpace(base.GetEnv("EMBED_MODEL"))
	inputKey := strings.TrimSpace(base.GetEnv("EMBED_INPUT_KEY"))
	embPath := strings.TrimSpace(base.GetEnv("EMBED_EMBEDDINGS_PATH"))
	if baseURL == "" || apiKey == "" || model == "" {
		return nil, errors.New("embedder env required: EMBED_BASEURL, EMBED_API_KEY, EMBED_MODEL")
	}
	timeoutSec := int64(30)
	if raw := strings.TrimSpace(base.GetEnv("EMBED_TIMEOUT_SECONDS")); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			timeoutSec = n
		}
	}
	return &knowledge.NvidiaEmbedClient{
		BaseURL:        baseURL,
		APIKey:         apiKey,
		Model:          model,
		InputKey:       inputKey,
		EmbeddingsPath: embPath,
		HTTPClient:     &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
	}, nil
}

func chunkerFromEnv() (knowledge.Chunker, string, error) {
	provider := strings.TrimSpace(base.GetEnv("LLM_PROVIDER"))
	apiKey := strings.TrimSpace(base.GetEnv("LLM_API_KEY"))
	baseURL := strings.TrimSpace(base.GetEnv("LLM_BASEURL"))
	model := strings.TrimSpace(base.GetEnv("LLM_MODEL"))
	if provider == "" || apiKey == "" || baseURL == "" || model == "" {
		return nil, "", nil
	}

	systemPrompt := strings.TrimSpace(base.GetEnv("LLM_SYSTEM_PROMPT"))
	h, err := llm.NewLLMProvider(context.Background(), provider, apiKey, baseURL, systemPrompt)
	if err != nil {
		return nil, "", err
	}
	return &knowledge.LLMChunker{LLM: h, Model: model}, fmt.Sprintf("LLMChunker enabled: provider=%s model=%s", provider, model), nil
}

func parseRecordIDs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// Prefer JSON array: ["id1","id2"]
	if strings.HasPrefix(s, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(s), &arr); err == nil {
			out := make([]string, 0, len(arr))
			for _, it := range arr {
				if v := strings.TrimSpace(it); v != "" {
					out = append(out, v)
				}
			}
			return out
		}
	}
	// Fallback: CSV
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func uploadMarkdownToStore(orgID uint, namespace string, docID uint, filename string, mdText string) (string, error) {
	if config.GlobalStore == nil || config.GlobalConfig == nil {
		return "", errors.New("store not initialized (LINGSTORAGE_*)")
	}
	bucket := strings.TrimSpace(config.GlobalConfig.Services.Storage.Bucket)
	if bucket == "" {
		return "", errors.New("storage bucket not configured (LINGSTORAGE_BUCKET)")
	}
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		ns = "default"
	}
	baseName := strings.TrimSpace(filename)
	if baseName == "" {
		baseName = "document"
	}
	baseName = filepath.Base(baseName)
	baseName = strings.TrimSuffix(baseName, filepath.Ext(baseName))
	if baseName == "" || baseName == "." {
		baseName = "document"
	}
	mdName := baseName + ".md"
	key := fmt.Sprintf("knowledge/%d/%s/%d/%d-%s", orgID, ns, docID, time.Now().UnixNano(), mdName)
	up, err := config.GlobalStore.UploadBytes(&lingstorage.UploadBytesRequest{
		Data:     []byte(mdText),
		Filename: mdName,
		Bucket:   bucket,
		Key:      key,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(up.URL), nil
}

func buildStructuredMarkdown(title string, namespace string, fileHash string, source string, chunks []struct {
	Idx  int
	Text string
}) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Document"
	}
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		ns = "default"
	}
	fh := strings.TrimSpace(fileHash)
	src := strings.TrimSpace(source)
	if src == "" {
		src = "upload"
	}

	var b strings.Builder
	_, _ = b.WriteString("# " + title + "\n\n")
	_, _ = b.WriteString("- namespace: `" + ns + "`\n")
	if fh != "" {
		_, _ = b.WriteString("- file_hash: `" + fh + "`\n")
	}
	_, _ = b.WriteString("- source: `" + src + "`\n")
	_, _ = b.WriteString("- chunks: `" + fmt.Sprintf("%d", len(chunks)) + "`\n")
	_, _ = b.WriteString("- generated_at: `" + time.Now().UTC().Format(time.RFC3339) + "`\n\n")

	for i, ch := range chunks {
		txt := strings.TrimSpace(ch.Text)
		if txt == "" {
			continue
		}
		_, _ = b.WriteString("## Chunk " + fmt.Sprintf("%d", i+1) + "\n\n")
		_, _ = b.WriteString("- index: `" + fmt.Sprintf("%d", ch.Idx) + "`\n")
		_, _ = b.WriteString("- length: `" + fmt.Sprintf("%d", len(txt)) + "`\n\n")
		_, _ = b.WriteString(txt + "\n\n")
	}
	return b.String()
}

func (h *Handlers) knowledgeDocMarkStatus(orgID uint, docID uint, status string) {
	if h == nil || h.db == nil || orgID == 0 || docID == 0 || strings.TrimSpace(status) == "" {
		return
	}
	_ = h.db.Model(&models.KnowledgeDocument{}).
		Where("org_id = ?", orgID).
		Where("id = ?", docID).
		Update("status", status).Error
}

func (h *Handlers) knowledgeDocFinalizeSuccess(orgID uint, docID uint, recordIDs []string, textURL string) {
	if h == nil || h.db == nil || orgID == 0 || docID == 0 {
		return
	}
	rawIDs, _ := json.Marshal(recordIDs)
	updates := map[string]any{
		"record_ids": string(rawIDs),
		"status":     models.KnowledgeStatusActive,
	}
	if strings.TrimSpace(textURL) != "" {
		updates["text_url"] = strings.TrimSpace(textURL)
	}
	_ = h.db.Model(&models.KnowledgeDocument{}).
		Where("org_id = ?", orgID).
		Where("id = ?", docID).
		Updates(updates).Error
}

func (h *Handlers) knowledgeDocFinalizeFailed(orgID uint, docID uint) {
	if h == nil || h.db == nil || orgID == 0 || docID == 0 {
		return
	}
	_ = h.db.Model(&models.KnowledgeDocument{}).
		Where("org_id = ?", orgID).
		Where("id = ?", docID).
		Update("status", models.KnowledgeStatusFailed).Error
}

func (h *Handlers) registerKnowledgeRoutes(api *gin.RouterGroup) {
	ns := api.Group("/knowledge-namespaces")
	ns.Use(models.AuthRequired, models.AdminRequired)
	{
		ns.GET("", h.knowledgeNamespacesListHandler)
		ns.POST("", h.knowledgeNamespaceCreateHandler)
		ns.GET("/:id", h.knowledgeNamespaceDetailHandler)
		ns.PUT("/:id", h.knowledgeNamespaceUpdateHandler)
		ns.POST("/:id/upload", h.knowledgeNamespaceUploadHandler)
		ns.POST("/:id/recall-test", h.knowledgeNamespaceRecallTestHandler)
		ns.DELETE("/:id", h.knowledgeNamespaceDeleteHandler)
	}

	docs := api.Group("/knowledge-documents")
	docs.Use(models.AuthRequired, models.AdminRequired)
	{
		docs.GET("", h.knowledgeDocumentsListHandler)
		docs.POST("", h.knowledgeDocumentCreateOrUpsertHandler)
		docs.GET("/:id", h.knowledgeDocumentDetailHandler)
		docs.PUT("/:id", h.knowledgeDocumentUpdateHandler)
		docs.GET("/:id/text", h.knowledgeDocumentTextGetHandler)
		docs.PUT("/:id/text", h.knowledgeDocumentTextPutHandler)
		docs.POST("/:id/upload", h.knowledgeDocumentReuploadHandler)
		docs.DELETE("/:id", h.knowledgeDocumentDeleteHandler)
	}
}

type KnowledgeRecallTestReq struct {
	Query    string  `json:"query" binding:"required"`
	TopK     int     `json:"topK"`
	DocID    *string `json:"docId"` // optional: compute recall/precision against this document (string id to avoid JS int precision)
	MinScore float64 `json:"minScore"`
}

func (h *Handlers) knowledgeNamespaceUploadHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	orgID := models.CurrentOrgID(c)
	nsRow, err := models.GetKnowledgeNamespace(h.db, orgID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "知识库不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
		response.FailWithCode(c, 400, "缺少文件 file", gin.H{"error": err.Error()})
		return
	}
	f, err := fh.Open()
	if err != nil {
		response.Fail(c, "打开文件失败", gin.H{"error": err.Error()})
		return
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		response.Fail(c, "读取文件失败", gin.H{"error": err.Error()})
		return
	}
	sum := md5.Sum(b)
	fileHash := fmt.Sprintf("%x", sum[:])

	// Create/Upsert document row first, then process asynchronously.
	docRow, err := models.UpsertKnowledgeDocument(h.db, orgID, 0, &models.KnowledgeDocumentUpsertReq{
		Namespace: nsRow.Namespace,
		Title:     fh.Filename,
		Source:    "upload",
		FileHash:  fileHash,
		RecordIDs: "",
		Status:    models.KnowledgeStatusProcessing,
	})
	if err != nil {
		response.Fail(c, "写入文档记录失败（DB）", gin.H{"error": err.Error()})
		return
	}

	go func(orgID uint, ns models.KnowledgeNamespace, docID uint, fileName string, fileHash string, content []byte) {
		start := time.Now()
		provider := knowledgeProviderForLog(&ns)
		logger.Info("knowledge.upload.job.start",
			zap.String("provider", provider),
			zap.Uint64("org_id", uint64(orgID)),
			zap.String("namespace", strings.TrimSpace(ns.Namespace)),
			zap.Uint64("doc_id", uint64(docID)),
			zap.String("file_name", strings.TrimSpace(fileName)),
			zap.String("file_hash", strings.TrimSpace(fileHash)),
			zap.Int("bytes", len(content)),
		)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		embedder, err := embedderFromEnv()
		if err != nil {
			logger.Error("knowledge.upload.job.embedder_failed",
				zap.String("provider", provider),
				zap.Uint64("org_id", uint64(orgID)),
				zap.String("namespace", strings.TrimSpace(ns.Namespace)),
				zap.Uint64("doc_id", uint64(docID)),
				zap.Error(err),
				zap.Duration("elapsed", time.Since(start)),
			)
			h.knowledgeDocFinalizeFailed(orgID, docID)
			return
		}

		parsed, err := knowledgeParser.ParseBytes(ctx, fileName, content, &knowledgeParser.ParseOptions{MaxTextLength: 200000})
		if err != nil {
			logger.Error("knowledge.upload.job.parse_failed",
				zap.String("provider", provider),
				zap.Uint64("org_id", uint64(orgID)),
				zap.String("namespace", strings.TrimSpace(ns.Namespace)),
				zap.Uint64("doc_id", uint64(docID)),
				zap.String("file_name", strings.TrimSpace(fileName)),
				zap.Error(err),
				zap.Duration("elapsed", time.Since(start)),
			)
			h.knowledgeDocFinalizeFailed(orgID, docID)
			return
		}

		type chunk struct {
			Idx  int
			Text string
		}
		chunks := make([]chunk, 0, 16)
		if ch, _, err := chunkerFromEnv(); err == nil && ch != nil {
			raw := strings.TrimSpace(parsed.Text)
			if raw != "" {
				llmChunks, err := ch.Chunk(ctx, raw, &knowledge.ChunkOptions{DocumentTitle: strings.TrimSpace(fileName)})
				if err == nil && len(llmChunks) > 0 {
					for _, it := range llmChunks {
						if strings.TrimSpace(it.Text) == "" {
							continue
						}
						chunks = append(chunks, chunk{Idx: it.Index, Text: it.Text})
					}
				}
			}
		}
		if len(chunks) == 0 {
			for _, s := range parsed.Sections {
				txt := strings.TrimSpace(s.Text)
				if txt == "" {
					continue
				}
				chunks = append(chunks, chunk{Idx: s.Index, Text: txt})
			}
		}
		if len(chunks) == 0 {
			txt := strings.TrimSpace(parsed.Text)
			if txt == "" {
				logger.Warn("knowledge.upload.job.empty_text",
					zap.String("provider", provider),
					zap.Uint64("org_id", uint64(orgID)),
					zap.String("namespace", strings.TrimSpace(ns.Namespace)),
					zap.Uint64("doc_id", uint64(docID)),
					zap.Duration("elapsed", time.Since(start)),
				)
				h.knowledgeDocFinalizeFailed(orgID, docID)
				return
			}
			chunks = append(chunks, chunk{Idx: 0, Text: txt})
		}
		logger.Info("knowledge.upload.job.chunked",
			zap.String("provider", provider),
			zap.Uint64("org_id", uint64(orgID)),
			zap.String("namespace", strings.TrimSpace(ns.Namespace)),
			zap.Uint64("doc_id", uint64(docID)),
			zap.Int("chunks", len(chunks)),
			zap.Duration("elapsed", time.Since(start)),
		)

		mdText := buildStructuredMarkdown(strings.TrimSpace(fileName), ns.Namespace, fileHash, "upload", func() []struct {
			Idx  int
			Text string
		} {
			out := make([]struct {
				Idx  int
				Text string
			}, 0, len(chunks))
			for _, it := range chunks {
				out = append(out, struct {
					Idx  int
					Text string
				}{Idx: it.Idx, Text: it.Text})
			}
			return out
		}())

		inputs := make([]string, 0, len(chunks))
		for _, ch := range chunks {
			inputs = append(inputs, ch.Text)
		}
		embedStart := time.Now()
		logger.Info("knowledge.upload.job.embed.start",
			zap.String("provider", provider),
			zap.Uint64("org_id", uint64(orgID)),
			zap.String("namespace", strings.TrimSpace(ns.Namespace)),
			zap.Uint64("doc_id", uint64(docID)),
			zap.Int("inputs", len(inputs)),
			zap.Duration("elapsed", time.Since(start)),
		)
		vecs, err := embedder.Embed(ctx, inputs)
		if err != nil || len(vecs) != len(chunks) || len(vecs) == 0 || len(vecs[0]) == 0 {
			logger.Error("knowledge.upload.job.embed_failed",
				zap.String("provider", provider),
				zap.Uint64("org_id", uint64(orgID)),
				zap.String("namespace", strings.TrimSpace(ns.Namespace)),
				zap.Uint64("doc_id", uint64(docID)),
				zap.Int("chunks", len(chunks)),
				zap.Int("vecs", len(vecs)),
				zap.Error(err),
				zap.Duration("elapsed", time.Since(start)),
			)
			h.knowledgeDocFinalizeFailed(orgID, docID)
			return
		}
		logger.Info("knowledge.upload.job.embed.done",
			zap.String("provider", provider),
			zap.Uint64("org_id", uint64(orgID)),
			zap.String("namespace", strings.TrimSpace(ns.Namespace)),
			zap.Uint64("doc_id", uint64(docID)),
			zap.Int("vecs", len(vecs)),
			zap.Int("vector_dim", len(vecs[0])),
			zap.Duration("embed_elapsed", time.Since(embedStart)),
			zap.Duration("elapsed", time.Since(start)),
		)
		for i := range vecs {
			normalizeVec64InPlace(vecs[i])
		}
		gotDim := len(vecs[0])
		if ns.VectorDim > 0 && gotDim != ns.VectorDim {
			logger.Error("knowledge.upload.job.vector_dim_mismatch",
				zap.String("provider", provider),
				zap.Uint64("org_id", uint64(orgID)),
				zap.String("namespace", strings.TrimSpace(ns.Namespace)),
				zap.Uint64("doc_id", uint64(docID)),
				zap.Int("expected_dim", ns.VectorDim),
				zap.Int("got_dim", gotDim),
				zap.Duration("elapsed", time.Since(start)),
			)
			h.knowledgeDocFinalizeFailed(orgID, docID)
			return
		}

		now := time.Now().UTC()
		records := make([]knowledge.Record, 0, len(chunks))
		recordIDs := make([]string, 0, len(chunks))
		for i, ch := range chunks {
			rid := uuid.NewString()
			recordIDs = append(recordIDs, rid)
			v64 := vecs[i]
			v32 := make([]float32, 0, len(v64))
			for _, x := range v64 {
				v32 = append(v32, float32(x))
			}
			records = append(records, knowledge.Record{
				ID:      rid,
				Source:  "upload",
				Title:   fileName,
				Content: ch.Text,
				Vector:  v32,
				Metadata: map[string]any{
					"doc_id":        fmt.Sprintf("%d", docID),
					"file_name":     fileName,
					"file_hash":     fileHash,
					"section_index": ch.Idx,
				},
				CreatedAt: now,
				UpdatedAt: now,
			})
		}

		kh, err := knowledgeHandlerForNS(&ns, embedder)
		if err != nil {
			logger.Error("knowledge.upload.job.handler_failed",
				zap.String("provider", provider),
				zap.Uint64("org_id", uint64(orgID)),
				zap.String("namespace", strings.TrimSpace(ns.Namespace)),
				zap.Uint64("doc_id", uint64(docID)),
				zap.Error(err),
				zap.Duration("elapsed", time.Since(start)),
			)
			h.knowledgeDocFinalizeFailed(orgID, docID)
			return
		}
		upsertStart := time.Now()
		logger.Info("knowledge.upload.job.upsert.start",
			zap.String("provider", provider),
			zap.Uint64("org_id", uint64(orgID)),
			zap.String("namespace", strings.TrimSpace(ns.Namespace)),
			zap.Uint64("doc_id", uint64(docID)),
			zap.Int("records", len(records)),
			zap.Duration("elapsed", time.Since(start)),
		)
		if err := kh.Upsert(ctx, records, &knowledge.UpsertOptions{Namespace: strings.TrimSpace(ns.Namespace), BatchSize: 64}); err != nil {
			logger.Error("knowledge.upload.job.upsert_failed",
				zap.String("provider", provider),
				zap.Uint64("org_id", uint64(orgID)),
				zap.String("namespace", strings.TrimSpace(ns.Namespace)),
				zap.Uint64("doc_id", uint64(docID)),
				zap.Int("records", len(records)),
				zap.Error(err),
				zap.Duration("elapsed", time.Since(start)),
			)
			h.knowledgeDocFinalizeFailed(orgID, docID)
			return
		}
		logger.Info("knowledge.upload.job.upsert.done",
			zap.String("provider", provider),
			zap.Uint64("org_id", uint64(orgID)),
			zap.String("namespace", strings.TrimSpace(ns.Namespace)),
			zap.Uint64("doc_id", uint64(docID)),
			zap.Int("records", len(records)),
			zap.Duration("upsert_elapsed", time.Since(upsertStart)),
			zap.Duration("elapsed", time.Since(start)),
		)

		textURL := ""
		if u, upErr := uploadMarkdownToStore(orgID, ns.Namespace, docID, fileName, mdText); upErr == nil && u != "" {
			textURL = u
		}

		// Update keyword index (best-effort).
		if eng, err := knowledgeSearchFromEnv(); err == nil && eng != nil {
			_ = eng.IndexBatch(ctx, func() []search.Doc {
				docs := make([]search.Doc, 0, len(records))
				for _, r := range records {
					docs = append(docs, search.Doc{
						ID:   r.ID,
						Type: "knowledge_record",
						Fields: map[string]any{
							"org_id":    fmt.Sprintf("%d", orgID),
							"namespace": strings.TrimSpace(ns.Namespace),
							"doc_id":    fmt.Sprintf("%d", docID),
							"title":     r.Title,
							"content":   r.Content,
							"file_hash": fileHash,
							"source":    "upload",
						},
					})
				}
				return docs
			}())
		}

		h.knowledgeDocFinalizeSuccess(orgID, docID, recordIDs, textURL)
		logger.Info("knowledge.upload.job.done",
			zap.String("provider", provider),
			zap.Uint64("org_id", uint64(orgID)),
			zap.String("namespace", strings.TrimSpace(ns.Namespace)),
			zap.Uint64("doc_id", uint64(docID)),
			zap.Int("records", len(recordIDs)),
			zap.Int("vector_dim", gotDim),
			zap.Bool("uploaded_text", strings.TrimSpace(textURL) != ""),
			zap.Duration("elapsed", time.Since(start)),
		)
	}(orgID, *nsRow, docRow.ID, fh.Filename, fileHash, b)

	response.Success(c, "已提交后台处理", gin.H{"document": docRow})
}

func (h *Handlers) knowledgeNamespaceRecallTestHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	orgID := models.CurrentOrgID(c)
	nsRow, err := models.GetKnowledgeNamespace(h.db, orgID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "知识库不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}

	var req KnowledgeRecallTestReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	topK := req.TopK
	if topK <= 0 {
		topK = 5
	}
	minScore := req.MinScore
	if minScore < 0 {
		minScore = 0
	}
	if minScore > 1 {
		response.FailWithCode(c, 400, "minScore 取值范围应为 0~1（Cosine 相似度）", gin.H{
			"got":  req.MinScore,
			"hint": "例如 0 / 0.2 / 0.3；不要填 2。",
		})
		return
	}

	var docRow *models.KnowledgeDocument
	expected := map[string]struct{}{}
	if req.DocID != nil && strings.TrimSpace(*req.DocID) != "" {
		n, err := strconv.ParseUint(strings.TrimSpace(*req.DocID), 10, 64)
		if err != nil || n == 0 {
			response.FailWithCode(c, 400, "docId 无效", gin.H{"docId": *req.DocID})
			return
		}
		d, err := models.GetKnowledgeDocument(h.db, orgID, uint(n))
		if err != nil {
			response.Fail(c, "查询文档失败", gin.H{"error": err.Error()})
			return
		}
		docRow = d
		for _, rid := range parseRecordIDs(d.RecordIDs) {
			expected[rid] = struct{}{}
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	embedder, err := embedderFromEnv()
	if err != nil {
		response.Fail(c, "Embedder 未配置", gin.H{"error": err.Error()})
		return
	}
	kh, err := knowledgeHandlerForNS(nsRow, embedder)
	if err != nil {
		response.Fail(c, "知识库后端未就绪", gin.H{"error": err.Error()})
		return
	}
	vecResults, err := kh.Query(ctx, strings.TrimSpace(req.Query), &knowledge.QueryOptions{
		Namespace: strings.TrimSpace(nsRow.Namespace),
		TopK:      topK,
		MinScore:  minScore,
		Filters: func() []knowledge.Filter {
			if docRow == nil {
				return nil
			}
			return []knowledge.Filter{
				{Field: "doc_id", Operator: knowledge.FilterOpEqual, Value: []any{fmt.Sprintf("%d", docRow.ID)}},
			}
		}(),
	})
	if err != nil {
		response.Fail(c, "召回失败", gin.H{"error": err.Error()})
		return
	}

	// Keyword (Bleve) retrieval (best-effort).
	type kwItem struct {
		ID      string
		Score   float64
		Title   string
		Content string
	}
	kwItems := make([]kwItem, 0, topK)
	if eng, e2 := knowledgeSearchFromEnv(); e2 == nil && eng != nil {
		must := map[string][]string{
			"org_id":    {fmt.Sprintf("%d", orgID)},
			"namespace": {strings.TrimSpace(nsRow.Namespace)},
		}
		if docRow != nil {
			must["doc_id"] = []string{fmt.Sprintf("%d", docRow.ID)}
		}
		kwRes, e3 := eng.Search(ctx, search.SearchRequest{
			Keyword:      strings.TrimSpace(req.Query),
			SearchFields: []string{"title", "content"},
			MustTerms:    must,
			From:         0,
			Size:         topK,
			IncludeFields: []string{
				"title",
				"content",
			},
		})
		if e3 == nil {
			for _, h := range kwRes.Hits {
				title, _ := h.Fields["title"].(string)
				content, _ := h.Fields["content"].(string)
				kwItems = append(kwItems, kwItem{ID: h.ID, Score: h.Score, Title: title, Content: content})
			}
		}
	}

	// Fuse results with Reciprocal Rank Fusion (RRF).
	const rrfK = 60.0
	type fused struct {
		ID       string
		Record   knowledge.Record
		Score    float64 // RRF score
		VecScore *float64
		KwScore  *float64
		VecRank  *int
		KwRank   *int
	}
	fusedMap := map[string]*fused{}
	for i, r := range vecResults {
		rank := i + 1
		id := r.Record.ID
		vs := r.Score
		item := fusedMap[id]
		if item == nil {
			rec := r.Record
			item = &fused{ID: id, Record: rec}
			fusedMap[id] = item
		}
		item.Score += 1.0 / (rrfK + float64(rank))
		item.VecScore = &vs
		item.VecRank = &rank
	}
	for i, h := range kwItems {
		rank := i + 1
		id := h.ID
		ks := h.Score
		item := fusedMap[id]
		if item == nil {
			item = &fused{
				ID: id,
				Record: knowledge.Record{
					ID:      id,
					Source:  "keyword",
					Title:   h.Title,
					Content: h.Content,
					Metadata: map[string]any{
						"org_id":    fmt.Sprintf("%d", orgID),
						"namespace": strings.TrimSpace(nsRow.Namespace),
					},
				},
			}
			fusedMap[id] = item
		}
		item.Score += 1.0 / (rrfK + float64(rank))
		item.KwScore = &ks
		item.KwRank = &rank
	}
	fusedList := make([]*fused, 0, len(fusedMap))
	for _, it := range fusedMap {
		if it.Record.Metadata == nil {
			it.Record.Metadata = map[string]any{}
		}
		it.Record.Metadata["hybrid_score"] = it.Score
		if it.VecScore != nil {
			it.Record.Metadata["vec_score"] = *it.VecScore
		}
		if it.KwScore != nil {
			it.Record.Metadata["kw_score"] = *it.KwScore
		}
		if it.VecRank != nil {
			it.Record.Metadata["vec_rank"] = *it.VecRank
		}
		if it.KwRank != nil {
			it.Record.Metadata["kw_rank"] = *it.KwRank
		}
		fusedList = append(fusedList, it)
	}
	sort.Slice(fusedList, func(i, j int) bool { return fusedList[i].Score > fusedList[j].Score })
	if len(fusedList) > topK {
		fusedList = fusedList[:topK]
	}
	results := make([]knowledge.QueryResult, 0, len(fusedList))
	for _, it := range fusedList {
		// Keep `score` as similarity-like score for UI (prefer vector cosine; fallback to keyword score).
		score := it.Score
		if it.VecScore != nil {
			score = *it.VecScore
		} else if it.KwScore != nil {
			score = *it.KwScore
		}
		results = append(results, knowledge.QueryResult{Record: it.Record, Score: score})
	}

	hits := 0
	if len(expected) > 0 {
		for _, r := range results {
			if _, ok := expected[r.Record.ID]; ok {
				hits++
			}
		}
	}
	recallAtK := 0.0
	precisionAtK := 0.0
	if len(expected) > 0 {
		recallAtK = float64(hits) / float64(len(expected))
	}
	if len(results) > 0 && len(expected) > 0 {
		precisionAtK = float64(hits) / float64(len(results))
	}

	response.Success(c, "ok", gin.H{
		"namespace":      nsRow,
		"query":          req.Query,
		"topK":           topK,
		"minScore":       minScore,
		"score_note":     "Qdrant Cosine score 通常在 0~1；越大越相似。",
		"document":       docRow,
		"hits":           hits,
		"expected":       len(expected),
		"recall_at_k":    recallAtK,
		"precision_at_k": precisionAtK,
		"results":        results,
	})
}

func (h *Handlers) knowledgeNamespacesListHandler(c *gin.Context) {
	page := models.ParseQueryInt(c, "page", 1)
	pageSize := models.ClampPageSize(models.ParseQueryInt(c, "pageSize", 20))
	status := strings.TrimSpace(c.Query("status"))
	if status == "" {
		status = models.KnowledgeStatusActive
	}

	orgID := models.CurrentOrgID(c)
	out, err := models.ListKnowledgeNamespaces(h.db, orgID, status, page, pageSize)
	if err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{
		"list":      out.List,
		"total":     out.Total,
		"page":      out.Page,
		"pageSize":  out.PageSize,
		"totalPage": out.TotalPage,
	})
}

func (h *Handlers) knowledgeNamespaceDetailHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	orgID := models.CurrentOrgID(c)
	row, err := models.GetKnowledgeNamespace(h.db, orgID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "知识库不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{"namespace": row})
}

func (h *Handlers) knowledgeNamespaceCreateHandler(c *gin.Context) {
	var req KnowledgeNamespaceUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	var status string
	if req.Status != nil {
		status = strings.TrimSpace(*req.Status)
	}

	orgID := models.CurrentOrgID(c)
	vp := models.NormalizeVectorProvider(req.VectorProvider)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	if strings.TrimSpace(req.Namespace) == "" {
		response.FailWithCode(c, 400, "namespace（collection 名）不能为空", nil)
		return
	}

	embedder, err := embedderFromEnv()
	if err != nil {
		response.Fail(c, "Embedder 未配置", gin.H{"error": err.Error()})
		return
	}
	probe, err := embedder.Embed(ctx, []string{"dimension_probe"})
	if err != nil || len(probe) == 0 || len(probe[0]) == 0 {
		response.Fail(c, "向量模型不可用（无法推导维度）", gin.H{"error": fmt.Sprintf("%v", err)})
		return
	}
	realDim := len(probe[0])
	tmpNS := &models.KnowledgeNamespace{
		VectorProvider: vp,
		Namespace:      strings.TrimSpace(req.Namespace),
	}
	kh, err := knowledgeHandlerForNS(tmpNS, embedder)
	if err != nil {
		response.Fail(c, "向量后端不可用", gin.H{"error": err.Error()})
		return
	}
	// Always verify backend connectivity on create.
	if err := kh.Ping(ctx); err != nil {
		response.Fail(c, "向量后端不可用（Ping 失败）", gin.H{
			"provider": vp,
			"error":    err.Error(),
		})
		return
	}
	if vp == models.KnowledgeVectorProviderQdrant {
		if err := kh.CreateNamespace(ctx, strings.TrimSpace(req.Namespace)); err != nil {
			response.Fail(c, "创建知识库失败（Qdrant）", gin.H{"error": err.Error()})
			return
		}
	}

	row, err := models.UpsertKnowledgeNamespace(h.db, orgID, 0, &models.KnowledgeNamespaceCreateUpdate{
		Namespace:      req.Namespace,
		Name:           req.Name,
		Description:    req.Description,
		VectorProvider: vp,
		EmbedModel:     req.EmbedModel,
		VectorDim:      realDim,
		Status:         status,
	})
	if err != nil {
		if vp == models.KnowledgeVectorProviderQdrant {
			_ = kh.DeleteNamespace(context.Background(), strings.TrimSpace(req.Namespace))
		}
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "创建成功", row)
}

func (h *Handlers) knowledgeNamespaceUpdateHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var req KnowledgeNamespaceUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}

	var status string
	if req.Status != nil {
		status = strings.TrimSpace(*req.Status)
	}

	orgID := models.CurrentOrgID(c)
	// Disallow namespace change: it maps to Qdrant collection.
	existing, err := models.GetKnowledgeNamespace(h.db, orgID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "知识库不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(existing.Namespace) != strings.TrimSpace(req.Namespace) {
		response.FailWithCode(c, 400, "namespace 不允许修改（对应向量后端资源标识）", nil)
		return
	}
	if models.NormalizeVectorProvider(req.VectorProvider) != models.NormalizeVectorProvider(existing.VectorProvider) {
		response.FailWithCode(c, 400, "vector_provider 不允许修改", nil)
		return
	}
	if existing.VectorDim > 0 && req.VectorDim > 0 && existing.VectorDim != req.VectorDim {
		response.FailWithCode(c, 400, "vector_dim 不允许修改（对应 collection 维度）", gin.H{
			"current": existing.VectorDim,
			"got":     req.VectorDim,
		})
		return
	}
	vdim := existing.VectorDim
	row, err := models.UpsertKnowledgeNamespace(h.db, orgID, id, &models.KnowledgeNamespaceCreateUpdate{
		Namespace:      req.Namespace,
		Name:           req.Name,
		Description:    req.Description,
		VectorProvider: existing.VectorProvider,
		EmbedModel:     req.EmbedModel,
		VectorDim:      vdim,
		Status:         status,
	})
	if err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "更新成功", row)
}

func (h *Handlers) knowledgeNamespaceDeleteHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	orgID := models.CurrentOrgID(c)
	row, err := models.GetKnowledgeNamespace(h.db, orgID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "知识库不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	kh, err := knowledgeHandlerForNS(row, nil)
	if err != nil {
		response.Fail(c, "向量后端不可用", gin.H{"error": err.Error()})
		return
	}
	if err := kh.DeleteNamespace(ctx, strings.TrimSpace(row.Namespace)); err != nil {
		response.Fail(c, "删除失败（向量后端）", gin.H{"error": err.Error()})
		return
	}

	if err := models.SoftDeleteKnowledgeNamespace(h.db, orgID, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "知识库不存在", nil)
			return
		}
		response.Fail(c, "删除失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "删除成功", gin.H{"id": id})
}

func (h *Handlers) knowledgeDocumentsListHandler(c *gin.Context) {
	page := models.ParseQueryInt(c, "page", 1)
	pageSize := models.ClampPageSize(models.ParseQueryInt(c, "pageSize", 20))
	namespace := strings.TrimSpace(c.Query("namespace"))
	status := strings.TrimSpace(c.Query("status"))
	if status == "" {
		status = models.KnowledgeStatusActive
	}

	orgID := models.CurrentOrgID(c)
	out, err := models.ListKnowledgeDocuments(h.db, orgID, namespace, status, page, pageSize)
	if err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{
		"list":      out.List,
		"total":     out.Total,
		"page":      out.Page,
		"pageSize":  out.PageSize,
		"totalPage": out.TotalPage,
	})
}

func (h *Handlers) knowledgeDocumentDetailHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	orgID := models.CurrentOrgID(c)
	row, err := models.GetKnowledgeDocument(h.db, orgID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "知识文档不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	out := gin.H{"document": row}
	if nsRow, err := models.GetKnowledgeNamespaceByOrgAndNamespace(h.db, orgID, row.Namespace); err == nil && nsRow != nil {
		out["vector_provider"] = nsRow.VectorProvider
	}
	response.Success(c, "ok", out)
}

func (h *Handlers) knowledgeDocumentCreateOrUpsertHandler(c *gin.Context) {
	var req KnowledgeDocumentUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	var status string
	if req.Status != nil {
		status = strings.TrimSpace(*req.Status)
	}

	orgID := models.CurrentOrgID(c)
	row, err := models.UpsertKnowledgeDocument(h.db, orgID, 0, &models.KnowledgeDocumentUpsertReq{
		Namespace: req.Namespace,
		Title:     req.Title,
		Source:    req.Source,
		FileHash:  req.FileHash,
		RecordIDs: req.RecordIDs,
		Status:    status,
	})
	if err != nil {
		response.Fail(c, "写入失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "写入成功", row)
}

func (h *Handlers) knowledgeDocumentTextGetHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	orgID := models.CurrentOrgID(c)
	doc, err := models.GetKnowledgeDocument(h.db, orgID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "知识文档不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(doc.TextURL) == "" {
		response.Success(c, "ok", gin.H{"url": "", "markdown": ""})
		return
	}
	// Best-effort server-side fetch to avoid CORS/auth issues on frontend.
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, strings.TrimSpace(doc.TextURL), nil)
	if err != nil {
		response.Fail(c, "读取失败", gin.H{"error": err.Error()})
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		response.Fail(c, "读取失败", gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		response.FailWithCode(c, 502, "读取存储失败", gin.H{"status": resp.StatusCode, "body": string(b)})
		return
	}
	response.Success(c, "ok", gin.H{"url": doc.TextURL, "markdown": string(b)})
}

type knowledgeDocumentTextPutReq struct {
	Markdown string `json:"markdown" binding:"required"`
}

func (h *Handlers) knowledgeDocumentTextPutHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var req knowledgeDocumentTextPutReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	mdText := strings.TrimSpace(req.Markdown)
	if mdText == "" {
		response.FailWithCode(c, 400, "markdown 不能为空", nil)
		return
	}
	orgID := models.CurrentOrgID(c)
	doc, err := models.GetKnowledgeDocument(h.db, orgID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "知识文档不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	// namespace meta for dim validation
	var nsRow models.KnowledgeNamespace
	if err := h.db.Where("org_id = ? AND namespace = ?", orgID, strings.TrimSpace(doc.Namespace)).First(&nsRow).Error; err != nil {
		response.Fail(c, "查询知识库失败", gin.H{"error": err.Error()})
		return
	}
	// Mark as processing and upload markdown immediately (fast path), then vectorize async.
	textURL := doc.TextURL
	if u, upErr := uploadMarkdownToStore(orgID, doc.Namespace, doc.ID, doc.Title, mdText+"\n"); upErr == nil && u != "" {
		textURL = u
	}
	_ = h.db.Model(&models.KnowledgeDocument{}).
		Where("org_id = ?", orgID).
		Where("id = ?", doc.ID).
		Updates(map[string]any{
			"text_url": textURL,
			"status":   models.KnowledgeStatusProcessing,
		}).Error
	doc.TextURL = textURL
	doc.Status = models.KnowledgeStatusProcessing

	go func(orgID uint, ns models.KnowledgeNamespace, doc models.KnowledgeDocument, mdText string) {
		start := time.Now()
		provider := knowledgeProviderForLog(&ns)
		logger.Info("knowledge.text_put.job.start",
			zap.String("provider", provider),
			zap.Uint64("org_id", uint64(orgID)),
			zap.String("namespace", strings.TrimSpace(ns.Namespace)),
			zap.Uint64("doc_id", uint64(doc.ID)),
			zap.String("title", strings.TrimSpace(doc.Title)),
			zap.Int("markdown_chars", len(mdText)),
		)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		embedder, err := embedderFromEnv()
		if err != nil {
			logger.Error("knowledge.text_put.job.embedder_failed",
				zap.String("provider", provider),
				zap.Uint64("org_id", uint64(orgID)),
				zap.String("namespace", strings.TrimSpace(ns.Namespace)),
				zap.Uint64("doc_id", uint64(doc.ID)),
				zap.Error(err),
				zap.Duration("elapsed", time.Since(start)),
			)
			h.knowledgeDocFinalizeFailed(orgID, doc.ID)
			return
		}

		type chunk struct {
			Idx  int
			Text string
		}
		chunks := make([]chunk, 0, 16)
		if ch, _, err := chunkerFromEnv(); err == nil && ch != nil {
			llmChunks, err := ch.Chunk(ctx, mdText, &knowledge.ChunkOptions{DocumentTitle: strings.TrimSpace(doc.Title)})
			if err == nil && len(llmChunks) > 0 {
				for _, it := range llmChunks {
					if strings.TrimSpace(it.Text) == "" {
						continue
					}
					chunks = append(chunks, chunk{Idx: it.Index, Text: it.Text})
				}
			}
		}
		if len(chunks) == 0 {
			const maxChars = knowledge.DefaultChunkMaxChars
			const overlap = knowledge.DefaultChunkOverlapChars
			s := mdText
			idx := 0
			for start := 0; start < len(s); {
				end := start + maxChars
				if end > len(s) {
					end = len(s)
				}
				part := strings.TrimSpace(s[start:end])
				if part != "" {
					chunks = append(chunks, chunk{Idx: idx, Text: part})
					idx++
				}
				if end == len(s) {
					break
				}
				start = end - overlap
				if start < 0 {
					start = 0
				}
			}
		}

		inputs := make([]string, 0, len(chunks))
		for _, ch := range chunks {
			inputs = append(inputs, ch.Text)
		}
		embedStart := time.Now()
		logger.Info("knowledge.text_put.job.embed.start",
			zap.String("provider", provider),
			zap.Uint64("org_id", uint64(orgID)),
			zap.String("namespace", strings.TrimSpace(ns.Namespace)),
			zap.Uint64("doc_id", uint64(doc.ID)),
			zap.Int("inputs", len(inputs)),
			zap.Duration("elapsed", time.Since(start)),
		)
		vecs, err := embedder.Embed(ctx, inputs)
		if err != nil || len(vecs) == 0 || len(vecs[0]) == 0 {
			logger.Error("knowledge.text_put.job.embed_failed",
				zap.String("provider", provider),
				zap.Uint64("org_id", uint64(orgID)),
				zap.String("namespace", strings.TrimSpace(ns.Namespace)),
				zap.Uint64("doc_id", uint64(doc.ID)),
				zap.Int("chunks", len(chunks)),
				zap.Int("vecs", len(vecs)),
				zap.Error(err),
				zap.Duration("elapsed", time.Since(start)),
			)
			h.knowledgeDocFinalizeFailed(orgID, doc.ID)
			return
		}
		logger.Info("knowledge.text_put.job.embed.done",
			zap.String("provider", provider),
			zap.Uint64("org_id", uint64(orgID)),
			zap.String("namespace", strings.TrimSpace(ns.Namespace)),
			zap.Uint64("doc_id", uint64(doc.ID)),
			zap.Int("vecs", len(vecs)),
			zap.Int("vector_dim", len(vecs[0])),
			zap.Duration("embed_elapsed", time.Since(embedStart)),
			zap.Duration("elapsed", time.Since(start)),
		)
		for i := range vecs {
			normalizeVec64InPlace(vecs[i])
		}
		gotDim := len(vecs[0])
		if ns.VectorDim > 0 && gotDim != ns.VectorDim {
			logger.Error("knowledge.text_put.job.vector_dim_mismatch",
				zap.String("provider", provider),
				zap.Uint64("org_id", uint64(orgID)),
				zap.String("namespace", strings.TrimSpace(ns.Namespace)),
				zap.Uint64("doc_id", uint64(doc.ID)),
				zap.Int("expected_dim", ns.VectorDim),
				zap.Int("got_dim", gotDim),
				zap.Duration("elapsed", time.Since(start)),
			)
			h.knowledgeDocFinalizeFailed(orgID, doc.ID)
			return
		}

		kh, err := knowledgeHandlerForNS(&ns, embedder)
		if err != nil {
			logger.Error("knowledge.text_put.job.handler_failed",
				zap.String("provider", provider),
				zap.Uint64("org_id", uint64(orgID)),
				zap.String("namespace", strings.TrimSpace(ns.Namespace)),
				zap.Uint64("doc_id", uint64(doc.ID)),
				zap.Error(err),
				zap.Duration("elapsed", time.Since(start)),
			)
			h.knowledgeDocFinalizeFailed(orgID, doc.ID)
			return
		}
		oldIDs := parseRecordIDs(doc.RecordIDs)
		if len(oldIDs) > 0 {
			_ = kh.Delete(ctx, oldIDs, &knowledge.DeleteOptions{Namespace: strings.TrimSpace(doc.Namespace)})
			// Delete from keyword index (best-effort).
			if eng, err := knowledgeSearchFromEnv(); err == nil && eng != nil {
				for _, rid := range oldIDs {
					_ = eng.Delete(ctx, rid)
				}
			}
		}

		now := time.Now().UTC()
		records := make([]knowledge.Record, 0, len(chunks))
		recordIDs := make([]string, 0, len(chunks))
		for i, ch := range chunks {
			rid := uuid.NewString()
			recordIDs = append(recordIDs, rid)
			v64 := vecs[i]
			v32 := make([]float32, 0, len(v64))
			for _, x := range v64 {
				v32 = append(v32, float32(x))
			}
			records = append(records, knowledge.Record{
				ID:      rid,
				Source:  "edit",
				Title:   doc.Title,
				Content: ch.Text,
				Vector:  v32,
				Metadata: map[string]any{
					"doc_id":        fmt.Sprintf("%d", doc.ID),
					"file_name":     doc.Title,
					"section_index": ch.Idx,
				},
				CreatedAt: now,
				UpdatedAt: now,
			})
		}
		upsertStart := time.Now()
		logger.Info("knowledge.text_put.job.upsert.start",
			zap.String("provider", provider),
			zap.Uint64("org_id", uint64(orgID)),
			zap.String("namespace", strings.TrimSpace(ns.Namespace)),
			zap.Uint64("doc_id", uint64(doc.ID)),
			zap.Int("records", len(records)),
			zap.Duration("elapsed", time.Since(start)),
		)
		if err := kh.Upsert(ctx, records, &knowledge.UpsertOptions{Namespace: strings.TrimSpace(doc.Namespace), Overwrite: true, BatchSize: 64}); err != nil {
			logger.Error("knowledge.text_put.job.upsert_failed",
				zap.String("provider", provider),
				zap.Uint64("org_id", uint64(orgID)),
				zap.String("namespace", strings.TrimSpace(ns.Namespace)),
				zap.Uint64("doc_id", uint64(doc.ID)),
				zap.Int("records", len(records)),
				zap.Error(err),
				zap.Duration("elapsed", time.Since(start)),
			)
			h.knowledgeDocFinalizeFailed(orgID, doc.ID)
			return
		}
		logger.Info("knowledge.text_put.job.upsert.done",
			zap.String("provider", provider),
			zap.Uint64("org_id", uint64(orgID)),
			zap.String("namespace", strings.TrimSpace(ns.Namespace)),
			zap.Uint64("doc_id", uint64(doc.ID)),
			zap.Int("records", len(records)),
			zap.Duration("upsert_elapsed", time.Since(upsertStart)),
			zap.Duration("elapsed", time.Since(start)),
		)

		// Update keyword index (best-effort).
		if eng, err := knowledgeSearchFromEnv(); err == nil && eng != nil {
			_ = eng.IndexBatch(ctx, func() []search.Doc {
				docs := make([]search.Doc, 0, len(records))
				for _, r := range records {
					docs = append(docs, search.Doc{
						ID:   r.ID,
						Type: "knowledge_record",
						Fields: map[string]any{
							"org_id":    fmt.Sprintf("%d", orgID),
							"namespace": strings.TrimSpace(doc.Namespace),
							"doc_id":    fmt.Sprintf("%d", doc.ID),
							"title":     r.Title,
							"content":   r.Content,
							"file_hash": strings.TrimSpace(doc.FileHash),
							"source":    "edit",
						},
					})
				}
				return docs
			}())
		}

		h.knowledgeDocFinalizeSuccess(orgID, doc.ID, recordIDs, "")
		logger.Info("knowledge.text_put.job.done",
			zap.String("provider", provider),
			zap.Uint64("org_id", uint64(orgID)),
			zap.String("namespace", strings.TrimSpace(ns.Namespace)),
			zap.Uint64("doc_id", uint64(doc.ID)),
			zap.Int("records", len(recordIDs)),
			zap.Int("vector_dim", gotDim),
			zap.Duration("elapsed", time.Since(start)),
		)
	}(orgID, nsRow, *doc, mdText)

	response.Success(c, "已提交后台处理", gin.H{"document": doc})
}

func (h *Handlers) knowledgeDocumentUpdateHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var req KnowledgeDocumentUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	var status string
	if req.Status != nil {
		status = strings.TrimSpace(*req.Status)
	}

	orgID := models.CurrentOrgID(c)
	row, err := models.UpsertKnowledgeDocument(h.db, orgID, id, &models.KnowledgeDocumentUpsertReq{
		Namespace: req.Namespace,
		Title:     req.Title,
		Source:    req.Source,
		FileHash:  req.FileHash,
		RecordIDs: req.RecordIDs,
		Status:    status,
	})
	if err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "更新成功", row)
}

// knowledgeDocumentReuploadHandler reuploads a file and rebuilds vectors for an existing document.
// POST /api/knowledge-documents/:id/upload (multipart form: file)
func (h *Handlers) knowledgeDocumentReuploadHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	orgID := models.CurrentOrgID(c)
	doc, err := models.GetKnowledgeDocument(h.db, orgID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "知识文档不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	// Load namespace meta by (org_id, namespace) so we can validate dim.
	var nsRow models.KnowledgeNamespace
	if err := h.db.Where("org_id = ? AND namespace = ?", orgID, strings.TrimSpace(doc.Namespace)).First(&nsRow).Error; err != nil {
		response.Fail(c, "查询知识库失败", gin.H{"error": err.Error()})
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
		response.FailWithCode(c, 400, "缺少文件 file", gin.H{"error": err.Error()})
		return
	}
	f, err := fh.Open()
	if err != nil {
		response.Fail(c, "打开文件失败", gin.H{"error": err.Error()})
		return
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		response.Fail(c, "读取文件失败", gin.H{"error": err.Error()})
		return
	}
	sum := md5.Sum(b)
	fileHash := fmt.Sprintf("%x", sum[:])
	oldRecordIDs := doc.RecordIDs

	// Mark as processing and clear old record_ids immediately; do the heavy work async.
	_ = h.db.Model(&models.KnowledgeDocument{}).
		Where("org_id = ?", orgID).
		Where("id = ?", doc.ID).
		Updates(map[string]any{
			"title":      fh.Filename,
			"file_hash":  fileHash,
			"source":     "upload",
			"record_ids": "",
			"status":     models.KnowledgeStatusProcessing,
		}).Error
	doc.Title = fh.Filename
	doc.FileHash = fileHash
	doc.Source = "upload"
	doc.RecordIDs = ""
	doc.Status = models.KnowledgeStatusProcessing

	go func(orgID uint, ns models.KnowledgeNamespace, doc models.KnowledgeDocument, oldRecordIDs string, fileName string, fileHash string, content []byte) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		embedder, err := embedderFromEnv()
		if err != nil {
			h.knowledgeDocFinalizeFailed(orgID, doc.ID)
			return
		}

		parsed, err := knowledgeParser.ParseBytes(ctx, fileName, content, &knowledgeParser.ParseOptions{MaxTextLength: 200000})
		if err != nil {
			h.knowledgeDocFinalizeFailed(orgID, doc.ID)
			return
		}

		type chunk struct {
			Idx  int
			Text string
		}
		chunks := make([]chunk, 0, 16)
		if ch, _, err := chunkerFromEnv(); err == nil && ch != nil {
			raw := strings.TrimSpace(parsed.Text)
			if raw != "" {
				llmChunks, err := ch.Chunk(ctx, raw, &knowledge.ChunkOptions{DocumentTitle: strings.TrimSpace(fileName)})
				if err == nil && len(llmChunks) > 0 {
					for _, it := range llmChunks {
						if strings.TrimSpace(it.Text) == "" {
							continue
						}
						chunks = append(chunks, chunk{Idx: it.Index, Text: it.Text})
					}
				}
			}
		}
		if len(chunks) == 0 {
			for _, s := range parsed.Sections {
				txt := strings.TrimSpace(s.Text)
				if txt == "" {
					continue
				}
				chunks = append(chunks, chunk{Idx: s.Index, Text: txt})
			}
		}
		if len(chunks) == 0 {
			txt := strings.TrimSpace(parsed.Text)
			if txt == "" {
				h.knowledgeDocFinalizeFailed(orgID, doc.ID)
				return
			}
			chunks = append(chunks, chunk{Idx: 0, Text: txt})
		}

		mdText := buildStructuredMarkdown(strings.TrimSpace(fileName), doc.Namespace, fileHash, "reupload", func() []struct {
			Idx  int
			Text string
		} {
			out := make([]struct {
				Idx  int
				Text string
			}, 0, len(chunks))
			for _, it := range chunks {
				out = append(out, struct {
					Idx  int
					Text string
				}{Idx: it.Idx, Text: it.Text})
			}
			return out
		}())

		inputs := make([]string, 0, len(chunks))
		for _, ch := range chunks {
			inputs = append(inputs, ch.Text)
		}
		vecs, err := embedder.Embed(ctx, inputs)
		if err != nil || len(vecs) == 0 || len(vecs[0]) == 0 {
			h.knowledgeDocFinalizeFailed(orgID, doc.ID)
			return
		}
		for i := range vecs {
			normalizeVec64InPlace(vecs[i])
		}
		gotDim := len(vecs[0])
		if ns.VectorDim > 0 && gotDim != ns.VectorDim {
			h.knowledgeDocFinalizeFailed(orgID, doc.ID)
			return
		}

		kh, err := knowledgeHandlerForNS(&ns, embedder)
		if err != nil {
			h.knowledgeDocFinalizeFailed(orgID, doc.ID)
			return
		}
		oldIDs := parseRecordIDs(oldRecordIDs)
		if len(oldIDs) > 0 {
			_ = kh.Delete(ctx, oldIDs, &knowledge.DeleteOptions{Namespace: strings.TrimSpace(doc.Namespace)})
			// Delete from keyword index (best-effort).
			if eng, err := knowledgeSearchFromEnv(); err == nil && eng != nil {
				for _, rid := range oldIDs {
					_ = eng.Delete(ctx, rid)
				}
			}
		}

		now := time.Now().UTC()
		records := make([]knowledge.Record, 0, len(chunks))
		recordIDs := make([]string, 0, len(chunks))
		for i, ch := range chunks {
			rid := uuid.NewString()
			recordIDs = append(recordIDs, rid)
			v64 := vecs[i]
			v32 := make([]float32, 0, len(v64))
			for _, x := range v64 {
				v32 = append(v32, float32(x))
			}
			records = append(records, knowledge.Record{
				ID:      rid,
				Source:  "upload",
				Title:   fileName,
				Content: ch.Text,
				Vector:  v32,
				Metadata: map[string]any{
					"doc_id":        fmt.Sprintf("%d", doc.ID),
					"file_name":     fileName,
					"file_hash":     fileHash,
					"section_index": ch.Idx,
				},
				CreatedAt: now,
				UpdatedAt: now,
			})
		}
		if err := kh.Upsert(ctx, records, &knowledge.UpsertOptions{Namespace: strings.TrimSpace(doc.Namespace), Overwrite: true, BatchSize: 64}); err != nil {
			h.knowledgeDocFinalizeFailed(orgID, doc.ID)
			return
		}

		textURL := ""
		if u, upErr := uploadMarkdownToStore(orgID, doc.Namespace, doc.ID, fileName, mdText); upErr == nil && u != "" {
			textURL = u
		}

		// Update keyword index (best-effort).
		if eng, err := knowledgeSearchFromEnv(); err == nil && eng != nil {
			_ = eng.IndexBatch(ctx, func() []search.Doc {
				docs := make([]search.Doc, 0, len(records))
				for _, r := range records {
					docs = append(docs, search.Doc{
						ID:   r.ID,
						Type: "knowledge_record",
						Fields: map[string]any{
							"org_id":    fmt.Sprintf("%d", orgID),
							"namespace": strings.TrimSpace(doc.Namespace),
							"doc_id":    fmt.Sprintf("%d", doc.ID),
							"title":     r.Title,
							"content":   r.Content,
							"file_hash": fileHash,
							"source":    "upload",
						},
					})
				}
				return docs
			}())
		}
		h.knowledgeDocFinalizeSuccess(orgID, doc.ID, recordIDs, textURL)
	}(orgID, nsRow, *doc, oldRecordIDs, fh.Filename, fileHash, b)

	response.Success(c, "已提交后台处理", gin.H{"document": doc})
}

func (h *Handlers) knowledgeDocumentDeleteHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	orgID := models.CurrentOrgID(c)

	doc, err := models.GetKnowledgeDocument(h.db, orgID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "知识文档不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}

	ids := parseRecordIDs(doc.RecordIDs)
	if len(ids) > 0 {
		var nsRow models.KnowledgeNamespace
		if err := h.db.Where("org_id = ? AND namespace = ?", orgID, strings.TrimSpace(doc.Namespace)).First(&nsRow).Error; err != nil {
			response.Fail(c, "查询知识库失败", gin.H{"error": err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		kh, err := knowledgeHandlerForNS(&nsRow, nil)
		if err != nil {
			response.Fail(c, "向量后端不可用", gin.H{"error": err.Error()})
			return
		}
		if err := kh.Delete(ctx, ids, &knowledge.DeleteOptions{Namespace: strings.TrimSpace(doc.Namespace)}); err != nil {
			response.Fail(c, "删除失败（向量 points）", gin.H{"error": err.Error()})
			return
		}

		// Delete from keyword index (best-effort).
		if eng, err := knowledgeSearchFromEnv(); err == nil && eng != nil {
			for _, rid := range ids {
				_ = eng.Delete(ctx, rid)
			}
		}
	}

	if err := models.SoftDeleteKnowledgeDocument(h.db, orgID, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "知识文档不存在", nil)
			return
		}
		response.Fail(c, "删除失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "删除成功", gin.H{"id": id})
}
