//! gRPC MediaNode 服务实现
//!
//! 接收 Go 控制面的 gRPC 调用，管理 Rust 媒体面。

use std::sync::Arc;

use lm_core::{EndpointId, SessionId, TrackId};
use lm_stream::{MediaStream, StreamId, StreamRegistry};
use crate::mixer::MixManager;
use crate::recorder::RecordingManager;
use crate::session::SessionManager;
use tokio::sync::mpsc;
use tokio_stream::{wrappers::ReceiverStream, StreamExt};
use tonic::{Request, Response, Status, Streaming};
use tracing::{info, warn};

// tonic-build 生成的代码
tonic::include_proto!("lingvoice.media.v1");

/// MediaNode gRPC 服务器
#[derive(Clone)]
pub struct MediaNodeServer {
    node_id: String,
    sessions: Arc<SessionManager>,
    mixes: Arc<MixManager>,
    recordings: Arc<RecordingManager>,
    /// 媒体流注册表（新的流抽象，用于帧级处理：录制、转封装）
    streams: Arc<StreamRegistry>,
}

impl MediaNodeServer {
    pub fn new(node_id: impl Into<String>) -> Self {
        Self {
            node_id: node_id.into(),
            sessions: Arc::new(SessionManager::new()),
            mixes: Arc::new(MixManager::new()),
            recordings: Arc::new(RecordingManager::new()),
            streams: Arc::new(StreamRegistry::new()),
        }
    }

    pub fn session_count(&self) -> usize {
        self.sessions.session_count()
    }

    pub fn mix_count(&self) -> usize {
        self.mixes.mix_count()
    }

    pub fn stream_count(&self) -> usize {
        self.streams.count()
    }

    pub fn node_id(&self) -> &str {
        &self.node_id
    }

    /// 获取流注册表引用（供 recorder 使用）
    pub fn streams(&self) -> &Arc<StreamRegistry> {
        &self.streams
    }
}

// ============================================================================
// gRPC trait 实现
// ============================================================================

#[tonic::async_trait]
impl media_node_server::MediaNode for MediaNodeServer {
    // --- 健康检查 ---

    async fn health_check(
        &self,
        _request: Request<HealthCheckRequest>,
    ) -> Result<Response<HealthCheckResponse>, Status> {
        Ok(Response::new(HealthCheckResponse {
            node_id: self.node_id.clone(),
            active_sessions: self.sessions.session_count() as u32,
            active_endpoints: 0, // TODO: aggregate
            total_packets_received: 0,
            total_packets_sent: 0,
            cpu_usage: 0.0,
            memory_usage_mb: 0,
        }))
    }

    // --- 会话管理 ---

    async fn create_session(
        &self,
        request: Request<CreateSessionRequest>,
    ) -> Result<Response<CreateSessionResponse>, Status> {
        let req = request.into_inner();
        let session_id = SessionId(req.session_id.clone());

        self.sessions
            .create_session(session_id.clone(), Some(req.room_id), Some(req.tenant_id))
            .map_err(|e| Status::already_exists(e.to_string()))?;

        info!(session = %req.session_id, "gRPC create_session");
        Ok(Response::new(CreateSessionResponse {
            session_id: req.session_id,
            node_id: self.node_id.clone(),
        }))
    }

    async fn destroy_session(
        &self,
        request: Request<DestroySessionRequest>,
    ) -> Result<Response<DestroySessionResponse>, Status> {
        let req = request.into_inner();
        let session_id = SessionId(req.session_id.clone());

        // 停止该 session 的所有录制
        let recording_results = self.recordings.stop_session_recordings(&session_id).await;
        if !recording_results.is_empty() {
            info!(
                session = %req.session_id,
                recordings_stopped = recording_results.len(),
                "stopped recordings on session destroy"
            );
        }

        self.sessions
            .destroy_session(&session_id)
            .map_err(|e| Status::not_found(e.to_string()))?;

        // 清理该 session 的所有 MediaStream
        // TODO: StreamRegistry 应该支持按 session 批量注销
        // 目前依赖 remove_track 逐个注销，session destroy 时 tracks 已被清理

        info!(session = %req.session_id, "gRPC destroy_session");
        Ok(Response::new(DestroySessionResponse {}))
    }

