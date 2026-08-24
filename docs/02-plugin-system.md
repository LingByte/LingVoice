# 02 — 插件系统

## 设计目标

1. **能力即插件**：ASR/TTS/LLM/Recorder/Detector/Tool 全部是插件，核心只定义契约。
2. **双形态同接口**：进程内（Go registry）与进程外（gRPC sidecar）对上层完全一致。
3. **配置驱动**：YAML 声明插件，启动时加载、校验、按依赖拓扑启动。
4. **可远程化**：插件可独立部署、独立扩缩、跨语言（Python ASR 模型、闭源 TTS）。
5. **双通道**：插件既有"控制通道"（给 Go 的文本/指令），又有"媒体通道"（给 Rust 的音频帧），支持 [04](./04-interface-contract.md) 的直连路径 B。

## Plugin 接口

```go
// control/pkg/plugin/plugin.go
type Plugin interface {
    Manifest() Manifest
    Configure(cfg Config) error
    Start(ctx Context) error
    Stop() error
}

type Manifest struct {
    Name         string
    Version      string
    Capabilities []CapabilityTag  // {"asr","tts","llm","recorder","detector","tool"}
    ConfigSchema json.RawMessage  // JSON Schema, 加载时校验
    Dependencies []string         // 依赖的其他插件名
    MediaEndpoint string          // 若为进程外且支持媒体直连, 填 grpc://host:port
}

type Context struct {
    Logger    Logger
    Metrics   Metrics
    Registry  *Registry         // 可查找其他插件
    NodeID    string
}
```

## 能力契约

每个能力是一个 Go interface，插件实现它。契约全部**流式**，适配实时场景。

```go
// control/pkg/capability/asr.go
type ASR interface {
    Stream(ctx context.Context, opts ASROpts) (ASRStream, error)
}
type ASRStream interface {
    SendAudio(frame AudioFrame) error   // 推音频 (路径 A: Go 代理; 路径 B: Rust 直连, 此方法不被 Go 调)
    RecvTranscript() (Transcript, error) // 收转写 (partial/final)
    Close() error
}
type Transcript struct {
    Text    string
    IsFinal bool
    Lang    string
    Words   []Word   // 带时间戳, 可选
}
```

```go
// control/pkg/capability/tts.go
type TTS interface {
    Stream(ctx context.Context, opts TTSOpts) (TTSStream, error)
}
type TTSStream interface {
    SendText(text string, isFinal bool) error  // 流式 SSZ/文本
    RecvAudio() (AudioFrame, error)             // 收音频 (路径 A: Go 转给 Rust; 路径 B: Rust 直接从插件拉)
    Close() error
}
```

```go
// control/pkg/capability/llm.go
type LLM interface {
    Chat(ctx context.Context, opts LLMOpts) (LLMStream, error)
}
type LLMStream interface {
    Send(messages []Message) error
    Recv() (Chunk, error)   // text delta / tool_call / usage
    Close() error
}
```

```go
// control/pkg/capability/recorder.go
type Recorder interface {
    Open(ctx context.Context, cfg SinkConfig) (RecordSink, error)
}
type RecordSink interface {
    Write(frame AudioFrame) error
    Close() error
}
```

## 双形态插件

### 进程内插件（in-process）

```go
// control/cmd/plugins/openai/asr.go
type OpenAIASR struct{ cfg OpenAIConfig }
func (p *OpenAIASR) Manifest() Manifest { return Manifest{Name:"openai-asr", Capabilities:[]{"asr"}, ...} }
func (p *OpenAIASR) Configure(c Config) error { ... }
func (p *OpenAIASR) Start(ctx Context) error  { ... }
func (p *OpenAIASR) Stream(...) (ASRStream, error) { ... }   // 实现 capability.ASR

func init() { plugin.Register("openai-asr", func() Plugin { return &OpenAIASR{} }) }
```

零开销，Go 原生。通过 `init()` 注册或显式 import 触发。

### 进程外插件（out-of-process）

用 gRPC 协议（hashicorp/go-plugin 思路，但用纯 gRPC 自定义）。中台 fork 子进程或连远端，把 `ASR.Stream` 转成 gRPC 双向流。

