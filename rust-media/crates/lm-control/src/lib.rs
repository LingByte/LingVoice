//! lm-control — gRPC 控制面服务
//!
//! 实现 MediaNode gRPC 服务，接收 Go 控制面的命令，
//! 管理 Rust 媒体面的会话/端点/轨道/路由/混音/录制。

pub mod bridge;
pub mod events;
pub mod mixer;
pub mod recorder;
pub mod service;
pub mod session;
pub mod transcode_manager;

pub use bridge::BridgeManager;
pub use events::EventBus;
pub use service::MediaNodeServer;
pub use transcode_manager::TranscodeManager;