    // --- 端点管理 ---

    async fn add_endpoint(
        &self,
        request: Request<AddEndpointRequest>,
    ) -> Result<Response<AddEndpointResponse>, Status> {
        let req = request.into_inner();
        let session_id = SessionId(req.session_id.clone());

        let session = self
            .sessions
            .get_session(&session_id)
            .ok_or_else(|| Status::not_found(format!("session {} not found", req.session_id)))?;

        // 解析编解码
        let codec = req
            .codecs
            .first()
            .and_then(|c| lm_codecs::codec_from_name(&c.codec))
            .unwrap_or(lm_core::CodecType::PcmU);

        let direction = match req.direction {
            i32 if i32 == Direction::Sendrecv as i32 => lm_core::Direction::SendRecv,
            i32 if i32 == Direction::Sendonly as i32 => lm_core::Direction::SendOnly,
            i32 if i32 == Direction::Recvonly as i32 => lm_core::Direction::RecvOnly,
            _ => lm_core::Direction::Inactive,
        };

        let endpoint = crate::session::Endpoint {
            id: EndpointId(req.endpoint_id.clone()),
            direction,
            codec,
        };

        session.endpoints.insert(EndpointId(req.endpoint_id.clone()), endpoint);

        info!(
            session = %req.session_id,
            endpoint = %req.endpoint_id,
            "gRPC add_endpoint"
        );

        Ok(Response::new(AddEndpointResponse {
            endpoint_id: req.endpoint_id,
            rtp_port: 0, // Phase 1: Go 层处理 RTP，Rust 不监听端口
        }))
    }

    async fn remove_endpoint(
        &self,
        request: Request<RemoveEndpointRequest>,
    ) -> Result<Response<RemoveEndpointResponse>, Status> {
        let req = request.into_inner();
        let session_id = SessionId(req.session_id.clone());

        let session = self
            .sessions
            .get_session(&session_id)
            .ok_or_else(|| Status::not_found(format!("session {} not found", req.session_id)))?;

        session
            .endpoints
            .remove(&EndpointId(req.endpoint_id.clone()));

        info!(
            session = %req.session_id,
            endpoint = %req.endpoint_id,
            "gRPC remove_endpoint"
        );

        Ok(Response::new(RemoveEndpointResponse {}))
    }

    // --- 轨道管理 ---

    async fn add_track(
        &self,
        request: Request<AddTrackRequest>,
    ) -> Result<Response<AddTrackResponse>, Status> {
        let req = request.into_inner();
        let session_id = SessionId(req.session_id.clone());

        let session = self
            .sessions
            .get_session(&session_id)
            .ok_or_else(|| Status::not_found(format!("session {} not found", req.session_id)))?;

        let track = req.track.ok_or_else(|| Status::invalid_argument("missing track"))?;
        let track_id = TrackId(track.track_id.clone());
        let codec = lm_codecs::codec_from_name(&track.codec).unwrap_or(lm_core::CodecType::PcmU);
        let kind = match track.kind.as_str() {
            "video" => lm_core::TrackKind::Video,
            _ => lm_core::TrackKind::Audio,
        };

        let track_state = crate::session::TrackState::new(
            track_id.clone(),
            EndpointId(req.endpoint_id.clone()),
            session_id.clone(),
            codec,
            kind,
            track.ssrc,
        );

        session.tracks.insert(track_id.clone(), Arc::new(track_state));

        // 同时注册 MediaStream（新的流抽象）
        let room_id = session.room_id.clone();
        let clock_rate = if kind == lm_core::TrackKind::Video {
            90000
        } else {
            match codec {
                lm_core::CodecType::Opus => 48000,
                lm_core::CodecType::PcmU | lm_core::CodecType::PcmA => 8000,
                lm_core::CodecType::G722 => 8000,
                _ => 48000,
            }
        };
        let media_stream = Arc::new(MediaStream::new(
            StreamId::new(req.session_id.clone(), track.track_id.clone()),
            room_id,
            codec,
            kind,
            track.ssrc,
            clock_rate,
        ));
        self.streams.register(media_stream);

        info!(
            session = %req.session_id,
            track = %track.track_id,
            "gRPC add_track"
        );

        Ok(Response::new(AddTrackResponse {
            track_id: track.track_id,
        }))
    }

