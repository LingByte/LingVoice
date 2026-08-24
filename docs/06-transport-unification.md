# 06 — 传输协议归一化

## 问题

中台要支持 WebRTC、SIP/RTP、WebSocket、RTMP、WHIP/WHEP 等多种协议接入。它们底层语义差异很大：

| | WebRTC | SIP/RTP | WebSocket | RTMP | WHIP/WHEP |
|---|---|---|---|---|---|
| 底层 | RTP over SRTP/UDP | RTP over UDP | TCP 字节流 | TCP + AMF | RTP (WebRTC) |
| 时间戳 | RTP ts + RTCP | RTP ts + RTCP | 无 | FLV timestamp | RTP ts |
| 序列号 | RTP seq | RTP seq | TCP 保序 | 无 | RTP seq |
| 帧边界 | RTP marker | RTP marker | 需自定义 | FLV tag | RTP marker |
| 抖动 | jitter buffer | jitter buffer | TCP 抹平→延迟 | TCP 抹平 | jitter buffer |
| 时钟域 | 发送方时钟 | 发送方时钟 | 接收方到达时刻 | 发送方 FLV ts | 发送方时钟 |

WebRTC 和 SIP 同构（都是 RTP），但 WebSocket 和 RTMP 是字节流，语义缺失。如果 media pipeline 直接处理各协议原生格式，会到处 `if protocol == ws`，污染核心。

## 核心思路：Transport 归一化层

**每个 transport 实现 `TrackSource`（入）和 `TrackSink`（出），产出/消费统一的 `MediaFrame`。media pipeline 永远只处理 `MediaFrame`，不感知来源协议。**

```
[WebRTC]  ─┐
[SIP/RTP] ─┤
[WebSocket]─┼──▶ TrackSource ──▶ MediaFrame ──▶ Media Pipeline (Router/Mixer/...)
[RTMP]    ─┤
[WHIP]    ─┘

Media Pipeline ──▶ MediaFrame ──▶ TrackSink ─┼──▶ [WebRTC]
                                              ├──▶ [SIP/RTP]
                                              ├──▶ [WebSocket]
                                              ├──▶ [RTMP]
                                              └──▶ [WHIP/WHEP]
```

## 统一 MediaFrame

```rust
// media/crates/media-core/src/frame.rs
pub struct MediaFrame {
    pub track_id: TrackId,
    pub kind: MediaKind,           // Audio | Video | Data
    pub codec: Codec,              // Opus | Pcm16 | G711 | H264 | Vp8 | Av1
    pub pts: Timestamp,            // 单调呈现时间戳 (会话主时钟域, 见下文)
    pub rtp_ts: Option<u32>,       // RTP 源保留原始 ts; WS/RTMP 源为 None
    pub seq: Option<u32>,          // RTP 源保留; 其他源合成本地计数器
    pub marker: bool,              // 帧边界
    pub payload: Bytes,            // 零拷贝 (bytes::Bytes)
    pub source: SourceKind,        // Webrtc | Sip | Ws | Rtmp | Whip | Ingress(直连)
    pub flags: FrameFlags,         // discontinuity | key_frame | ...
}
```

**设计要点**：
- `pts` 是 pipeline 内的**统一时钟**，所有协议归一化后都用它。
- `rtp_ts`/`seq` 保留原始值，供需要回写 RTP 的 sink（如转推 RTP）使用；非 RTP 源为 `None`。
- `payload` 是 `bytes::Bytes`（零拷贝引用计数），多订阅者共享同一份内存。
- `source` 标记来源，用于诊断/路由策略，不用于逻辑分支。

## TrackSource trait（入方向）

```rust
pub trait TrackSource: Send + Sync {
    /// 协议归一化: 从协议原生格式产出 MediaFrame
    fn next_frame(&mut self) -> impl Future<Output = Option<MediaFrame>>;
    fn track_kind(&self) -> MediaKind;
    fn codec(&self) -> Codec;
}

// 各协议实现:
// - WebRtcSource: 从 RTP packet 解出 payload, 映射 rtp_ts/seq/marker, pts 由 RTCP + 本地时钟校准
// - SipSource:    同 WebRTC (都是 RTP)
// - WsSource:     见下文 "WebSocket 适配"
// - RtmpSource:   解 FLV tag, FLV timestamp → pts
// - WhipSource:   同 WebRTC
```

## TrackSink trait（出方向）

```rust
pub trait TrackSink: Send + Sync {
    /// 把 MediaFrame 转成协议原生格式发出
    fn write_frame(&mut self, frame: MediaFrame) -> impl Future<Output = Result<()>>;
}

// - WebRtcSink: payload → RTP packet, pts → rtp_ts (若源非 RTP 则本地合成), 加 seq/marker
// - SipSink:    同上
// - WsSink:     见下文
// - RtmpSink:   MediaFrame → FLV tag
```

## WebSocket 适配（重点）

WebSocket 是字节流，缺失 RTP 语义。adapter 要**补齐**这些语义。

### 入方向：WsSource

WebSocket 音频有几种常见格式，adapter 按格式分支处理：

**格式 1：裸 Opus（一包一帧）**
- 每条 WS 消息 = 一个 Opus frame
- 帧边界天然（消息边界）
- PTS：用接收单调时钟 `now()`，或按 `sample_rate / frames_per_packet` 递增
- seq：本地计数器
- marker：每包都 true（每包是独立帧）

**格式 2：裸 PCM 流（连续字节）**
- WS 消息可能是任意大小的 PCM 块，不与帧对齐
- adapter 按 `frame_duration_ms`（默认 20ms）× `sample_rate` × `channels` × `bytes_per_sample` 切帧
- PTS：按帧时长递增（`pts += 20ms`）
- codec：标 `Pcm16`
- 需要 resample 到 pipeline 主采样率（若不一致）

