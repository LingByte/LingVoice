//! HTTP 服务器 — HLS / HTTP-FLV 输出 + API
//!
//! Rust media-node 只处理帧级转封装输出，不处理输入协议。
//! 输入协议（RTMP/RTSP/SRT/GB28181/WHIP）由 Go 层处理，
//! Go 解包后通过 gRPC 把 RTP 喂给 Rust，Rust 组装为 MediaFrame。
//!
//! Rust 对外提供的 HTTP 端点：
//! - GET  /hls/{stream_id}/playlist.m3u8    — HLS playlist
//! - GET  /hls/{stream_id}/{segment}        — HLS 分段
//! - POST /hls/{stream_id}/start            — 启动 HLS 转封装
//! - GET  /flv/{stream_id}                  — HTTP-FLV 流
//! - POST /flv/{stream_id}/start            — 启动 FLV 转封装
//! - GET  /api/streams                      — 流列表
//! - GET  /health                           — 健康检查
//!
//! WHIP/WHEP 信令由 Go 层处理，Go 通过 gRPC 把媒体喂给 Rust。

use std::sync::Arc;

use axum::{
    body::Body,
    extract::{Query, State},
    http::{header, StatusCode},
    response::{IntoResponse, Response},
    routing::{get, post},
    Router,
};
use lm_control::MediaNodeServer;
use lm_core::{CodecType, MediaFrame, StreamSink};
use lm_protocol::{FlvRemuxer, HlsConfig, HlsPlaylist, HlsRemuxer, Remuxer};
use lm_stream::{StreamId, StreamRegistry};
use parking_lot::RwLock;
use tokio::sync::mpsc;
use tokio_stream::StreamExt;
use tracing::{error, info, warn};

/// HTTP 服务器共享状态
#[derive(Clone)]
pub struct HttpState {
    pub media_node: MediaNodeServer,
    pub streams: Arc<StreamRegistry>,
    pub hls_outputs: Arc<RwLock<std::collections::HashMap<String, Arc<HlsOutput>>>>,
    pub flv_outputs: Arc<RwLock<std::collections::HashMap<String, Arc<FlvOutput>>>>,
}

/// HLS 输出
pub struct HlsOutput {
    pub playlist: Arc<RwLock<HlsPlaylist>>,
    pub segments: Arc<RwLock<std::collections::HashMap<String, Vec<u8>>>>,
    pub remuxer: Arc<RwLock<HlsRemuxer>>,
    pub sink_handle: Arc<RwLock<Option<Arc<dyn StreamSink>>>>,
}

/// FLV 输出（流式）
pub struct FlvOutput {
    pub subscribers: Arc<RwLock<Vec<mpsc::Sender<Vec<u8>>>>>,
    pub remuxer: Arc<RwLock<FlvRemuxer>>,
    pub sink_handle: Arc<RwLock<Option<Arc<dyn StreamSink>>>>,
}

// ============================================================================
// Sink 实现
// ============================================================================

/// HLS Sink — 收到 MediaFrame 后驱动 remuxer
struct HlsSink {
    remuxer: Arc<RwLock<HlsRemuxer>>,
    segments: Arc<RwLock<std::collections::HashMap<String, Vec<u8>>>>,
    playlist: Arc<RwLock<HlsPlaylist>>,
}

impl StreamSink for HlsSink {
    fn on_frame(&self, frame: &MediaFrame) {
        let mut remuxer = self.remuxer.write();
        let data = remuxer.push_frame(frame);
        *self.playlist.write() = remuxer.playlist().clone();

        // 存储分段数据
        if !data.is_empty() {
            let seg_name = format!("seg{:04}.ts", remuxer.playlist().segment_count());
            self.segments.write().insert(seg_name, data);
        }
    }
}

/// FLV Sink — 收到 MediaFrame 后驱动 remuxer，分发给所有 HTTP-FLV 订阅者
struct FlvSink {
    remuxer: Arc<RwLock<FlvRemuxer>>,
    subscribers: Arc<RwLock<Vec<mpsc::Sender<Vec<u8>>>>>,
}

impl StreamSink for FlvSink {
    fn on_frame(&self, frame: &MediaFrame) {
        let data = self.remuxer.write().push_frame(frame);
        if !data.is_empty() {
            let mut subs = self.subscribers.write();
            // 移除已关闭的订阅者，向存活者发送数据
            subs.retain(|tx| tx.try_send(data.clone()).is_ok());
        }
    }
}

// ============================================================================
// 启动 HTTP 服务器
// ============================================================================

