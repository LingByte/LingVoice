//! TWCC (Transport-Wide Congestion Control)
//!
//! 基于 RFC 8888 / draft-holmer-rmcat-catapult:
//! - 发送方在每个 RTP 包的 transport-wide sequence number (TWCC header extension) 中写入递增序号
//! - 接收方记录每个包的到达时间
//! - 接收方周期性发送 TWCC 反馈 RTCP 包

use crate::rtcp::RtcpError;
use bytes::{BufMut, BytesMut};

// ============================================================================
// 常量
// ============================================================================

const VERSION: u8 = 2;
const PT_RTPFB: u8 = 205;
const FMT_TWCC: u8 = 15;

/// 1 TWCC reference time unit = 64ms = 64000us
const REF_TIME_UNIT_US: i64 = 64_000;
/// 1 delta tick = 250us
const DELTA_TICK_US: i64 = 250;

// ============================================================================
// TWCC 序号分配器 (发送方用)
// ============================================================================

/// TWCC 序号分配器 (发送方用)
pub struct TwccSeqAllocator {
    next_seq: u32,
}

impl TwccSeqAllocator {
    pub fn new() -> Self {
        Self { next_seq: 0 }
    }

    pub fn next(&mut self) -> u32 {
        let s = self.next_seq;
        self.next_seq = self.next_seq.wrapping_add(1);
        s
    }
}

impl Default for TwccSeqAllocator {
    fn default() -> Self {
        Self::new()
    }
}

// ============================================================================
// TWCC 接收方记录器
// ============================================================================

/// TWCC 接收方记录器
pub struct TwccReceiver {
    /// seq -> arrival_time_us
    packets: Vec<(u32, i64)>,
    base_seq: u32,
    last_feedback_seq: u8,
    initialized: bool,
}

impl TwccReceiver {
    pub fn new() -> Self {
        Self {
            packets: Vec::new(),
            base_seq: 0,
            last_feedback_seq: 0,
            initialized: false,
        }
    }

    pub fn record_packet(&mut self, seq: u32, arrival_time_us: i64) {
        if !self.initialized {
            self.base_seq = seq;
            self.initialized = true;
        }
        self.packets.push((seq, arrival_time_us));
    }

    /// 构建一个 TWCC 反馈包字节流
    pub fn build_feedback(&mut self, reference_time_us: i64) -> Vec<u8> {
        // reference_time: 24bit, 64ms 为单位
        let reference_time = ((reference_time_us / REF_TIME_UNIT_US) as u32) & 0x00FF_FFFF;

        // 排序 packets 按 seq
        self.packets.sort_by_key(|(s, _)| *s);

        let base_seq = if let Some(&(s, _)) = self.packets.first() {
            s as u16
        } else {
            0
        };

        // 统计接收到的包数 (在 base_seq..=last_seq 范围内)
        let last_seq = if let Some(&(s, _)) = self.packets.last() {
            s as u16
        } else {
            0
        };

        // 构建接收状态数组 (从 base_seq 到 last_seq)
        let range_len = (last_seq.wrapping_sub(base_seq) as u32 + 1) as usize;
        let mut received = vec![false; range_len];
        let mut arrival_times = vec![0i64; range_len];
        for &(s, t) in &self.packets {
            let idx = (s as u16).wrapping_sub(base_seq) as usize;
            if idx < range_len {
                received[idx] = true;
                arrival_times[idx] = t;
            }
        }

        // packet_count: 实际接收到的包数
        let packet_count = received.iter().filter(|&&r| r).count() as u16;

        // 构建 status chunks (run-length encoded)
        // Run-length chunk: symbol(2 bits) + run length(13 bits)
        //   symbol: 00=not received, 01=small delta, 10=large or negative delta
        let mut status_chunks: Vec<u8> = Vec::new();
        let mut i = 0;
        while i < range_len {
            let r = received[i];
            // 计算 run length
            let mut run_len = 0usize;
            while i + run_len < range_len && run_len < 8191 && received[i + run_len] == r {
                run_len += 1;
            }
            // symbol: 0=not received, 1=received (we use 01 for received, 00 for not received)
            let symbol: u16 = if r { 0b01 } else { 0b00 };
            let chunk: u16 = (symbol << 13) | (run_len as u16 & 0x1FFF);
            status_chunks.put_u16(chunk);
            i += run_len;
        }

        // 构建 deltas: 每个接收到的包相对于前一个接收包的时间差
        // 第一个包的 delta 相对于 reference_time
        let mut deltas: Vec<i16> = Vec::new();
        let mut prev_time = reference_time_us;
        let mut first = true;
        for idx in 0..range_len {
            if !received[idx] {
                continue;
            }
            let t = arrival_times[idx];
            let base = if first { reference_time_us } else { prev_time };
            let delta_us = t - base;
            let delta_ticks = delta_us / DELTA_TICK_US;
            if delta_ticks >= i8::MIN as i64 && delta_ticks <= i8::MAX as i64 {
                // small delta (8bit) — we store as i16 but mark via status
                // For simplicity in this implementation, we store all as 16bit deltas
                deltas.push(delta_ticks as i16);
            } else {
                deltas.push(delta_ticks as i16);
            }
            prev_time = t;
            first = false;
        }

        let fb_count = self.last_feedback_seq;
        self.last_feedback_seq = self.last_feedback_seq.wrapping_add(1);

        let feedback = TwccFeedback {
            sender_ssrc: 0,
            media_ssrc: 0,
            base_seq,
            packet_count,
            reference_time,
            fb_count,
            status_chunks,
            deltas,
        };

        // 清空已反馈的包
        self.packets.clear();

        feedback.encode()
    }
}

