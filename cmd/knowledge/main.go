package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/knowledge"
	"github.com/LingByte/LingVoice/pkg/llm"
	"github.com/LingByte/LingVoice/pkg/utils/base"
	knowledgeParser "github.com/LingByte/LingVoice/pkg/utils/parser"
	"github.com/google/uuid"
)

const (
	defaultAddr                = ":7081"
	defaultQdrantBaseURL       = "http://localhost:6333"
	defaultQdrantCollection    = "lingvoice_knowledge_demo"
	defaultParseMaxTextChars   = 20000
	defaultUploadMaxBytes      = 20 << 20
	defaultTopK                = 3
	defaultQueryPreviewChars   = 300
	defaultSectionPreviewChars = 500
)

func main() {
	var (
		addr             = flag.String("addr", defaultAddr, "http listen addr")
		qdrantBaseURL    = flag.String("qdrant-base-url", getenvOr(defaultQdrantBaseURL, "QDRANT_BASE_URL"), "qdrant base url")
		qdrantCollection = flag.String("qdrant-collection", getenvOr(defaultQdrantCollection, "QDRANT_COLLECTION"), "qdrant collection/namespace")
		parseMaxChars    = flag.Int("parse-max-chars", getenvInt("PARSE_MAX_CHARS", defaultParseMaxTextChars), "max parsed text chars")
		uploadMaxBytes   = flag.Int64("upload-max-bytes", getenvInt64("UPLOAD_MAX_BYTES", defaultUploadMaxBytes), "max upload size")
	)
	flag.Parse()

	embedder, embedInfo, err := buildEmbedderFromEnv()
	if err != nil {
		log.Fatalf("build embedder failed: %v", err)
	}

	llmChunker, chunkInfo, err := buildLLMChunkerFromEnv()
	if err != nil {
		log.Printf("build llm chunker skipped: %v", err)
	}
	if llmChunker != nil {
		log.Printf("llm chunker enabled: %s", chunkInfo)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Simple HTML page, so you can test locally without rebuilding the web frontend.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = uploadPageHTML(w, embedInfo)
	})
	mux.HandleFunc("/api/index", func(w http.ResponseWriter, r *http.Request) {
		handleIndex(w, r, &handleIndexDeps{
			qdrantBaseURL:     strings.TrimSpace(*qdrantBaseURL),
			defaultCollection: strings.TrimSpace(*qdrantCollection),
			embedder:          embedder,
			chunker:           llmChunker,
			parseMaxChars:     *parseMaxChars,
			uploadMaxBytes:    *uploadMaxBytes,
		})
	})
	mux.HandleFunc("/api/upload", func(w http.ResponseWriter, r *http.Request) {
		handleUpload(w, r, &handleIndexDeps{
			qdrantBaseURL:     strings.TrimSpace(*qdrantBaseURL),
			defaultCollection: strings.TrimSpace(*qdrantCollection),
			embedder:          embedder,
			chunker:           llmChunker,
			parseMaxChars:     *parseMaxChars,
			uploadMaxBytes:    *uploadMaxBytes,
		})
	})
	mux.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		handleTest(w, r, &handleIndexDeps{
			qdrantBaseURL:     strings.TrimSpace(*qdrantBaseURL),
			defaultCollection: strings.TrimSpace(*qdrantCollection),
			embedder:          embedder,
			chunker:           llmChunker,
			parseMaxChars:     *parseMaxChars,
			uploadMaxBytes:    *uploadMaxBytes,
		})
	})
	mux.HandleFunc("/api/docs", func(w http.ResponseWriter, r *http.Request) {
		handleDocs(w, r, &handleIndexDeps{
			qdrantBaseURL:     strings.TrimSpace(*qdrantBaseURL),
			defaultCollection: strings.TrimSpace(*qdrantCollection),
			embedder:          embedder,
			chunker:           llmChunker,
			parseMaxChars:     *parseMaxChars,
			uploadMaxBytes:    *uploadMaxBytes,
		})
	})
	mux.HandleFunc("/api/namespaces", func(w http.ResponseWriter, r *http.Request) {
		handleNamespaces(w, r, &handleIndexDeps{
			qdrantBaseURL:     strings.TrimSpace(*qdrantBaseURL),
			defaultCollection: strings.TrimSpace(*qdrantCollection),
			embedder:          embedder,
			chunker:           llmChunker,
			parseMaxChars:     *parseMaxChars,
			uploadMaxBytes:    *uploadMaxBytes,
		})
	})
	mux.HandleFunc("/api/doc", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			handleDeleteDoc(w, r, &handleIndexDeps{
				qdrantBaseURL:     strings.TrimSpace(*qdrantBaseURL),
				defaultCollection: strings.TrimSpace(*qdrantCollection),
				embedder:          embedder,
				chunker:           llmChunker,
				parseMaxChars:     *parseMaxChars,
				uploadMaxBytes:    *uploadMaxBytes,
			})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/namespace", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			handleDeleteNamespace(w, r, &handleIndexDeps{
				qdrantBaseURL:     strings.TrimSpace(*qdrantBaseURL),
				defaultCollection: strings.TrimSpace(*qdrantCollection),
				embedder:          embedder,
				chunker:           llmChunker,
				parseMaxChars:     *parseMaxChars,
				uploadMaxBytes:    *uploadMaxBytes,
			})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	log.Printf("knowledge test server listening on %s", *addr)
	if err := http.ListenAndServe(*addr, withCORS(mux)); err != nil {
		log.Fatal(err)
	}
}

