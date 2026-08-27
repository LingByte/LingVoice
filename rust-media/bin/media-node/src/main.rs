use lm_control::MediaNodeServer;
use std::net::SocketAddr;
use tracing::info;
use tracing_appender::non_blocking::WorkerGuard;
use tracing_subscriber::{fmt, layer::SubscriberExt, util::SubscriberInitExt, EnvFilter};

mod http_server;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let _guard = init_logging();

    let node_id = hostname_or_id();
    let grpc_addr: SocketAddr = "0.0.0.0:50051".parse()?;
    let http_addr = std::env::var("HTTP_ADDR").unwrap_or_else(|_| "0.0.0.0:8082".to_string());

    info!(node_id = %node_id, grpc_addr = %grpc_addr, http_addr = %http_addr, "starting LingVoice media node");

    let server = MediaNodeServer::new(node_id.clone());

    // 启动 gRPC 服务器（MediaNodeServer 现在是 Clone，内部都是 Arc）
    let grpc_server = server.clone();
    let grpc_task = tokio::spawn(async move {
        info!(node_id = %grpc_server.node_id(), "gRPC server listening on :50051");
        tonic::transport::Server::builder()
            .add_service(lm_control::service::media_node_server::MediaNodeServer::new(grpc_server))
            .serve(grpc_addr)
            .await
    });

    // 启动 HTTP 服务器（HLS/HTTP-FLV 输出 + API）
    let http_server = server.clone();
    let http_task = tokio::spawn(async move {
        if let Err(e) = http_server::start_http_server(http_server, &http_addr).await {
            tracing::error!(error = %e, "HTTP server failed");
        }
    });

    // 等待任一服务器退出
    tokio::select! {
        result = grpc_task => {
            if let Ok(Err(e)) = result {
                return Err(Box::new(e) as Box<dyn std::error::Error>);
            }
        }
        result = http_task => {
            if let Err(e) = result {
                return Err(Box::new(e) as Box<dyn std::error::Error>);
            }
        }
    }

    Ok(())
}

/// 初始化日志：同时输出到 stdout 和 logs/rust-media/（按天滚动）
///
/// 默认级别 info。可通过 RUST_LOG 环境变量覆盖，如:
///   RUST_LOG=info         — 仅 info 及以上
///   RUST_LOG=debug        — 全局 debug
///   RUST_LOG=lm_control=debug  — 仅 lm_control debug
fn init_logging() -> WorkerGuard {
    let filter = EnvFilter::try_from_default_env().unwrap_or_else(|_| "info".into());

    // 文件日志：按天滚动
    let file_appender = tracing_appender::rolling::daily("logs/rust-media", "media-node.log");
    let (non_blocking_file, guard) = tracing_appender::non_blocking(file_appender);

    tracing_subscriber::registry()
        .with(filter)
        .with(fmt::layer().with_writer(std::io::stdout))
        .with(fmt::layer().with_writer(non_blocking_file))
        .init();

    info!("logging initialized: stdout + logs/rust-media/");

    // guard 必须保持存活，否则后台写入线程会退出
    guard
}

fn hostname_or_id() -> String {
    std::env::var("NODE_ID").unwrap_or_else(|_| {
        std::env::var("HOSTNAME").unwrap_or_else(|_| uuid::Uuid::new_v4().to_string())
    })
}
