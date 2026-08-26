package plugin_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/plugin"
	"github.com/LingByte/LingVoice/pkg/plugin/mock"
)

// ─── 测试辅助插件 ──────────────────────────────────────────────────────────────

// testPlugin 是一个用于测试的最小插件实现，同时实现 ASR/TTS/LLM/Detector。
type testPlugin struct {
	name   string
	ptype  plugin.PluginType
	closed bool
}

func (p *testPlugin) Name() string         { return p.name }
func (p *testPlugin) Type() plugin.PluginType     { return p.ptype }
func (p *testPlugin) Init(plugin.Config) error    { return nil }
func (p *testPlugin) Close() error         { p.closed = true; return nil }
func (p *testPlugin) Recognize(context.Context, []float32) (string, error) {
	return "asr-text", nil
}
func (p *testPlugin) Synthesize(context.Context, string) ([]float32, error) {
	return []float32{0.1, 0.2}, nil
}
func (p *testPlugin) Generate(context.Context, string) (string, error) {
	return "llm-response", nil
}
func (p *testPlugin) Process(context.Context, []float32) (bool, float64, error) {
	return true, 0.5, nil
}

// 编译时检查 testPlugin 实现了多个能力接口
var _ plugin.ASR = (*testPlugin)(nil)
var _ plugin.TTS = (*testPlugin)(nil)
var _ plugin.LLM = (*testPlugin)(nil)
var _ plugin.Detector = (*testPlugin)(nil)
var _ plugin.Plugin = (*testPlugin)(nil)

// ─── Registry ──────────────────────────────────────────────────────────────────

func TestNewRegistry(t *testing.T) {
	r := plugin.NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
}

