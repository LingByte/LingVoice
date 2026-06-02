package a2a

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// JSONRPCVersion is the JSON-RPC protocol version string.
const JSONRPCVersion = "2.0"

// Standard JSON-RPC error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// A2A-specific error codes.
const (
	CodeTaskNotFound         = -32001
	CodeTaskNotCancelable    = -32002
	CodeUnsupportedOperation = -32003
)

// JSONRPCRequest is a JSON-RPC 2.0 request object.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCError describes a JSON-RPC error.
type JSONRPCError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

// JSONRPCResponse is a JSON-RPC 2.0 response object.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

func normalizeMethod(method string) string {
	switch method {
	case MethodSendMessage:
		return MethodMessageSend
	case MethodSendStreamingMessage:
		return MethodMessageStream
	case MethodGetTask:
		return MethodTasksGet
	case MethodCancelTask:
		return MethodTasksCancel
	case MethodSubscribeToTask:
		return MethodTasksResubscribe
	case "ListTasks":
		return MethodTasksList
	case "RedrivePushDeadLetter", "RedrivePushDeadLetters":
		return MethodPushDeadLetterRedrive
	default:
		return method
	}
}

func rpcOK(id json.RawMessage, result any) JSONRPCResponse {
	return JSONRPCResponse{JSONRPC: JSONRPCVersion, ID: id, Result: result}
}

func rpcErr(id json.RawMessage, code int, msg string) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: msg},
	}
}

func decodeParams[T any](raw json.RawMessage) (T, error) {
	var out T
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("invalid params: %w", err)
	}
	return out, nil
}

func writeJSONRPC(w http.ResponseWriter, status int, resp JSONRPCResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func writeSSEJSONRPC(w http.ResponseWriter, flusher http.Flusher, resp JSONRPCResponse) {
	b, _ := json.Marshal(resp)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}