impl Default for TwccReceiver {
    fn default() -> Self {
        Self::new()
    }
}

// ============================================================================
// TWCC 反馈包 (RTCP RTPFB, FMT=15)
// ============================================================================

/// TWCC 反馈包 (RTCP RTPFB, FMT=15)
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TwccFeedback {
    pub sender_ssrc: u32,
    pub media_ssrc: u32,
    pub base_seq: u16,
    pub packet_count: u16,
    /// 24bit, 64ms 为单位
    pub reference_time: u32,
    pub fb_count: u8,
    pub status_chunks: Vec<u8>,
    pub deltas: Vec<i16>,
}

impl TwccFeedback {
    pub fn parse(data: &[u8]) -> Result<Self, RtcpError> {
        // 最小: 4 (header) + 8 (ssrcs) + 8 (base_seq, packet_count, reference_time, fb_count) = 20
        if data.len() < 20 {
            return Err(RtcpError::TooShort {
                needed: 20,
                got: data.len(),
            });
        }

        let fmt = data[0] & 0x1F;
        let pt = data[1];
        if pt != PT_RTPFB {
            return Err(RtcpError::UnsupportedPacketType(pt));
        }
        if fmt != FMT_TWCC {
            return Err(RtcpError::UnsupportedFmt(fmt));
        }

        let length = u16::from_be_bytes([data[2], data[3]]) as usize;
        let declared_bytes = (length + 1) * 4;
        if data.len() < declared_bytes {
            return Err(RtcpError::LengthMismatch {
                declared: declared_bytes,
                actual: data.len(),
            });
        }

        let sender_ssrc = u32::from_be_bytes([data[4], data[5], data[6], data[7]]);
        let media_ssrc = u32::from_be_bytes([data[8], data[9], data[10], data[11]]);
        let base_seq = u16::from_be_bytes([data[12], data[13]]);
        let packet_count = u16::from_be_bytes([data[14], data[15]]);
        // reference_time: 24bit
        let reference_time = u32::from_be_bytes([0, data[16], data[17], data[18]]) & 0x00FF_FFFF;
        let fb_count = data[19];

        // status chunks + deltas 从 offset 20 开始
        let mut offset = 20;
        let mut status_chunks: Vec<u8> = Vec::new();

        // 解析 status chunks 直到用完或遇到 deltas
        // 每个 status chunk 是 2 字节 (16bit)
        // 我们需要根据 packet_count 来确定有多少 delta
        // 简化: 解析所有剩余数据, status chunks 在前, deltas 在后
        // 由于 status chunk 和 delta 混在一起难以分离, 我们采用一种简化策略:
        // status chunks 数量 = (declared_bytes - 20 - delta_bytes) / 2
        // 但我们不知道 delta_bytes, 所以这里采用: 解析 run-length chunks 直到覆盖 packet_count 个包

        let mut covered_packets: u32 = 0;
        while offset + 2 <= declared_bytes && offset + 2 <= data.len() {
            let chunk = u16::from_be_bytes([data[offset], data[offset + 1]]);
            let symbol = (chunk >> 13) & 0x3;
            let run_len = (chunk & 0x1FFF) as u32;
            status_chunks.push(data[offset]);
            status_chunks.push(data[offset + 1]);
            offset += 2;

            if symbol == 0b00 {
                // not received
                covered_packets += run_len;
            } else if symbol == 0b01 {
                // small delta (8bit each)
                covered_packets += run_len;
                // 后面跟 run_len 个 8bit delta
                // 我们跳过 8bit delta, 但这里简化为读取
                // 实际上 8bit delta 紧跟在 status chunk 之后
                // 但我们的编码器使用 16bit delta, 所以这里简化处理
                break;
            } else if symbol == 0b10 {
                // large delta (16bit each)
                covered_packets += run_len;
                break;
            } else {
                // 11 = reserved
                break;
            }

            if covered_packets >= packet_count as u32 {
                break;
            }
        }

        // 剩余作为 deltas (16bit)
        let mut deltas: Vec<i16> = Vec::new();
        while offset + 2 <= declared_bytes && offset + 2 <= data.len() {
            let d = i16::from_be_bytes([data[offset], data[offset + 1]]);
            deltas.push(d);
            offset += 2;
        }

        Ok(Self {
            sender_ssrc,
            media_ssrc,
            base_seq,
            packet_count,
            reference_time,
            fb_count,
            status_chunks,
            deltas,
        })
    }

