// Package webhook 提供基于 HTTP Webhook 的事件回调机制。
//
// Sender 实现 common.EventHandler 接口，将协议信令事件以 JSON POST
// 请求异步推送到外部 HTTP 端点。每次发送都会附带 HMAC-SHA256 签名头
// (X-LingVoice-Signature)，接收方可据此验证请求来源。发送失败时按
// 指数退避策略重试。
//
// 媒体帧 (OnMediaFrame) 与数据通道消息 (OnData) 频率过高，默认不转发。
package webhook
