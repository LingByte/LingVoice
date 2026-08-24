# 04 — Rust ↔ Go 接口契约

这是双语言分离的命门。契约定义得好，两层可以独立演进、独立部署、独立替换；定义得差，Rust/Go 耦合成一团，分离就失去意义。

## 设计目标

1. **Go 不代理媒体字节**：音频流尽量 Rust↔插件直连，Go 只做控制决策。
2. **契约稳定**：proto 字段只增不删，版本化演进。
3. **控制与媒体分离**：控制 RPC（建会话、加轨、路由）走普通 gRPC；媒体流走双向流 gRPC。
4. **可绕过 Go 的直连路径**：Rust 可以被指示把某条音频轨直接推给一个 gRPC 插件端点，或从一个端点拉流。

## gRPC 服务总览

```
proto/
├── media.proto     # MediaNode service — Rust 暴露给 Go 的控制 + 媒体接口
├── audio.proto     # AudioFrame / TrackRef / SinkConfig / EgressTarget
├── signal.proto    # 信令透传 (offer/answer/candidate)
└── plugin.proto    # CapabilityPlugin service — 插件暴露给 Go/Rust 的能力接口
```

## MediaNode service（Rust 实现，Go 调用）

```proto
// proto/media.proto
service MediaNode {
  // —— 会话/轨道控制 (普通 unary RPC) ——
  rpc CreateSession(CreateSessionReq) returns (CreateSessionResp);
  rpc CloseSession(CloseSessionReq) returns (CloseSessionResp);
  rpc AddTrack(AddTrackReq) returns (AddTrackResp);
  rpc RemoveTrack(RemoveTrackReq) returns (RemoveTrackResp);
  rpc Route(RouteReq) returns (RouteResp);              // 轨间路由 / 混音
  rpc StartRecording(StartRecordingReq) returns (StartRecordingResp);
  rpc StopRecording(StopRecordingReq) returns (StopRecordingResp);

  // —— 信令透传 ——
  rpc HandleSignaling(stream SignalingFrame) returns (stream SignalingFrame);

  // —— 媒体直连 (Go 可选代理, 或指示 Rust 直连插件) ——
  rpc PublishAudio(stream AudioFrame) returns (PublishAck);   // Go 推 TTS 音频进来
  rpc SubscribeAudio(SubscribeReq) returns (stream AudioFrame); // Go 拉用户音频 (给 ASR)

  // —— 媒体 egress 直连插件 (绕过 Go) ——
  rpc StartEgress(EgressReq) returns (EgressResp);   // 指示 Rust 把 track 直推到 grpc endpoint
  rpc StartIngress(IngressReq) returns (IngressResp); // 指示 Rust 从 grpc endpoint 拉流并入轨
}
```

## 音频帧与轨道引用

```proto
// proto/audio.proto
message AudioFrame {
  string track_id = 1;
  uint32 sample_rate = 2;
  uint32 channels = 3;
  string codec = 4;          // "opus" | "pcm" | "g711"
  uint64 rtp_ts = 5;
  uint64 pts_ms = 6;
  bytes  payload = 7;
  bool   marker = 8;         // RTP marker bit
}

message TrackRef { string track_id = 1; string session_id = 2; }

message EgressTarget {
  string uri = 1;            // "grpc://asr-plugin.default.svc:7000/stream"
  string codec = 2;          // 期望编码
  uint32 sample_rate = 3;
  map<string,string> headers = 4;  // 如 session/track 元数据
}

message SinkConfig {
  oneof target {
    string file_path = 1;
    string s3_uri = 2;
    string rtmp_uri = 3;
  }
  string format = 4;         // "wav" | "mp3" | "flac" | "mkv"
  bool   mix = 5;            // 是否混音多轨
  repeated string track_ids = 6;
}
```

## 两条音频路径

### 路径 A：Go 代理（v1，简单）

```
Rust media ──SubscribeAudio──▶ Go ──▶ ASR plugin (gRPC stream)
                                  ◀── transcript
TTS plugin ──▶ Go ──PublishAudio──▶ Rust media ──▶ 用户
```

适用：Phase 1，单节点，音频量不大。Go 充当"大脑 + 中转"。代价是 Go 处理音频字节，但 20ms/帧 ~2KB 的量级 Go 完全扛得住。

### 路径 B：Rust 直连插件（v2，优化）

```
Rust media ──StartEgress(grpc://asr:7000)──▶ ASR plugin ──transcript──▶ Go (控制通道)
TTS plugin ◀──StartIngress(grpc://tts:7000)── Rust media ◀── 用户
   TTS plugin ──text──▶ Go (控制)
```

Go 只收发**控制消息**（transcript 文本、TTS 文本指令），不碰音频字节。Rust 与插件之间是 gRPC 双向流，音频零次经过 Go。

**关键点**：插件需要同时实现 `CapabilityPlugin`（给 Go 的控制/文本接口）和接受 `AudioFrame` 流（给 Rust 的媒体接口）。详见 [02-plugin-system.md](./02-plugin-system.md) 的"双通道插件"。

## 信令透传

WebRTC/SIP 的 SDP offer/answer、ICE candidate 不在 Go 里处理，而是透传到 Rust media node：

```proto
// proto/signal.proto
message SignalingFrame {
  string session_id = 1;
  oneof payload {
    SdpOffer  offer = 2;
    SdpAnswer answer = 3;
    IceCandidate candidate = 4;
    Bye bye = 5;
  }
}
```

Go 信令网关接收客户端信令（WS/HTTP），按 sessionID 查 etcd 找到 media node，把信令帧透传过去。Rust 负责真正的 WebRTC/SIP 协议栈。

## 编码与性能

- **v1 用 protobuf**：20ms 音频帧 ~2KB，protobuf 序列化开销可忽略；调试友好。
- **热路径优化（后期）**：高频 egress 可切到 shared memory（同机）或 FlatBuffers（跨机），契约不变，只换 transport 层。
- **背压**：`PublishAudio` / `SubscribeAudio` 流支持 window 反压，Rust 慢消费时 Go 阻塞或丢老帧（按策略）。

## 版本化

- proto 用 `buf` 管理，breaking change 走 v2 service（`MediaNodeV2`），旧 service 保留过渡。
- Rust/Go 各自从 proto 生成代码，`proto/` 是唯一真相源。
