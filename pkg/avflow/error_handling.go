package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"fmt"
	"sync"
	"time"
)

// ErrorSeverity represents error severity level
type ErrorSeverity string

const (
	ErrorSeverityInfo    ErrorSeverity = "info"
	ErrorSeverityWarning ErrorSeverity = "warning"
	ErrorSeverityError   ErrorSeverity = "error"
	ErrorSeverityCritical ErrorSeverity = "critical"
)

// ComponentError represents an error from a component
type ComponentError struct {
	// Component ID
	ComponentID string

	// Error message
	Message string

	// Error severity
	Severity ErrorSeverity

	// Error timestamp
	Timestamp time.Time

	// Underlying error
	Err error

	// Recovery action
	RecoveryAction string

	// Retry count
	RetryCount int
}

// CircuitBreakerState represents the state of a circuit breaker
type CircuitBreakerState string

const (
	CircuitBreakerClosed CircuitBreakerState = "closed"
	CircuitBreakerOpen   CircuitBreakerState = "open"
	CircuitBreakerHalfOpen CircuitBreakerState = "half-open"
)

// CircuitBreakerConfig represents circuit breaker configuration
type CircuitBreakerConfig struct {
	// Failure threshold (number of failures before opening)
	FailureThreshold int

	// Success threshold (number of successes before closing)
	SuccessThreshold int

	// Timeout before attempting to close (half-open state)
	Timeout time.Duration

	// Maximum concurrent requests in half-open state
	MaxConcurrentRequests int
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	config            CircuitBreakerConfig
	state             CircuitBreakerState
	failureCount      int
	successCount      int
	lastFailureTime   time.Time
	mu                sync.RWMutex
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{
		config: config,
		state:  CircuitBreakerClosed,
	}
}

// Call executes a function with circuit breaker protection
func (cb *CircuitBreaker) Call(fn func() error) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Check state
	switch cb.state {
	case CircuitBreakerOpen:
		// Check if timeout has passed
		if time.Since(cb.lastFailureTime) > cb.config.Timeout {
			cb.state = CircuitBreakerHalfOpen
			cb.successCount = 0
			return fn()
		}
		return fmt.Errorf("circuit breaker is open")

	case CircuitBreakerHalfOpen:
		// Try to execute
		err := fn()
		if err != nil {
			cb.failureCount++
			cb.state = CircuitBreakerOpen
			cb.lastFailureTime = time.Now()
			return err
		}

		cb.successCount++
		if cb.successCount >= cb.config.SuccessThreshold {
			cb.state = CircuitBreakerClosed
			cb.failureCount = 0
		}
		return nil

	case CircuitBreakerClosed:
		err := fn()
		if err != nil {
			cb.failureCount++
			if cb.failureCount >= cb.config.FailureThreshold {
				cb.state = CircuitBreakerOpen
				cb.lastFailureTime = time.Now()
			}
			return err
		}

		cb.failureCount = 0
		return nil

	default:
		return fmt.Errorf("unknown circuit breaker state: %s", cb.state)
	}
}

// GetState returns the current state
func (cb *CircuitBreaker) GetState() CircuitBreakerState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Reset resets the circuit breaker
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = CircuitBreakerClosed
	cb.failureCount = 0
	cb.successCount = 0
}

// RetryPolicy defines retry behavior
type RetryPolicy struct {
	// Maximum number of retries
	MaxRetries int

	// Initial backoff duration
	InitialBackoff time.Duration

	// Maximum backoff duration
	MaxBackoff time.Duration

	// Backoff multiplier
	BackoffMultiplier float64

	// Jitter factor (0.0 to 1.0)
	JitterFactor float64
}

// Retrier implements retry logic with exponential backoff
type Retrier struct {
	policy RetryPolicy
}

// NewRetrier creates a new retrier
func NewRetrier(policy RetryPolicy) *Retrier {
	return &Retrier{
		policy: policy,
	}
}