type handleIndexDeps struct {
	qdrantBaseURL     string
	defaultCollection string
	embedder          knowledge.Embedder
	chunker           knowledge.Chunker
	parseMaxChars     int
	uploadMaxBytes    int64
}

func handleIndex(w http.ResponseWriter, r *http.Request, deps *handleIndexDeps) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	if err := r.ParseMultipartForm(deps.uploadMaxBytes); err != nil {
		http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	queryText := strings.TrimSpace(r.FormValue("query"))
	collection := strings.TrimSpace(r.FormValue("collection"))
	if collection == "" {
		collection = deps.defaultCollection
	}

	parsed, docID, err := parseUpload(ctx, file, header, deps.parseMaxChars)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	preview := clip(parsed.Text, defaultQueryPreviewChars)

	// Decide query: if user doesn't pass it, use extracted content preview.
	if queryText == "" {
		queryText = parsed.Text
		if len(queryText) > 2000 {
			queryText = queryText[:2000]
		}
	}

	// Vectorize extracted content and upsert to Qdrant.
	qh := &knowledge.QdrantHandler{
		BaseURL:    deps.qdrantBaseURL,
		Collection: collection,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Embedder:   deps.embedder,
	}

	records, err := recordsFromParseResult(ctx, parsed, deps.embedder, deps.chunker, docID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	uploadedIDs := make([]string, 0, len(records))
	uploadedIDSet := make(map[string]struct{}, len(records))
	for i := range records {
		if strings.TrimSpace(records[i].ID) == "" {
			continue
		}
		uploadedIDs = append(uploadedIDs, records[i].ID)
		uploadedIDSet[records[i].ID] = struct{}{}
	}
	// Upsert uses EnsureCollection internally (vectorDim inferred from first record.Vector).
	if err := qh.Upsert(ctx, records, &knowledge.UpsertOptions{Namespace: collection, Overwrite: true, BatchSize: 16}); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	// Recall test
	topK := defaultTopK
	results, err := qh.Query(ctx, queryText, &knowledge.QueryOptions{
		Namespace: collection,
		TopK:      topK,
		MinScore:  0,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	type rec struct {
		ID      string  `json:"id"`
		Score   float64 `json:"score"`
		Content string  `json:"content"`
		Title   string  `json:"title"`
	}
	out := make([]rec, 0, len(results))
	hits := 0
	for _, it := range results {
		if _, ok := uploadedIDSet[it.Record.ID]; ok {
			hits++
		}
		out = append(out, rec{
			ID:      it.Record.ID,
			Score:   it.Score,
			Content: clip(it.Record.Content, defaultSectionPreviewChars),
			Title:   clip(it.Record.Title, 120),
		})
	}

	relevantCount := len(uploadedIDs)
	recallAtK := 0.0
	if relevantCount > 0 {
		recallAtK = float64(hits) / float64(relevantCount)
	}
	precisionAtK := 0.0
	if len(results) > 0 {
		precisionAtK = float64(hits) / float64(len(results))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                  true,
		"collection":          collection,
		"parsed_file":         header.Filename,
		"query_used":          clip(queryText, defaultQueryPreviewChars),
		"extracted_preview":   preview,
		"sections":            len(parsed.Sections),
		"records_upserted":    len(records),
		"uploaded_record_ids": uploadedIDs,
		"top_k":               topK,
		"recall_at_k":         recallAtK,
		"precision_at_k":      precisionAtK,
		"relevant_count":      relevantCount,
		"hits_count":          hits,
		"results":             out,
	})
}

func handleUpload(w http.ResponseWriter, r *http.Request, deps *handleIndexDeps) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	if err := r.ParseMultipartForm(deps.uploadMaxBytes); err != nil {
		http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	collection := strings.TrimSpace(r.FormValue("collection"))
	if collection == "" {
		collection = deps.defaultCollection
	}

	parsed, docID, err := parseUpload(ctx, file, header, deps.parseMaxChars)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	qh := qdrantForCollection(deps, collection)
	records, err := recordsFromParseResult(ctx, parsed, deps.embedder, deps.chunker, docID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := qh.Upsert(ctx, records, &knowledge.UpsertOptions{Namespace: collection, Overwrite: true, BatchSize: 16}); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	uploadedIDs := make([]string, 0, len(records))
	for i := range records {
		uploadedIDs = append(uploadedIDs, records[i].ID)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                  true,
		"collection":          collection,
		"doc_id":              docID,
		"parsed_file":         header.Filename,
		"sections":            len(parsed.Sections),
		"records_upserted":    len(records),
		"uploaded_record_ids": uploadedIDs,
		"extracted_preview":   clip(parsed.Text, defaultQueryPreviewChars),
	})
}

type testReq struct {
	Collection string  `json:"collection"`
	Query      string  `json:"query"`
	DocID      string  `json:"doc_id"`
	TopK       int     `json:"top_k"`
	MinScore   float64 `json:"min_score"`
}

func handleTest(w http.ResponseWriter, r *http.Request, deps *handleIndexDeps) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	var req testReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body: "+err.Error(), http.StatusBadRequest)
		return
	}
	collection := strings.TrimSpace(req.Collection)
	if collection == "" {
		collection = deps.defaultCollection
	}
	docID := strings.TrimSpace(req.DocID)
	queryText := strings.TrimSpace(req.Query)

	qh := qdrantForCollection(deps, collection)
	topK := req.TopK
	if topK <= 0 {
		topK = defaultTopK
	}
	minScore := req.MinScore
	if minScore == 0 {
		minScore = 0
	}

	if queryText == "" {
		if docID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "query and doc_id both empty"})
			return
		}
		derived, err := deriveDocQuery(ctx, qh, docID, 2000)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		queryText = derived
	}
	if strings.TrimSpace(queryText) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "query is empty"})
		return
	}

	results, err := qh.Query(ctx, queryText, &knowledge.QueryOptions{
		Namespace: collection,
		TopK:      topK,
		MinScore:  minScore,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	type recOut struct {
		ID      string  `json:"id"`
		Score   float64 `json:"score"`
		Content string  `json:"content"`
		Title   string  `json:"title"`
	}
	out := make([]recOut, 0, len(results))
	hits := 0
	for _, it := range results {
		if docID != "" {
			if v, ok := it.Record.Metadata["doc_id"].(string); ok && v == docID {
				hits++
			}
		}
		out = append(out, recOut{
			ID:      it.Record.ID,
			Score:   it.Score,
			Content: clip(it.Record.Content, defaultSectionPreviewChars),
			Title:   clip(it.Record.Title, 120),
		})
	}

	relevantCount := 0
	if docID != "" {
		cnt, err := countDocRecords(ctx, qh, docID, 10000)
		if err == nil {
			relevantCount = cnt
		}
	}
	recallAtK := 0.0
	if relevantCount > 0 {
		recallAtK = float64(hits) / float64(relevantCount)
	}
	precisionAtK := 0.0
	if len(out) > 0 {
		precisionAtK = float64(hits) / float64(len(out))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"collection":     collection,
		"doc_id":         docID,
		"query_used":     clip(queryText, defaultQueryPreviewChars),
		"top_k":          topK,
		"relevant_count": relevantCount,
		"hits_count":     hits,
		"recall_at_k":    recallAtK,
		"precision_at_k": precisionAtK,
		"results":        out,
	})
}

