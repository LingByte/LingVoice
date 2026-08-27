//! lm-telemetry — 统计与健康状态收集
//!
//! 参考 forge-media metrics.rs + xiu StatisticsStream + pulsebeam recorder.rs。
//!
//! 架构：
//! - `TrackCounter`: per-track 原子计数器（零锁，热路径直接 fetch_add）
//! - `StatsCollector`: 全局收集器，管理 per-track/per-session 计数器
//! - `HealthCollector`: 节点级健康指标（CPU/内存/会话数）
//!
//! 热路径（push_rtp/pull_rtp）调用 `TrackCounter` 的原子操作，
//! 非热路径（health_check/get_stats）调用 `StatsCollector::snapshot()` 聚合。

use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use dashmap::DashMap;
use serde::{Deserialize, Serialize};

// ============================================================================
// 数据结构（保持向后兼容）
// ============================================================================

/// 健康状态
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HealthStatus {
    pub node_id: String,
    pub active_sessions: u32,
    pub active_endpoints: u32,
    pub total_packets_received: u64,
    pub total_packets_sent: u64,
    pub cpu_usage: f64,
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

// ============================================================================
// TrackCounter — per-track 原子计数器
// ============================================================================

/// Per-track 原子计数器
///
/// 热路径直接调用原子操作，无锁。
/// 参考 pulsebeam 的 AtomicU64 slot 设计。
#[derive(Debug)]
pub struct TrackCounter {
    pub track_id: String,
    pub session_id: String,
    packets_received: AtomicU64,
    packets_sent: AtomicU64,
    packets_lost: AtomicU64,
    bytes_received: AtomicU64,
    bytes_sent: AtomicU64,
    /// 最近一次码率计算的时间戳（ms）
    last_bitrate_calc_ms: AtomicU64,
    /// 上次计算时的 bytes_received
    last_bytes_received: AtomicU64,
    /// 上次计算时的 bytes_sent
    last_bytes_sent: AtomicU64,
    /// 计算出的接收码率 kbps
    recv_bitrate_kbps: AtomicU64,
    /// 计算出的发送码率 kbps
    send_bitrate_kbps: AtomicU64,
}

impl TrackCounter {
    pub fn new(track_id: impl Into<String>, session_id: impl Into<String>) -> Self {
        Self {
            track_id: track_id.into(),
            session_id: session_id.into(),
            packets_received: AtomicU64::new(0),
            packets_sent: AtomicU64::new(0),
            packets_lost: AtomicU64::new(0),
            bytes_received: AtomicU64::new(0),
            bytes_sent: AtomicU64::new(0),
            last_bitrate_calc_ms: AtomicU64::new(0),
            last_bytes_received: AtomicU64::new(0),
            last_bytes_sent: AtomicU64::new(0),
            recv_bitrate_kbps: AtomicU64::new(0),
            send_bitrate_kbps: AtomicU64::new(0),
        }
    }

    #[inline]
    pub fn record_received(&self, bytes: u64) {
        self.packets_received.fetch_add(1, Ordering::Relaxed);
        self.bytes_received.fetch_add(bytes, Ordering::Relaxed);
    }

    #[inline]
    pub fn record_sent(&self, bytes: u64) {
        self.packets_sent.fetch_add(1, Ordering::Relaxed);
        self.bytes_sent.fetch_add(bytes, Ordering::Relaxed);
    }

    #[inline]
    pub fn record_lost(&self, count: u64) {
        self.packets_lost.fetch_add(count, Ordering::Relaxed);
    }

    /// 计算瞬时码率（调用时计算，基于自上次调用以来的字节差）
    pub fn update_bitrate(&self, now_ms: u64) {
        let last_ms = self.last_bitrate_calc_ms.load(Ordering::Relaxed);
        if last_ms == 0 {
            self.last_bitrate_calc_ms.store(now_ms, Ordering::Relaxed);
            self.last_bytes_received.store(
                self.bytes_received.load(Ordering::Relaxed),
                Ordering::Relaxed,
            );
            self.last_bytes_sent
                .store(self.bytes_sent.load(Ordering::Relaxed), Ordering::Relaxed);
            return;
        }
        let elapsed_ms = now_ms.saturating_sub(last_ms);
        if elapsed_ms < 1000 {
            return;
        }
        let cur_recv = self.bytes_received.load(Ordering::Relaxed);
        let cur_sent = self.bytes_sent.load(Ordering::Relaxed);
        let last_recv = self.last_bytes_received.load(Ordering::Relaxed);
        let last_sent = self.last_bytes_sent.load(Ordering::Relaxed);

        let recv_delta_bits = cur_recv.saturating_sub(last_recv) * 8;
        let sent_delta_bits = cur_sent.saturating_sub(last_sent) * 8;
        let recv_kbps = (recv_delta_bits / elapsed_ms) as u64;
        let sent_kbps = (sent_delta_bits / elapsed_ms) as u64;

        self.recv_bitrate_kbps.store(recv_kbps, Ordering::Relaxed);
        self.send_bitrate_kbps.store(sent_kbps, Ordering::Relaxed);
        self.last_bitrate_calc_ms.store(now_ms, Ordering::Relaxed);
        self.last_bytes_received.store(cur_recv, Ordering::Relaxed);
        self.last_bytes_sent.store(cur_sent, Ordering::Relaxed);
    }

    /// 快照为 TrackStats
    pub fn snapshot(&self) -> TrackStats {
        let packets_received = self.packets_received.load(Ordering::Relaxed);
        let packets_sent = self.packets_sent.load(Ordering::Relaxed);
        let packets_lost = self.packets_lost.load(Ordering::Relaxed);
        let bytes_received = self.bytes_received.load(Ordering::Relaxed);
        let bytes_sent = self.bytes_sent.load(Ordering::Relaxed);

        let loss_pct = if packets_received > 0 {
            (packets_lost as f32 / packets_received as f32) * 100.0
        } else {
            0.0
        };

        TrackStats {
            track_id: self.track_id.clone(),
            packets_received,
            packets_sent,
            packets_lost,
            bytes_received,
            bytes_sent,
            jitter_ms: 0,
            rtt_ms: 0,
            loss_pct,
            bitrate_kbps: self.recv_bitrate_kbps.load(Ordering::Relaxed) as u32,
        }
    }
}

// ============================================================================
// StatsCollector — 全局统计收集器
// ============================================================================

/// 全局统计收集器
///
/// 管理 per-track 计数器，提供聚合查询。
/// 热路径通过 `get_track_counter()` 获取 Arc<TrackCounter> 后直接调用原子操作。
pub struct StatsCollector {
    /// (session_id, track_id) → Arc<TrackCounter>
    tracks: DashMap<(String, String), Arc<TrackCounter>>,
    /// 全局聚合计数器
    total_packets_received: AtomicU64,
    total_packets_sent: AtomicU64,
}

impl Default for StatsCollector {
    fn default() -> Self {
        Self::new()
    }
}

impl StatsCollector {
    pub fn new() -> Self {
        Self {
            tracks: DashMap::new(),
            total_packets_received: AtomicU64::new(0),
            total_packets_sent: AtomicU64::new(0),
        }
    }

    /// 注册 track 计数器
    pub fn register_track(&self, session_id: &str, track_id: &str) -> Arc<TrackCounter> {
        let counter = Arc::new(TrackCounter::new(track_id, session_id));
        self.tracks.insert(
            (session_id.to_string(), track_id.to_string()),
            counter.clone(),
        );
        counter
    }

    /// 获取 track 计数器
    pub fn get_track_counter(&self, session_id: &str, track_id: &str) -> Option<Arc<TrackCounter>> {
        self.tracks
            .get(&(session_id.to_string(), track_id.to_string()))
            .map(|c| c.clone())
    }

    /// 注销 track 计数器
    pub fn unregister_track(&self, session_id: &str, track_id: &str) {
        self.tracks
            .remove(&(session_id.to_string(), track_id.to_string()));
    }

    /// 按 session 批量注销
    pub fn unregister_session(&self, session_id: &str) {
        self.tracks.retain(|(sid, _), _| sid != session_id);
    }

    /// 累加全局接收计数（热路径调用）
    #[inline]
    pub fn add_packets_received(&self, count: u64) {
        self.total_packets_received
            .fetch_add(count, Ordering::Relaxed);
    }

    /// 累加全局发送计数（热路径调用）
    #[inline]
    pub fn add_packets_sent(&self, count: u64) {
        self.total_packets_sent.fetch_add(count, Ordering::Relaxed);
    }

    /// 获取 session 级别统计
    pub fn session_stats(&self, session_id: &str) -> Vec<TrackStats> {
        self.tracks
            .iter()
            .filter(|r| r.key().0 == session_id)
            .map(|r| r.value().snapshot())
            .collect()
    }

    /// 获取所有 track 计数器（用于 health check 聚合）
    pub fn all_track_counters(&self) -> Vec<Arc<TrackCounter>> {
        self.tracks.iter().map(|r| r.value().clone()).collect()
    }

    /// 健康检查快照
    pub fn health_snapshot(
        &self,
        node_id: &str,
        active_sessions: u32,
        active_endpoints: u32,
    ) -> HealthStatus {
        HealthStatus {
            node_id: node_id.to_string(),
            active_sessions,
            active_endpoints,
            total_packets_received: self.total_packets_received.load(Ordering::Relaxed),
            total_packets_sent: self.total_packets_sent.load(Ordering::Relaxed),
            cpu_usage: get_cpu_usage(),
            memory_usage_mb: get_memory_usage_mb(),
        }
    }

    /// 更新所有 track 的瞬时码率
    pub fn update_all_bitrates(&self, now_ms: u64) {
        for r in self.tracks.iter() {
            r.value().update_bitrate(now_ms);
        }
    }

    /// track 数量
    pub fn track_count(&self) -> usize {
        self.tracks.len()
    }
}

// ============================================================================
// 系统指标采集
// ============================================================================

/// 获取 CPU 使用率（0.0-1.0）
///
/// 实现策略：
/// - Linux: 读取 `/proc/stat` 两次采样，计算 delta 得到区间 CPU 使用率
/// - macOS: 使用 `host_processor_info` (Mach API) 两次采样计算
/// - 跨平台 fallback: 使用进程自身的 CPU 时间（user + system / elapsed）
///
/// 返回值范围: 0.0..=1.0
fn get_cpu_usage() -> f64 {
    let usage = get_cpu_usage_inner();
    // clamp 到 [0.0, 1.0] 避免异常值
    usage.clamp(0.0, 1.0)
}

/// 内部实现：返回原始 CPU 使用率（可能略超 1.0，由调用方 clamp）
fn get_cpu_usage_inner() -> f64 {
    #[cfg(target_os = "linux")]
    {
        if let Some(usage) = get_cpu_usage_linux() {
            return usage;
        }
    }
    #[cfg(target_os = "macos")]
    {
        if let Some(usage) = get_cpu_usage_macos() {
            return usage;
        }
    }
    // 跨平台 fallback: 进程自身 CPU 使用率
    get_cpu_usage_process_fallback()
}

/// Linux: 读取 /proc/stat 两次采样计算 CPU 使用率
#[cfg(target_os = "linux")]
fn get_cpu_usage_linux() -> Option<f64> {
    fn read_cpu_times() -> Option<(u64, u64)> {
        let content = std::fs::read_to_string("/proc/stat").ok()?;
        let first_line = content.lines().next()?;
        let parts: Vec<&str> = first_line.split_whitespace().collect();
        if parts.len() < 5 || parts[0] != "cpu" {
            return None;
        }
        // user, nice, system, idle, iowait, irq, softirq, steal...
        let mut fields: Vec<u64> = Vec::with_capacity(parts.len() - 1);
        for i in 1..parts.len() {
            fields.push(parts[i].parse().ok()?);
        }
        let idle = fields.get(3).copied().unwrap_or(0) + fields.get(4).copied().unwrap_or(0); // idle + iowait
        let total: u64 = fields.iter().sum();
        if total == 0 {
            return None;
        }
        Some((idle, total))
    }

    let (idle1, total1) = read_cpu_times()?;
    std::thread::sleep(std::time::Duration::from_millis(100));
    let (idle2, total2) = read_cpu_times()?;

    let total_delta = total2.saturating_sub(total1);
    let idle_delta = idle2.saturating_sub(idle1);
    if total_delta == 0 {
        return Some(0.0);
    }
    let usage = 1.0 - (idle_delta as f64 / total_delta as f64);
    Some(usage)
}

/// macOS: 使用 host_processor_info (Mach API) 两次采样计算 CPU 使用率
#[cfg(target_os = "macos")]
fn get_cpu_usage_macos() -> Option<f64> {
    extern "C" {
        fn mach_host_self() -> u32;
        fn host_processor_info(
            host: u32,
            flavor: u32,
            out_processor_count: *mut u32,
            cpu_info: *mut *mut i32,
            cpu_info_count: *mut u32,
        ) -> i32;
        fn vm_deallocate(target_task: u32, address: *mut i32, size: u32) -> i32;
    }

    const PROCESSOR_CPU_LOAD_INFO: u32 = 2;
    const HOST_CPU_LOAD_INFO_COUNT: u32 = 4; // CPU_STATE_MAX

    fn read_cpu_ticks() -> Option<(u64, u64)> {
        unsafe {
            let host = mach_host_self();
            let mut processor_count: u32 = 0;
            let mut cpu_info_ptr: *mut i32 = std::ptr::null_mut();
            let mut cpu_info_count: u32 = 0;

            let kr = host_processor_info(
                host,
                PROCESSOR_CPU_LOAD_INFO,
                &mut processor_count as *mut u32,
                &mut cpu_info_ptr as *mut *mut i32,
                &mut cpu_info_count as *mut u32,
            );
            if kr != 0 || cpu_info_ptr.is_null() || processor_count == 0 {
                return None;
            }

            // cpu_info_count = processor_count * HOST_CPU_LOAD_INFO_COUNT
            // 每个 CPU 有 4 个 tick 值: user, nice, system, idle
            let mut total_user: u64 = 0;
            let mut total_system: u64 = 0;
            let mut total_idle: u64 = 0;

            let info_slice =
                std::slice::from_raw_parts(cpu_info_ptr as *const i32, cpu_info_count as usize);
            for i in 0..processor_count as usize {
                let base = i * HOST_CPU_LOAD_INFO_COUNT as usize;
                total_user += info_slice[base] as u64;
                let _nice = info_slice[base + 1] as u64;
                total_system += info_slice[base + 2] as u64;
                total_idle += info_slice[base + 3] as u64;
            }

            // 释放 Mach 内存
            vm_deallocate(mach_host_self(), cpu_info_ptr, cpu_info_count * 4);

            let total = total_user + total_system + total_idle;
            if total == 0 {
                return None;
            }
            Some((total_idle, total))
        }
    }

    let (idle1, total1) = read_cpu_ticks()?;
    std::thread::sleep(std::time::Duration::from_millis(100));
    let (idle2, total2) = read_cpu_ticks()?;

    let total_delta = total2.saturating_sub(total1);
    let idle_delta = idle2.saturating_sub(idle1);
    if total_delta == 0 {
        return Some(0.0);
    }
    let usage = 1.0 - (idle_delta as f64 / total_delta as f64);
    Some(usage)
}

/// 跨平台 fallback: 使用进程自身的 CPU 时间计算使用率
///
/// - Linux: 两次采样 /proc/self/stat，计算 (utime+stime delta) / elapsed
/// - macOS/其他: 使用 `ps -o %cpu=` 获取进程 CPU 百分比
///
/// 这是单进程的 CPU 占用，多核环境下可能 > 1.0（由调用方 clamp）。
fn get_cpu_usage_process_fallback() -> f64 {
    #[cfg(target_os = "linux")]
    {
        use std::time::Instant;

        fn read_process_ticks() -> Option<u64> {
            let content = std::fs::read_to_string("/proc/self/stat").ok()?;
            let fields: Vec<&str> = content.split_whitespace().collect();
            // /proc/[pid]/stat: field 14=utime, 15=stime (clock ticks, 1-based)
            // 0-based index: 13, 14
            let utime: u64 = fields.get(13).and_then(|s| s.parse().ok()).unwrap_or(0);
            let stime: u64 = fields.get(14).and_then(|s| s.parse().ok()).unwrap_or(0);
            Some(utime + stime)
        }

        let clk_tck = unsafe { libc_clk_tck() } as f64;
        let (ticks1, t1) = match read_process_ticks() {
            Some(t) => (t, Instant::now()),
            None => return 0.0,
        };
        std::thread::sleep(std::time::Duration::from_millis(100));
        let (ticks2, t2) = match read_process_ticks() {
            Some(t) => (t, Instant::now()),
            None => return 0.0,
        };
        let elapsed = t2.duration_since(t1).as_secs_f64();
        if elapsed <= 0.0 {
            return 0.0;
        }
        let tick_delta = ticks2.saturating_sub(ticks1) as f64;
        let cpu_seconds = tick_delta / clk_tck;
        cpu_seconds / elapsed
    }
    #[cfg(not(target_os = "linux"))]
    {
        // macOS/其他: 使用 ps 命令获取进程 CPU 百分比
        use std::process::Command;
        let pid = std::process::id().to_string();
        let output = Command::new("ps")
            .args(["-p", &pid, "-o", "%cpu="])
            .output();
        match output {
            Ok(out) => {
                let text = String::from_utf8_lossy(&out.stdout);
                let pct: f64 = text.trim().parse().unwrap_or(0.0);
                pct / 100.0
            }
            Err(_) => 0.0,
        }
    }
}

/// 获取 Linux clock ticks per second (sysconf(_SC_CLK_TCK))
#[cfg(target_os = "linux")]
unsafe fn libc_clk_tck() -> i64 {
    extern "C" {
        fn sysconf(name: i32) -> i64;
    }
    const SC_CLK_TCK: i32 = 2;
    let ticks = sysconf(SC_CLK_TCK);
    if ticks > 0 {
        ticks
    } else {
        100
    }
}

/// 获取内存使用（MB）
fn get_memory_usage_mb() -> u64 {
    #[cfg(target_os = "linux")]
    {
        if let Ok(content) = std::fs::read_to_string("/proc/meminfo") {
            let mut mem_total = 0u64;
            let mut mem_available = 0u64;
            for line in content.lines() {
                if line.starts_with("MemTotal:") {
                    if let Some(v) = line.split_whitespace().nth(1) {
                        mem_total = v.parse().unwrap_or(0);
                    }
                } else if line.starts_with("MemAvailable:") {
                    if let Some(v) = line.split_whitespace().nth(1) {
                        mem_available = v.parse().unwrap_or(0);
                    }
                }
            }
            if mem_total > mem_available {
                return (mem_total - mem_available) / 1024;
            }
        }
    }
    #[cfg(target_os = "macos")]
    {
        if let Ok(output) = std::process::Command::new("vm_stat").output() {
            let text = String::from_utf8_lossy(&output.stdout);
            let mut page_size = 4096u64;
            let mut active = 0u64;
            let mut wired = 0u64;

            for line in text.lines() {
                if line.contains("page size of") {
                    if let Some(ps) = line.split("page size of ").nth(1) {
                        page_size = ps.trim_end_matches(" bytes").parse().unwrap_or(4096);
                    }
                }
                if let Some(val) = line.split(": ").nth(1) {
                    let val = val.trim_end_matches(".").replace(",", "");
                    let pages: u64 = val.parse().unwrap_or(0);
                    if line.contains("active") {
                        active = pages * page_size;
                    } else if line.contains("wired down") || line.contains("Wired down") {
                        wired = pages * page_size;
                    }
                }
            }
            return (active + wired) / (1024 * 1024);
        }
    }
    0
}

// ============================================================================
// 测试
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_track_counter_basic() {
        let counter = TrackCounter::new("track-1", "session-1");
        counter.record_received(160);
        counter.record_received(160);
        counter.record_sent(320);

        let stats = counter.snapshot();
        assert_eq!(stats.packets_received, 2);
        assert_eq!(stats.packets_sent, 1);
        assert_eq!(stats.bytes_received, 320);
        assert_eq!(stats.bytes_sent, 320);
        assert_eq!(stats.track_id, "track-1");
    }

    #[test]
    fn test_track_counter_loss_pct() {
        let counter = TrackCounter::new("track-1", "session-1");
        counter.record_received(160);
        counter.record_received(160);
        counter.record_received(160);
        counter.record_lost(1);

        let stats = counter.snapshot();
        assert_eq!(stats.packets_received, 3);
        assert_eq!(stats.packets_lost, 1);
        assert!((stats.loss_pct - 33.333334).abs() < 0.01);
    }

    #[test]
    fn test_track_counter_bitrate() {
        let counter = TrackCounter::new("track-1", "session-1");
        counter.record_received(16000);

        // 第一次调用只记录基线
        counter.update_bitrate(1000);

        // 1 秒后再记录
        counter.record_received(16000);

        counter.update_bitrate(2000);

        let stats = counter.snapshot();
        // 16000 bytes * 8 bits / 1000 ms = 128 kbps
        assert!(
            stats.bitrate_kbps > 0,
            "bitrate should be > 0, got {}",
            stats.bitrate_kbps
        );
    }

    #[test]
    fn test_stats_collector_register() {
        let collector = StatsCollector::new();
        let counter = collector.register_track("session-1", "track-1");
        counter.record_received(160);

        let stats = collector.session_stats("session-1");
        assert_eq!(stats.len(), 1);
        assert_eq!(stats[0].packets_received, 1);
    }

    #[test]
    fn test_stats_collector_unregister_session() {
        let collector = StatsCollector::new();
        collector.register_track("session-1", "track-a");
        collector.register_track("session-1", "track-b");
        collector.register_track("session-2", "track-c");

        assert_eq!(collector.track_count(), 3);

        collector.unregister_session("session-1");
        assert_eq!(collector.track_count(), 1);

        let stats = collector.session_stats("session-1");
        assert!(stats.is_empty());
    }

    #[test]
    fn test_stats_collector_health() {
        let collector = StatsCollector::new();
        collector.add_packets_received(100);
        collector.add_packets_sent(200);

        let health = collector.health_snapshot("node-1", 5, 10);
        assert_eq!(health.node_id, "node-1");
        assert_eq!(health.active_sessions, 5);
        assert_eq!(health.active_endpoints, 10);
        assert_eq!(health.total_packets_received, 100);
        assert_eq!(health.total_packets_sent, 200);
    }

    #[test]
    fn test_stats_collector_global_counters() {
        let collector = StatsCollector::new();
        collector.add_packets_received(50);
        collector.add_packets_received(30);
        collector.add_packets_sent(100);

        let health = collector.health_snapshot("node-1", 0, 0);
        assert_eq!(health.total_packets_received, 80);
        assert_eq!(health.total_packets_sent, 100);
    }

    #[test]
    fn test_track_counter_concurrent() {
        use std::thread;
        let counter = Arc::new(TrackCounter::new("track-1", "session-1"));
        let mut handles = vec![];

        for _ in 0..4 {
            let c = counter.clone();
            handles.push(thread::spawn(move || {
                for _ in 0..1000 {
                    c.record_received(160);
                }
            }));
        }
        for h in handles {
            h.join().unwrap();
        }

        let stats = counter.snapshot();
        assert_eq!(stats.packets_received, 4000);
        assert_eq!(stats.bytes_received, 4000 * 160);
    }

    #[test]
    fn test_cpu_usage_range() {
        let usage = get_cpu_usage();
        // CPU 使用率应在 0.0..=1.0 范围内
        assert!(
            (0.0..=1.0).contains(&usage),
            "cpu_usage should be in [0.0, 1.0], got {}",
            usage
        );
    }

    #[test]
    fn test_cpu_usage_health_snapshot() {
        let collector = StatsCollector::new();
        let health = collector.health_snapshot("node-1", 0, 0);
        // health_snapshot 内部调用 get_cpu_usage，应返回有效值
        assert!(
            (0.0..=1.0).contains(&health.cpu_usage),
            "health.cpu_usage should be in [0.0, 1.0], got {}",
            health.cpu_usage
        );
    }
}
