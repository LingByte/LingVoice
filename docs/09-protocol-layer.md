# 09 — 协议层设计（Go 控制面）

> **背景**：之前文档把"SIP 收发"笼统放进 Rust 实时媒体面，造成误解——SIP 涉及鉴权/路由/分配 AI，这些是业务决策，怎么能在媒体面？本文档纠正这个划分，明确协议层的归属和拆分边界。

## 一、问题：协议层不能笼统归一边

"协议层"是一个模糊的词。一个协议（如 SIP/WebRTC/WebSocket）实际包含**三种不同的东西**：

```
1. 协议传输 (transport):     字节收发, 消息解析, 事务状态机, 重传定时器
                             → 基础设施, 不含业务逻辑

2. 协议应用 (application):   鉴权, 路由, 转接, 呼叫控制, 分配 AI
                             → 业务逻辑, 必须在 Go

3. 媒体协商 (SDP):           编解码/端口/ICE 协商, 结果驱动 RTP 建立
                             → 和媒体绑定, 在 Rust
```

**之前的错误**：把第 1 层和第 3 层混在一起说"SIP 收发在 Rust"，没说清楚第 2 层（鉴权/路由/分配 AI）在 Go。

**正确做法**：协议层拆分——协议传输和媒体协商在 Rust，协议应用（业务决策）在 Go。

## 二、各协议的拆分边界

### SIP

```
SIP INVITE 到达
    │
    ▼
Rust (协议传输): UDP/TCP 收发, 解析消息, 提取 From/To/SDP, 管理事务状态机
    │
    │ gRPC 上报: "收到 INVITE, from=xxx, to=yyy, SDP=zzz"
    ▼
Go (协议应用): 鉴权 → 路由 → 分配 AI → 决策
    │
    │ gRPC 下发: "回复 401 挑战" 或 "接受, codec=opus, 转给 AI agent Y"
    ▼
Rust (协议传输): 发 401 或 200 OK
    │
    │ (如果 200 OK)
    ▼
Rust (媒体协商+媒体): SDP 协商结果 → 建立 RTP → 媒体流
```

| 子层 | 归属 | 理由 |
|------|------|------|
| SIP 消息收发 + 事务状态机 | Rust (`rsipstack`) | 成熟栈，SDP→RTP 耦合，无 GC 定时器精确 |
| 鉴权（401/407 挑战） | Go | 业务逻辑，需访问用户数据库 |
| 路由（INVITE 转给谁） | Go | 业务决策，需访问路由表/AI 分配 |
| 转接（REFER/Replaces） | Go | 业务决策，需会话状态 |
| SDP 协商（codec/端口/ICE） | Rust (`negotiate.rs`) | 结果直接驱动同进程 RTP 建立 |
| RTP/SRTP 媒体 | Rust (`rustrtc`) | 20ms 实时热路径 |

**为什么 SIP 协议传输留 Rust 不放 Go**：务实选择。Go 没有成熟的 SIP 栈（`go-sip` 等生态弱），`rsipstack` 已验证 5000 并发。SDP 协商结果直接驱动同进程 RTP 建立，跨 gRPC 同步增加复杂度。代价是每个 SIP 消息要 gRPC 往返 Go 做决策，但 SIP 信令容忍 100ms 级延迟。

### WebRTC

```
浏览器
    │
    ├── WebSocket (信令) ──────────────▶ Go 控制面
    │     传: SDP offer/answer, ICE candidate, 控制指令
    │
    └── WebRTC PeerConnection (媒体) ──▶ Rust 媒体面
          传: SRTP 音频字节, 20ms 帧
```

| 子层 | 归属 | 理由 |
|------|------|------|
| SDP offer/answer 交换协调 | Go | 信令，业务方决定接哪个客户端 |
| ICE candidate 协调 | Go | 信令，候选地址交换 |
| PeerConnection 生命周期 | Rust | 对象在 Rust，Go 通过 gRPC 调 `CreatePeerConnection`/`SetRemoteDescription`/`AddIceCandidate` |
| DTLS-SRTP/RTP/RTCP | Rust (`rustrtc`) | 20ms 实时热路径，GC 不可接受 |
| jitter buffer/NACK/PLI | Rust (`rustrtc`) | 实时媒体处理 |

**关键**：WebRTC 的信令通道（传 SDP/ICE 的那条路）在 Go，媒体通道（PeerConnection 的 SRTP 流）在 Rust。Go 协调 SDP 交换，Rust 执行 PeerConnection 操作。

### WebSocket

