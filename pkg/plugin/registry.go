package plugin

import (
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// Registry 管理插件工厂和实例。
// 它支持通过工厂函数注册插件类型，通过 YAML 配置加载并实例化插件。
type Registry struct {
	mu        sync.RWMutex
	factories map[PluginType]map[string]PluginFactory // type → name → factory
	instances map[PluginType]Plugin                   // type → active instance
}

// NewRegistry 创建一个新的插件注册表。
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[PluginType]map[string]PluginFactory),
		instances: make(map[PluginType]Plugin),
	}
}

// Register 注册一个插件工厂。
// pluginType 为能力类型（asr/tts/llm/detector/recorder）。
// name 为插件实现名称（如 "mock"、"whisper"）。
// 同一 type+name 重复注册会返回错误。
func (r *Registry) Register(pluginType PluginType, name string, factory PluginFactory) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.factories[pluginType]; !ok {
		r.factories[pluginType] = make(map[string]PluginFactory)
	}
	if _, exists := r.factories[pluginType][name]; exists {
		return fmt.Errorf("plugin already registered: type=%s name=%s", pluginType, name)
	}
	r.factories[pluginType][name] = factory
	return nil
}

// MustRegister 注册插件工厂，如果出错则 panic。
func (r *Registry) MustRegister(pluginType PluginType, name string, factory PluginFactory) {
	if err := r.Register(pluginType, name, factory); err != nil {
		panic(err)
	}
}

// Create 根据类型和实现名称创建一个新的插件实例（未初始化）。
func (r *Registry) Create(pluginType PluginType, name string) (Plugin, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	typeFactories, ok := r.factories[pluginType]
	if !ok {
		return nil, fmt.Errorf("no plugins registered for type %s", pluginType)
	}
	factory, ok := typeFactories[name]
	if !ok {
		return nil, fmt.Errorf("plugin not found: type=%s name=%s", pluginType, name)
	}
	return factory(), nil
}

// Load 从配置创建并初始化一个插件实例，并注册为该类型的活跃实例。
func (r *Registry) Load(cfg Config) (Plugin, error) {
	p, err := r.Create(cfg.Type, cfg.Name)
	if err != nil {
		return nil, err
	}
	if err := p.Init(cfg); err != nil {
		return nil, fmt.Errorf("init plugin %s/%s: %w", cfg.Type, cfg.Name, err)
	}

	r.mu.Lock()
	// 如果已有旧实例，先关闭
	if old, ok := r.instances[cfg.Type]; ok {
		_ = old.Close()
	}
	r.instances[cfg.Type] = p
	r.mu.Unlock()

	return p, nil
}

// Get 获取指定类型的活跃插件实例。
func (r *Registry) Get(pluginType PluginType) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.instances[pluginType]
	return p, ok
}

// GetASR 获取 ASR 插件实例。
func (r *Registry) GetASR() (ASR, bool) {
	p, ok := r.Get(PluginTypeASR)
	if !ok {
		return nil, false
	}
	return AsASR(p)
}

// GetTTS 获取 TTS 插件实例。
func (r *Registry) GetTTS() (TTS, bool) {
	p, ok := r.Get(PluginTypeTTS)
	if !ok {
		return nil, false
	}
	return AsTTS(p)
}

// GetLLM 获取 LLM 插件实例。
func (r *Registry) GetLLM() (LLM, bool) {
	p, ok := r.Get(PluginTypeLLM)
	if !ok {
		return nil, false
	}
	return AsLLM(p)
}

// GetDetector 获取 Detector 插件实例。
func (r *Registry) GetDetector() (Detector, bool) {
	p, ok := r.Get(PluginTypeDetector)
	if !ok {
		return nil, false
	}
	return AsDetector(p)
}

// GetRecorder 获取 Recorder 插件实例。
func (r *Registry) GetRecorder() (Recorder, bool) {
	p, ok := r.Get(PluginTypeRecorder)
	if !ok {
		return nil, false
	}
	return AsRecorder(p)
}

// Close 关闭所有活跃插件实例。
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var firstErr error
	for t, p := range r.instances {
		if err := p.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(r.instances, t)
	}
	return firstErr
}

// ─── YAML 配置 ────────────────────────────────────────────────────────────────

// PluginConfig YAML 中单个插件的配置。
type PluginConfig struct {
	Name   string         `yaml:"name"`   // 插件实现名称（如 "mock"）
	Config map[string]any `yaml:"config"` // 插件特定参数
}

// PluginsConfig YAML 顶层配置。
type PluginsConfig struct {
	Plugins map[PluginType]PluginConfig `yaml:"plugins"`
}

// LoadFromFile 从 YAML 文件加载插件配置并实例化所有插件。
//
// YAML 格式示例：
//
//	plugins:
//	  asr:
//	    name: mock
//	    config:
//	      text: "你好世界"
//	  tts:
//	    name: mock
//	    config:
//	      frequency: 440
//	      duration: 1.0
//	  llm:
//	    name: mock
//	    config:
//	      prefix: "你说的是："
//	  detector:
//	    name: energy
//	    config:
//	      threshold: 0.01
func (r *Registry) LoadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read plugin config %s: %w", path, err)
	}
	return r.LoadFromBytes(data)
}

// LoadFromBytes 从 YAML 字节加载插件配置并实例化所有插件。
func (r *Registry) LoadFromBytes(data []byte) error {
	var cfg PluginsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse plugin config: %w", err)
	}

	for pluginType, pc := range cfg.Plugins {
		config := Config{
			Name:   pc.Name,
			Type:   pluginType,
			Params: pc.Config,
		}
		if _, err := r.Load(config); err != nil {
			return fmt.Errorf("load plugin %s: %w", pluginType, err)
		}
	}
	return nil
}

// LoadFromMap 从 map 配置加载插件（编程式配置，用于测试）。
func (r *Registry) LoadFromMap(configs map[PluginType]Config) error {
	for pluginType, cfg := range configs {
		cfg.Type = pluginType
		if _, err := r.Load(cfg); err != nil {
			return fmt.Errorf("load plugin %s: %w", pluginType, err)
		}
	}
	return nil
}
