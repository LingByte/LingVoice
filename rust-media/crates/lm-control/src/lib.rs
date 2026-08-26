//! lm-control — gRPC 控制面服务
//!
//! 实现 MediaNode gRPC 服务，接收 Go 控制面的命令，
//! 管理 Rust 媒体面的会话/端点/轨道/路由/混音/录制。

pub mod mixer;
pub mod recorder;
pub mod session;
pub mod service;

pub use service::MediaNodeServer;