pub async fn start_http_server(media_node: MediaNodeServer, addr: &str) -> anyhow::Result<()> {
    let streams = media_node.streams().clone();

    let state = HttpState {
        media_node: media_node.clone(),
        streams,
        hls_outputs: Arc::new(RwLock::new(std::collections::HashMap::new())),
        flv_outputs: Arc::new(RwLock::new(std::collections::HashMap::new())),
    };

    let app = Router::new()
        // HLS — stream_id 通过查询参数传递：POST /hls/start?stream_id=session/track
        .route("/hls/start", post(hls_start_handler))
        .route("/hls/playlist.m3u8", get(hls_playlist_handler))
        .route("/hls/segment", get(hls_segment_handler))
        // HTTP-FLV
        .route("/flv/start", post(flv_start_handler))
        .route("/flv/stream", get(flv_stream_handler))
        // API
        .route("/api/streams", get(list_streams_handler))
        .route("/health", get(health_handler))
        .with_state(state);

    let socket_addr: std::net::SocketAddr = addr.parse()?;
    info!(addr = addr, "HTTP server starting (HLS/FLV output + API)");

    let listener = tokio::net::TcpListener::bind(socket_addr).await?;
    axum::serve(listener, app).await?;
    Ok(())
}

// ============================================================================
// HLS handlers
// ============================================================================

/// 查询参数：stream_id
#[derive(serde::Deserialize)]
struct StreamIdQuery {
    stream_id: String,
}

/// 查询参数：stream_id + segment
#[derive(serde::Deserialize)]
struct SegmentQuery {
    stream_id: String,
    segment: String,
}

/// 启动 HLS 转封装：订阅流，创建 remuxer
async fn hls_start_handler(
    State(state): State<HttpState>,
    Query(q): Query<StreamIdQuery>,
) -> Response {
    let stream_id = q.stream_id;
    let sid = parse_stream_id(&stream_id);
    let stream = match state.streams.get(&sid) {
        Some(s) => s,
        None => return (StatusCode::NOT_FOUND, "stream not found").into_response(),
    };

    // 检查是否已存在
    {
        let outputs = state.hls_outputs.read();
        if outputs.contains_key(&stream_id) {
            return (StatusCode::OK, "HLS output already active").into_response();
        }
    }

    // 创建 HLS remuxer
    let config = HlsConfig {
        output_dir: format!("./hls/{}", stream_id),
        stream_name: stream_id.clone(),
        segment_duration: 1.0, // 1 秒分段，便于测试
        window_size: 5,
        ..Default::default()
    };
    let remuxer = HlsRemuxer::new(config, stream.codec, CodecType::Opus);

    let output = Arc::new(HlsOutput {
        playlist: Arc::new(RwLock::new(remuxer.playlist().clone())),
        segments: Arc::new(RwLock::new(std::collections::HashMap::new())),
        remuxer: Arc::new(RwLock::new(remuxer)),
        sink_handle: Arc::new(RwLock::new(None)),
    });

    // 创建 sink 并订阅流
    let sink = Arc::new(HlsSink {
        remuxer: output.remuxer.clone(),
        segments: output.segments.clone(),
        playlist: output.playlist.clone(),
    });

    stream.add_source_sink(sink.clone() as Arc<dyn StreamSink>);
    *output.sink_handle.write() = Some(sink as Arc<dyn StreamSink>);

    state.hls_outputs.write().insert(stream_id.clone(), output);

    info!(stream = %stream_id, "HLS output started");
    (
        StatusCode::OK,
        format!("HLS output started for {stream_id}"),
    )
        .into_response()
}

/// HLS playlist
async fn hls_playlist_handler(
    State(state): State<HttpState>,
    Query(q): Query<StreamIdQuery>,
) -> Response {
    let stream_id = q.stream_id;
    let outputs = state.hls_outputs.read();
    if let Some(output) = outputs.get(&stream_id) {
        let playlist = output.playlist.read();
        Response::builder()
            .status(StatusCode::OK)
            .header(header::CONTENT_TYPE, "application/vnd.apple.mpegurl")
            .header(header::CACHE_CONTROL, "no-cache")
            .body(Body::from(playlist.content().to_string()))
            .unwrap()
    } else {
        (
            StatusCode::NOT_FOUND,
            "HLS output not found, POST /hls/{stream_id}/start first",
        )
            .into_response()
    }
}