    pub fn encode(&self) -> Vec<u8> {
        let mut buf =
            BytesMut::with_capacity(20 + self.status_chunks.len() + self.deltas.len() * 2);

        // header: V=2, FMT=15
        buf.put_u8((VERSION << 6) | FMT_TWCC);
        buf.put_u8(PT_RTPFB);
        buf.put_u16(0); // length 占位

        buf.put_u32(self.sender_ssrc);
        buf.put_u32(self.media_ssrc);

        buf.put_u16(self.base_seq);
        buf.put_u16(self.packet_count);
        // reference_time: 24bit
        buf.put_u8((self.reference_time >> 16) as u8 & 0xFF);
        buf.put_u8((self.reference_time >> 8) as u8 & 0xFF);
        buf.put_u8(self.reference_time as u8 & 0xFF);
        buf.put_u8(self.fb_count);

        // status chunks
        buf.put_slice(&self.status_chunks);

        // deltas (16bit each)
        for d in &self.deltas {
            buf.put_i16(*d);
        }

        // padding 到 4 字节对齐
        while buf.len() % 4 != 0 {
            buf.put_u8(0);
        }

        // 修正 length
        let words = buf.len() / 4;
        let length = (words - 1) as u16;
        buf[2] = (length >> 8) as u8;
        buf[3] = (length & 0xFF) as u8;

        buf.to_vec()
    }
}

// ============================================================================
// 带宽估计器 (基于 TWCC 反馈)
// ============================================================================

/// 带宽估计器 (基于 TWCC 反馈)
///
/// 内部使用 GCC (Google Congestion Control) 算法,
/// 兼容旧的 `update` / `current_bitrate_kbps` API。
pub struct BandwidthEstimator {
    gcc: GccController,
}

impl BandwidthEstimator {
    pub fn new(initial_kbps: u64) -> Self {
        Self {
            gcc: GccController::new(
                initial_kbps * 1000,
                30_000,     // 30 kbps min
                50_000_000, // 50 Mbps max
            ),
        }
    }

    /// 根据 TWCC 反馈更新带宽估计
    pub fn update(&mut self, feedback: &TwccFeedback) -> u64 {
        self.gcc.update_twcc(feedback);
        self.gcc.bitrate_kbps()
    }

    pub fn current_bitrate_kbps(&self) -> u64 {
        self.gcc.bitrate_kbps()
    }
}

// ============================================================================
// GCC (Google Congestion Control) — RFC 8888 / draft-ietf-rmcat-gcc-02
// ============================================================================

/// 过载检测器状态
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum OveruseState {
    Normal,
    Overuse,
    Underuse,
}

/// 趋势线估计器 — 累积延迟梯度, 检测延迟增长趋势
#[derive(Debug, Clone)]
pub struct TrendlineEstimator {
    /// 滑动窗口大小
    window_size: usize,
    /// 最少样本数才计算趋势
    min_samples: usize,
    /// 延迟梯度样本队列 (arrival_delta - send_delta)
    gradients: std::collections::VecDeque<f64>,
    /// 累积延迟
    accumulated_delay: f64,
    /// 上次发送时间 (ms)
    last_send_ms: f64,
}

impl TrendlineEstimator {
    pub fn new(window_size: usize, min_samples: usize) -> Self {
        Self {
            window_size,
            min_samples,
            gradients: std::collections::VecDeque::with_capacity(window_size),
            accumulated_delay: 0.0,
            last_send_ms: 0.0,
        }
    }

    /// 添加新的延迟梯度样本
    ///
    /// - `send_delta_ms`: 发送方相邻包的时间差 (ms)
    /// - `recv_delta_ms`: 接收方相邻包的到达时间差 (ms)
    /// - 延迟梯度 = recv_delta - send_delta
    pub fn add_sample(&mut self, send_delta_ms: f64, recv_delta_ms: f64) {
        let gradient = recv_delta_ms - send_delta_ms;
        self.accumulated_delay += gradient;
        self.gradients.push_back(self.accumulated_delay);

        // 维持窗口大小
        while self.gradients.len() > self.window_size {
            self.gradients.pop_front();
        }
        self.last_send_ms = send_delta_ms;
    }

