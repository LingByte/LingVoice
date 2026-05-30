package a2a

import "github.com/LingByte/LingVoice/pkg/protocol/schema"

// ProtocolVersion is the supported A2A protocol version (Google A2A spec).
const ProtocolVersion = "0.3.0"

// Transport protocol identifiers from the A2A spec.
const (
	TransportJSONRPC  = "JSONRPC"
	TransportGRPC     = "GRPC"
	TransportHTTPJSON = "HTTP+JSON"
)

// JSON-RPC method names (category/action).
const (
	MethodMessageSend      = "message/send"
	MethodMessageStream    = "message/stream"
	MethodTasksGet         = "tasks/get"
	MethodTasksCancel      = "tasks/cancel"
	MethodTasksResubscribe = "tasks/resubscribe"
	MethodTasksList        = "tasks/list"

	MethodPushNotificationSet    = "tasks/pushNotificationConfig/set"
	MethodPushNotificationGet    = "tasks/pushNotificationConfig/get"
	MethodPushNotificationList   = "tasks/pushNotificationConfig/list"
	MethodPushNotificationDelete = "tasks/pushNotificationConfig/delete"
	MethodPushDeadLetterList     = "tasks/pushNotification/deadLetter/list"
	MethodPushDeadLetterRedrive  = "tasks/pushNotification/deadLetter/redrive"

	MethodSendMessage           = "SendMessage"
	MethodSendStreamingMessage  = "SendStreamingMessage"
	MethodGetTask               = "GetTask"
	MethodCancelTask            = "CancelTask"
	MethodSubscribeToTask       = "SubscribeToTask"
)

// TaskState values from the A2A spec.
type TaskState string

const (
	TaskSubmitted     TaskState = "submitted"
	TaskWorking       TaskState = "working"
	TaskInputRequired TaskState = "input-required"
	TaskCompleted     TaskState = "completed"
	TaskCanceled      TaskState = "canceled"
	TaskFailed        TaskState = "failed"
	TaskRejected      TaskState = "rejected"
	TaskAuthRequired  TaskState = "auth-required"
	TaskUnknown       TaskState = "unknown"
)

func (s TaskState) Terminal() bool {
	switch s {
	case TaskCompleted, TaskCanceled, TaskFailed, TaskRejected:
		return true
	default:
		return false
	}
}

// AuthScheme describes supported authentication in AgentCard (legacy field).
type AuthScheme struct {
	Type        string `json:"type"` // bearer, api_key, mtls
	Header      string `json:"header,omitempty"`
	Description string `json:"description,omitempty"`
}

// AgentCapabilities lists agent features exposed in AgentCard.
type AgentCapabilities struct {
	Streaming              bool `json:"streaming"`
	PushNotifications      bool `json:"pushNotifications,omitempty"`
	StateTransitionHistory bool `json:"stateTransitionHistory,omitempty"`
	Tasks                  bool `json:"tasks,omitempty"` // LingVoice extension for handler routing
}

// AgentInterface declares a URL and transport pair.
type AgentInterface struct {
	URL       string `json:"url"`
	Transport string `json:"transport"`
}

// AgentCard describes a remote agent endpoint (A2A discovery).
type AgentCard struct {
	ProtocolVersion      string            `json:"protocolVersion"`
	Name                 string            `json:"name"`
	Description          string            `json:"description,omitempty"`
	URL                  string            `json:"url"`
	PreferredTransport   string            `json:"preferredTransport"`
	AdditionalInterfaces []AgentInterface  `json:"additionalInterfaces,omitempty"`
	Version              string            `json:"version,omitempty"`
	Capabilities         AgentCapabilities `json:"capabilities"`
	DefaultInputModes    []string          `json:"defaultInputModes,omitempty"`
	DefaultOutputModes   []string          `json:"defaultOutputModes,omitempty"`
	AuthSchemes          []AuthScheme      `json:"authSchemes,omitempty"`
}

// Part is a discriminated union of message/artifact content.
type Part struct {
	Kind     string         `json:"kind"`
	Text     string         `json:"text,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// A2AMessage is an A2A protocol message (distinct from protocol/schema.Message).
type A2AMessage struct {
	Kind             string         `json:"kind,omitempty"`
	Role             string         `json:"role"`
	Parts            []Part         `json:"parts"`
	MessageID        string         `json:"messageId,omitempty"`
	TaskID           string         `json:"taskId,omitempty"`
	ContextID        string         `json:"contextId,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
	ReferenceTaskIDs []string       `json:"referenceTaskIds,omitempty"`
}

// TaskStatus is the current state of a task.
type TaskStatus struct {
	State     TaskState   `json:"state"`
	Message   *A2AMessage `json:"message,omitempty"`
	Timestamp string      `json:"timestamp,omitempty"`
}