func handleDocs(w http.ResponseWriter, r *http.Request, deps *handleIndexDeps) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	collection := strings.TrimSpace(r.URL.Query().Get("collection"))
	if collection == "" {
		collection = deps.defaultCollection
	}
	qh := qdrantForCollection(deps, collection)
	limit := 10000
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 20000 {
			limit = n
		}
	}
	records, err := qh.List(ctx, &knowledge.ListOptions{Namespace: collection, Limit: limit})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	type docAgg struct {
		DocID         string `json:"doc_id"`
		FileName      string `json:"file_name"`
		RecordCount   int    `json:"record_count"`
		SectionsCount int    `json:"sections_count"`
	}
	byDoc := map[string]*docAgg{}
	sectionsByDoc := map[string]map[int]struct{}{}
	for _, rec := range records.Records {
		docID := ""
		if v, ok := rec.Metadata["doc_id"].(string); ok {
			docID = v
		}
		if docID == "" {
			// fallback by record id prefix before ':'
			parts := strings.SplitN(rec.ID, ":", 2)
			if len(parts) > 1 {
				docID = parts[0]
			}
		}
		if docID == "" {
			continue
		}
		fileName := ""
		if v, ok := rec.Metadata["file_name"].(string); ok {
			fileName = v
		}
		a := byDoc[docID]
		if a == nil {
			a = &docAgg{DocID: docID, FileName: fileName}
			byDoc[docID] = a
			sectionsByDoc[docID] = map[int]struct{}{}
		}
		a.RecordCount++
		if si, ok := rec.Metadata["section_index"].(int); ok {
			sectionsByDoc[docID][si] = struct{}{}
		} else if sf, ok := rec.Metadata["section_index"].(float64); ok {
			sectionsByDoc[docID][int(sf)] = struct{}{}
		}
	}

	docs := make([]docAgg, 0, len(byDoc))
	for _, a := range byDoc {
		a.SectionsCount = len(sectionsByDoc[a.DocID])
		docs = append(docs, *a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "collection": collection, "docs": docs})
}