    async fn remove_track(
        &self,
        request: Request<RemoveTrackRequest>,
    ) -> Result<Response<RemoveTrackResponse>, Status> {
        let req = request.into_inner();
        let session_id = SessionId(req.session_id.clone());

        let session = self
            .sessions
            .get_session(&session_id)
            .ok_or_else(|| Status::not_found(format!("session {} not found", req.session_id)))?;

        session.tracks.remove(&TrackId(req.track_id.clone()));

        // 同时注销 MediaStream
        self.streams.unregister(&StreamId::new(req.session_id.clone(), req.track_id.clone()));

        info!(
            session = %req.session_id,
            track = %req.track_id,
            "gRPC remove_track"
        );

        Ok(Response::new(RemoveTrackResponse {}))
    }

    // --- 媒体传输 ---

    async fn push_rtp(
        &self,
        request: Request<Streaming<PushRtpRequest>>,
    ) -> Result<Response<PushRtpResponse>, Status> {
        let mut stream = request.into_inner();
        let mut packets_received: u64 = 0;

        // 在流开始时检查是否处于混音模式
        // 如果是，创建解码器并在此流的生命周期内复用
        let mut mix_decoder: Option<Box<dyn audio_codec::Decoder>> = None;
        let mut mix_input_tx: Option<mpsc::Sender<lm_core::AudioFrame>> = None;
        let mut mix_checked = false;

        while let Some(req) = stream.next().await {
            let req = req?;
            if let Some(packet) = req.packet {
                packets_received += 1;

                let session_id = SessionId(req.session_id.clone());
                let track_id = TrackId(req.track_id.clone());

                // 首包时检查混音模式（避免每包都查）
                if !mix_checked {
                    mix_checked = true;
                    if let Some(tx) = self.mixes.get_mix_input(&session_id, &track_id) {
                        // 混音模式：创建解码器
                        let session = self.sessions.get_session(&session_id);
                        let codec = session
                            .and_then(|s| s.tracks.get(&track_id).map(|t| t.codec))
                            .unwrap_or(lm_core::CodecType::Opus);
                        let ac_codec = match codec {
                            lm_core::CodecType::Opus => audio_codec::CodecType::Opus,
                            lm_core::CodecType::PcmU => audio_codec::CodecType::PCMU,
                            lm_core::CodecType::PcmA => audio_codec::CodecType::PCMA,
                            lm_core::CodecType::G722 => audio_codec::CodecType::G722,
                            _ => audio_codec::CodecType::Opus,
                        };
                        mix_decoder = Some(audio_codec::create_decoder(ac_codec));
                        mix_input_tx = Some(tx);
                        info!(
                            session = %req.session_id,
                            track = %req.track_id,
                            "push_rtp in MIX mode, decoder created"
                        );
                    }
                }

                if let (Some(decoder), Some(tx)) = (&mut mix_decoder, &mix_input_tx) {
                    // === 混音模式 ===
                    // 解码 Opus → PCM，发送到 mixer input
                    let samples = decoder.decode(&packet.payload);
                    if !samples.is_empty() {
                        let frame = lm_core::AudioFrame {
                            samples,
                            sample_rate: decoder.sample_rate(),
                            timestamp: packet.timestamp as u64,
                        };
                        let _ = tx.try_send(frame);
                    }
                    // 不做 SFU 转发，混音输出通过 mix-output track 的 pull_rtp 获取
                } else {
                    // === SFU 转发模式 ===
                    let src_kind = self.sessions.get_session(&session_id)
                        .and_then(|s| s.tracks.get(&track_id).map(|t| t.kind));

                    let pkt_out = crate::session::RtpPacketOut {
                        ssrc: packet.ssrc,
                        payload_type: packet.payload_type,
                        sequence_number: packet.sequence_number,
                        timestamp: packet.timestamp,
                        marker: packet.marker,
                        payload: bytes::Bytes::from(packet.payload.clone()),
                        rid: packet.rid.clone(),
                        clock_rate: packet.clock_rate,
                    };

                    // 1. SFU 转发：广播到同 room 其他 session 的 peer track
                    //    如果源 codec 与目标 codec 不同，做转码（仅音频）
                    let peer_tracks = match src_kind {
                        Some(kind) => self.sessions.get_room_peer_tracks_by_kind(&session_id, kind),
                        None => {
                            warn!(session = %req.session_id, track = %req.track_id, "source track kind unknown, skipping kind filter");
                            Vec::new()
                        }
                    };
                    let peer_count = peer_tracks.len();

                    // 获取源 codec（用于转码判断）
                    let src_codec = self.sessions.get_session(&session_id)
                        .and_then(|s| s.tracks.get(&track_id).map(|t| t.codec));

                    for peer_track in &peer_tracks {
                        // 检查是否需要转码（仅音频，且 codec 不同）
                        let need_transcode = src_kind == Some(lm_core::TrackKind::Audio)
                            && src_codec.is_some()
                            && peer_track.codec != src_codec.unwrap()
                            && peer_track.codec.is_audio()
                            && src_codec.unwrap().is_audio();

                        if need_transcode {
                            // 转码：src codec → PCM → dst codec
                            let dst_pkt = transcode_audio_packet(
                                &pkt_out,
                                src_codec.unwrap(),
                                peer_track.codec,
                            );
                            if let Some(dst_pkt) = dst_pkt {
                                let _ = peer_track.rtp_broadcast.send(dst_pkt);
                            }
                            // 转码失败则跳过该包（避免发送噪音）
                        } else {
                            // 同 codec 或视频：裸包转发
                            let _ = peer_track.rtp_broadcast.send(pkt_out.clone());
                        }
                    }

                    // 2. 媒体流处理：用 MediaStream 组装帧，通知源流订阅者（录制等）
                    let stream_id = StreamId::new(req.session_id.clone(), req.track_id.clone());
                    if let Some(media_stream) = self.streams.get(&stream_id) {
                        let rtp_pkt = lm_transport::RtpPacket {
                            ssrc: packet.ssrc,
                            payload_type: packet.payload_type as u8,
                            sequence_number: packet.sequence_number as u16,
                            timestamp: packet.timestamp,
                            marker: packet.marker,
                            payload: bytes::Bytes::from(packet.payload),
                            rid: packet.rid.clone(),
                        };
                        media_stream.push_packet(&rtp_pkt);
                    }

                    // 3. 也广播到源 track 自身的 broadcast channel
                    // （保持兼容：旧的 pull_rtp 订阅者仍用 broadcast）
                    if let Some(session) = self.sessions.get_session(&session_id) {
                        if let Some(src_track) = session.tracks.get(&track_id) {
                            let _ = src_track.rtp_broadcast.send(pkt_out.clone());
                        }
                    }

                    if packets_received % 1000 == 0 {
                        info!(
                            session = %req.session_id,
                            track = %req.track_id,
                            ssrc = packet.ssrc,
                            seq = packet.sequence_number,
                            peer_count,
                            total = packets_received,
                            "rtp push_rtp progress (sampled 1/1000)"
                        );
                    }
                }
            }
        }

        info!(packets_received, "push_rtp stream ended");
        Ok(Response::new(PushRtpResponse { packets_received }))
    }