| 子层 | 归属 | 理由 |
|------|------|------|
| WS 作为信令通道（传 JSON 控制消息） | Go | 信令，goroutine 模型适合大量 WS 连接 |
| WS 作为媒体通道（voip_bridge 双向 PCM16） | Rust | 20ms 帧实时媒体，和 RTP 本质相同 |

**区分标准**：传的是控制消息还是媒体字节。信令 WS 在 Go，媒体 WS（voip_bridge）在 Rust。

### RTMP/WHIP/WHEP

| 子层 | 归属 | 理由 |
|------|------|------|
| 推流会话建立（握手/鉴权） | Go | 信令，业务方决定推流策略 |
| RTMP/WHIP 媒体流收发 | Rust | 媒体字节，20ms 帧 |

## 三、Go 协议层的职责

基于以上拆分，Go 协议层负责所有协议的**应用层（业务决策）**：

```
Go 协议层 (control/protocol/)
├── sip/          SIP 业务决策
│   ├── auth.go         鉴权 (401/407 挑战, digest 校验)
│   ├── router.go       路由 (INVITE 转给谁, 查路由表/分配 AI)
│   ├── transfer.go     转接 (REFER/Replaces 业务逻辑)
│   ├── registrar.go    注册管理 (REGISTER 业务逻辑, 位置表)
│   └── handler.go      gRPC 回调处理 (Rust 上报 SIP 事件 → Go 决策 → 下发指令)
│
├── webrtc/      WebRTC 信令协调
│   ├── signaling.go     SDP offer/answer 交换
│   ├── ice.go           ICE candidate 协调
│   └── session.go       PeerConnection 生命周期协调 (gRPC 调 Rust)
│
├── websocket/   WebSocket 信令通道
│   ├── gateway.go       信令 WS server (客户端连这里传 JSON)
│   └── message.go       控制消息协议 (SDP/ICE/开始录音/停止/转接)
│
├── rtmp/        RTMP/WHIP 推流会话
│   └── session.go       推流握手/鉴权/会话管理
│
└── common/      协议共享
    ├── event.go         协议事件统一抽象 (IncomingCall/Answered/Hangup/...)
    ├── command.go       协议指令统一抽象 (Forward/Reject/Transfer/...)
    └── codec.go         SDP/编解码能力描述 (与 Rust 协商用)
```

### Go 协议层与 Rust 的 gRPC 交互

```protobuf
// Rust → Go: 上报协议事件
service ProtocolEventSink {
  rpc OnSipEvent(SipEvent) returns (SipDecision);  // Rust 上报 SIP 事件, Go 返回决策
  rpc OnWebrtcEvent(WebrtcEvent) returns (WebrtcDecision);
}

// Go → Rust: 下发协议指令
service ProtocolCommand {
  rpc SendSipResponse(SipResponse) returns (Ack);  // Go 让 Rust 发 401/200 OK
  rpc CreatePeerConnection(PcConfig) returns (PcId);
  rpc SetRemoteDescription(PcSdp) returns (Ack);
  rpc AddIceCandidate(Candidate) returns (Ack);
}
```

**关键设计**：Go 不直接操作 SIP socket 或 PeerConnection，而是通过 gRPC 让 Rust 执行。Go 只做决策，Rust 做执行。

## 四、协议事件统一抽象

不同协议的信令事件需要统一抽象，让上层（会话管理/编排）不关心是 SIP 还是 WebRTC：

```go
// control/protocol/common/event.go

// ProtocolEvent 是所有协议信令事件的统一抽象
type ProtocolEvent interface {
    Type() EventType
    SessionID() string    // 协议层会话 ID (SIP Call-ID / WebRTC PC ID / WS Conn ID)
    From() string         // 主叫
    To() string           // 被叫
    SDP() *SDPInfo        // 媒体描述 (可能为空)
    Timestamp() time.Time
}

type EventType int
const (
    EventIncomingCall EventType = iota  // 来电 (SIP INVITE / WebRTC offer)
    EventAnswered                        // 接听 (SIP 200 OK / WebRTC answer)
    EventHangup                          // 挂断 (SIP BYE / WebRTC close)
    EventTransfer                        // 转接 (SIP REFER)
    EventRinging                         // 振铃 (SIP 180)
    EventError                           // 错误
)

// ProtocolCommand 是所有协议指令的统一抽象
type ProtocolCommand interface {
    CommandType() CommandType
    SessionID() string
}

type CommandType int
const (
    CmdForward CommandType = iota   // 转发 (指定目标)
    CmdReject                        // 拒绝 (指定原因)
    CmdAnswer                        // 接听
    CmdHangup                        // 挂断
    CmdTransfer                      // 转接 (指定目标)
    CmdStartRecording                // 开始录音
    CmdStopRecording                 // 停止录音
)
```