func handleDeleteDoc(w http.ResponseWriter, r *http.Request, deps *handleIndexDeps) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	collection := strings.TrimSpace(r.URL.Query().Get("collection"))
	if collection == "" {
		collection = deps.defaultCollection
	}
	docID := strings.TrimSpace(r.URL.Query().Get("doc_id"))
	if docID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "doc_id required"})
		return
	}

	qh := qdrantForCollection(deps, collection)
	records, err := qh.List(ctx, &knowledge.ListOptions{Namespace: collection, Limit: 10000})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ids := make([]string, 0)
	for _, rec := range records.Records {
		if v, ok := rec.Metadata["doc_id"].(string); ok && v == docID {
			ids = append(ids, rec.ID)
		}
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted_count": 0, "doc_id": docID})
		return
	}
	if err := qh.Delete(ctx, ids, &knowledge.DeleteOptions{Namespace: collection}); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted_count": len(ids), "doc_id": docID})
}

func handleNamespaces(w http.ResponseWriter, r *http.Request, deps *handleIndexDeps) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	qh := &knowledge.QdrantHandler{BaseURL: deps.qdrantBaseURL, HTTPClient: &http.Client{Timeout: 15 * time.Second}}
	namespaces, err := qh.ListNamespaces(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "namespaces": namespaces})
}

func handleDeleteNamespace(w http.ResponseWriter, r *http.Request, deps *handleIndexDeps) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "name required"})
		return
	}
	qh := &knowledge.QdrantHandler{BaseURL: deps.qdrantBaseURL, HTTPClient: &http.Client{Timeout: 15 * time.Second}}
	if err := qh.DeleteNamespace(ctx, name); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted_namespace": name})
}

