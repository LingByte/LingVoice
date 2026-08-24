# 03 — 分布式设计

## 拓扑

```
                    ┌──────────────────────┐
                    │   etcd cluster        │
                    │  (session→node 路由)  │
                    └──────────┬───────────┘
                               │
        ┌──────────────────────┼──────────────────────┐
        │                      │                      │
   ┌────▼─────┐          ┌────▼─────┐          ┌────▼─────┐
   │ control  │          │ control  │          │ control  │   (无状态, 多副本)
   │  node 1  │          │  node 2  │          │  node 3  │
   └────┬─────┘          └────┬─────┘          └────┬─────┘
        │                      │                      │
        │   gRPC (调度+媒体控制+音频直连)              │
        │                      │                      │
   ┌────▼─────┐          ┌────▼─────┐          ┌────▼─────┐
   │  media   │          │  media   │          │  media   │   (有状态, 会话亲和)
   │  node A  │          │  node B  │          │  node C  │     (Rust)
   └──────────┘          └──────────┘          └──────────┘
        │                      │                      │
   ┌────▼─────┐          ┌────▼─────┐          ┌────▼─────┐
   │ capability│         │ capability│         │ capability│  (插件, 可独立部署)
   │ plugins  │          │ plugins  │          │ plugins  │
   └──────────┘          └──────────┘          └──────────┘
```

## 集群状态（etcd）

### 键空间

```
/lingvoice/nodes/media/{nodeID}         → MediaNodeInfo (TTL 心跳)
/lingvoice/nodes/control/{nodeID}       → ControlNodeInfo
/lingvoice/sessions/{sessionID}         → SessionRouting
/lingvoice/plugins/{capability}/{name}  → PluginInfo (可选, 服务发现)
/lingvoice/leases/{nodeID}              → lease
```

### MediaNodeInfo

```go
type MediaNodeInfo struct {
    NodeID       string
    Addr         string            // gRPC 地址
    Region       string
    Labels       map[string]string // {"gpu":"true","codec":"opus,h264"}
    Load         LoadReport        // CPU/内存/活跃会话数/带宽
    Capabilities []string          // 支持的 transport / codec
    HeartbeatAt  int64
}
```

节点定期心跳（lease 续约），TTL 过期则标记下线。

### SessionRouting

```go
type SessionRouting struct {
    SessionID    string
    MediaNodeID  string
    ControlNodeID string  // 当前持有会话编排的 control 副本
    CreatedAt    int64
    State        string   // creating/active/closing/failed
}
```

## 会话调度

Scheduler 在 `CreateSession` 时选 media node：

1. 过滤：按 `region`、`labels`（如需要 GPU 跑某 ASR）、`capabilities`（如需 SIP transport）筛选候选
2. 打分：按 `Load`（活跃会话数、CPU、带宽）加权
3. 亲和：同一业务/room 优先同 node（减少跨节点）
4. 选定后写 etcd 路由表，向该 node 发 `CreateSession` gRPC

控制面副本之间无协调——靠 etcd 乐观锁（CAS 写 SessionRouting）防并发创建冲突。

## 信令路由

客户端连任意 control node 的信令网关：

```
client ──WS/HTTP──▶ control node
                     │
                     ├─ etcd 查 sessionID → mediaNodeID
                     │   (新会话: scheduler 选 node, 写路由)
                     │
                     └─ gRPC 透传信令到 media node
                         (HandleSignaling 双向流)
```

控制面副本无状态，任意副本都能服务任意客户端的信令。

## 故障转移

### Control node 故障
- 无状态，客户端重连其他副本即可
- 在跑会话的编排状态（agent loop 上下文）需持久化或可重建（见下）

### Media node 故障
- 心跳超时 → etcd 标记下线 → 关联会话标 `failed`
- **媒体流不可迁移**（RTP/WebRTC 连接绑定在原 node）
- 控制面通知业务方，业务侧重建会话（新会话调度到健康 node）
- 录音若已写部分文件，保留为分片，合并由后期处理

### 编排状态恢复
- agent loop 的对话历史可存 etcd（轻量）或外部存储
- 重建会话时按 sessionID 恢复历史，重新绑定到新 media node
- 不追求无缝（语音场景几秒中断可接受），追求可恢复

## SFU 级联（Phase 4）

跨 media node 的多方会议需要级联：

```
media node A (参与者 1,2) ←──back-to-back──▶ media node B (参与者 3,4)
```

- A 把混音流作为一条新 track 推给 B，B 反之
- 参与者只连本 node，跨 node 流量是单条混音轨，带宽可控
- 调度策略：尽量同地域同 node，跨地域才级联

## 多租户

- 每个会话带 `tenantID`，贯穿 etcd 路由、插件选择、配额
- 插件可按租户隔离配置（`tenantID → plugin override`）
- 资源配额：每租户最大并发会话、带宽、录音存储
- 网络隔离（后期）：media node 按 tenant 分组部署

## 可观测性

- **Metrics**：Prometheus，每会话/每节点指标（活跃流、帧率、丢包、ASR 延迟、TTS 首字节延迟）
- **Tracing**：OpenTelemetry，trace 贯穿 control→media→plugin，span 含 sessionID/trackID
- **Media diagnostics**：RTCP 统计、抖动、丢包率，Rust 侧采集上报
- **Logging**：结构化日志，带 sessionID/trackID 贯穿