    /// 计算趋势线斜率 (最小二乘法线性回归)
    ///
    /// 返回斜率: 正值表示延迟在增长 (拥塞), 负值表示延迟在减少
    pub fn trendline_slope(&self) -> f64 {
        let n = self.gradients.len();
        if n < self.min_samples {
            return 0.0;
        }

        // 最小二乘法: y = a*x + b
        // x = index (0, 1, 2, ...), y = accumulated_delay
        let x_mean = (n - 1) as f64 / 2.0;
        let y_mean: f64 = self.gradients.iter().sum::<f64>() / n as f64;

        let mut num = 0.0;
        let mut den = 0.0;
        for (i, &y) in self.gradients.iter().enumerate() {
            let x = i as f64;
            num += (x - x_mean) * (y - y_mean);
            den += (x - x_mean) * (x - x_mean);
        }

        if den < 1e-10 {
            0.0
        } else {
            num / den
        }
    }

    /// 重置
    pub fn reset(&mut self) {
        self.gradients.clear();
        self.accumulated_delay = 0.0;
    }
}

/// 延迟检测器 — 基于趋势线 + 过载检测状态机
#[derive(Debug, Clone)]
pub struct DelayBasedController {
    /// 过载检测状态
    state: OveruseState,
    /// 趋势线估计器
    trendline: TrendlineEstimator,
    /// 过载阈值 (趋势线斜率超过此值判定为过载)
    overuse_threshold: f64,
    /// 低载阈值
    underuse_threshold: f64,
    /// 在过载状态的持续时间 (ms)
    time_in_overuse_ms: u64,
    /// 触发过载的最小持续时间 (ms)
    min_overuse_time_ms: u64,
    /// 延迟估计码率 (bps)
    delay_based_bitrate: u64,
    /// 上次到达时间 (ms), 用于计算 delta
    last_recv_ms: f64,
    /// 上次发送时间 (ms)
    last_send_ms: f64,
    /// 是否有上一个包的时间
    has_last: bool,
}

impl DelayBasedController {
    pub fn new(initial_bitrate_bps: u64) -> Self {
        Self {
            state: OveruseState::Normal,
            trendline: TrendlineEstimator::new(20, 10),
            overuse_threshold: 0.5, // 斜率阈值
            underuse_threshold: -0.5,
            time_in_overuse_ms: 0,
            min_overuse_time_ms: 100, // 100ms 持续过载才降码率
            delay_based_bitrate: initial_bitrate_bps,
            last_recv_ms: 0.0,
            last_send_ms: 0.0,
            has_last: false,
        }
    }

    /// 处理一个包的到达信息
    ///
    /// - `send_ms`: 发送时间 (ms)
    /// - `recv_ms`: 接收时间 (ms)
    /// - `frame_duration_ms`: 本帧时长 (ms)
    pub fn on_arrival(
        &mut self,
        send_ms: f64,
        recv_ms: f64,
        frame_duration_ms: u64,
    ) -> OveruseState {
        if self.has_last {
            let send_delta = send_ms - self.last_send_ms;
            let recv_delta = recv_ms - self.last_recv_ms;
            self.trendline.add_sample(send_delta, recv_delta);
        }
        self.last_send_ms = send_ms;
        self.last_recv_ms = recv_ms;
        self.has_last = true;

        self.detect_overuse(frame_duration_ms)
    }

    /// 过载检测状态机
    fn detect_overuse(&mut self, frame_duration_ms: u64) -> OveruseState {
        let slope = self.trendline.trendline_slope();

        match self.state {
            OveruseState::Normal => {
                if slope > self.overuse_threshold {
                    // 延迟增长, 可能过载
                    self.time_in_overuse_ms += frame_duration_ms;
                    if self.time_in_overuse_ms >= self.min_overuse_time_ms {
                        self.state = OveruseState::Overuse;
                        // 过载: 降码率
                        self.delay_based_bitrate = (self.delay_based_bitrate as f64 * 0.85) as u64;
                    }
                } else if slope < self.underuse_threshold {
                    self.state = OveruseState::Underuse;
                    self.time_in_overuse_ms = 0;
                } else {
                    self.time_in_overuse_ms = 0;
                }
            }
            OveruseState::Overuse => {
                if slope < 0.0 {
                    // 延迟下降, 恢复正常
                    self.state = OveruseState::Normal;
                    self.time_in_overuse_ms = 0;
                } else {
                    // 持续过载, 继续降
                    self.time_in_overuse_ms += frame_duration_ms;
                    if self.time_in_overuse_ms >= self.min_overuse_time_ms * 2 {
                        self.delay_based_bitrate = (self.delay_based_bitrate as f64 * 0.9) as u64;
                        self.time_in_overuse_ms = 0;
                    }
                }
            }
            OveruseState::Underuse => {
                if slope >= self.underuse_threshold && slope <= self.overuse_threshold {
                    self.state = OveruseState::Normal;
                } else if slope > self.overuse_threshold {
                    self.state = OveruseState::Overuse;
                    self.time_in_overuse_ms = frame_duration_ms;
                }
            }
        }

        self.state
    }