**上层（会话管理/编排）只看 `ProtocolEvent` 和 `ProtocolCommand`，不关心底层是 SIP 还是 WebRTC。** 协议适配器负责把 SIP/WebRTC/WebSocket 的具体消息翻译成统一事件/指令。

## 五、协议适配器模式

每个协议有一个适配器，把协议特定消息翻译成统一事件/指令：

```go
// control/protocol/sip/handler.go

// SipHandler 把 Rust 上报的 SIP 事件翻译成 ProtocolEvent,
// 把上层 ProtocolCommand 翻译成 SIP 指令下发给 Rust
type SipHandler struct {
    auth      *AuthService
    router    *RouterService
    sink      ProtocolEventSink  // 上报给会话管理
}

func (h *SipHandler) OnSipEvent(ctx context.Context, ev *SipEvent) (*SipDecision, error) {
    switch ev.Type {
    case SipEvent_INVITE:
        // 1. 鉴权
        if err := h.auth.Verify(ev.From, ev.AuthHeader); err != nil {
            return &SipDecision{Action: SipAction_SEND_401}, nil
        }
        // 2. 路由
        target, err := h.router.Route(ev.To, ev.SDP)
        if err != nil {
            return &SipDecision{Action: SipAction_SEND_404}, nil
        }
        // 3. 分配 AI / 转给坐席
        // 4. 上报给会话管理
        h.sink.OnEvent(&IncomingCallEvent{
            SessionID: ev.CallId,
            From:      ev.From,
            To:        ev.To,
            SDP:       parseSdp(ev.Sdp),
        })
        // 5. 返回决策给 Rust
        return &SipDecision{
            Action:   SipAction_SEND_200_OK,
            Codec:    target.Codec,
            RouteTo:  target.Destination,
        }, nil

    case SipEvent_BYE:
        h.sink.OnEvent(&HangupEvent{SessionID: ev.CallId})
        return &SipDecision{Action: SipAction_ACK}, nil
    }
}
```

## 六、与之前文档的修正

| 之前文档的说法 | 修正 |
|---------------|------|
| "SIP 收发在 Rust" | 不准确。SIP 协议传输在 Rust，SIP 业务决策（鉴权/路由/分配 AI）在 Go |
| "协议层在 Rust 实时媒体面" | 不准确。协议传输和媒体协商在 Rust，协议应用（业务决策）在 Go |
| "SIP 是信令协议" | 对，但 SIP 的传输层（收发/事务状态机）和 SDP 协商与媒体绑定，留 Rust |

**核心原则不变**：传控制消息 → Go，传媒体字节 → Rust。但 SIP 的协议传输层虽然传的是信令消息，因为与 SDP→RTP 耦合 + 成熟栈在 Rust，务实选择留 Rust，业务决策通过 gRPC 回调 Go。

## 七、Phase 1 协议层实现范围

用户决定先实现 Go 协议层。Phase 1 范围：

### 必做（打通 SIP 基本通话）

| 模块 | 功能 |
|------|------|
| `protocol/sip/handler.go` | gRPC 回调处理：Rust 上报 SIP 事件 → Go 决策 → 下发指令 |
| `protocol/sip/auth.go` | SIP 鉴权（digest auth） |
| `protocol/sip/router.go` | SIP 路由（分机号 → 目标，或 → AI agent） |
| `protocol/common/event.go` | 协议事件统一抽象 |
| `protocol/common/command.go` | 协议指令统一抽象 |
| `proto/protocol.proto` | Rust↔Go 协议层 gRPC 契约 |

### 可选（WebRTC 信令，若进度允许）

| 模块 | 功能 |
|------|------|
| `protocol/webrtc/signaling.go` | SDP offer/answer 交换 |
| `protocol/websocket/gateway.go` | 信令 WS server |

### 不做（后续 Phase）

- RTMP/WHIP 推流会话
- SIP REFER/Replaces 转接
- SIP 注册位置管理
- 多租户协议隔离

### 验证方式

1. Go 层 sipgo 收到 SIP INVITE → gRPC 上报控制面
2. Go 鉴权 + 路由 → 返回决策（接受/拒绝/转 AI）
3. Go 层执行决策 → 建立媒体
4. 两个 SIP 软电话互拨，验证 Go 协议层介入决策链路
