package webrtc

import (
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"
	"github.com/pion/interceptor/pkg/nack"
	"github.com/pion/interceptor/pkg/report"
	"github.com/pion/interceptor/pkg/stats"
	"github.com/pion/interceptor/pkg/twcc"
	"github.com/pion/webrtc/v4"
)

// createInterceptorRegistry 创建拦截器注册表
// 返回 registry 和 stats getter（用于可观测性指标采集）
func createInterceptorRegistry(cfg Config, me *webrtc.MediaEngine, isPublisher bool) (*interceptor.Registry, stats.Getter, error) {
	ir := &interceptor.Registry{}

	// 1. NACK 重传 — 发布者用 responder（响应 NACK 请求重传），订阅者用 generator（生成 NACK 请求）
	if isPublisher {
		nackResponder, err := nack.NewResponderInterceptor()
		if err != nil {
			return nil, nil, err
		}
		ir.Add(nackResponder)
	} else {
		nackGenerator, err := nack.NewGeneratorInterceptor(
			nack.GeneratorMaxNacksPerPacket(cfg.NACKMaxRetry),
			nack.GeneratorInterval(100*time.Millisecond),
		)
		if err != nil {
			return nil, nil, err
		}
		ir.Add(nackGenerator)
	}

	// 2. 间隔 PLI（关键帧请求）— 订阅者端定期请求关键帧
	if !isPublisher && cfg.PLIInterval > 0 {
		pliReceiver, err := intervalpli.NewReceiverInterceptor(
			intervalpli.GeneratorInterval(cfg.PLIInterval),
		)
		if err != nil {
			return nil, nil, err
		}
		ir.Add(pliReceiver)
	}

	// 3. RTCP Sender/Receiver Report — 定期发送 SR/RR
	if isPublisher {
		senderReport, err := report.NewSenderInterceptor(
			report.SenderInterval(1 * time.Second),
		)
		if err != nil {
			return nil, nil, err
		}
		ir.Add(senderReport)
	} else {
		receiverReport, err := report.NewReceiverInterceptor(
			report.ReceiverInterval(1 * time.Second),
		)
		if err != nil {
			return nil, nil, err
		}
		ir.Add(receiverReport)
	}

	// 4. TWCC — Transport-wide Congestion Control
	if cfg.EnableTWCC {
		twccHeaderExt, err := twcc.NewHeaderExtensionInterceptor()
		if err != nil {
			return nil, nil, err
		}
		ir.Add(twccHeaderExt)

		if isPublisher {
			// 发布者端发送 TWCC 反馈
			twccSender, err := twcc.NewSenderInterceptor(
				twcc.SendInterval(100*time.Millisecond),
			)
			if err != nil {
				return nil, nil, err
			}
			ir.Add(twccSender)
		}
	}

	// 5. Stats 拦截器 — 收集统计信息
	statsFactory, err := stats.NewInterceptor()
	if err != nil {
		return nil, nil, err
	}

	// 通过回调获取 stats getter
	var statsGetter stats.Getter
	statsFactory.OnNewPeerConnection(func(_ string, getter stats.Getter) {
		statsGetter = getter
	})
	ir.Add(statsFactory)

	// 6. 使用 pion/webrtc 内置的默认拦截器
	if err := webrtc.RegisterDefaultInterceptors(me, ir); err != nil {
		return nil, nil, err
	}

	return ir, statsGetter, nil
}

// createSettingEngine 创建 SettingEngine
func createSettingEngine(cfg Config) webrtc.SettingEngine {
	se := webrtc.SettingEngine{}

	// ICE UDP 端口范围
	if cfg.EphemeralUDPPortRange[0] > 0 && cfg.EphemeralUDPPortRange[1] > 0 {
		_ = se.SetEphemeralUDPPortRange(cfg.EphemeralUDPPortRange[0], cfg.EphemeralUDPPortRange[1])
	}

	// ICE 超时
	if cfg.ICEDisconnectedTimeout > 0 {
		se.SetICETimeouts(
			time.Duration(cfg.ICEDisconnectedTimeout)*time.Second,
			time.Duration(cfg.ICEFailedTimeout)*time.Second,
			time.Duration(cfg.ICEKeepaliveInterval)*time.Second,
		)
	}

	// 禁用 SRTP 重放保护（SFU 场景下可提高性能）
	se.DisableSRTPReplayProtection(true)
	se.DisableSRTCPReplayProtection(true)

	// DTLS 重传间隔
	se.SetDTLSRetransmissionInterval(100 * time.Millisecond)

	return se
}