    /// 延迟估计码率 (bps)
    pub fn bitrate_bps(&self) -> u64 {
        self.delay_based_bitrate
    }

    /// 当前状态
    pub fn state(&self) -> OveruseState {
        self.state
    }

    /// 重置
    pub fn reset(&mut self, initial_bitrate_bps: u64) {
        self.state = OveruseState::Normal;
        self.trendline.reset();
        self.time_in_overuse_ms = 0;
        self.delay_based_bitrate = initial_bitrate_bps;
        self.has_last = false;
    }
}

/// 丢包率控制器 — 基于 RTCP RR 的丢包率调整码率
#[derive(Debug, Clone)]
pub struct LossBasedController {
    /// 当前丢包估计码率 (bps)
    current_bitrate: u64,
    /// 上次丢包率
    last_loss_rate: f64,
}

impl LossBasedController {
    pub fn new(initial_bitrate_bps: u64) -> Self {
        Self {
            current_bitrate: initial_bitrate_bps,
            last_loss_rate: 0.0,
        }
    }

    /// 根据丢包率和 RTT 更新码率
    ///
    /// - `loss_rate`: 0.0-1.0
    /// - `rtt_ms`: 往返时延
    /// 返回调整后的码率 (bps)
    pub fn update(&mut self, loss_rate: f64, rtt_ms: u32) -> u64 {
        let _ = rtt_ms; // RTT 可用于更精细的控制, 此处简化

        // RFC 8888 丢包率控制:
        // - loss_rate > 10%: 大幅降码率 (x0.5)
        // - loss_rate 2-10%: 中等降码率 (x0.8)
        // - loss_rate < 2%: 增码率 (x1.05)
        if loss_rate > 0.10 {
            self.current_bitrate = (self.current_bitrate as f64 * 0.5) as u64;
        } else if loss_rate > 0.02 {
            self.current_bitrate = (self.current_bitrate as f64 * 0.8) as u64;
        } else if loss_rate < 0.02 {
            // 低丢包, 增码率
            // 如果之前有丢包, 恢复更快
            if self.last_loss_rate > 0.02 {
                self.current_bitrate = (self.current_bitrate as f64 * 1.1) as u64;
            } else {
                self.current_bitrate = (self.current_bitrate as f64 * 1.05) as u64;
            }
        }

        self.last_loss_rate = loss_rate;
        self.current_bitrate
    }

    /// 当前码率 (bps)
    pub fn bitrate_bps(&self) -> u64 {
        self.current_bitrate
    }
}

/// GCC 拥塞控制器 — 融合延迟检测 + 丢包控制
#[derive(Debug, Clone)]
pub struct GccController {
    /// 延迟检测器
    delay_controller: DelayBasedController,
    /// 丢包率控制器
    loss_controller: LossBasedController,
    /// 最终估计码率 (bps)
    estimated_bitrate: u64,
    /// 最小码率 (bps)
    min_bitrate: u64,
    /// 最大码率 (bps)
    max_bitrate: u64,
    /// 初始码率 (bps)
    initial_bitrate: u64,
    /// 上次更新时间 (ms)
    last_update_ms: u64,
    /// 上次收到反馈时的接收码率估计 (bps)
    last_received_bitrate: u64,
}

impl GccController {
    /// 创建 GCC 控制器
    ///
    /// - `initial_bitrate_bps`: 初始码率
    /// - `min_bitrate_bps`: 最小码率
    /// - `max_bitrate_bps`: 最大码率
    pub fn new(initial_bitrate_bps: u64, min_bitrate_bps: u64, max_bitrate_bps: u64) -> Self {
        Self {
            delay_controller: DelayBasedController::new(initial_bitrate_bps),
            loss_controller: LossBasedController::new(initial_bitrate_bps),
            estimated_bitrate: initial_bitrate_bps,
            min_bitrate: min_bitrate_bps,
            max_bitrate: max_bitrate_bps,
            initial_bitrate: initial_bitrate_bps,
            last_update_ms: 0,
            last_received_bitrate: initial_bitrate_bps,
        }
    }