func TestRegistry_RegisterAndCreate(t *testing.T) {
	r := plugin.NewRegistry()
	factory := func() plugin.Plugin { return &testPlugin{name: "test", ptype: plugin.PluginTypeASR} }

	if err := r.Register(plugin.PluginTypeASR, "test", factory); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	p, err := r.Create(plugin.PluginTypeASR, "test")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if p == nil {
		t.Fatal("Create returned nil plugin")
	}
	if p.Name() != "test" {
		t.Errorf("plugin Name() = %q, want %q", p.Name(), "test")
	}
	if p.Type() != plugin.PluginTypeASR {
		t.Errorf("plugin Type() = %q, want %q", p.Type(), plugin.PluginTypeASR)
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	r := plugin.NewRegistry()
	factory := func() plugin.Plugin { return &testPlugin{name: "test", ptype: plugin.PluginTypeASR} }

	if err := r.Register(plugin.PluginTypeASR, "test", factory); err != nil {
		t.Fatalf("first Register returned error: %v", err)
	}
	// 重复注册同一 type+name 应返回错误
	if err := r.Register(plugin.PluginTypeASR, "test", factory); err == nil {
		t.Error("expected error on duplicate Register, got nil")
	}
}

func TestRegistry_RegisterDifferentTypes(t *testing.T) {
	r := plugin.NewRegistry()
	// 同名但不同类型可以注册
	if err := r.Register(plugin.PluginTypeASR, "mock", func() plugin.Plugin { return &testPlugin{name: "mock", ptype: plugin.PluginTypeASR} }); err != nil {
		t.Fatalf("Register ASR mock: %v", err)
	}
	if err := r.Register(plugin.PluginTypeTTS, "mock", func() plugin.Plugin { return &testPlugin{name: "mock", ptype: plugin.PluginTypeTTS} }); err != nil {
		t.Fatalf("Register TTS mock: %v", err)
	}
}

func TestRegistry_MustRegister(t *testing.T) {
	r := plugin.NewRegistry()
	r.MustRegister(plugin.PluginTypeASR, "test", func() plugin.Plugin { return &testPlugin{name: "test", ptype: plugin.PluginTypeASR} })

	// MustRegister 重复注册应 panic
	defer func() {
		if recover() == nil {
			t.Error("expected panic on duplicate MustRegister, got none")
		}
	}()
	r.MustRegister(plugin.PluginTypeASR, "test", func() plugin.Plugin { return &testPlugin{name: "test", ptype: plugin.PluginTypeASR} })
}

func TestRegistry_CreateNotFound(t *testing.T) {
	r := plugin.NewRegistry()
	// 未注册的类型
	if _, err := r.Create(plugin.PluginTypeASR, "nonexistent"); err == nil {
		t.Error("expected error for unregistered type, got nil")
	}

	// 注册了类型但 name 不存在
	if err := r.Register(plugin.PluginTypeASR, "test", func() plugin.Plugin { return &testPlugin{name: "test", ptype: plugin.PluginTypeASR} }); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := r.Create(plugin.PluginTypeASR, "nonexistent"); err == nil {
		t.Error("expected error for nonexistent plugin name, got nil")
	}
}

func TestRegistry_CreateUnregisteredType(t *testing.T) {
	r := plugin.NewRegistry()
	if _, err := r.Create(plugin.PluginTypeRecorder, "any"); err == nil {
		t.Error("expected error for unregistered type, got nil")
	}
}

func TestRegistry_LoadAndGet(t *testing.T) {
	r := plugin.NewRegistry()
	if err := r.Register(plugin.PluginTypeASR, "test", func() plugin.Plugin { return &testPlugin{name: "test", ptype: plugin.PluginTypeASR} }); err != nil {
		t.Fatalf("Register: %v", err)
	}

	p, err := r.Load(plugin.Config{Name: "test", Type: plugin.PluginTypeASR})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if p == nil {
		t.Fatal("Load returned nil plugin")
	}

	// Get 应返回活跃实例
	got, ok := r.Get(plugin.PluginTypeASR)
	if !ok {
		t.Fatal("Get returned ok=false after Load")
	}
	if got != p {
		t.Error("Get returned different instance than Load")
	}
}

func TestRegistry_GetNotLoaded(t *testing.T) {
	r := plugin.NewRegistry()
	if _, ok := r.Get(plugin.PluginTypeASR); ok {
		t.Error("Get returned ok=true for unloaded type")
	}
}

func TestRegistry_LoadReplacesOldInstance(t *testing.T) {
	r := plugin.NewRegistry()
	if err := r.Register(plugin.PluginTypeASR, "test", func() plugin.Plugin { return &testPlugin{name: "test", ptype: plugin.PluginTypeASR} }); err != nil {
		t.Fatalf("Register: %v", err)
	}

	p1, err := r.Load(plugin.Config{Name: "test", Type: plugin.PluginTypeASR})
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	tp1 := p1.(*testPlugin)

	p2, err := r.Load(plugin.Config{Name: "test", Type: plugin.PluginTypeASR})
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}

	// 旧实例应被关闭
	if !tp1.closed {
		t.Error("old instance was not closed on Load replacement")
	}

	// Get 应返回新实例
	got, ok := r.Get(plugin.PluginTypeASR)
	if !ok || got != p2 {
		t.Error("Get did not return the new instance after Load replacement")
	}
}

func TestRegistry_Close(t *testing.T) {
	r := plugin.NewRegistry()
	if err := r.Register(plugin.PluginTypeASR, "test", func() plugin.Plugin { return &testPlugin{name: "test", ptype: plugin.PluginTypeASR} }); err != nil {
		t.Fatalf("Register: %v", err)
	}

	p, err := r.Load(plugin.Config{Name: "test", Type: plugin.PluginTypeASR})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tp := p.(*testPlugin)

	if err := r.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
	if !tp.closed {
		t.Error("plugin was not closed by Registry.Close")
	}

	// Close 后实例应被清除
	if _, ok := r.Get(plugin.PluginTypeASR); ok {
		t.Error("Get returned ok=true after Close")
	}
}

// ─── 类型安全访问器 ──────────────────────────────────────────────────────────────

func TestRegistry_GetASR(t *testing.T) {
	r := plugin.NewRegistry()
	if err := r.Register(plugin.PluginTypeASR, "test", func() plugin.Plugin { return &testPlugin{name: "test", ptype: plugin.PluginTypeASR} }); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := r.Load(plugin.Config{Name: "test", Type: plugin.PluginTypeASR}); err != nil {
		t.Fatalf("Load: %v", err)
	}

	asr, ok := r.GetASR()
	if !ok {
		t.Fatal("GetASR returned ok=false")
	}
	if asr == nil {
		t.Fatal("GetASR returned nil")
	}
	text, err := asr.Recognize(context.Background(), []float32{0.1})
	if err != nil {
		t.Fatalf("Recognize: %v", err)
	}
	if text != "asr-text" {
		t.Errorf("Recognize = %q, want %q", text, "asr-text")
	}
}

