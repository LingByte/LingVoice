package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/internal/httputil"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// HandlerConfig configures an A2A HTTP handler.
type HandlerConfig struct {
	Card          AgentCard
	Auth          ServerAuth
	Handler       func(ctx context.Context, req *MessageRequest) (*schema.Message, error)
	StreamHandler func(ctx context.Context, req *MessageRequest) (*schema.StreamReader[*schema.Message], error)
	// PushHTTPClient is used for push notification webhook delivery.
	PushHTTPClient *http.Client
	// PushOnlyTerminal limits webhooks to the first terminal transition (default true).
	PushOnlyTerminal *bool
	// PushRetry configures webhook retry attempts and dead-letter retention.
	PushRetry PushRetryConfig
}

// Handler serves A2A requests for a local agent.
type Handler struct {
	card          AgentCard
	auth          ServerAuth
	handler       func(ctx context.Context, req *MessageRequest) (*schema.Message, error)
	streamHandler func(ctx context.Context, req *MessageRequest) (*schema.StreamReader[*schema.Message], error)
	tasks         *taskManager
	push          *pushDelivery
}

// NewHandler builds an A2A handler.
func NewHandler(cfg HandlerConfig) *Handler {
	card := cfg.Card
	if card.ProtocolVersion == "" {
		card.ProtocolVersion = ProtocolVersion
	}
	if card.PreferredTransport == "" {
		card.PreferredTransport = TransportJSONRPC
	}
	if card.URL == "" && card.Name != "" {
		card.URL = "/"
	}
	if cfg.StreamHandler != nil {
		card.Capabilities.Streaming = true
	}
	if len(card.AuthSchemes) == 0 {
		card.AuthSchemes = DefaultAuthSchemes(cfg.Auth)
	}
	if len(card.DefaultInputModes) == 0 {
		card.DefaultInputModes = []string{"text/plain"}
	}
	if len(card.DefaultOutputModes) == 0 {
		card.DefaultOutputModes = []string{"text/plain"}
	}
	if card.PreferredTransport == TransportJSONRPC && len(card.AdditionalInterfaces) == 0 && card.URL != "" {
		card.AdditionalInterfaces = []AgentInterface{{URL: card.URL, Transport: TransportJSONRPC}}
	}
	onlyTerminal := true
	if cfg.PushOnlyTerminal != nil {
		onlyTerminal = *cfg.PushOnlyTerminal
	}
	h := &Handler{
		card:          card,
		auth:          cfg.Auth,
		handler:       cfg.Handler,
		streamHandler: cfg.StreamHandler,
		tasks:         newTaskManager(),
		push:          newPushDelivery(cfg.PushHTTPClient, onlyTerminal, cfg.PushRetry),
	}
	h.tasks.onUpdate = h.onTaskUpdate
	return h
}