/// HLS segment
async fn hls_segment_handler(
    State(state): State<HttpState>,
    Query(q): Query<SegmentQuery>,
) -> Response {
    let stream_id = q.stream_id;
    let segment = q.segment;
    let outputs = state.hls_outputs.read();
    if let Some(output) = outputs.get(&stream_id) {
        let segments = output.segments.read();
        if let Some(data) = segments.get(&segment) {
            Response::builder()
                .status(StatusCode::OK)
                .header(header::CONTENT_TYPE, "video/mp2t")
                .body(Body::from(data.clone()))
                .unwrap()
        } else {
            (StatusCode::NOT_FOUND, "segment not found").into_response()
        }
    } else {
        (StatusCode::NOT_FOUND, "HLS output not found").into_response()
    }
}

// ============================================================================
// HTTP-FLV handlers
// ============================================================================

/// 启动 FLV 转封装
async fn flv_start_handler(
    State(state): State<HttpState>,
    Query(q): Query<StreamIdQuery>,
) -> Response {
    let stream_id = q.stream_id;
    let sid = parse_stream_id(&stream_id);
    let stream = match state.streams.get(&sid) {
        Some(s) => s,
        None => return (StatusCode::NOT_FOUND, "stream not found").into_response(),
    };

    {
        let outputs = state.flv_outputs.read();
        if outputs.contains_key(&stream_id) {
            return (StatusCode::OK, "FLV output already active").into_response();
        }
    }

    let output = Arc::new(FlvOutput {
        subscribers: Arc::new(RwLock::new(Vec::new())),
        remuxer: Arc::new(RwLock::new(FlvRemuxer::new(true, true))),
        sink_handle: Arc::new(RwLock::new(None)),
    });

    let sink = Arc::new(FlvSink {
        remuxer: output.remuxer.clone(),
        subscribers: output.subscribers.clone(),
    });

    stream.add_source_sink(sink.clone() as Arc<dyn StreamSink>);
    *output.sink_handle.write() = Some(sink as Arc<dyn StreamSink>);

    state.flv_outputs.write().insert(stream_id.clone(), output);

    info!(stream = %stream_id, "FLV output started");
    (
        StatusCode::OK,
        format!("FLV output started for {stream_id}"),
    )
        .into_response()
}

/// HTTP-FLV 流（chunked transfer streaming）
async fn flv_stream_handler(
    State(state): State<HttpState>,
    Query(q): Query<StreamIdQuery>,
) -> Response {
    let stream_id = q.stream_id;
    let outputs = state.flv_outputs.read();
    let output = match outputs.get(&stream_id) {
        Some(o) => o.clone(),
        None => {
            return (
                StatusCode::NOT_FOUND,
                "FLV output not found, POST /flv/{stream_id}/start first",
            )
                .into_response()
        }
    };
    drop(outputs);

    // 创建 channel 用于向这个 HTTP 客户端推送 FLV 数据
    let (tx, rx) = mpsc::channel::<Vec<u8>>(256);
    output.subscribers.write().push(tx);

    // chunked stream body
    let stream = tokio_stream::wrappers::ReceiverStream::new(rx).map(Ok::<_, std::io::Error>);

    Response::builder()
        .status(StatusCode::OK)
        .header(header::CONTENT_TYPE, "video/x-flv")
        .header(header::CACHE_CONTROL, "no-cache")
        .body(Body::from_stream(stream))
        .unwrap()
}

// ============================================================================
// API handlers
// ============================================================================

async fn list_streams_handler(State(state): State<HttpState>) -> Response {
    let hls_count = state.hls_outputs.read().len();
    let flv_count = state.flv_outputs.read().len();

    let response = serde_json::json!({
        "hls_outputs": hls_count,
        "flv_outputs": flv_count,
        "total_streams": state.media_node.stream_count(),
    });

    Response::builder()
        .status(StatusCode::OK)
        .header(header::CONTENT_TYPE, "application/json")
        .body(Body::from(serde_json::to_string(&response).unwrap()))
        .unwrap()
}

async fn health_handler(State(state): State<HttpState>) -> Response {
    let response = serde_json::json!({
        "status": "ok",
        "node_id": state.media_node.node_id(),
        "sessions": state.media_node.session_count(),
        "streams": state.media_node.stream_count(),
    });

    Response::builder()
        .status(StatusCode::OK)
        .header(header::CONTENT_TYPE, "application/json")
        .body(Body::from(serde_json::to_string(&response).unwrap()))
        .unwrap()
}

// ============================================================================
// 辅助函数
// ============================================================================

/// 解析 stream_id：支持 "session_id/track_id" 或 "session_id" 格式
fn parse_stream_id(s: &str) -> StreamId {
    if let Some((session, track)) = s.split_once('/') {
        StreamId::new(session, track)
    } else {
        // 默认 track_id 为 "video"
        StreamId::new(s, "video")
    }
}