    type PullRtpStream = ReceiverStream<Result<RtpPacket, Status>>;

    async fn pull_rtp(
        &self,
        request: Request<PullRtpRequest>,
    ) -> Result<Response<Self::PullRtpStream>, Status> {
        let req = request.into_inner();
        let session_id = SessionId(req.session_id.clone());
        let track_id = TrackId(req.track_id.clone());

        let session = self
            .sessions
            .get_session(&session_id)
            .ok_or_else(|| Status::not_found(format!("session {} not found", req.session_id)))?;

        let track_state = session
            .tracks
            .get(&track_id)
            .map(|t| t.clone())
            .ok_or_else(|| Status::not_found(format!("track {} not found", req.track_id)))?;

        // 订阅 broadcast channel
        let mut rx = track_state.subscribe();
        let (tx, rx_out) = mpsc::channel(256);

        info!(
            session = %req.session_id,
            track = %req.track_id,
            "gRPC pull_rtp stream opened"
        );

        // spawn forwarder: broadcast receiver → gRPC stream
        tokio::spawn(async move {
            let mut packets_sent: u64 = 0;
            let mut lagged_total: u64 = 0;
            loop {
                match rx.recv().await {
                    Ok(pkt) => {
                        let rtp_packet = RtpPacket {
                            ssrc: pkt.ssrc,
                            payload_type: pkt.payload_type,
                            sequence_number: pkt.sequence_number,
                            timestamp: pkt.timestamp,
                            marker: pkt.marker,
                            payload: pkt.payload.to_vec(),
                            rid: pkt.rid,
                            clock_rate: pkt.clock_rate,
                        };
                        if tx.send(Ok(rtp_packet)).await.is_err() {
                            info!(packets_sent, lagged_total, "pull_rtp stream closed, stopping forwarder");
                            break;
                        }
                        packets_sent += 1;
                        if packets_sent % 1000 == 0 {
                            info!(packets_sent, lagged_total, "pull_rtp progress (sampled 1/1000)");
                        }
                    }
                    Err(tokio::sync::broadcast::error::RecvError::Lagged(n)) => {
                        lagged_total += n as u64;
                        if lagged_total % 100 < n as u64 {
                            warn!(skipped = n, lagged_total, "pull_rtp lagged (sampled)");
                        }
                    }
                    Err(tokio::sync::broadcast::error::RecvError::Closed) => {
                        info!(packets_sent, lagged_total, "pull_rtp broadcast closed, stopping forwarder");
                        break;
                    }
                }
            }
        });

        Ok(Response::new(ReceiverStream::new(rx_out)))
    }