func qdrantForCollection(deps *handleIndexDeps, collection string) *knowledge.QdrantHandler {
	return &knowledge.QdrantHandler{
		BaseURL:    deps.qdrantBaseURL,
		Collection: collection,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Embedder:   deps.embedder,
	}
}

func deriveDocQuery(ctx context.Context, qh *knowledge.QdrantHandler, docID string, maxScan int) (string, error) {
	records, err := qh.List(ctx, &knowledge.ListOptions{Namespace: qh.Collection, Limit: maxScan})
	if err != nil {
		return "", err
	}
	for _, rec := range records.Records {
		if v, ok := rec.Metadata["doc_id"].(string); ok && v == docID && strings.TrimSpace(rec.Content) != "" {
			return rec.Content, nil
		}
	}
	return "", errors.New("doc_id has no records in this collection (or not scanned)")
}

func countDocRecords(ctx context.Context, qh *knowledge.QdrantHandler, docID string, maxScan int) (int, error) {
	records, err := qh.List(ctx, &knowledge.ListOptions{Namespace: qh.Collection, Limit: maxScan})
	if err != nil {
		return 0, err
	}
	cnt := 0
	for _, rec := range records.Records {
		if v, ok := rec.Metadata["doc_id"].(string); ok && v == docID {
			cnt++
		}
	}
	return cnt, nil
}

func parseUpload(ctx context.Context, file multipart.File, header *multipart.FileHeader, parseMaxChars int) (*knowledgeParser.ParseResult, string, error) {
	if header == nil {
		return nil, "", errors.New("missing file header")
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, "", err
	}
	if len(content) == 0 {
		return nil, "", errors.New("empty uploaded file")
	}

	opts := &knowledgeParser.ParseOptions{
		MaxTextLength:      parseMaxChars,
		PreserveLineBreaks: true,
	}
	// allow overriding max text length by env in build stage (optional)
	// caller sets parseMaxChars separately, but parser doesn't expose it here.
	// We'll set a safe default here and clamp later.

	req := &knowledgeParser.ParseRequest{
		FileName:    header.Filename,
		ContentType: header.Header.Get("Content-Type"),
		Content:     content,
		Metadata: map[string]any{
			"uploaded_filename": header.Filename,
			"uploaded_size":     len(content),
		},
	}

	res, err := knowledgeParser.ParseAuto(ctx, req, opts)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(res.Text) == "" {
		return nil, "", errors.New("parsed text is empty")
	}
	// Clamp big content to keep payload size manageable.
	res.Text = clip(res.Text, parseMaxChars)
	for i := range res.Sections {
		res.Sections[i].Text = clip(res.Sections[i].Text, parseMaxChars)
	}
	sum := sha256.Sum256(append(content, []byte(header.Filename)...))
	// Use a short stable prefix as doc_id.
	docID := hex.EncodeToString(sum[:])[:16]
	return res, docID, nil
}