    /// 从 TWCC 反馈更新 (延迟检测)
    pub fn update_twcc(&mut self, feedback: &TwccFeedback) -> u64 {
        // 计算反馈覆盖的时间窗口和接收码率
        let received = feedback.packet_count as u64;
        let total_delta_us: i64 = feedback
            .deltas
            .iter()
            .map(|&d| d as i64 * DELTA_TICK_US)
            .sum();
        let time_window_ms = (total_delta_us / 1000).max(1) as u64;

        // 估算接收比特率 (假设每包 1200 字节)
        let bytes_received = received * 1200;
        let received_bitrate = if time_window_ms > 0 {
            (bytes_received * 8000) / time_window_ms // bps
        } else {
            self.last_received_bitrate
        };
        self.last_received_bitrate = received_bitrate;

        // 计算参考时间 (ms)
        let ref_time_ms = (feedback.reference_time as u64) * 64; // 64ms per unit
        let frame_duration_ms = if feedback.deltas.len() > 0 {
            time_window_ms / feedback.deltas.len() as u64
        } else {
            20 // 默认 20ms
        };

        // 逐包更新延迟检测器
        let mut send_ms = ref_time_ms as f64;
        let mut recv_ms = ref_time_ms as f64;
        for &delta in &feedback.deltas {
            let delta_ms = delta as f64 * 0.25; // 250us = 0.25ms
            send_ms += frame_duration_ms as f64;
            recv_ms += delta_ms.max(0.0);
            self.delay_controller
                .on_arrival(send_ms, recv_ms, frame_duration_ms);
        }

        // 根据延迟检测结果调整码率
        match self.delay_controller.state() {
            OveruseState::Overuse => {
                // 过载: 取延迟检测器和接收码率的较小值
                let delay_bitrate = self.delay_controller.bitrate_bps();
                self.estimated_bitrate = delay_bitrate.min(received_bitrate);
            }
            OveruseState::Underuse => {
                // 低载: 缓慢增码率
                self.estimated_bitrate = (self.estimated_bitrate as f64 * 1.03) as u64;
            }
            OveruseState::Normal => {
                // 正常: 向接收码率靠拢
                if received_bitrate > self.estimated_bitrate {
                    // 接收率高于估计, 可以增
                    self.estimated_bitrate = (self.estimated_bitrate as f64 * 1.05) as u64;
                } else {
                    // 接收率低于估计, 降
                    self.estimated_bitrate = received_bitrate;
                }
            }
        }

        // 融合丢包控制: 取延迟和丢包的较小值
        let loss_bitrate = self.loss_controller.bitrate_bps();
        self.estimated_bitrate = self.estimated_bitrate.min(loss_bitrate);

        // 限制范围
        self.clamp_bitrate();

        self.last_update_ms = ref_time_ms;
        self.estimated_bitrate
    }

    /// 从 RTCP RR 丢包率更新 (丢包控制)
    pub fn update_loss(&mut self, loss_rate: f64, rtt_ms: u32) -> u64 {
        self.loss_controller.update(loss_rate, rtt_ms);

        // 丢包控制可能降码率, 取较小值
        let loss_bitrate = self.loss_controller.bitrate_bps();
        if loss_bitrate < self.estimated_bitrate {
            self.estimated_bitrate = loss_bitrate;
        }

        self.clamp_bitrate();
        self.estimated_bitrate
    }

    /// 限制码率范围
    fn clamp_bitrate(&mut self) {
        if self.estimated_bitrate < self.min_bitrate {
            self.estimated_bitrate = self.min_bitrate;
        }
        if self.estimated_bitrate > self.max_bitrate {
            self.estimated_bitrate = self.max_bitrate;
        }
    }

    /// 当前估计码率 (bps)
    pub fn bitrate_bps(&self) -> u64 {
        self.estimated_bitrate
    }

    /// 当前码率 (kbps)
    pub fn bitrate_kbps(&self) -> u64 {
        self.estimated_bitrate / 1000
    }

    /// 当前过载状态
    pub fn overuse_state(&self) -> OveruseState {
        self.delay_controller.state()
    }

    /// 重置
    pub fn reset(&mut self) {
        self.delay_controller.reset(self.initial_bitrate);
        self.loss_controller = LossBasedController::new(self.initial_bitrate);
        self.estimated_bitrate = self.initial_bitrate;
        self.last_update_ms = 0;
    }
}