// Execute executes a function with retry logic
func (r *Retrier) Execute(fn func() error) error {
	var lastErr error
	backoff := r.policy.InitialBackoff

	for attempt := 0; attempt <= r.policy.MaxRetries; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}

		lastErr = err

		if attempt < r.policy.MaxRetries {
			// Wait before retrying
			time.Sleep(backoff)

			// Calculate next backoff
			backoff = time.Duration(float64(backoff) * r.policy.BackoffMultiplier)
			if backoff > r.policy.MaxBackoff {
				backoff = r.policy.MaxBackoff
			}
		}
	}

	return lastErr
}

// ErrorHandler handles component errors
type ErrorHandler struct {
	handlers map[ErrorSeverity][]func(*ComponentError)
	mu       sync.RWMutex
}

// NewErrorHandler creates a new error handler
func NewErrorHandler() *ErrorHandler {
	return &ErrorHandler{
		handlers: make(map[ErrorSeverity][]func(*ComponentError)),
	}
}

// RegisterHandler registers an error handler for a severity level
func (eh *ErrorHandler) RegisterHandler(severity ErrorSeverity, handler func(*ComponentError)) {
	eh.mu.Lock()
	defer eh.mu.Unlock()

	eh.handlers[severity] = append(eh.handlers[severity], handler)
}

// HandleError handles an error
func (eh *ErrorHandler) HandleError(err *ComponentError) {
	eh.mu.RLock()
	handlers := eh.handlers[err.Severity]
	eh.mu.RUnlock()

	for _, handler := range handlers {
		handler(err)
	}
}

// RecoveryStrategy defines recovery behavior
type RecoveryStrategy interface {
	// Recover attempts to recover from an error
	Recover(err *ComponentError) error

	// CanRecover checks if recovery is possible
	CanRecover(err *ComponentError) bool
}

// RestartRecovery implements restart recovery strategy
type RestartRecovery struct {
	component Component
}

// NewRestartRecovery creates a new restart recovery strategy
func NewRestartRecovery(component Component) *RestartRecovery {
	return &RestartRecovery{
		component: component,
	}
}

// Recover restarts the component
func (rr *RestartRecovery) Recover(err *ComponentError) error {
	// In a real implementation, this would restart the component
	return nil
}

// CanRecover checks if restart is possible
func (rr *RestartRecovery) CanRecover(err *ComponentError) bool {
	return err.Severity != ErrorSeverityCritical
}

// FallbackRecovery implements fallback recovery strategy
type FallbackRecovery struct {
	fallbackComponent Component
}

// NewFallbackRecovery creates a new fallback recovery strategy
func NewFallbackRecovery(fallback Component) *FallbackRecovery {
	return &FallbackRecovery{
		fallbackComponent: fallback,
	}
}

// Recover switches to fallback component
func (fr *FallbackRecovery) Recover(err *ComponentError) error {
	// In a real implementation, this would switch to fallback
	return nil
}

// CanRecover checks if fallback is available
func (fr *FallbackRecovery) CanRecover(err *ComponentError) bool {
	return fr.fallbackComponent != nil
}

// HealthChecker monitors component health
type HealthChecker struct {
	checks map[string]func() bool
	mu     sync.RWMutex
}

// NewHealthChecker creates a new health checker
func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		checks: make(map[string]func() bool),
	}
}

// RegisterCheck registers a health check
func (hc *HealthChecker) RegisterCheck(name string, check func() bool) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	hc.checks[name] = check
}

// CheckHealth checks overall health
func (hc *HealthChecker) CheckHealth() map[string]bool {
	hc.mu.RLock()
	checks := hc.checks
	hc.mu.RUnlock()

	results := make(map[string]bool)
	for name, check := range checks {
		results[name] = check()
	}
	return results
}

// IsHealthy checks if all components are healthy
func (hc *HealthChecker) IsHealthy() bool {
	results := hc.CheckHealth()
	for _, healthy := range results {
		if !healthy {
			return false
		}
	}
	return true
}