// HTTPHandler returns an http.Handler with auth middleware.
func (h *Handler) HTTPHandler() http.Handler {
	return AuthMiddleware(h.auth, h)
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.handler == nil {
		http.Error(w, "nil handler", http.StatusInternalServerError)
		return
	}
	path := r.URL.Path
	switch {
	case isAgentCardPath(path):
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h.card)
		return
	case r.Method == http.MethodPost && (path == "/" || path == h.rpcPath()):
		h.handleJSONRPC(w, r)
		return
	case r.Method == http.MethodGet && path == "/v1/push/deadLetters":
		h.handleRESTPushDeadLetterList(w, r)
		return
	case r.Method == http.MethodPost && path == "/v1/push/deadLetters:redrive":
		h.handleRESTPushDeadLetterRedrive(w, r)
		return
	case r.Method == http.MethodPost && (path == "/v1/message:send" || path == "/message:send"):
		h.handleRESTMessageSend(w, r)
		return
	case r.Method == http.MethodPost && (path == "/v1/message:stream" || path == "/message:stream"):
		h.handleRESTMessageStream(w, r)
		return
	case r.Method == http.MethodGet && path == "/v1/tasks":
		h.handleRESTTasksList(w, r)
		return
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/pushNotificationConfig"):
		if id := taskSubResourceID(path, "/pushNotificationConfig"); id != "" {
			h.handleRESTPushNotificationSet(w, r, id)
			return
		}
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/pushNotificationConfig"):
		if id := taskSubResourceID(path, "/pushNotificationConfig"); id != "" {
			h.handleRESTPushNotificationGet(w, r, id)
			return
		}
	case r.Method == http.MethodDelete && strings.HasSuffix(path, "/pushNotificationConfig"):
		if id := taskSubResourceID(path, "/pushNotificationConfig"); id != "" {
			h.handleRESTPushNotificationDelete(w, r, id)
			return
		}
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/tasks/"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/tasks/"), "/")
		if id != "" && !strings.Contains(id, "/") {
			h.handleRESTTasksGet(w, r, id)
			return
		}
	case r.Method == http.MethodPost && strings.HasSuffix(path, ":cancel"):
		if id := taskIDFromREST(path, ":cancel"); id != "" {
			h.handleRESTTasksCancel(w, r, id)
			return
		}
	case r.Method == http.MethodPost && strings.HasSuffix(path, ":subscribe"):
		if id := taskIDFromREST(path, ":subscribe"); id != "" {
			h.handleRESTTasksResubscribe(w, r, id)
			return
		}
	case r.Method == http.MethodPost && (path == "/v1/messages" || path == "/messages"):
		h.handleLegacyMessages(w, r)
		return
	case r.Method == http.MethodPost && (path == "/v1/messages:stream" || path == "/messages/stream"):
		h.handleLegacyStream(w, r)
		return
	}
	http.NotFound(w, r)
}

func (h *Handler) rpcPath() string {
	u := strings.TrimSuffix(h.card.URL, "/")
	if u == "" || u == "/" {
		return "/"
	}
	if i := strings.Index(u, "://"); i >= 0 {
		if j := strings.Index(u[i+3:], "/"); j >= 0 {
			return u[i+3+j:]
		}
		return "/"
	}
	return u
}

func isAgentCardPath(path string) bool {
	switch path {
	case "/.well-known/agent-card.json", "/.well-known/agent.json", "/agent.json", "/agent-card.json":
		return true
	default:
		return false
	}
}

func (h *Handler) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONRPC(w, http.StatusBadRequest, rpcErr(nil, CodeParseError, err.Error()))
		return
	}
	if req.JSONRPC != JSONRPCVersion {
		writeJSONRPC(w, http.StatusBadRequest, rpcErr(req.ID, CodeInvalidRequest, "invalid jsonrpc version"))
		return
	}
	method := normalizeMethod(req.Method)
	switch method {
	case MethodMessageSend:
		h.rpcMessageSend(w, r, req)
	case MethodMessageStream:
		h.rpcMessageStream(w, r, req)
	case MethodTasksGet:
		h.rpcTasksGet(w, req)
	case MethodTasksCancel:
		h.rpcTasksCancel(w, req)
	case MethodTasksResubscribe:
		h.rpcTasksResubscribe(w, r, req)
	case MethodTasksList:
		h.rpcTasksList(w, req)
	case MethodPushNotificationSet:
		h.rpcPushNotificationSet(w, req)
	case MethodPushNotificationGet:
		h.rpcPushNotificationGet(w, req)
	case MethodPushNotificationList:
		h.rpcPushNotificationList(w, req)
	case MethodPushNotificationDelete:
		h.rpcPushNotificationDelete(w, req)
	case MethodPushDeadLetterList:
		h.rpcPushDeadLetterList(w, req)
	case MethodPushDeadLetterRedrive:
		h.rpcPushDeadLetterRedrive(w, req)
	default:
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeMethodNotFound, "method not found"))
	}
}

func (h *Handler) rpcMessageSend(w http.ResponseWriter, r *http.Request, req JSONRPCRequest) {
	params, err := decodeParams[MessageSendParams](req.Params)
	if err != nil || len(params.Message.Parts) == 0 {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeInvalidParams, "invalid message/send params"))
		return
	}
	result, rpcErrResp := h.executeSend(r.Context(), params)
	if rpcErrResp != nil {
		writeJSONRPC(w, http.StatusOK, *rpcErrResp)
		return
	}
	writeJSONRPC(w, http.StatusOK, rpcOK(req.ID, result))
}