// Artifact is generated task output.
type Artifact struct {
	ArtifactID string         `json:"artifactId"`
	Name       string         `json:"name,omitempty"`
	Parts      []Part         `json:"parts"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// Task is the stateful unit of work in A2A.
type Task struct {
	Kind      string         `json:"kind,omitempty"`
	ID        string         `json:"id"`
	ContextID string         `json:"contextId"`
	Status    TaskStatus     `json:"status"`
	History   []A2AMessage   `json:"history,omitempty"`
	Artifacts []Artifact     `json:"artifacts,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// MessageSendConfiguration configures send/stream requests.
type MessageSendConfiguration struct {
	AcceptedOutputModes  []string `json:"acceptedOutputModes,omitempty"`
	HistoryLength        int      `json:"historyLength,omitempty"`
	Blocking             *bool    `json:"blocking,omitempty"`
}

// MessageSendParams is the payload for message/send and message/stream.
type MessageSendParams struct {
	Message       A2AMessage              `json:"message"`
	Configuration *MessageSendConfiguration `json:"configuration,omitempty"`
	Metadata      map[string]any          `json:"metadata,omitempty"`
}

// TaskQueryParams is the payload for tasks/get.
type TaskQueryParams struct {
	ID            string `json:"id"`
	HistoryLength int    `json:"historyLength,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// TaskIDParams is the payload for tasks/cancel and related methods.
type TaskIDParams struct {
	ID       string         `json:"id"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// TaskListParams is the payload for tasks/list.
type TaskListParams struct {
	ContextID string `json:"contextId,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// TaskListResult is the tasks/list response.
type TaskListResult struct {
	Tasks []Task `json:"tasks"`
}

// PushNotificationConfig configures webhook delivery for task updates.
type PushNotificationConfig struct {
	TaskID    string         `json:"taskId"`
	URL       string         `json:"url"`
	Token     string         `json:"token,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// PushNotificationSetParams is the payload for tasks/pushNotificationConfig/set.
type PushNotificationSetParams struct {
	TaskID string                 `json:"taskId"`
	Config PushNotificationConfig `json:"config"`
}

// PushNotificationGetParams is the payload for tasks/pushNotificationConfig/get.
type PushNotificationGetParams struct {
	TaskID string `json:"taskId"`
}

// PushNotificationListParams is the payload for tasks/pushNotificationConfig/list.
type PushNotificationListParams struct {
	TaskID string `json:"taskId,omitempty"`
}

// PushNotificationListResult lists push notification configs.
type PushNotificationListResult struct {
	Configs []PushNotificationConfig `json:"configs"`
}

// PushNotificationDeleteParams is the payload for tasks/pushNotificationConfig/delete.
type PushNotificationDeleteParams struct {
	TaskID string `json:"taskId"`
}

// TaskStatusUpdateEvent is streamed during message/stream.
type TaskStatusUpdateEvent struct {
	Kind      string     `json:"kind,omitempty"`
	TaskID    string     `json:"taskId"`
	ContextID string     `json:"contextId"`
	Status    TaskStatus `json:"status"`
	Final     bool       `json:"final"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// TaskArtifactUpdateEvent is streamed when artifacts are produced.
type TaskArtifactUpdateEvent struct {
	Kind      string   `json:"kind,omitempty"`
	TaskID    string   `json:"taskId"`
	ContextID string   `json:"contextId"`
	Artifact  Artifact `json:"artifact"`
	Append    bool     `json:"append,omitempty"`
	LastChunk bool     `json:"lastChunk,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// SendResult is message/send result (Task or Message).
type SendResult struct {
	Task    *Task       `json:"task,omitempty"`
	Message *A2AMessage `json:"message,omitempty"`
}

// MessageRequest is the internal invoke payload for agent handlers.
type MessageRequest struct {
	Messages  []*schema.Message `json:"messages,omitempty"`
	Vars      map[string]any    `json:"vars,omitempty"`
	TaskID    string            `json:"task_id,omitempty"`
	ContextID string            `json:"context_id,omitempty"`
	Blocking  bool              `json:"blocking,omitempty"`
}

// MessageResponse is the legacy REST invoke response.
type MessageResponse struct {
	Message    *schema.Message `json:"message,omitempty"`
	Error      string          `json:"error,omitempty"`
	TaskID     string          `json:"task_id,omitempty"`
	TaskStatus string          `json:"task_status,omitempty"`
}

// StreamEvent is the legacy SSE payload for /v1/messages:stream.
type StreamEvent struct {
	Event   string          `json:"event"`
	Message *schema.Message `json:"message,omitempty"`
	Error   string          `json:"error,omitempty"`
	TaskID  string          `json:"task_id,omitempty"`
}

// LegacyTaskStatus describes async task state for /v1/tasks/{id}.
type LegacyTaskStatus struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}
