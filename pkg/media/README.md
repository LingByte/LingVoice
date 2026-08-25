# pkg/media

LingVoice 媒体平面核心包。纯 Go 实现，不依赖 CGO / Rust / C（`encoder/opus.go` 除外，它通过 `hraban/opus` 绑定 libopus）。

## 目录结构

```
pkg/media/
├── types.go          # 核心类型：MediaPacket / AudioPacket / DTMFPacket / CodecConfig / MediaSession 等
├── session.go        # 媒体会话生命周期：输入/输出 transport 循环、编解码、filter 链、状态机
├── router.go         # 输出路由：按 transport 分发 packet
├── stage.go          # pipeline stage 抽象
├── eventbus.go       # 事件总线：发布/订阅 packet/state/error/lifecycle 事件，多 worker 消费
├── executor.go       # AsyncTaskRunner：多 worker 异步任务池
├── processor.go      # 媒体处理器注册表
├── codec.go          # 编解码接口定义
├── resampler.go      # 采样率转换：cubic 插值 + StreamResampler（流式复用）
├── lowpass.go        # FIR 低通滤波器（降采样抗混叠）+ DC 阻塞高通
├── audio_util.go     # 音频工具函数
├── cache.go          # 本地文件缓存（MD5 key → 磁盘）
└── encoder/          # 编解码器子包（见下方）
```

## encoder 子包

```
encoder/
├── registry.go       # codec name → factory 注册表，CreateEncode / CreateDecode
├── pcm.go            # PCM → PCM passthrough + resample
├── g711.go           # G.711 A-law / μ-law 共享查表
├── pcma.go           # G.711 A-law 编解码 + factory
├── pcmu.go           # G.711 μ-law 编解码 + factory
├── g722.go           # G.722 纯 Go 编解码 + factory
├── opus.go           # Opus 编解码（hraban/opus, cgo libopus）
└── pool.go           # int16 / byte scratch buffer 对象池
```

### 支持的编解码

| Codec | Encode | Decode | 实现 |
|-------|--------|--------|------|
| `pcm`  | ✅ | ✅ | passthrough + resample |
| `pcmu` | ✅ | ✅ | 纯 Go G.711 μ-law, 8 kHz |
| `pcma` | ✅ | ✅ | 纯 Go G.711 A-law, 8 kHz |
| `g722` | ✅ | ✅ | 纯 Go G.722, 16 kHz |
| `opus` | ✅ | ✅ | cgo libopus, 48 kHz |

## Logger

全包统一使用 `github.com/LingByte/ling-base/common/logger`（zap）。包级 `log` 变量在 `eventbus.go` 中定义：

```go
var log = func() *zap.Logger {
    if logger.Lg != nil {
        return logger.Lg
    }
    return zap.NewNop()  // 测试未调用 logger.Init() 时的 fallback
}()
```

调用方式：
```go
log.With(logger.WithFields(map[string]interface{}{"sessionID": s.ID})...).Info("session started")
log.With(zap.Any("sessionID", s.ID)).Warn("output transport processing ended")
```

## 构建

```bash
# 默认构建（纯 Go + G.711 + G.722 + PCM，不需要 libopus）
go build ./pkg/media/...

# 运行测试
go test ./pkg/media/...

# 运行 opus 测试（需要 libopus: brew install opus）
CGO_ENABLED=1 go test -tags opus ./pkg/media/encoder/ -v

# 运行基准测试
go test -run=^$ -bench=. -benchmem ./pkg/media/...
go test -run=^$ -bench=. -benchmem ./pkg/media/encoder/...
```

## 基准测试结果

测试环境：Intel Core i5-7360U @ 2.30GHz, 4 核, macOS, Go 1.26。
帧大小：20ms（8 kHz = 160 samples / 320 bytes，16 kHz = 320 samples / 640 bytes）。

### pkg/media

| Benchmark | 单线程 | g=4 | g=16 | g=64 |
|-----------|--------|-----|------|------|
| ResamplePCM 8k→16k | 6459 ns | 3647 ns | 3519 ns | 3983 ns |
| StreamResampler 8k→16k (复用) | 7575 ns | 3370 ns | 3796 ns | 3347 ns |
| LowPassFIR 16k→8k | 24020 ns | 13339 ns | 13262 ns | 12675 ns |
| EventBus Publish | 1172 ns | 1151 ns | 1208 ns | 1048 ns |
| MediaCache BuildKey | 637 ns | 397 ns | 409 ns | 388 ns |

### pkg/media/encoder

| Benchmark | 单线程 | g=4 | g=16 | g=64 |
|-----------|--------|-----|------|------|
| PCMA Encode | 906 ns | 542 ns | 545 ns | 555 ns |
| PCMA Decode | 488 ns | 343 ns | 363 ns | 389 ns |
| PCMU Encode | 1063 ns | 590 ns | 590 ns | 600 ns |
| PCMU Decode | 676 ns | 490 ns | 460 ns | 520 ns |
| G.722 Encode | 1696 ns | 1059 ns | 1183 ns | 1063 ns |
| G.722 Decode | 3072 ns | 1865 ns | 1923 ns | 1812 ns |
| PcmToPcm (resample 8k→16k) | 9466 ns | 4013 ns | 4755 ns | 4775 ns |
| Factory PCMA RoundTrip | 16308 ns | 6175 ns | 7861 ns | 8969 ns |
| Pool (pooled vs alloc) | 95 ns vs 1694 ns | — | — | — |

### 关键发现

1. **G.711 编解码**：~500-1000 ns/op（20ms 帧），即 ~20μs 处理 1 秒音频，远超实时要求。并发下吞吐提升 1.5-2×。
2. **G.722 编解码**：~1700-3100 ns/op，比 G.711 慢 3-4×（ADPCM 状态机），仍远超实时。
3. **Resampler**：StreamResampler 复用比每次新建快（少 1 次 alloc），并发下 4 核提升 ~2×。
4. **LowPassFIR**：最慢的单帧操作（~24μs），因为 31-tap FIR 卷积。并发下 4 核提升 ~1.8×。
5. **EventBus**：~1μs/op（含 channel send + worker dispatch），并发下无明显瓶颈。
6. **Pool**：对象池比直接分配快 18×（95 ns vs 1694 ns），零内存分配路径。
7. **并发扩展性**：4 核机器上 g=4 已接近最优，g=16/g=64 因上下文切换开销不再提升甚至略降。