func (h *Handler) executeSend(ctx context.Context, params MessageSendParams) (any, *JSONRPCResponse) {
	userMsg := prepareUserMessage(params.Message)
	taskID := taskIDFrom(params)
	contextID := contextIDFrom(params)
	blocking := blockingFromConfig(params.Configuration)

	if existing, ok := h.tasks.get(taskID); ok && existing.Status.State.Terminal() {
		resp := rpcErr(nil, CodeTaskNotFound, "task already terminal")
		return nil, &resp
	}

	req := messageRequestFromSend(params, taskID, contextID, blocking)
	ctx = ContextWithTaskID(ctx, taskID)

	if !blocking {
		working := taskFromAgentMessage(taskID, contextID, userMsg, nil, nil)
		working.Status = TaskStatus{State: TaskWorking, Timestamp: nowRFC3339()}
		h.tasks.put(working)
		go func() {
			msg, err := h.handler(ctx, req)
			done := taskFromAgentMessage(taskID, contextID, userMsg, msg, err)
			h.tasks.put(done)
		}()
		return working, nil
	}

	msg, err := h.handler(ctx, req)
	result := sendResultFromHandler(h.useTaskResult(), taskID, contextID, userMsg, msg)
	if err != nil {
		if h.useTaskResult() {
			task := taskFromAgentMessage(taskID, contextID, userMsg, msg, err)
			h.tasks.put(task)
			return task, nil
		}
		resp := rpcErr(nil, CodeInternalError, err.Error())
		return nil, &resp
	}
	if task, ok := result.(*Task); ok {
		h.tasks.put(task)
	} else if t, ok := result.(Task); ok {
		h.tasks.put(&t)
	}
	return result, nil
}

func (h *Handler) useTaskResult() bool {
	return h.card.Capabilities.Tasks
}

func (h *Handler) rpcMessageStream(w http.ResponseWriter, r *http.Request, req JSONRPCRequest) {
	if h.streamHandler == nil {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeUnsupportedOperation, "streaming not supported"))
		return
	}
	if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		writeJSONRPC(w, http.StatusBadRequest, rpcErr(req.ID, CodeInvalidRequest, "Accept: text/event-stream required"))
		return
	}
	params, err := decodeParams[MessageSendParams](req.Params)
	if err != nil || len(params.Message.Parts) == 0 {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeInvalidParams, "invalid message/stream params"))
		return
	}
	h.streamSend(w, r, req.ID, params)
}

func (h *Handler) streamSend(w http.ResponseWriter, r *http.Request, id json.RawMessage, params MessageSendParams) {
	userMsg := prepareUserMessage(params.Message)
	taskID := taskIDFrom(params)
	contextID := contextIDFrom(params)
	req := messageRequestFromSend(params, taskID, contextID, false)
	ctx := ContextWithTaskID(r.Context(), taskID)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONRPC(w, http.StatusInternalServerError, rpcErr(id, CodeInternalError, "streaming unsupported"))
		return
	}

	working := taskFromAgentMessage(taskID, contextID, userMsg, nil, nil)
	working.Status = TaskStatus{State: TaskWorking, Timestamp: nowRFC3339()}
	h.tasks.put(working)
	writeSSEJSONRPC(w, flusher, rpcOK(id, TaskStatusUpdateEvent{
		Kind: "status-update", TaskID: taskID, ContextID: contextID,
		Status: working.Status, Final: false,
	}))

	sr, err := h.streamHandler(ctx, req)
	if err != nil {
		failed := taskFromAgentMessage(taskID, contextID, userMsg, nil, err)
		h.tasks.put(failed)
		writeSSEJSONRPC(w, flusher, rpcOK(id, TaskStatusUpdateEvent{
			Kind: "status-update", TaskID: taskID, ContextID: contextID,
			Status: failed.Status, Final: true,
		}))
		return
	}
	defer sr.Close()

	artifactID := newID()
	var fullText strings.Builder
	var chunkCount int
	for {
		msg, recvErr := sr.Recv()
		if recvErr != nil {
			break
		}
		if msg == nil {
			continue
		}
		chunk := msg.PlainText()
		if chunk == "" {
			continue
		}
		fullText.WriteString(chunk)
		chunkCount++
		writeSSEJSONRPC(w, flusher, rpcOK(id, TaskArtifactUpdateEvent{
			Kind: "artifact-update", TaskID: taskID, ContextID: contextID,
			Artifact: Artifact{ArtifactID: artifactID, Name: "response", Parts: partsFromText(chunk)},
			Append: chunkCount > 1, LastChunk: false,
		}))
	}
	finalMsg := schema.AssistantMessage(fullText.String(), nil)
	done := taskFromAgentMessage(taskID, contextID, userMsg, finalMsg, nil)
	h.tasks.put(done)
	writeSSEJSONRPC(w, flusher, rpcOK(id, TaskStatusUpdateEvent{
		Kind: "status-update", TaskID: taskID, ContextID: contextID,
		Status: done.Status, Final: true,
	}))
}