    async fn inject_audio(
        &self,
        mut request: Request<Streaming<InjectAudioRequest>>,
    ) -> Result<Response<InjectAudioResponse>, Status> {
        let mut frames_injected: u64 = 0;
        while let Some(req) = request.get_mut().next().await {
            let req = req?;
            if req.frame.is_some() {
                frames_injected += 1;
            }
        }
        // TODO: 接收 PCM 帧，编码后注入 egress
        Ok(Response::new(InjectAudioResponse { frames_injected }))
    }

    // --- 录制 ---

    async fn start_recording(
        &self,
        request: Request<StartRecordingRequest>,
    ) -> Result<Response<StartRecordingResponse>, Status> {
        let req = request.into_inner();
        let session_id = SessionId(req.session_id.clone());

        let session = self
            .sessions
            .get_session(&session_id)
            .ok_or_else(|| Status::not_found(format!("session {} not found", req.session_id)))?;

        let recording_id = uuid::Uuid::new_v4().to_string();

        // 录制目录：使用 req.path 或默认 ./recordings
        let output_dir = if req.path.is_empty() {
            "./recordings".to_string()
        } else {
            req.path.clone()
        };

        info!(
            session = %req.session_id,
            format = %req.format,
            output_dir = %output_dir,
            recording_id = %recording_id,
            "gRPC start_recording"
        );

        self.recordings
            .start_recording(&recording_id, &session, &output_dir, &self.streams)
            .await
            .map_err(|e| Status::internal(format!("start recording: {e}")))?;

        Ok(Response::new(StartRecordingResponse { recording_id }))
    }

    async fn stop_recording(
        &self,
        request: Request<StopRecordingRequest>,
    ) -> Result<Response<StopRecordingResponse>, Status> {
        let req = request.into_inner();

        info!(
            session = %req.session_id,
            recording_id = %req.recording_id,
            "gRPC stop_recording"
        );

        let result = self
            .recordings
            .stop_recording(&req.recording_id)
            .await
            .map_err(|e| Status::internal(format!("stop recording: {e}")))?;

        Ok(Response::new(StopRecordingResponse {
            file_path: result.file_path,
            duration_ms: result.duration_ms,
            file_size: result.file_size,
        }))
    }

    // --- 混音 ---

    async fn start_mix(
        &self,
        request: Request<StartMixRequest>,
    ) -> Result<Response<StartMixResponse>, Status> {
        let req = request.into_inner();
        let sample_rate = if req.sample_rate > 0 { req.sample_rate } else { 48000 };
        let max_speakers = req.max_speakers as usize;
        let output_codec = if req.output_codec.is_empty() { "opus" } else { req.output_codec.as_str() };

        let state = self.mixes.start_mix(&req.room_id, sample_rate, max_speakers, output_codec);
        let mix_id = state.mix_id.clone();

        info!(
            room_id = %req.room_id,
            mix_id = %mix_id,
            sample_rate,
            max_speakers,
            output_codec,
            "gRPC start_mix"
        );

        Ok(Response::new(StartMixResponse {
            mix_id,
        }))
    }

