package plugin

import (
	"fmt"
	"sync"
)

// Loader 管理进程内插件的加载和生命周期。
// 它是 Registry 的上层封装，提供按类型批量加载和便捷查询。
//
// 与 Registry 的区别：
//   - Registry 负责插件工厂注册和单实例管理
//   - Loader 负责从配置源加载一组插件，并提供类型安全的访问器
//
// 典型用法：
//
//	loader := plugin.NewLoader()
//	plugin.RegisterBuiltins(loader.Registry)
//	err := loader.LoadFromFile("config/plugins.yaml")
//	asr := loader.ASR()  // 获取 ASR 插件
type Loader struct {
	*Registry
	mu sync.RWMutex
}

// NewLoader 创建一个新的插件加载器。
func NewLoader() *Loader {
	return &Loader{
		Registry: NewRegistry(),
	}
}

// LoadFromFile 从 YAML 配置文件加载所有插件。
func (l *Loader) LoadFromFile(path string) error {
	return l.Registry.LoadFromFile(path)
}

// LoadFromBytes 从 YAML 字节数据加载所有插件。
func (l *Loader) LoadFromBytes(data []byte) error {
	return l.Registry.LoadFromBytes(data)
}

// LoadFromMap 从编程式配置 map 加载所有插件（用于测试和代码内配置）。
func (l *Loader) LoadFromMap(configs map[PluginType]Config) error {
	return l.Registry.LoadFromMap(configs)
}

// ─── 类型安全访问器 ──────────────────────────────────────────────────────────

// ASR 返回 ASR 插件实例，如果未加载则返回 nil, false。
func (l *Loader) ASR() (ASR, bool) {
	return l.Registry.GetASR()
}

// TTS 返回 TTS 插件实例。
func (l *Loader) TTS() (TTS, bool) {
	return l.Registry.GetTTS()
}

// LLM 返回 LLM 插件实例。
func (l *Loader) LLM() (LLM, bool) {
	return l.Registry.GetLLM()
}

// Detector 返回 Detector 插件实例。
func (l *Loader) Detector() (Detector, bool) {
	return l.Registry.GetDetector()
}

// Recorder 返回 Recorder 插件实例。
func (l *Loader) Recorder() (Recorder, bool) {
	return l.Registry.GetRecorder()
}

// MustASR 返回 ASR 插件实例，如果未加载则 panic。
func (l *Loader) MustASR() ASR {
	asr, ok := l.ASR()
	if !ok {
		panic("ASR plugin not loaded")
	}
	return asr
}

// MustTTS 返回 TTS 插件实例，如果未加载则 panic。
func (l *Loader) MustTTS() TTS {
	tts, ok := l.TTS()
	if !ok {
		panic("TTS plugin not loaded")
	}
	return tts
}

// MustLLM 返回 LLM 插件实例，如果未加载则 panic。
func (l *Loader) MustLLM() LLM {
	llm, ok := l.LLM()
	if !ok {
		panic("LLM plugin not loaded")
	}
	return llm
}

// MustDetector 返回 Detector 插件实例，如果未加载则 panic。
func (l *Loader) MustDetector() Detector {
	det, ok := l.Detector()
	if !ok {
		panic("Detector plugin not loaded")
	}
	return det
}

// ─── 内置插件注册 ─────────────────────────────────────────────────────────────

// RegisterBuiltins 注册所有内置插件工厂到给定的 Registry。
// 目前包含 mock 系列插件。第三方插件可通过 Register 自行注册。
//
// 注意：调用此函数会引入对 pkg/plugin/mock 包的依赖。
// 如果不想引入 mock 依赖，请直接使用 Registry.Register 手动注册。
var (
	builtinMu      sync.Mutex
	builtinRegOnce bool
)

// RegisterFunc 是注册内置插件的函数类型。
type RegisterFunc func(*Registry) error

var builtinRegistrars []RegisterFunc

// RegisterBuiltin 注册一个内置插件注册函数。
// 在 RegisterBuiltins 调用时依次执行。
func RegisterBuiltin(fn RegisterFunc) {
	builtinMu.Lock()
	defer builtinMu.Unlock()
	builtinRegistrars = append(builtinRegistrars, fn)
}

// RegisterBuiltins 注册所有通过 RegisterBuiltin 登记的内置插件。
func RegisterBuiltins(r *Registry) error {
	builtinMu.Lock()
	registrars := make([]RegisterFunc, len(builtinRegistrars))
	copy(registrars, builtinRegistrars)
	builtinMu.Unlock()

	for _, fn := range registrars {
		if err := fn(r); err != nil {
			return fmt.Errorf("register builtins: %w", err)
		}
	}
	return nil
}