func (h *Handler) rpcTasksGet(w http.ResponseWriter, req JSONRPCRequest) {
	params, err := decodeParams[TaskQueryParams](req.Params)
	if err != nil || params.ID == "" {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeInvalidParams, "invalid tasks/get params"))
		return
	}
	task, ok := h.tasks.get(params.ID)
	if !ok {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeTaskNotFound, "task not found"))
		return
	}
	writeJSONRPC(w, http.StatusOK, rpcOK(req.ID, task))
}

func (h *Handler) rpcTasksCancel(w http.ResponseWriter, req JSONRPCRequest) {
	params, err := decodeParams[TaskIDParams](req.Params)
	if err != nil || params.ID == "" {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeInvalidParams, "invalid tasks/cancel params"))
		return
	}
	task, err := h.tasks.cancel(params.ID)
	if err != nil {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeTaskNotCancelable, err.Error()))
		return
	}
	writeJSONRPC(w, http.StatusOK, rpcOK(req.ID, task))
}

func (h *Handler) rpcTasksResubscribe(w http.ResponseWriter, r *http.Request, req JSONRPCRequest) {
	if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		writeJSONRPC(w, http.StatusBadRequest, rpcErr(req.ID, CodeInvalidRequest, "Accept: text/event-stream required"))
		return
	}
	params, err := decodeParams[TaskIDParams](req.Params)
	if err != nil || params.ID == "" {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeInvalidParams, "invalid tasks/resubscribe params"))
		return
	}
	task, ok := h.tasks.get(params.ID)
	if !ok {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeTaskNotFound, "task not found"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONRPC(w, http.StatusInternalServerError, rpcErr(req.ID, CodeInternalError, "streaming unsupported"))
		return
	}
	writeSSEJSONRPC(w, flusher, rpcOK(req.ID, TaskStatusUpdateEvent{
		Kind: "status-update", TaskID: task.ID, ContextID: task.ContextID,
		Status: task.Status, Final: task.Status.State.Terminal(),
	}))
}

func (h *Handler) rpcTasksList(w http.ResponseWriter, req JSONRPCRequest) {
	params, err := decodeParams[TaskListParams](req.Params)
	if err != nil {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeInvalidParams, "invalid tasks/list params"))
		return
	}
	tasks := h.tasks.list(params.ContextID, params.Limit)
	writeJSONRPC(w, http.StatusOK, rpcOK(req.ID, TaskListResult{Tasks: tasks}))
}

func (h *Handler) rpcPushNotificationSet(w http.ResponseWriter, req JSONRPCRequest) {
	params, err := decodeParams[PushNotificationSetParams](req.Params)
	if err != nil || params.TaskID == "" || params.Config.URL == "" {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeInvalidParams, "invalid pushNotificationConfig/set params"))
		return
	}
	cfg := params.Config
	cfg.TaskID = params.TaskID
	if err := h.tasks.setPush(cfg); err != nil {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeTaskNotFound, err.Error()))
		return
	}
	writeJSONRPC(w, http.StatusOK, rpcOK(req.ID, cfg))
}