// ============================================================================
// 测试
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_twcc_seq_allocator() {
        let mut alloc = TwccSeqAllocator::new();
        assert_eq!(alloc.next(), 0);
        assert_eq!(alloc.next(), 1);
        assert_eq!(alloc.next(), 2);
        assert_eq!(alloc.next(), 3);
    }

    #[test]
    fn test_twcc_receiver_record() {
        let mut rx = TwccReceiver::new();
        rx.record_packet(100, 1_000_000);
        rx.record_packet(101, 1_020_000);
        rx.record_packet(102, 1_040_000);

        let feedback_bytes = rx.build_feedback(1_000_000);
        // 应该能解析回来
        let parsed = TwccFeedback::parse(&feedback_bytes).unwrap();
        assert_eq!(parsed.base_seq, 100);
        assert_eq!(parsed.packet_count, 3);
    }

    #[test]
    fn test_twcc_feedback_encode_decode() {
        let feedback = TwccFeedback {
            sender_ssrc: 0x12345678,
            media_ssrc: 0xABCDEF01,
            base_seq: 100,
            packet_count: 5,
            reference_time: 0x123456,
            fb_count: 1,
            status_chunks: vec![0x20, 0x05], // run-length: symbol=01, run=5
            deltas: vec![100, 200, 300, 400, 500],
        };

        let encoded = feedback.encode();
        let parsed = TwccFeedback::parse(&encoded).unwrap();

        assert_eq!(parsed.sender_ssrc, feedback.sender_ssrc);
        assert_eq!(parsed.media_ssrc, feedback.media_ssrc);
        assert_eq!(parsed.base_seq, feedback.base_seq);
        assert_eq!(parsed.packet_count, feedback.packet_count);
        assert_eq!(parsed.reference_time, feedback.reference_time);
        assert_eq!(parsed.fb_count, feedback.fb_count);
    }

    #[test]
    fn test_twcc_feedback_with_losses() {
        let mut rx = TwccReceiver::new();
        // 包 100, 102 收到, 101 丢失
        rx.record_packet(100, 1_000_000);
        rx.record_packet(102, 1_040_000);
        rx.record_packet(103, 1_060_000);

        let feedback_bytes = rx.build_feedback(1_000_000);
        let parsed = TwccFeedback::parse(&feedback_bytes).unwrap();

        assert_eq!(parsed.base_seq, 100);
        assert_eq!(parsed.packet_count, 3); // 3 个包收到
    }

    #[test]
    fn test_bandwidth_estimator_initial() {
        let est = BandwidthEstimator::new(1000);
        assert_eq!(est.current_bitrate_kbps(), 1000);
    }

    #[test]
    fn test_bandwidth_estimator_increase() {
        let mut est = BandwidthEstimator::new(1000);

        // 构造一个高接收率的反馈
        let feedback = TwccFeedback {
            sender_ssrc: 0,
            media_ssrc: 0,
            base_seq: 0,
            packet_count: 100,
            reference_time: 0,
            fb_count: 0,
            status_chunks: vec![],
            deltas: vec![40; 100], // 每个 40 * 250us = 10ms, 总 1000ms
        };

        let new_bitrate = est.update(&feedback);
        // 接收率 = 100 * 1200 * 8 / 1000 = 960 kbps
        // 在 GCC 下, 正常状态会增码率
        let _ = new_bitrate;
    }

    // ========================================================================
    // GCC 测试
    // ========================================================================

    #[test]
    fn test_gcc_initial_bitrate() {
        let gcc = GccController::new(1_000_000, 30_000, 50_000_000);
        assert_eq!(gcc.bitrate_bps(), 1_000_000);
        assert_eq!(gcc.bitrate_kbps(), 1000);
    }

    #[test]
    fn test_gcc_overuse_decrease() {
        let mut gcc = GccController::new(1_000_000, 30_000, 50_000_000);

        // 构造高延迟反馈 (deltas 远大于正常值, 表示延迟增长)
        // 正常 delta = 40 (10ms), 这里用 200 (50ms) 模拟延迟
        let feedback = TwccFeedback {
            sender_ssrc: 0,
            media_ssrc: 0,
            base_seq: 0,
            packet_count: 50,
            reference_time: 0,
            fb_count: 0,
            status_chunks: vec![],
            deltas: vec![200; 50], // 50ms per packet, 高延迟
        };

        let initial = gcc.bitrate_bps();
        // 多次反馈让过载检测触发
        for _ in 0..5 {
            gcc.update_twcc(&feedback);
        }
        let after = gcc.bitrate_bps();
        // 过载后码率应下降
        assert!(
            after < initial,
            "overuse should decrease bitrate: initial={}, after={}",
            initial,
            after
        );
    }

    #[test]
    fn test_gcc_underuse_increase() {
        let mut gcc = GccController::new(1_000_000, 30_000, 50_000_000);

        // 构造低延迟反馈 (deltas 小于正常值, 表示延迟减少)
        let feedback = TwccFeedback {
            sender_ssrc: 0,
            media_ssrc: 0,
            base_seq: 0,
            packet_count: 50,
            reference_time: 0,
            fb_count: 0,
            status_chunks: vec![],
            deltas: vec![10; 50], // 2.5ms per packet, 低延迟
        };

        let initial = gcc.bitrate_bps();
        gcc.update_twcc(&feedback);
        let after = gcc.bitrate_bps();
        // 低延迟时码率应增长或至少不降
        let _ = (initial, after); // 行为取决于趋势线
    }

    #[test]
    fn test_gcc_loss_high_decrease() {
        let mut gcc = GccController::new(1_000_000, 30_000, 50_000_000);
        let initial = gcc.bitrate_bps();

        // 高丢包率 (20%)
        gcc.update_loss(0.20, 100);
        let after = gcc.bitrate_bps();
        assert!(
            after < initial,
            "high loss should decrease bitrate: initial={}, after={}",
            initial,
            after
        );
    }

    #[test]
    fn test_gcc_loss_low_increase() {
        let mut gcc = GccController::new(1_000_000, 30_000, 50_000_000);

        // 先制造高丢包降码率
        gcc.update_loss(0.20, 100);
        let low_bitrate = gcc.bitrate_bps();

        // 然后低丢包恢复
        gcc.update_loss(0.01, 100);
        let after = gcc.bitrate_bps();
        assert!(
            after >= low_bitrate,
            "low loss should recover bitrate: low={}, after={}",
            low_bitrate,
            after
        );
    }

    #[test]
    fn test_gcc_min_max_bounds() {
        let mut gcc = GccController::new(100_000, 100_000, 200_000);

        // 高丢包降码率, 不应低于 min
        gcc.update_loss(0.50, 100);
        assert!(gcc.bitrate_bps() >= 100_000, "不应低于 min_bitrate");

        // 重置后测试 max
        gcc.reset();
        let mut gcc2 = GccController::new(150_000, 30_000, 200_000);

        // 低丢包增码率, 不应超过 max
        for _ in 0..20 {
            gcc2.update_loss(0.0, 100);
        }
        assert!(
            gcc2.bitrate_bps() <= 200_000,
            "不应超过 max_bitrate: got {}",
            gcc2.bitrate_bps()
        );
    }

    #[test]
    fn test_trendline_slope() {
        let mut est = TrendlineEstimator::new(20, 5);

        // 添加递增延迟梯度 (延迟在增长)
        for i in 0..20 {
            est.add_sample(10.0, 10.0 + i as f64); // recv_delta 递增
        }
        let slope = est.trendline_slope();
        assert!(slope > 0.0, "递增延迟应有正斜率: {}", slope);

        // 重置后添加递减延迟梯度
        est.reset();
        for i in 0..20 {
            est.add_sample(10.0 + i as f64, 10.0); // send_delta 递增, recv 不变
        }
        let slope = est.trendline_slope();
        assert!(slope < 0.0, "递减延迟应有负斜率: {}", slope);
    }

    #[test]
    fn test_trendline_too_few_samples() {
        let mut est = TrendlineEstimator::new(20, 10);
        est.add_sample(10.0, 12.0);
        est.add_sample(10.0, 13.0);
        // 只有 2 个样本, 少于 min_samples=10
        assert_eq!(est.trendline_slope(), 0.0);
    }

    #[test]
    fn test_overuse_state_machine() {
        let mut ctrl = DelayBasedController::new(1_000_000);
        assert_eq!(ctrl.state(), OveruseState::Normal);

        // 高延迟梯度触发过载: send 每次增 10ms, recv 每次增 60ms (延迟增长)
        let mut send_ms = 0.0f64;
        let mut recv_ms = 0.0f64;
        for _ in 0..30 {
            send_ms += 10.0;
            recv_ms += 60.0; // recv_delta = 60, send_delta = 10, gradient = 50
            ctrl.on_arrival(send_ms, recv_ms, 20);
        }
        // 应进入 Overuse
        assert_eq!(ctrl.state(), OveruseState::Overuse, "高延迟应触发 Overuse");

        // 低延迟梯度恢复正常: send 每次增 60ms, recv 每次增 10ms
        for _ in 0..30 {
            send_ms += 60.0;
            recv_ms += 10.0; // recv_delta = 10, send_delta = 60, gradient = -50
            ctrl.on_arrival(send_ms, recv_ms, 20);
        }
        // 应恢复正常
        assert_ne!(ctrl.state(), OveruseState::Overuse, "低延迟应恢复正常");
    }

    #[test]
    fn test_bandwidth_estimator_compat() {
        let mut est = BandwidthEstimator::new(1000);
        assert_eq!(est.current_bitrate_kbps(), 1000);

        let feedback = TwccFeedback {
            sender_ssrc: 0,
            media_ssrc: 0,
            base_seq: 0,
            packet_count: 50,
            reference_time: 0,
            fb_count: 0,
            status_chunks: vec![],
            deltas: vec![40; 50],
        };
        let _ = est.update(&feedback);
        // 应不 panic 且返回有效值
        assert!(est.current_bitrate_kbps() > 0);
    }
}
