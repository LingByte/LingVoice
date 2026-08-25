use lm_control::MediaNodeServer;
use std::net::SocketAddr;
use tracing::info;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // 初始化日志
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| "info,lm_control=debug".into()),
        )
        .init();

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

fn hostname_or_id() -> String {
    std::env::var("NODE_ID").unwrap_or_else(|_| {
        std::env::var("HOSTNAME").unwrap_or_else(|_| {
            uuid::Uuid::new_v4().to_string()
        })
    })
}