func (h *Handler) rpcPushNotificationGet(w http.ResponseWriter, req JSONRPCRequest) {
	params, err := decodeParams[PushNotificationGetParams](req.Params)
	if err != nil || params.TaskID == "" {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeInvalidParams, "invalid pushNotificationConfig/get params"))
		return
	}
	cfg, ok := h.tasks.getPush(params.TaskID)
	if !ok {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeTaskNotFound, "push config not found"))
		return
	}
	writeJSONRPC(w, http.StatusOK, rpcOK(req.ID, cfg))
}

func (h *Handler) rpcPushNotificationList(w http.ResponseWriter, req JSONRPCRequest) {
	params, err := decodeParams[PushNotificationListParams](req.Params)
	if err != nil {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeInvalidParams, "invalid pushNotificationConfig/list params"))
		return
	}
	writeJSONRPC(w, http.StatusOK, rpcOK(req.ID, PushNotificationListResult{Configs: h.tasks.listPush(params.TaskID)}))
}

func (h *Handler) rpcPushNotificationDelete(w http.ResponseWriter, req JSONRPCRequest) {
	params, err := decodeParams[PushNotificationDeleteParams](req.Params)
	if err != nil || params.TaskID == "" {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeInvalidParams, "invalid pushNotificationConfig/delete params"))
		return
	}
	if err := h.tasks.deletePush(params.TaskID); err != nil {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeTaskNotFound, err.Error()))
		return
	}
	writeJSONRPC(w, http.StatusOK, rpcOK(req.ID, map[string]any{"deleted": true}))
}

func (h *Handler) rpcPushDeadLetterList(w http.ResponseWriter, req JSONRPCRequest) {
	writeJSONRPC(w, http.StatusOK, rpcOK(req.ID, PushDeadLetterListResult{Entries: h.ListPushDeadLetters()}))
}

func (h *Handler) handleRESTPushDeadLetterList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(PushDeadLetterListResult{Entries: h.ListPushDeadLetters()})
}

func (h *Handler) rpcPushDeadLetterRedrive(w http.ResponseWriter, req JSONRPCRequest) {
	params, err := decodeParams[PushDeadLetterRedriveParams](req.Params)
	if err != nil {
		writeJSONRPC(w, http.StatusOK, rpcErr(req.ID, CodeInvalidParams, "invalid deadLetter/redrive params"))
		return
	}
	writeJSONRPC(w, http.StatusOK, rpcOK(req.ID, h.RedrivePushDeadLetters(params.TaskID)))
}

func (h *Handler) handleRESTPushDeadLetterRedrive(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("taskId")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.RedrivePushDeadLetters(taskID))
}

func (h *Handler) handleRESTMessageSend(w http.ResponseWriter, r *http.Request) {
	var params MessageSendParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, rpcErrResp := h.executeSend(r.Context(), params)
	if rpcErrResp != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": rpcErrResp.Error})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch v := result.(type) {
	case *Task:
		_ = json.NewEncoder(w).Encode(map[string]any{"task": v})
	case Task:
		_ = json.NewEncoder(w).Encode(map[string]any{"task": v})
	case *A2AMessage:
		_ = json.NewEncoder(w).Encode(map[string]any{"message": v})
	case A2AMessage:
		_ = json.NewEncoder(w).Encode(map[string]any{"message": v})
	default:
		_ = json.NewEncoder(w).Encode(result)
	}
}

func (h *Handler) handleRESTMessageStream(w http.ResponseWriter, r *http.Request) {
	var params MessageSendParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.streamSend(w, r, json.RawMessage(`"stream"`), params)
}

func (h *Handler) handleRESTTasksGet(w http.ResponseWriter, r *http.Request, id string) {
	historyLen := 0
	if raw := r.URL.Query().Get("historyLength"); raw != "" {
		fmt.Sscanf(raw, "%d", &historyLen)
	}
	task, ok := h.tasks.get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	_ = historyLen
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(task)
}

func (h *Handler) handleRESTTasksCancel(w http.ResponseWriter, r *http.Request, id string) {
	task, err := h.tasks.cancel(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(task)
}

func (h *Handler) handleRESTTasksResubscribe(w http.ResponseWriter, r *http.Request, id string) {
	req := JSONRPCRequest{ID: json.RawMessage(`"sub"`)}
	params, _ := json.Marshal(TaskIDParams{ID: id})
	req.Params = params
	h.rpcTasksResubscribe(w, r, req)
}

func (h *Handler) handleRESTTasksList(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		fmt.Sscanf(raw, "%d", &limit)
	}
	tasks := h.tasks.list(r.URL.Query().Get("contextId"), limit)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(TaskListResult{Tasks: tasks})
}

