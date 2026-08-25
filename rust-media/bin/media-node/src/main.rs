use lm_control::MediaNodeServer;
use std::net::SocketAddr;
use tracing::info;
use tracing_appender::non_blocking::WorkerGuard;
use tracing_subscriber::{fmt, layer::SubscriberExt, util::SubscriberInitExt, EnvFilter};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let _guard = init_logging();

    let node_id = hostname_or_id();
    let addr: SocketAddr = "0.0.0.0:50051".parse()?;

    info!(node_id = %node_id, addr = %addr, "starting LingVoice media node");

    let server = MediaNodeServer::new(node_id.clone());

    info!(node_id = %node_id, "media node listening on :50051");

    tonic::transport::Server::builder()
        .add_service(lm_control::service::media_node_server::MediaNodeServer::new(server))
        .serve(addr)
        .await?;

    Ok(())
}

/// 初始化日志：同时输出到 stdout 和 logs/rust-media/（按天滚动）
fn init_logging() -> WorkerGuard {
    let filter = EnvFilter::try_from_default_env()
        .unwrap_or_else(|_| "info,lm_control=debug".into());

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
        std::env::var("HOSTNAME").unwrap_or_else(|_| {
            uuid::Uuid::new_v4().to_string()
        })
    })
}