func TestRegistry_GetTTS(t *testing.T) {
	r := plugin.NewRegistry()
	if err := r.Register(plugin.PluginTypeTTS, "test", func() plugin.Plugin { return &testPlugin{name: "test", ptype: plugin.PluginTypeTTS} }); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := r.Load(plugin.Config{Name: "test", Type: plugin.PluginTypeTTS}); err != nil {
		t.Fatalf("Load: %v", err)
	}

	tts, ok := r.GetTTS()
	if !ok {
		t.Fatal("GetTTS returned ok=false")
	}
	audio, err := tts.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(audio) != 2 {
		t.Errorf("Synthesize audio len = %d, want 2", len(audio))
	}
}

func TestRegistry_GetLLM(t *testing.T) {
	r := plugin.NewRegistry()
	if err := r.Register(plugin.PluginTypeLLM, "test", func() plugin.Plugin { return &testPlugin{name: "test", ptype: plugin.PluginTypeLLM} }); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := r.Load(plugin.Config{Name: "test", Type: plugin.PluginTypeLLM}); err != nil {
		t.Fatalf("Load: %v", err)
	}

	llm, ok := r.GetLLM()
	if !ok {
		t.Fatal("GetLLM returned ok=false")
	}
	resp, err := llm.Generate(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp != "llm-response" {
		t.Errorf("Generate = %q, want %q", resp, "llm-response")
	}
}

func TestRegistry_GetDetector(t *testing.T) {
	r := plugin.NewRegistry()
	if err := r.Register(plugin.PluginTypeDetector, "test", func() plugin.Plugin { return &testPlugin{name: "test", ptype: plugin.PluginTypeDetector} }); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := r.Load(plugin.Config{Name: "test", Type: plugin.PluginTypeDetector}); err != nil {
		t.Fatalf("Load: %v", err)
	}

	det, ok := r.GetDetector()
	if !ok {
		t.Fatal("GetDetector returned ok=false")
	}
	speaking, _, err := det.Process(context.Background(), []float32{0.1})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !speaking {
		t.Error("Process speaking = false, want true")
	}
}

func TestRegistry_GetASRNotLoaded(t *testing.T) {
	r := plugin.NewRegistry()
	if _, ok := r.GetASR(); ok {
		t.Error("GetASR returned ok=true for unloaded type")
	}
}

// ─── Capability 辅助函数 ──────────────────────────────────────────────────────────

func TestAsASR(t *testing.T) {
	p := &testPlugin{name: "test", ptype: plugin.PluginTypeASR}
	asr, ok := plugin.AsASR(p)
	if !ok {
		t.Fatal("plugin.AsASR returned ok=false for ASR plugin")
	}
	if asr == nil {
		t.Fatal("plugin.AsASR returned nil")
	}
}

func TestAsTTS(t *testing.T) {
	p := &testPlugin{name: "test", ptype: plugin.PluginTypeTTS}
	tts, ok := plugin.AsTTS(p)
	if !ok {
		t.Fatal("plugin.AsTTS returned ok=false for TTS plugin")
	}
	if tts == nil {
		t.Fatal("plugin.AsTTS returned nil")
	}
}

func TestAsLLM(t *testing.T) {
	p := &testPlugin{name: "test", ptype: plugin.PluginTypeLLM}
	llm, ok := plugin.AsLLM(p)
	if !ok {
		t.Fatal("plugin.AsLLM returned ok=false for LLM plugin")
	}
	if llm == nil {
		t.Fatal("plugin.AsLLM returned nil")
	}
}

func TestAsDetector(t *testing.T) {
	p := &testPlugin{name: "test", ptype: plugin.PluginTypeDetector}
	det, ok := plugin.AsDetector(p)
	if !ok {
		t.Fatal("plugin.AsDetector returned ok=false for Detector plugin")
	}
	if det == nil {
		t.Fatal("plugin.AsDetector returned nil")
	}
}

func TestAsASRNotImplemented(t *testing.T) {
	// 一个只实现 Plugin 基础接口的类型
	p := &basicPlugin{}
	if _, ok := plugin.AsASR(p); ok {
		t.Error("plugin.AsASR returned ok=true for non-ASR plugin")
	}
}

// basicPlugin 只实现 Plugin 接口，不实现任何能力接口
type basicPlugin struct{}

func (b *basicPlugin) Name() string      { return "basic" }
func (b *basicPlugin) Type() plugin.PluginType  { return plugin.PluginTypeASR }
func (b *basicPlugin) Init(plugin.Config) error { return nil }
func (b *basicPlugin) Close() error      { return nil }

var _ plugin.Plugin = (*basicPlugin)(nil)

// ─── Loader ────────────────────────────────────────────────────────────────────

func TestNewLoader(t *testing.T) {
	l := plugin.NewLoader()
	if l == nil {
		t.Fatal("NewLoader returned nil")
	}
	if l.Registry == nil {
		t.Fatal("Loader.Registry is nil")
	}
}

func TestLoader_LoadFromMap(t *testing.T) {
	l := plugin.NewLoader()
	if err := mock.Register(l.Registry); err != nil {
		t.Fatalf("mock.Register: %v", err)
	}

	configs := map[plugin.PluginType]plugin.Config{
		plugin.PluginTypeASR:      {Name: "mock", Params: map[string]any{"text": "测试文本"}},
		plugin.PluginTypeTTS:      {Name: "mock", Params: map[string]any{"frequency": float64(440.0), "duration": float64(0.5)}},
		plugin.PluginTypeLLM:      {Name: "mock", Params: map[string]any{"prefix": "回复："}},
		plugin.PluginTypeDetector: {Name: "energy", Params: map[string]any{"threshold": float64(0.01)}},
	}
	if err := l.LoadFromMap(configs); err != nil {
		t.Fatalf("LoadFromMap: %v", err)
	}

	// 验证各类型插件已加载
	asr, ok := l.ASR()
	if !ok {
		t.Fatal("Loader.ASR() returned ok=false")
	}
	text, err := asr.Recognize(context.Background(), []float32{0.1})
	if err != nil {
		t.Fatalf("ASR Recognize: %v", err)
	}
	if text != "测试文本" {
		t.Errorf("ASR text = %q, want %q", text, "测试文本")
	}

	tts, ok := l.TTS()
	if !ok {
		t.Fatal("Loader.TTS() returned ok=false")
	}
	audio, err := tts.Synthesize(context.Background(), "hi")
	if err != nil {
		t.Fatalf("TTS Synthesize: %v", err)
	}
	if len(audio) == 0 {
		t.Error("TTS Synthesize returned empty audio")
	}

	llm, ok := l.LLM()
	if !ok {
		t.Fatal("Loader.LLM() returned ok=false")
	}
	resp, err := llm.Generate(context.Background(), "你好")
	if err != nil {
		t.Fatalf("LLM Generate: %v", err)
	}
	if resp != "回复：你好" {
		t.Errorf("LLM response = %q, want %q", resp, "回复：你好")
	}

	det, ok := l.Detector()
	if !ok {
		t.Fatal("Loader.Detector() returned ok=false")
	}
	speaking, _, err := det.Process(context.Background(), []float32{0.5, 0.5})
	if err != nil {
		t.Fatalf("Detector Process: %v", err)
	}
	if !speaking {
		t.Error("Detector speaking = false, want true")
	}
}

func TestLoader_ASRNotLoaded(t *testing.T) {
	l := plugin.NewLoader()
	if _, ok := l.ASR(); ok {
		t.Error("Loader.ASR() returned ok=true before loading")
	}
}

func TestLoader_MustASRPanics(t *testing.T) {
	l := plugin.NewLoader()
	defer func() {
		if recover() == nil {
			t.Error("expected panic on MustASR when not loaded, got none")
		}
	}()
	_ = l.MustASR()
}

func TestLoader_MustTTPanics(t *testing.T) {
	l := plugin.NewLoader()
	defer func() {
		if recover() == nil {
			t.Error("expected panic on MustTTS when not loaded, got none")
		}
	}()
	_ = l.MustTTS()
}

func TestLoader_MustLLMPanics(t *testing.T) {
	l := plugin.NewLoader()
	defer func() {
		if recover() == nil {
			t.Error("expected panic on MustLLM when not loaded, got none")
		}
	}()
	_ = l.MustLLM()
}

func TestLoader_MustDetectorPanics(t *testing.T) {
	l := plugin.NewLoader()
	defer func() {
		if recover() == nil {
			t.Error("expected panic on MustDetector when not loaded, got none")
		}
	}()
	_ = l.MustDetector()
}

func TestLoader_LoadFromBytes(t *testing.T) {
	l := plugin.NewLoader()
	if err := mock.Register(l.Registry); err != nil {
		t.Fatalf("mock.Register: %v", err)
	}

	yamlData := []byte(`
plugins:
  asr:
    name: mock
    config:
      text: "YAML测试"
  tts:
    name: mock
    config:
      frequency: 440
      duration: 0.5
  llm:
    name: mock
    config:
      prefix: "YAML回复："
  detector:
    name: energy
    config:
      threshold: 0.01
`)
	if err := l.LoadFromBytes(yamlData); err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}

	asr, ok := l.ASR()
	if !ok {
		t.Fatal("Loader.ASR() returned ok=false")
	}
	text, _ := asr.Recognize(context.Background(), nil)
	if text != "YAML测试" {
		t.Errorf("ASR text = %q, want %q", text, "YAML测试")
	}

	llm, ok := l.LLM()
	if !ok {
		t.Fatal("Loader.LLM() returned ok=false")
	}
	resp, _ := llm.Generate(context.Background(), "test")
	if resp != "YAML回复：test" {
		t.Errorf("LLM response = %q, want %q", resp, "YAML回复：test")
	}
}