```proto
// proto/plugin.proto
service CapabilityPlugin {
  rpc Manifest(Empty) returns (ManifestResp);
  rpc Configure(ConfigReq) returns (ConfigureResp);
  rpc ASRStream(stream AudioFrame) returns (stream Transcript);   // 媒体通道
  rpc TTSStream(stream TextChunk) returns (stream AudioFrame);
  rpc LLMChat(LLMReq) returns (stream LLMChunk);
  // ...
}
```

Go 侧 `plugin.rpc` 包提供一个 `RemotePlugin` 适配器，实现 `Plugin` + 各 capability 接口，内部转 gRPC。上层无感知。

适用：Python ASR 模型（funasr/whisper）、闭源 TTS、不稳定插件隔离、独立扩缩。

## 双通道插件（路径 B 关键）

一个进程外插件可同时暴露：
- **控制通道**（给 Go）：`CapabilityPlugin.LLMChat`、ASR 的 transcript 回送
- **媒体通道**（给 Rust）：`CapabilityPlugin.ASRStream` 接受 `AudioFrame`

配置示例：

```yaml
plugins:
  - name: funasr-asr
    type: out-of-process
    command: ["python", "-m", "funasr_plugin"]
    capabilities: [asr]
    media_endpoint: grpc://funasr-plugin:7000   # Rust 直连此端点推音频
    config:
      model: paraformer-realtime
```

编排器决定走路径 A 还是 B：
- 路径 A：Go 调 `ASR.Stream`，Go 推音频、收转写。
- 路径 B：Go 调 Rust `StartEgress(grpc://funasr-plugin:7000)`，Rust 直推音频给插件；插件通过控制通道把 transcript 回送给 Go。

路径 B 时 Go 的 `ASRStream.SendAudio` 不被调用，Go 只 `RecvTranscript`。

## 加载与发现

```yaml
# config.yaml
plugins:
  - name: openai-asr
    type: in-process
    capabilities: [asr]
    config: { model: whisper-3, api_key: ${OPENAI_KEY} }
  - name: openai-tts
    type: in-process
    capabilities: [tts]
    config: { voice: alloy, api_key: ${OPENAI_KEY} }
  - name: openai-llm
    type: in-process
    capabilities: [llm]
    config: { model: gpt-4o, api_key: ${OPENAI_KEY} }
  - name: file-recorder
    type: in-process
    capabilities: [recorder]
    config: { root: /var/recordings }
  - name: funasr-asr
    type: out-of-process
    command: ["python", "-m", "funasr_plugin"]
    capabilities: [asr]
    media_endpoint: grpc://funasr-plugin:7000
    config: { model: paraformer-realtime }
```

Loader 流程：
1. 解析所有 manifest
2. JSON Schema 校验每个插件 config
3. 构建依赖图，拓扑排序
4. 按序实例化 → `Configure` → `Start`
5. 注册到 Registry，按 capability 索引（一个 capability 可有多个实现，按优先级/标签选择）

## 能力选择（多实现）

一个 capability 可注册多个插件（如同时有 openai-asr 和 funasr-asr）。选择策略：
- **会话级指定**：`CreateSession` 时指定 `asr: funasr-asr`
- **默认优先级**：manifest 声明 `priority`，未指定时取最高
- **按标签路由**：`lang=zh` 路由到 funasr，`lang=en` 路由到 openai

## 插件生命周期

```
Registered → Configured → Starting → Ready ⇄ Degraded → Stopping → Stopped
```

- `Degraded`：健康检查失败（如远端插件断连），编排器可降级到备用插件或返回错误。
- 健康检查：进程内走 `Health()` 方法；进程外走 gRPC health check。
- 热重载（后期）：watch config 变更，重建插件实例，不影响在跑会话（新会话用新实例）。

## 官方插件清单（规划）

| 插件 | 类型 | 阶段 |
|------|------|------|
| mock-asr / mock-tts / mock-llm | in-process | Phase 1 |
| openai-asr / openai-tts / openai-llm | in-process | Phase 1 末 |
| file-recorder / s3-recorder | in-process | Phase 2 |
| deepgram-asr | in-process | Phase 3 |
| funasr-asr (Python) | out-of-process | Phase 3 |
| volcengine-asr/tts | in-process | Phase 3 |
| livekit-recorder (混音录制) | in-process | Phase 3 |