**格式 3：带轻量应用层头（自定义协议）**
- 头里可能有 `pts`/`seq`/`codec`/`sample_rate`
- adapter 解头，映射到 `MediaFrame` 字段
- 推荐中台自定义一个简单 WS 音频子协议（见下文），让浏览器客户端用

### 出方向：WsSink

- Opus 源：直接发 payload（每帧一条 WS 消息）
- PCM 源：按帧发，或攒一小批发（减少消息数）
- 可选加轻量头（`pts + seq`），让浏览器做播放对齐/丢包检测

### 推荐的 WS 音频子协议

为了让浏览器/客户端有标准可循，定义一个简单的 WS 音频消息格式（JSON 头 + 二进制 payload，或纯二进制头）：

```
[2 字节 magic][1 字节 version][1 字节 codec][4 字节 pts_ms][4 字节 seq][2 字节 payload_len][payload...]
```

- 浏览器用 `AudioWorklet` 采集 PCM → 打包 → WS 发送
- 服务端 `WsSource` 解包，映射到 `MediaFrame`
- 反向同理

但这**不是强制的**——adapter 支持裸 Opus/PCM/自定义头三种模式，由会话配置或 WS 子协议协商决定。

## 时钟域统一（关键）

不同协议的时钟域不同，混音/路由/录制需要统一时钟：

```
WebRTC 源:  RTP ts (90kHz 或 codec-specific) + RTCP SR 校准 → 会话主时钟
SIP 源:     同上
WS 源:      接收方单调时钟 (tokio Instant) → 会话主时钟
RTMP 源:    FLV ts (ms) → 会话主时钟
```

**会话主时钟**：会话创建时选定一个基准（如 media node 启动后的单调时钟，或 NTP 校准）。所有 `TrackSource` 把自己的时钟映射到会话主时钟：

```rust
pub trait ClockDomain {
    fn to_session_clock(&self, source_ts: SourceTimestamp) -> Timestamp;
}

// RtpClockDomain: 用 RTCP SR 里的 NTP + RTP ts 对建立映射, 插值到会话时钟
// WsClockDomain:  接收 Instant 直接映射 (若会话主时钟也是 Instant 域)
// RtmpClockDomain: FLV ms → 会话时钟
```

**Mixer** 在混音前把所有 track 的 PTS 对齐到会话主时钟，按 PTS 排序对齐帧。**Recorder** 用会话主时钟写文件时间戳。

## 跨协议混音示例

场景：WebRTC 用户 A + SIP 用户 B + WebSocket 客户端 C，三方会议混音。

```
A (WebRTC/Opus) ──▶ WebRtcSource ──▶ MediaFrame(Opus, pts=session_clock)
                                          │
                                          ▼
                                     [Decoder] ──▶ MediaFrame(PCM, 48kHz)
                                          │
B (SIP/G711)    ──▶ SipSource ──▶ MediaFrame(G711) ─▶ [Decoder] ─▶ PCM ─┐
                                          │                            │
C (WS/Opus)     ──▶ WsSource ──▶ MediaFrame(Opus) ─▶ [Decoder] ─▶ PCM ─┤
                                                                       ▼
                                                                   [Mixer]
                                                                   (对齐 PTS, 重采样到统一 48kHz, 求和)
                                                                       │
                                                                       ▼
                                                                   [Encoder]
                                                                   (按各 sink 需要的 codec 编码)
                                                                       │
                                          ┌────────────────────────────┼────────────────────────────┐
                                          ▼                            ▼                            ▼
                                   WebRtcSink (Opus)            SipSink (G711)              WsSink (Opus)
                                          │                            │                            │
                                          ▼                            ▼                            ▼
                                       A 听到                      B 听到                       C 听到
```

关键：**Decoder/Mixer/Encoder 只处理 PCM MediaFrame，完全不感知 A/B/C 的来源协议**。协议差异在 Source/Sink 层吸收。

## 协议无关的 pipeline 组装

编排层（Go）创建会话时不写死协议，而是声明 track 的"角色"：

```proto
message AddTrackReq {
  string session_id = 1;
  string track_id = 2;
  MediaKind kind = 3;
  oneof transport {
    WebRtcTransport webrtc = 4;   // {ice_servers, ...}
    SipTransport sip = 5;         // {from, to, codec}
    WsTransport ws = 6;           // {subprotocol, codec, frame_duration_ms}
    RtmpTransport rtmp = 7;
  }
  string role = 8;                // "agent-listen" | "agent-speak" | "conference" | ...
}
```

Rust media node 根据 transport 类型实例化对应的 Source/Sink，但 pipeline 内部组装（router/mixer/decoder/encoder）完全一致，按 `role` 决定拓扑。

## 总结

| 问题 | 解法 |
|------|------|
| 协议语义差异 | Transport 层归一化到统一 `MediaFrame`，pipeline 不感知协议 |
| WS 缺时间戳 | WsSource 用接收时钟打 PTS，本地计数器合成 seq，按帧时长切 PCM |
| WS 帧边界 | Opus 一包一帧天然；裸 PCM 按 20ms 切；推荐自定义轻量头子协议 |
| 时钟域不统一 | 各 Source 实现 `ClockDomain`，映射到会话主时钟；Mixer 按 PTS 对齐 |
| 跨协议混音 | Source 解码到 PCM → Mixer 对齐 PTS 混音 → Encoder 按 sink 编码 |
| pipeline 协议无关 | 编排层声明 track 角色 + transport 参数，Rust 实例化对应 Source/Sink，pipeline 拓扑按 role 组装 |
