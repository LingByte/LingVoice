//! lm-telemetry — 统计与健康状态

use serde::{Deserialize, Serialize};

/// 健康状态
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HealthStatus {
    /// 节点 ID
    pub node_id: String,
    /// 活跃会话数
    pub active_sessions: u32,
    /// 活跃端点数
    pub active_endpoints: u32,
    /// 总接收包数
    pub total_packets_received: u64,
    /// 总发送包数
    pub total_packets_sent: u64,
    /// CPU 使用率（0.0-1.0）
    pub cpu_usage: f64,
    /// 内存使用（MB）
    pub memory_usage_mb: u64,
}

/// 轨道统计
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct TrackStats {
    pub track_id: String,
    pub packets_received: u64,
    pub packets_sent: u64,
    pub packets_lost: u64,
    pub bytes_received: u64,
    pub bytes_sent: u64,
    pub jitter_ms: u32,
    pub rtt_ms: u32,
    pub loss_pct: f32,
    pub bitrate_kbps: u32,
}

/// 会话统计
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Stats {
    pub session_id: String,
    pub duration_ms: u64,
    pub tracks: Vec<TrackStats>,
}
