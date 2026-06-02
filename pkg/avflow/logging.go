package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"fmt"
	"sync"
	"time"
)

// LogLevel represents log level
type LogLevel int

const (
	LogLevelDebug LogLevel = iota
	LogLevelInfo
	LogLevelWarn
	LogLevelError
	LogLevelFatal
)

// LogEntry represents a log entry
type LogEntry struct {
	// Timestamp
	Timestamp time.Time

	// Log level
	Level LogLevel

	// Component ID
	ComponentID string

	// Message
	Message string

	// Additional fields
	Fields map[string]interface{}

	// Stack trace (for errors)
	StackTrace string
}

// Logger defines the logging interface
type Logger interface {
	// Log logs a message
	Log(level LogLevel, componentID, message string, fields map[string]interface{})

	// Debug logs a debug message
	Debug(componentID, message string)

	// Info logs an info message
	Info(componentID, message string)

	// Warn logs a warning message
	Warn(componentID, message string)

	// Error logs an error message
	Error(componentID, message string, err error)

	// Fatal logs a fatal message
	Fatal(componentID, message string)

	// GetLogs returns recent logs
	GetLogs(limit int) []LogEntry
}

// DefaultLogger is a simple in-memory logger
type DefaultLogger struct {
	logs   []LogEntry
	mu     sync.RWMutex
	maxLen int
}

// NewDefaultLogger creates a new default logger
func NewDefaultLogger(maxLen int) *DefaultLogger {
	return &DefaultLogger{
		logs:   make([]LogEntry, 0, maxLen),
		maxLen: maxLen,
	}
}

// Log logs a message
func (dl *DefaultLogger) Log(level LogLevel, componentID, message string, fields map[string]interface{}) {
	dl.mu.Lock()
	defer dl.mu.Unlock()

	entry := LogEntry{
		Timestamp:   time.Now(),
		Level:       level,
		ComponentID: componentID,
		Message:     message,
		Fields:      fields,
	}

	dl.logs = append(dl.logs, entry)

	// Keep only recent logs
	if len(dl.logs) > dl.maxLen {
		dl.logs = dl.logs[len(dl.logs)-dl.maxLen:]
	}
}

// Debug logs a debug message
func (dl *DefaultLogger) Debug(componentID, message string) {
	dl.Log(LogLevelDebug, componentID, message, nil)
}

// Info logs an info message
func (dl *DefaultLogger) Info(componentID, message string) {
	dl.Log(LogLevelInfo, componentID, message, nil)
}

// Warn logs a warning message
func (dl *DefaultLogger) Warn(componentID, message string) {
	dl.Log(LogLevelWarn, componentID, message, nil)
}

// Error logs an error message
func (dl *DefaultLogger) Error(componentID, message string, err error) {
	fields := map[string]interface{}{
		"error": err.Error(),
	}
	dl.Log(LogLevelError, componentID, message, fields)
}

// Fatal logs a fatal message
func (dl *DefaultLogger) Fatal(componentID, message string) {
	dl.Log(LogLevelFatal, componentID, message, nil)
}

// GetLogs returns recent logs
func (dl *DefaultLogger) GetLogs(limit int) []LogEntry {
	dl.mu.RLock()
	defer dl.mu.RUnlock()

	if limit <= 0 || limit > len(dl.logs) {
		limit = len(dl.logs)
	}

	result := make([]LogEntry, limit)
	copy(result, dl.logs[len(dl.logs)-limit:])
	return result
}

// Tracer defines the tracing interface
type Tracer interface {
	// StartSpan starts a new span
	StartSpan(operationName string) Span

	// GetTrace returns the current trace
	GetTrace() *Trace
}

// Span represents a trace span
type Span struct {
	// Span ID
	ID string

	// Operation name
	OperationName string

	// Start time
	StartTime time.Time

	// End time
	EndTime time.Time

	// Duration in milliseconds
	Duration int64

	// Tags
	Tags map[string]interface{}

	// Logs
	Logs []SpanLog

	// Parent span ID
	ParentSpanID string
}

// SpanLog represents a log within a span
type SpanLog struct {
	// Timestamp
	Timestamp time.Time

	// Message
	Message string

	// Fields
	Fields map[string]interface{}
}

// Trace represents a complete trace
type Trace struct {
	// Trace ID
	ID string

	// Spans
	Spans []Span

	// Start time
	StartTime time.Time

	// End time
	EndTime time.Time

	// Duration in milliseconds
	Duration int64
}

// DefaultTracer is a simple in-memory tracer
type DefaultTracer struct {
	traceID string
	spans   []Span
	mu      sync.RWMutex
}

// NewDefaultTracer creates a new default tracer
func NewDefaultTracer(traceID string) *DefaultTracer {
	return &DefaultTracer{
		traceID: traceID,
		spans:   make([]Span, 0),
	}
}

// StartSpan starts a new span
func (dt *DefaultTracer) StartSpan(operationName string) Span {
	span := Span{
		ID:            fmt.Sprintf("%s-%d", dt.traceID, len(dt.spans)),
		OperationName: operationName,
		StartTime:     time.Now(),
		Tags:          make(map[string]interface{}),
		Logs:          make([]SpanLog, 0),
	}
	return span
}

// EndSpan ends a span
func (dt *DefaultTracer) EndSpan(span *Span) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	span.EndTime = time.Now()
	span.Duration = span.EndTime.Sub(span.StartTime).Milliseconds()

	dt.spans = append(dt.spans, *span)
}

// GetTrace returns the current trace
func (dt *DefaultTracer) GetTrace() *Trace {
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	if len(dt.spans) == 0 {
		return nil
	}

	startTime := dt.spans[0].StartTime
	endTime := dt.spans[len(dt.spans)-1].EndTime

	trace := &Trace{
		ID:        dt.traceID,
		Spans:     make([]Span, len(dt.spans)),
		StartTime: startTime,
		EndTime:   endTime,
		Duration:  endTime.Sub(startTime).Milliseconds(),
	}

	copy(trace.Spans, dt.spans)
	return trace
}

// LogLevelString returns string representation of log level
func LogLevelString(level LogLevel) string {
	switch level {
	case LogLevelDebug:
		return "DEBUG"
	case LogLevelInfo:
		return "INFO"
	case LogLevelWarn:
		return "WARN"
	case LogLevelError:
		return "ERROR"
	case LogLevelFatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}
