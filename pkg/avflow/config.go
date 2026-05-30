package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ConfigSource represents a configuration source
type ConfigSource interface {
	// Load loads configuration
	Load() (map[string]interface{}, error)

	// Watch watches for configuration changes
	Watch(callback func(map[string]interface{})) error
}

// ConfigManager manages application configuration
type ConfigManager struct {
	config    map[string]interface{}
	defaults  map[string]interface{}
	sources   []ConfigSource
	mu        sync.RWMutex
	watchers  []func(map[string]interface{})
}

// NewConfigManager creates a new config manager
func NewConfigManager() *ConfigManager {
	return &ConfigManager{
		config:   make(map[string]interface{}),
		defaults: make(map[string]interface{}),
		sources:  make([]ConfigSource, 0),
		watchers: make([]func(map[string]interface{}), 0),
	}
}

// SetDefault sets a default configuration value
func (cm *ConfigManager) SetDefault(key string, value interface{}) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.defaults[key] = value
	if _, exists := cm.config[key]; !exists {
		cm.config[key] = value
	}
}

// Set sets a configuration value
func (cm *ConfigManager) Set(key string, value interface{}) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.config[key] = value
	cm.notifyWatchers()
}

// Get gets a configuration value
func (cm *ConfigManager) Get(key string) interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if value, exists := cm.config[key]; exists {
		return value
	}
	return nil
}

// GetString gets a string configuration value
func (cm *ConfigManager) GetString(key string, defaultValue string) string {
	value := cm.Get(key)
	if str, ok := value.(string); ok {
		return str
	}
	return defaultValue
}

// GetInt gets an integer configuration value
func (cm *ConfigManager) GetInt(key string, defaultValue int) int {
	value := cm.Get(key)
	if i, ok := value.(int); ok {
		return i
	}
	if f, ok := value.(float64); ok {
		return int(f)
	}
	return defaultValue
}

// GetBool gets a boolean configuration value
func (cm *ConfigManager) GetBool(key string, defaultValue bool) bool {
	value := cm.Get(key)
	if b, ok := value.(bool); ok {
		return b
	}
	return defaultValue
}

// GetDuration gets a duration configuration value
func (cm *ConfigManager) GetDuration(key string, defaultValue time.Duration) time.Duration {
	value := cm.Get(key)
	if str, ok := value.(string); ok {
		if d, err := time.ParseDuration(str); err == nil {
			return d
		}
	}
	return defaultValue
}

// AddSource adds a configuration source
func (cm *ConfigManager) AddSource(source ConfigSource) error {
	cm.mu.Lock()
	cm.sources = append(cm.sources, source)
	cm.mu.Unlock()

	// Load from source
	config, err := source.Load()
	if err != nil {
		return err
	}

	// Merge configuration
	cm.mu.Lock()
	for key, value := range config {
		cm.config[key] = value
	}
	cm.mu.Unlock()

	return nil
}

// Watch registers a watcher for configuration changes
func (cm *ConfigManager) Watch(callback func(map[string]interface{})) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.watchers = append(cm.watchers, callback)
}

// notifyWatchers notifies all watchers of configuration changes
func (cm *ConfigManager) notifyWatchers() {
	config := make(map[string]interface{})
	for k, v := range cm.config {
		config[k] = v
	}

	for _, watcher := range cm.watchers {
		go watcher(config)
	}
}

// GetAll returns all configuration
func (cm *ConfigManager) GetAll() map[string]interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make(map[string]interface{})
	for k, v := range cm.config {
		result[k] = v
	}
	return result
}

// Validate validates configuration
func (cm *ConfigManager) Validate(schema map[string]ConfigValidator) error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	for key, validator := range schema {
		value, exists := cm.config[key]
		if !exists {
			if validator.Required {
				return fmt.Errorf("required configuration key missing: %s", key)
			}
			continue
		}

		if err := validator.Validate(value); err != nil {
			return fmt.Errorf("validation failed for key %s: %v", key, err)
		}
	}

	return nil
}

// ConfigValidator validates a configuration value
type ConfigValidator struct {
	// Whether the value is required
	Required bool

	// Type check function
	TypeCheck func(interface{}) bool

	// Custom validation function
	Validate func(interface{}) error
}

// JSONConfigSource loads configuration from JSON
type JSONConfigSource struct {
	data []byte
}

// NewJSONConfigSource creates a new JSON config source
func NewJSONConfigSource(data []byte) *JSONConfigSource {
	return &JSONConfigSource{
		data: data,
	}
}

// Load loads configuration from JSON
func (jcs *JSONConfigSource) Load() (map[string]interface{}, error) {
	var config map[string]interface{}
	if err := json.Unmarshal(jcs.data, &config); err != nil {
		return nil, err
	}
	return config, nil
}

// Watch watches for configuration changes (not implemented for JSON)
func (jcs *JSONConfigSource) Watch(callback func(map[string]interface{})) error {
	return fmt.Errorf("watch not supported for JSON config source")
}

// MapConfigSource loads configuration from a map
type MapConfigSource struct {
	data map[string]interface{}
}

// NewMapConfigSource creates a new map config source
func NewMapConfigSource(data map[string]interface{}) *MapConfigSource {
	return &MapConfigSource{
		data: data,
	}
}

// Load loads configuration from map
func (mcs *MapConfigSource) Load() (map[string]interface{}, error) {
	result := make(map[string]interface{})
	for k, v := range mcs.data {
		result[k] = v
	}
	return result, nil
}

// Watch watches for configuration changes (not implemented for map)
func (mcs *MapConfigSource) Watch(callback func(map[string]interface{})) error {
	return fmt.Errorf("watch not supported for map config source")
}

// GraphConfig represents graph-specific configuration
type GraphConfig struct {
	// Graph name
	Name string

	// Buffer size for channels
	BufferSize int

	// Timeout for component operations
	Timeout time.Duration

	// Enable distributed execution
	DistributedEnabled bool

	// Enable error recovery
	ErrorRecoveryEnabled bool

	// Enable performance monitoring
	PerformanceMonitoringEnabled bool

	// Enable logging
	LoggingEnabled bool

	// Log level
	LogLevel LogLevel
}

// DefaultGraphConfig returns default graph configuration
func DefaultGraphConfig() GraphConfig {
	return GraphConfig{
		Name:                         "default-graph",
		BufferSize:                   64,
		Timeout:                      30 * time.Second,
		DistributedEnabled:           false,
		ErrorRecoveryEnabled:         true,
		PerformanceMonitoringEnabled: true,
		LoggingEnabled:               true,
		LogLevel:                     LogLevelInfo,
	}
}