func (h *Handler) handleRESTPushNotificationSet(w http.ResponseWriter, r *http.Request, taskID string) {
	var cfg PushNotificationConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil || cfg.URL == "" {
		http.Error(w, "invalid push config", http.StatusBadRequest)
		return
	}
	cfg.TaskID = taskID
	if err := h.tasks.setPush(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg)
}

func (h *Handler) handleRESTPushNotificationGet(w http.ResponseWriter, r *http.Request, taskID string) {
	cfg, ok := h.tasks.getPush(taskID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg)
}

func (h *Handler) handleRESTPushNotificationDelete(w http.ResponseWriter, r *http.Request, taskID string) {
	if err := h.tasks.deletePush(taskID); err != nil {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func taskIDFromREST(path, suffix string) string {
	path = strings.TrimSuffix(path, suffix)
	path = strings.TrimPrefix(path, "/v1/tasks/")
	if path == "" || strings.Contains(path, "/") {
		return ""
	}
	return path
}

func taskSubResourceID(path, suffix string) string {
	path = strings.TrimPrefix(path, "/v1/tasks/")
	if !strings.HasSuffix(path, suffix) {
		return ""
	}
	id := strings.TrimSuffix(path, suffix)
	id = strings.TrimSuffix(id, "/")
	if id == "" || strings.Contains(id, "/") {
		return ""
	}
	return id
}

func (h *Handler) handleLegacyMessages(w http.ResponseWriter, r *http.Request) {
	var req MessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if req.TaskID != "" {
		ctx = ContextWithTaskID(ctx, req.TaskID)
	}
	msg, err := h.handler(ctx, &req)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		if req.TaskID != "" {
			h.tasks.put(taskFromAgentMessage(req.TaskID, req.ContextID, A2AMessage{}, nil, err))
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(MessageResponse{Error: err.Error(), TaskID: req.TaskID, TaskStatus: "failed"})
		return
	}
	if req.TaskID != "" {
		h.tasks.put(taskFromAgentMessage(req.TaskID, req.ContextID, A2AMessage{}, msg, nil))
	}
	_ = json.NewEncoder(w).Encode(MessageResponse{Message: msg, TaskID: req.TaskID, TaskStatus: "completed"})
}

func (h *Handler) handleLegacyStream(w http.ResponseWriter, r *http.Request) {
	if h.streamHandler == nil {
		http.Error(w, "streaming not supported", http.StatusNotImplemented)
		return
	}
	var req MessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sr, err := h.streamHandler(r.Context(), &req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	defer sr.Close()
	for {
		msg, err := sr.Recv()
		if err != nil {
			writeLegacySSE(w, StreamEvent{Event: "done", TaskID: req.TaskID})
			flusher.Flush()
			return
		}
		writeLegacySSE(w, StreamEvent{Event: "message", Message: msg, TaskID: req.TaskID})
		flusher.Flush()
	}
}

func writeLegacySSE(w http.ResponseWriter, ev StreamEvent) {
	b, _ := json.Marshal(ev)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

// ClientConfig configures an A2A client.
type ClientConfig struct {
	HTTPClient *http.Client
	Auth       AuthConfig
	Timeout    time.Duration
}

// Client calls remote A2A agents.
type Client struct {
	HTTPClient *http.Client
	Auth       AuthConfig
}

// NewClient creates an A2A client.
func NewClient(cfg ...ClientConfig) *Client {
	c := Client{HTTPClient: &http.Client{Timeout: 60 * time.Second}}
	if len(cfg) > 0 {
		if cfg[0].HTTPClient != nil {
			c.HTTPClient = cfg[0].HTTPClient
		}
		c.Auth = cfg[0].Auth
		if cfg[0].Timeout > 0 && c.HTTPClient != nil {
			c.HTTPClient.Timeout = cfg[0].Timeout
		}
	}
	return &c
}

// FetchCard retrieves an agent card from a base URL.
func (c *Client) FetchCard(ctx context.Context, baseURL string) (*AgentCard, error) {
	if c == nil || c.HTTPClient == nil {
		return nil, fmt.Errorf("a2a: nil client")
	}
	for _, path := range []string{"/.well-known/agent-card.json", "/.well-known/agent.json", "/agent.json"} {
		card, err := c.fetchCardPath(ctx, baseURL, path)
		if err == nil {
			return card, nil
		}
	}
	return nil, fmt.Errorf("a2a: agent card not found")
}

func (c *Client) fetchCardPath(ctx context.Context, baseURL, path string) (*AgentCard, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trimSlash(baseURL)+path, nil)
	if err != nil {
		return nil, err
	}
	ApplyAuthHeaders(req, c.Auth)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("a2a: card status %d", resp.StatusCode)
	}
	var card AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		return nil, err
	}
	return &card, nil
}

func (c *Client) rpcEndpoint(baseURL string, card *AgentCard) string {
	if card != nil && card.URL != "" {
		if strings.HasPrefix(card.URL, "http") {
			return strings.TrimSuffix(card.URL, "/")
		}
		return trimSlash(baseURL) + strings.TrimSuffix(card.URL, "/")
	}
	return trimSlash(baseURL)
}

// SendMessage invokes message/send via JSON-RPC 2.0.
func (c *Client) SendMessage(ctx context.Context, baseURL string, params MessageSendParams) (any, error) {
	if c == nil || c.HTTPClient == nil {
		return nil, fmt.Errorf("a2a: nil client")
	}
	card, _ := c.FetchCard(ctx, baseURL)
	endpoint := c.rpcEndpoint(baseURL, card)
	body, err := json.Marshal(JSONRPCRequest{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  MethodMessageSend,
		Params:  mustMarshal(params),
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, stringsReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Version", ProtocolVersion)
	ApplyAuthHeaders(req, c.Auth)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("a2a: unauthorized")
	}
	var out JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, fmt.Errorf("a2a: %s", out.Error.Message)
	}
	return out.Result, nil
}

// SendMessages posts messages using JSON-RPC message/send (backward compatible helper).
func (c *Client) SendMessages(ctx context.Context, baseURL string, messages []*schema.Message) (*schema.Message, error) {
	text := lastSchemaUserText(messages)
	params := MessageSendParams{
		Message: A2AMessage{
			Kind: "message", Role: "user", MessageID: newID(),
			Parts: partsFromText(text),
		},
	}
	result, err := c.SendMessage(ctx, baseURL, params)
	if err != nil {
		return nil, err
	}
	if m, ok := result.(map[string]any); ok {
		return messageFromResultMap(m)
	}
	switch v := result.(type) {
	case map[string]any:
		return messageFromResultMap(v)
	default:
		b, _ := json.Marshal(result)
		var task Task
		if json.Unmarshal(b, &task) == nil && task.ID != "" {
			return messageFromTask(&task)
		}
		var msg A2AMessage
		if json.Unmarshal(b, &msg) == nil && len(msg.Parts) > 0 {
			return schema.AssistantMessage(textFromParts(msg.Parts), nil), nil
		}
	}
	return nil, fmt.Errorf("a2a: unexpected result type")
}

func messageFromTask(task *Task) (*schema.Message, error) {
	if task == nil {
		return nil, fmt.Errorf("a2a: nil task")
	}
	if len(task.Artifacts) > 0 {
		return schema.AssistantMessage(textFromParts(task.Artifacts[0].Parts), nil), nil
	}
	if task.Status.Message != nil {
		return schema.AssistantMessage(textFromParts(task.Status.Message.Parts), nil), nil
	}
	return schema.AssistantMessage("", nil), nil
}

func messageFromResultMap(m map[string]any) (*schema.Message, error) {
	if msgRaw, ok := m["message"]; ok {
		b, _ := json.Marshal(msgRaw)
		var msg A2AMessage
		if json.Unmarshal(b, &msg) == nil {
			return schema.AssistantMessage(textFromParts(msg.Parts), nil), nil
		}
	}
	if taskRaw, ok := m["task"]; ok {
		b, _ := json.Marshal(taskRaw)
		var task Task
		if json.Unmarshal(b, &task) == nil {
			return messageFromTask(&task)
		}
	}
	b, _ := json.Marshal(m)
	var task Task
	if json.Unmarshal(b, &task) == nil && task.ID != "" {
		return messageFromTask(&task)
	}
	var msg A2AMessage
	if json.Unmarshal(b, &msg) == nil {
		return schema.AssistantMessage(textFromParts(msg.Parts), nil), nil
	}
	return nil, fmt.Errorf("a2a: cannot parse result")
}

// StreamMessages opens an SSE stream via JSON-RPC message/stream.
func (c *Client) StreamMessages(ctx context.Context, baseURL string, messages []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
	if c == nil || c.HTTPClient == nil {
		return nil, fmt.Errorf("a2a: nil client")
	}
	text := lastSchemaUserText(messages)
	params := MessageSendParams{
		Message: A2AMessage{
			Kind: "message", Role: "user", MessageID: newID(),
			Parts: partsFromText(text),
		},
	}
	card, _ := c.FetchCard(ctx, baseURL)
	endpoint := c.rpcEndpoint(baseURL, card)

	outSR, outSW := schema.Pipe[*schema.Message](32)
	go func() {
		defer outSW.Close()
		headers := map[string]string{
			"Content-Type": "application/json",
			"Accept":       "text/event-stream",
			"A2A-Version":  ProtocolVersion,
		}
		if c.Auth.BearerToken != "" {
			headers["Authorization"] = "Bearer " + c.Auth.BearerToken
		}
		if c.Auth.APIKey != "" {
			header := c.Auth.APIKeyHeader
			if header == "" {
				header = "X-API-Key"
			}
			headers[header] = c.Auth.APIKey
		}
		err := httputil.PostSSE(ctx, c.HTTPClient, endpoint, headers, JSONRPCRequest{
			JSONRPC: JSONRPCVersion,
			ID:      json.RawMessage(`2`),
			Method:  MethodMessageStream,
			Params:  mustMarshal(params),
		}, func(data string) error {
			var rpc JSONRPCResponse
			if err := json.Unmarshal([]byte(data), &rpc); err != nil {
				return err
			}
			if rpc.Error != nil {
				return fmt.Errorf("a2a: stream error: %s", rpc.Error.Message)
			}
			b, _ := json.Marshal(rpc.Result)
			var art TaskArtifactUpdateEvent
			if json.Unmarshal(b, &art) == nil && art.Kind == "artifact-update" && len(art.Artifact.Parts) > 0 {
				outSW.Send(schema.AssistantMessage(textFromParts(art.Artifact.Parts), nil), nil)
				return nil
			}
			var status TaskStatusUpdateEvent
			if json.Unmarshal(b, &status) == nil && status.Final {
				return io.EOF
			}
			return nil
		})
		if err != nil && !errors.Is(err, io.EOF) {
			outSW.Send(nil, err)
		}
	}()
	return outSR, nil
}

// GetTask fetches task state via JSON-RPC tasks/get.
func (c *Client) GetTask(ctx context.Context, baseURL, taskID string) (*Task, error) {
	if c == nil || c.HTTPClient == nil {
		return nil, fmt.Errorf("a2a: nil client")
	}
	card, _ := c.FetchCard(ctx, baseURL)
	endpoint := c.rpcEndpoint(baseURL, card)
	body, _ := json.Marshal(JSONRPCRequest{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`3`),
		Method:  MethodTasksGet,
		Params:  mustMarshal(TaskQueryParams{ID: taskID}),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, stringsReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Version", ProtocolVersion)
	ApplyAuthHeaders(req, c.Auth)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, fmt.Errorf("a2a: %s", out.Error.Message)
	}
	b, _ := json.Marshal(out.Result)
	var task Task
	if err := json.Unmarshal(b, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func lastSchemaUserText(messages []*schema.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] != nil && messages[i].Role == schema.User {
			return messages[i].PlainText()
		}
	}
	return ""
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

func stringsReader(b []byte) io.Reader {
	return strings.NewReader(string(b))
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