    async fn stop_mix(
        &self,
        request: Request<StopMixRequest>,
    ) -> Result<Response<StopMixResponse>, Status> {
        let req = request.into_inner();
        self.mixes
            .stop_mix(&req.mix_id)
            .map_err(|e| Status::not_found(e))?;
        info!(mix_id = %req.mix_id, "gRPC stop_mix");
        Ok(Response::new(StopMixResponse {}))
    }

    async fn add_mix_participant(
        &self,
        request: Request<AddMixParticipantRequest>,
    ) -> Result<Response<AddMixParticipantResponse>, Status> {
        let req = request.into_inner();
        let session_id = SessionId(req.session_id.clone());

        let session = self
            .sessions
            .get_session(&session_id)
            .ok_or_else(|| Status::not_found(format!("session {} not found", req.session_id)))?;

        // 查找该 session 的音频 source track
        // 通常是 AddTrack 创建的音频 track，track_id 由 Go 侧传入
        let source_track_id = TrackId(req.track_id.clone());

        // 获取采样率和输出编码（从 mix state）
        let sample_rate = self
            .mixes
            .mix_sample_rate(&req.mix_id)
            .unwrap_or(48000);
        let output_codec = self
            .mixes
            .mix_output_codec(&req.mix_id)
            .unwrap_or_else(|| "opus".to_string());

        let mix_track_id = self
            .mixes
            .add_participant(&req.mix_id, &session, &source_track_id, sample_rate, &output_codec)
            .await
            .map_err(|e| Status::internal(e))?;

        // 如果 muted，设置 self→all 的增益为 0
        if req.muted {
            let _ = self.mixes.set_route_gain(
                &req.mix_id,
                &req.session_id,
                "__all__",
                0.0,
            );
        }

        info!(
            mix_id = %req.mix_id,
            session = %req.session_id,
            mix_track = %mix_track_id.0,
            muted = req.muted,
            "gRPC add_mix_participant"
        );

        Ok(Response::new(AddMixParticipantResponse {}))
    }

    async fn remove_mix_participant(
        &self,
        request: Request<RemoveMixParticipantRequest>,
    ) -> Result<Response<RemoveMixParticipantResponse>, Status> {
        let req = request.into_inner();
        let session_id = SessionId(req.session_id.clone());

        let session = self
            .sessions
            .get_session(&session_id)
            .ok_or_else(|| Status::not_found(format!("session {} not found", req.session_id)))?;

        // 查找该 session 的音频 source track（需要从 mix state 获取）
        // 简化：用 session_id 推导 track_id
        // TODO: 从 mix state 获取 source_track_id
        let source_track_id = TrackId(format!("audio-{}", req.session_id));

        self.mixes
            .remove_participant(&req.mix_id, &session, &source_track_id)
            .await
            .map_err(|e| Status::internal(e))?;

        info!(
            mix_id = %req.mix_id,
            session = %req.session_id,
            "gRPC remove_mix_participant"
        );
        Ok(Response::new(RemoveMixParticipantResponse {}))
    }

    async fn set_mix_gain(
        &self,
        request: Request<SetMixGainRequest>,
    ) -> Result<Response<SetMixGainResponse>, Status> {
        let req = request.into_inner();
        self.mixes
            .set_route_gain(
                &req.mix_id,
                &req.src_session_id,
                &req.dst_session_id,
                req.gain,
            )
            .map_err(|e| Status::not_found(e))?;

        info!(
            mix_id = %req.mix_id,
            src = %req.src_session_id,
            dst = %req.dst_session_id,
            gain = req.gain,
            "gRPC set_mix_gain"
        );
        Ok(Response::new(SetMixGainResponse {}))
    }

    // --- 桥接 ---

