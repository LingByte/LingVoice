//! gRPC MediaNode 服务实现
//!
//! 接收 Go 控制面的 gRPC 调用，管理 Rust 媒体面。

use std::sync::Arc;

use lm_core::{EndpointId, SessionId, TrackId};
use crate::mixer::MixManager;
use crate::session::SessionManager;
use tokio::sync::mpsc;
use tokio_stream::{wrappers::ReceiverStream, StreamExt};
use tonic::{Request, Response, Status, Streaming};
use tracing::{info, warn};

// tonic-build 生成的代码
tonic::include_proto!("lingvoice.media.v1");

/// MediaNode gRPC 服务器
pub struct MediaNodeServer {
    node_id: String,
    sessions: Arc<SessionManager>,
    mixes: Arc<MixManager>,
}

impl MediaNodeServer {
    pub fn new(node_id: impl Into<String>) -> Self {
        Self {
            node_id: node_id.into(),
            sessions: Arc::new(SessionManager::new()),
            mixes: Arc::new(MixManager::new()),
        }
    }

    pub fn session_count(&self) -> usize {
        self.sessions.session_count()
    }

    pub fn mix_count(&self) -> usize {
        self.mixes.mix_count()
    }

    pub fn node_id(&self) -> &str {
        &self.node_id
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

        self.sessions
            .destroy_session(&session_id)
            .map_err(|e| Status::not_found(e.to_string()))?;

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
                    // === SFU 转发模式（当前行为）===
                    let src_kind = self.sessions.get_session(&session_id)
                        .and_then(|s| s.tracks.get(&track_id).map(|t| t.kind));

                    let pkt_out = crate::session::RtpPacketOut {
                        ssrc: packet.ssrc,
                        payload_type: packet.payload_type,
                        sequence_number: packet.sequence_number,
                        timestamp: packet.timestamp,
                        marker: packet.marker,
                        payload: bytes::Bytes::from(packet.payload),
                        rid: packet.rid.clone(),
                        clock_rate: packet.clock_rate,
                    };

                    let peer_tracks = match src_kind {
                        Some(kind) => self.sessions.get_room_peer_tracks_by_kind(&session_id, kind),
                        None => {
                            warn!(session = %req.session_id, track = %req.track_id, "source track kind unknown, skipping kind filter");
                            Vec::new()
                        }
                    };
                    let peer_count = peer_tracks.len();
                    for peer_track in &peer_tracks {
                        let _ = peer_track.rtp_broadcast.send(pkt_out.clone());
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
        let recording_id = uuid::Uuid::new_v4().to_string();

        info!(
            session = %req.session_id,
            format = %req.format,
            path = %req.path,
            recording_id = %recording_id,
            "gRPC start_recording"
        );

        // TODO: 实际启动录制

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

        // TODO: 实际停止录制

        Ok(Response::new(StopRecordingResponse {
            file_path: String::new(),
            duration_ms: 0,
            file_size: 0,
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