func recordsFromParseResult(
	ctx context.Context,
	parsed *knowledgeParser.ParseResult,
	embedder knowledge.Embedder,
	chunker knowledge.Chunker,
	docID string,
) ([]knowledge.Record, error) {
	if parsed == nil {
		return nil, errors.New("parsed is nil")
	}
	if embedder == nil {
		return nil, errors.New("embedder is nil")
	}
	docID = strings.TrimSpace(docID)
	if docID == "" {
		return nil, errors.New("docID is empty")
	}

	type inputItem struct {
		Title string
		Text  string
		Index int
	}

	items := make([]inputItem, 0, 1)

	// 1) Prefer LLM chunking when enabled.
	if chunker != nil {
		chunks, err := chunker.Chunk(ctx, parsed.Text, &knowledge.ChunkOptions{
			DocumentTitle: parsed.FileName,
		})
		if err != nil {
			return nil, err
		}
		for i := range chunks {
			t := strings.TrimSpace(chunks[i].Text)
			if t == "" {
				continue
			}
			items = append(items, inputItem{
				Title: strings.TrimSpace(chunks[i].Title),
				Text:  t,
				Index: chunks[i].Index,
			})
		}
	}

	// 2) Fallback to parser sections.
	if len(items) == 0 {
		sections := parsed.Sections
		if len(sections) == 0 {
			sections = []knowledgeParser.Section{
				{Type: knowledgeParser.SectionTypeDocument, Index: 0, Title: parsed.FileName, Text: parsed.Text, Metadata: parsed.Metadata},
			}
		}
		for _, s := range sections {
			t := strings.TrimSpace(s.Text)
			if t == "" {
				continue
			}
			items = append(items, inputItem{
				Title: strings.TrimSpace(s.Title),
				Text:  t,
				Index: s.Index,
			})
		}
	}

	if len(items) == 0 {
		return nil, errors.New("no text to embed")
	}

	embedTexts := make([]string, 0, len(items))
	indexes := make([]int, 0, len(items))
	titles := make([]string, 0, len(items))
	for _, it := range items {
		embedTexts = append(embedTexts, it.Text)
		indexes = append(indexes, it.Index)
		titles = append(titles, it.Title)
	}

	vecs, err := embedder.Embed(ctx, embedTexts)
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, errors.New("embedding returned empty")
	}

	records := make([]knowledge.Record, 0, len(vecs))
	now := time.Now().UTC()
	for i := range vecs {
		v := vecs[i]
		if len(v) == 0 {
			continue
		}
		f32 := make([]float32, len(v))
		for j := range v {
			f32[j] = float32(v[j])
		}

		title := clip(parsed.FileName, 120)
		if strings.TrimSpace(titles[i]) != "" {
			title = clip(titles[i], 120)
		}

		records = append(records, knowledge.Record{
			ID:      uuid.NewString(),
			Source:  "upload",
			Title:   title,
			Content: clip(embedTexts[i], defaultSectionPreviewChars),
			Vector:  f32,
			Tags:    []string{"upload"},
			Metadata: map[string]any{
				"doc_id":        docID,
				"file_name":     parsed.FileName,
				"section_index": indexes[i], // chunk_index is stored here too
			},
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	return records, nil
}

func uploadPageHTML(w io.Writer, embedInfo string) error {
	_, _ = io.WriteString(w, `<!doctype html>
<html>
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width,initial-scale=1"/>
  <title>Knowledge/Qdrant Demo</title>
  <style>
    body{font-family:system-ui,Helvetica,Arial,sans-serif;margin:20px;}
    textarea,input,button{font-size:14px}
    textarea{width:100%;height:120px}
    .row{margin:12px 0}
    pre{background:#0b1020;color:#e7eefc;padding:12px;border-radius:8px;overflow:auto}
    .muted{color:#666;font-size:12px}
    .grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}
    .card{border:1px solid #e5e7eb;border-radius:10px;padding:12px}
    table{width:100%;border-collapse:collapse}
    th,td{padding:8px;border-bottom:1px solid #eee;font-size:13px;vertical-align:top}
    th{color:#444;text-align:left}
    button.small{font-size:12px;padding:6px 10px}
  </style>
</head>
<body>
  <h2>Knowledge/Qdrant Demo（上传 / 召回分离）</h2>
  <div class="muted">Embed: `+htmlEscape(embedInfo)+`</div>

  <div class="row">
    <input id="collection" placeholder="collection/namespace（不填用默认）" style="width:520px"/>
  </div>

  <div class="grid">
    <div class="card">
      <h3>1) 上传并入库</h3>
      <div class="row"><input type="file" id="file"/></div>
      <div class="row">doc_id（每次上传自动生成，供后续测试/删除）</div>
      <div class="row"><input id="doc_id" placeholder="doc_id will appear here" style="width:520px" /></div>
      <div class="row">
        <button onclick="uploadDoc()">上传并入库</button>
        <button class="small" onclick="refreshDocs()">刷新文档列表</button>
        <button class="small" onclick="refreshNamespaces()">刷新 namespace</button>
        <button class="small" onclick="deleteDoc()">删除该文档</button>
      </div>
      <div class="row"><pre id="uploadOut">等待上传...</pre></div>
    </div>

    <div class="card">
      <h3>2) 测试召回</h3>
      <div class="row">Query（不填：如果 doc_id 有值，会从该 doc 的一条内容推导 query）</div>
      <div class="row"><textarea id="query" placeholder="输入要召回的文本，例如：介绍一下陈挺"></textarea></div>
      <div class="row">
        <span>topK</span>
        <input id="top_k" type="number" value="3" style="width:100px"/>
        <span style="margin-left:12px">doc_id（可选，仅用于计算 recall/precision）</span>
      </div>
      <div class="row"><input id="test_doc_id" placeholder="填 doc_id 或留空" style="width:520px" /></div>
      <div class="row">
        <button onclick="testRecall()">开始召回</button>
      </div>
      <div class="row"><pre id="testOut">等待召回...</pre></div>
    </div>
  </div>

  <div class="grid">
    <div class="card">
      <h3>3) 自己已上传的文档（demo 以 doc_id 归类）</h3>
      <div class="row"><pre id="docsOut">点击“刷新文档列表”</pre></div>
    </div>
    <div class="card">
      <h3>4) Qdrant namespaces</h3>
      <div class="row"><pre id="namespacesOut">点击“刷新 namespace”</pre></div>
      <div class="row muted">注意：删 namespace 会连同里面的 points 一并删除。</div>
      <div class="row">
        <input id="namespace_name" placeholder="输入 namespace 名称，例如：lingvoice_knowledge_demo" style="width:520px"/>
        <button class="small" onclick="deleteNamespace()">删除 namespace</button>
      </div>
    </div>
  </div>

<script>
function getCollection(){
  return (document.getElementById('collection').value || '').trim();
}
function getDocID(){
  return (document.getElementById('doc_id').value || '').trim();
}
function setText(id, s){
  document.getElementById(id).textContent = s;
}

async function uploadDoc(){
  const out = document.getElementById('uploadOut');
  const fileEl = document.getElementById('file');
  if(!fileEl.files || fileEl.files.length===0){
    setText('uploadOut','请选择一个文件');
    return;
  }
  out.textContent = '上传中/解析中/写入Qdrant...';
  const fd = new FormData();
  fd.append('file', fileEl.files[0]);
  fd.append('collection', getCollection());
  const resp = await fetch('/api/upload', {method:'POST', body: fd});
  const data = await resp.json().catch(()=>({error:'non-json response'}));
  out.textContent = JSON.stringify(data, null, 2);
  if(data && data.doc_id){
    document.getElementById('doc_id').value = data.doc_id;
    document.getElementById('test_doc_id').value = data.doc_id;
  }
  await refreshDocs();
}

async function testRecall(){
  const out = document.getElementById('testOut');
  const query = document.getElementById('query').value || '';
  const docID = document.getElementById('test_doc_id').value || getDocID();
  const topK = parseInt(document.getElementById('top_k').value || '3', 10);
  out.textContent = '召回中...';
  const resp = await fetch('/api/test', {
    method:'POST',
    headers:{'Content-Type':'application/json'},
    body: JSON.stringify({
      collection: getCollection(),
      query: query,
      doc_id: docID,
      top_k: topK,
      min_score: 0
    })
  });
  const data = await resp.json().catch(()=>({error:'non-json response'}));
  out.textContent = JSON.stringify(data, null, 2);
}

async function refreshDocs(){
  const c = getCollection();
  const out = document.getElementById('docsOut');
  out.textContent = '加载中...';
  const url = '/api/docs?collection=' + encodeURIComponent(c);
  const resp = await fetch(url);
  const data = await resp.json().catch(()=>({error:'non-json response'}));
  if(data && data.docs){
    let lines = [];
    lines.push('collection=' + data.collection);
    lines.push('docs(' + data.docs.length + '):');
    data.docs.forEach(d=>{
      lines.push('- doc_id=' + d.doc_id + ', file=' + d.file_name + ', records=' + d.record_count + ', sections=' + d.sections_count);
    });
    out.textContent = lines.join('\\n');
  }else{
    out.textContent = JSON.stringify(data, null, 2);
  }
}

async function refreshNamespaces(){
  const out = document.getElementById('namespacesOut');
  out.textContent = '加载中...';
  const resp = await fetch('/api/namespaces');
  const data = await resp.json().catch(()=>({error:'non-json response'}));
  if(data && data.namespaces){
    out.textContent = JSON.stringify(data.namespaces, null, 2);
  }else{
    out.textContent = JSON.stringify(data, null, 2);
  }
}

async function deleteDoc(){
  const out = document.getElementById('uploadOut');
  const docID = getDocID();
  const c = getCollection();
  if(!docID){
    out.textContent = 'doc_id 为空，先上传或填入 doc_id';
    return;
  }
  out.textContent = '删除中...';
  const resp = await fetch('/api/doc?collection=' + encodeURIComponent(c) + '&doc_id=' + encodeURIComponent(docID), {method:'DELETE'});
  const data = await resp.json().catch(()=>({error:'non-json response'}));
  out.textContent = JSON.stringify(data, null, 2);
  await refreshDocs();
}

async function deleteNamespace(){
  const out = document.getElementById('namespacesOut');
  const name = (document.getElementById('namespace_name').value || '').trim();
  if(!name){
    out.textContent = 'namespace_name 为空';
    return;
  }
  out.textContent = '删除 namespace 中...';
  const resp = await fetch('/api/namespace?name=' + encodeURIComponent(name), {method:'DELETE'});
  const data = await resp.json().catch(()=>({error:'non-json response'}));
  out.textContent = JSON.stringify(data, null, 2);
  await refreshNamespaces();
}
</script>
</body>
</html>`)
	return nil
}

func buildEmbedderFromEnv() (knowledge.Embedder, string, error) {
	// If Nvidia embedding env configured, use the real embedder. Otherwise fallback to a deterministic fake embedder.
	baseURL := strings.TrimSpace(base.GetEnv("EMBED_BASE_URL"))
	apiKey := strings.TrimSpace(base.GetEnv("EMBED_API_KEY"))
	model := strings.TrimSpace(base.GetEnv("EMBED_MODEL"))
	if baseURL != "" && apiKey != "" && model != "" {
		c := &knowledge.NvidiaEmbedClient{
			BaseURL:        baseURL,
			APIKey:         apiKey,
			Model:          model,
			InputKey:       strings.TrimSpace(os.Getenv("EMBED_INPUT_KEY")),
			EmbeddingsPath: strings.TrimSpace(os.Getenv("EMBED_EMBEDDINGS_PATH")),
			HTTPClient:     &http.Client{Timeout: 60 * time.Second},
		}
		return c, "NvidiaEmbedClient (real HTTP)", nil
	}

	dim := getenvInt("FAKE_EMBED_DIM", 64)
	if dim <= 0 {
		dim = 64
	}
	return newFakeEmbedder(dim), fmt.Sprintf("FakeEmbedder dim=%d (env not set)", dim), nil
}

func buildLLMChunkerFromEnv() (knowledge.Chunker, string, error) {
	// Use base.GetEnv-style env var names as requested.
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

	chunker := &knowledge.LLMChunker{
		LLM:   h,
		Model: model,
	}
	return chunker, fmt.Sprintf("LLMChunker enabled: provider=%s model=%s", provider, model), nil
}

func newFakeEmbedder(dim int) knowledge.Embedder {
	return &fakeEmbedderImpl{dim: dim}
}

type fakeEmbedderImpl struct{ dim int }

func (f *fakeEmbedderImpl) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	_ = ctx
	out := make([][]float64, 0, len(inputs))
	for _, in := range inputs {
		s := in
		vec := make([]float64, f.dim)
		// Simple deterministic hash-based vector (NOT semantic). For pipeline verification.
		seed := fnv1a64(s)
		for i := 0; i < f.dim; i++ {
			seed = seed*6364136223846793005 + 1
			// Map to [0,1)
			vec[i] = float64(seed%1000000) / 1000000.0
		}
		out = append(out, vec)
	}
	return out, nil
}

func fnv1a64(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	var h uint64 = offset64
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func clip(s string, maxChars int) string {
	s = strings.TrimSpace(s)
	if maxChars <= 0 {
		return s
	}
	if len(s) <= maxChars {
		return s
	}
	return s[:maxChars] + "…"
}

func htmlEscape(s string) string {
	replacer := strings.NewReplacer(
		`&`, "&amp;",
		`<`, "&lt;",
		`>`, "&gt;",
		`"`, "&quot;",
		`'`, "&#39;",
	)
	return replacer.Replace(s)
}

func getenvOr(def string, key string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func getenvInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}

func getenvInt64(key string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	i, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return i
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Local testing: allow everything.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