    async fn bridge_sessions(
        &self,
        request: Request<BridgeSessionsRequest>,
    ) -> Result<Response<BridgeSessionsResponse>, Status> {
        let req = request.into_inner();
        info!(
            session_a = %req.session_a_id,
            session_b = %req.session_b_id,
            "gRPC bridge_sessions"
        );
        // TODO: 实际桥接
        Ok(Response::new(BridgeSessionsResponse {
            relay_mode: true,
            codec_a: "pcmu".to_string(),
            codec_b: "pcmu".to_string(),
        }))
    }

    async fn unbridge_sessions(
        &self,
        request: Request<UnbridgeSessionsRequest>,
    ) -> Result<Response<UnbridgeSessionsResponse>, Status> {
        let req = request.into_inner();
        info!(
            session_a = %req.session_a_id,
            session_b = %req.session_b_id,
            "gRPC unbridge_sessions"
        );
        Ok(Response::new(UnbridgeSessionsResponse {}))
    }

    // --- DTMF ---

    async fn send_dtmf(
        &self,
        request: Request<SendDtmfRequest>,
    ) -> Result<Response<SendDtmfResponse>, Status> {
        let req = request.into_inner();
        info!(
            session = %req.session_id,
            digit = %req.digit,
            "gRPC send_dtmf"
        );
        Ok(Response::new(SendDtmfResponse {}))
    }

    // --- 事件 ---

    type EventsStream = ReceiverStream<Result<MediaEvent, Status>>;

    async fn events(
        &self,
        request: Request<EventsRequest>,
    ) -> Result<Response<Self::EventsStream>, Status> {
        let req = request.into_inner();
        let (_tx, rx) = mpsc::channel(100);

        info!(session = %req.session_id, "gRPC events stream opened");

        // TODO: 订阅会话事件
        // 目前返回空流

        Ok(Response::new(ReceiverStream::new(rx)))
    }

    // --- 统计 ---

    async fn get_stats(
        &self,
        request: Request<GetStatsRequest>,
    ) -> Result<Response<GetStatsResponse>, Status> {
        let req = request.into_inner();
        let session_id = SessionId(req.session_id.clone());

        let session = self
            .sessions
            .get_session(&session_id)
            .ok_or_else(|| Status::not_found(format!("session {} not found", req.session_id)))?;

        let duration_ms = session.created_at.elapsed().as_millis() as u64;

        Ok(Response::new(GetStatsResponse {
            session_id: req.session_id,
            duration_ms,
            tracks: vec![],
        }))
    }
}

// ============================================================================
// 转码辅助函数
// ============================================================================