func TestLoader_LoadFromBytesInvalidYAML(t *testing.T) {
	l := plugin.NewLoader()
	if err := l.LoadFromBytes([]byte("invalid: yaml: [")); err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestLoader_LoadFromBytesUnknownPlugin(t *testing.T) {
	l := plugin.NewLoader()
	yamlData := []byte(`
plugins:
  asr:
    name: nonexistent
`)
	if err := l.LoadFromBytes(yamlData); err == nil {
		t.Error("expected error for unknown plugin, got nil")
	}
}

// ─── plugin.RegisterBuiltins ──────────────────────────────────────────────────────────

func TestRegisterBuiltins(t *testing.T) {
	// mock 包的 init() 已注册了 builtin，plugin.RegisterBuiltins 应包含 mock 插件
	r := plugin.NewRegistry()
	if err := plugin.RegisterBuiltins(r); err != nil {
		t.Fatalf("plugin.RegisterBuiltins: %v", err)
	}

	// 验证 mock 插件已注册
	if _, err := r.Create(plugin.PluginTypeASR, "mock"); err != nil {
		t.Errorf("Create ASR mock after plugin.RegisterBuiltins: %v", err)
	}
	if _, err := r.Create(plugin.PluginTypeTTS, "mock"); err != nil {
		t.Errorf("Create TTS mock after plugin.RegisterBuiltins: %v", err)
	}
	if _, err := r.Create(plugin.PluginTypeLLM, "mock"); err != nil {
		t.Errorf("Create LLM mock after plugin.RegisterBuiltins: %v", err)
	}
	if _, err := r.Create(plugin.PluginTypeDetector, "energy"); err != nil {
		t.Errorf("Create Detector energy after plugin.RegisterBuiltins: %v", err)
	}
}

// ─── plugin.PluginType 常量 ──────────────────────────────────────────────────────────────

func TestPluginTypeConstants(t *testing.T) {
	tests := []struct {
		got, want plugin.PluginType
	}{
		{plugin.PluginTypeASR, plugin.PluginType("asr")},
		{plugin.PluginTypeTTS, plugin.PluginType("tts")},
		{plugin.PluginTypeLLM, plugin.PluginType("llm")},
		{plugin.PluginTypeDetector, plugin.PluginType("detector")},
		{plugin.PluginTypeRecorder, plugin.PluginType("recorder")},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("plugin.PluginType = %q, want %q", tt.got, tt.want)
		}
	}
}