/// 将音频 RTP 包从源 codec 转码为目标 codec。
///
/// 流程：src payload → PCM samples → dst payload
/// 失败时返回 None（调用方应跳过该包）。
///
/// 支持：Opus ↔ PCMU ↔ PCMA ↔ PCM(l16)
fn transcode_audio_packet(
    pkt: &crate::session::RtpPacketOut,
    src_codec: lm_core::CodecType,
    dst_codec: lm_core::CodecType,
) -> Option<crate::session::RtpPacketOut> {
    // PCM (L16) 不需要解码，直接作为 samples
    let samples: Vec<audio_codec::Sample> = if src_codec == lm_core::CodecType::Pcm {
        // L16 little-endian → i16 samples
        let payload = &pkt.payload;
        if payload.len() % 2 != 0 {
            return None;
        }
        payload
            .chunks_exact(2)
            .map(|c| i16::from_le_bytes([c[0], c[1]]))
            .collect()
    } else {
        // 其他 codec 用 decoder 解码
        let ac_src = match src_codec {
            lm_core::CodecType::Opus => audio_codec::CodecType::Opus,
            lm_core::CodecType::PcmU => audio_codec::CodecType::PCMU,
            lm_core::CodecType::PcmA => audio_codec::CodecType::PCMA,
            lm_core::CodecType::G722 => audio_codec::CodecType::G722,
            _ => return None,
        };
        let mut decoder = audio_codec::create_decoder(ac_src);
        decoder.decode(&pkt.payload)
    };

    if samples.is_empty() {
        return None;
    }

    // 编码到目标 codec
    let dst_payload: bytes::Bytes = if dst_codec == lm_core::CodecType::Pcm {
        // L16 little-endian
        let mut buf = Vec::with_capacity(samples.len() * 2);
        for s in &samples {
            buf.extend_from_slice(&s.to_le_bytes());
        }
        bytes::Bytes::from(buf)
    } else {
        let ac_dst = match dst_codec {
            lm_core::CodecType::Opus => audio_codec::CodecType::Opus,
            lm_core::CodecType::PcmU => audio_codec::CodecType::PCMU,
            lm_core::CodecType::PcmA => audio_codec::CodecType::PCMA,
            lm_core::CodecType::G722 => audio_codec::CodecType::G722,
            _ => return None,
        };
        let mut encoder = audio_codec::create_encoder(ac_dst);
        let mut buf = vec![0u8; samples.len() * 4]; // 足够大的输出缓冲
        match encoder.encode_into(&samples, &mut buf) {
            Ok(n) => bytes::Bytes::from(buf[..n].to_vec()),
            Err(_) => return None,
        }
    };

    // 构造转码后的 RTP 包
    // SSRC 保持原值（接收方按 SSRC 区分流）
    // payload_type 用目标 codec 的常见值
    let dst_pt = match dst_codec {
        lm_core::CodecType::Opus => 111,
        lm_core::CodecType::PcmU => 0,
        lm_core::CodecType::PcmA => 8,
        lm_core::CodecType::G722 => 9,
        lm_core::CodecType::Pcm => 96, // 动态
        _ => pkt.payload_type,
    };

    Some(crate::session::RtpPacketOut {
        ssrc: pkt.ssrc,
        payload_type: dst_pt,
        sequence_number: pkt.sequence_number,
        timestamp: pkt.timestamp,
        marker: pkt.marker,
        payload: dst_payload,
        rid: pkt.rid.clone(),
        clock_rate: pkt.clock_rate,
    })
}

#[cfg(test)]
mod transcode_tests {
    use super::*;

    fn make_pkt(payload: &[u8]) -> crate::session::RtpPacketOut {
        crate::session::RtpPacketOut {
            ssrc: 1234,
            payload_type: 111,
            sequence_number: 1,
            timestamp: 960,
            marker: true,
            payload: bytes::Bytes::from(payload.to_vec()),
            rid: String::new(),
            clock_rate: 48000,
        }
    }

    #[test]
    fn test_transcode_pcmu_to_pcm() {
        // PCMU 编码：0xFF 是 silence
        let pkt = make_pkt(&[0xFF; 160]);
        let out = transcode_audio_packet(&pkt, lm_core::CodecType::PcmU, lm_core::CodecType::Pcm);
        assert!(out.is_some());
        let out = out.unwrap();
        assert_eq!(out.payload.len(), 320); // 160 samples * 2 bytes
        assert_eq!(out.payload_type, 96); // PCM 动态 PT
    }

    #[test]
    fn test_transcode_pcm_to_pcmu() {
        // L16 silence = 0
        let pcm = vec![0i16; 160];
        let mut payload = Vec::new();
        for s in &pcm {
            payload.extend_from_slice(&s.to_le_bytes());
        }
        let pkt = make_pkt(&payload);
        let out = transcode_audio_packet(&pkt, lm_core::CodecType::Pcm, lm_core::CodecType::PcmU);
        assert!(out.is_some());
        let out = out.unwrap();
        assert_eq!(out.payload.len(), 160); // 160 samples * 1 byte
        assert_eq!(out.payload_type, 0); // PCMU PT
    }

    #[test]
    fn test_transcode_same_codec_returns_none_for_unsupported() {
        // 同 codec 不应该走转码路径，但函数本身对同 codec 也能工作
        // 这里测试不支持的 codec 返回 None
        let pkt = make_pkt(&[0x00; 10]);
        let out = transcode_audio_packet(&pkt, lm_core::CodecType::H264, lm_core::CodecType::Opus);
        assert!(out.is_none());
    }

    #[test]
    fn test_transcode_pcm_odd_payload_returns_none() {
        // L16 必须是偶数长度
        let pkt = make_pkt(&[0x00; 3]);
        let out = transcode_audio_packet(&pkt, lm_core::CodecType::Pcm, lm_core::CodecType::PcmU);
        assert!(out.is_none());
    }
}
