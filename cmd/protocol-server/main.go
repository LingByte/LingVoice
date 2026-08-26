package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LingByte/LingVoice/pkg/media/transcodectl"
	"github.com/LingByte/LingVoice/pkg/protocol"
	"github.com/LingByte/LingVoice/pkg/protocol/api"
	"github.com/LingByte/LingVoice/pkg/protocol/auth"
	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/LingVoice/pkg/protocol/mqtt"
	"github.com/LingByte/LingVoice/pkg/protocol/rtmp"
	"github.com/LingByte/LingVoice/pkg/protocol/sip"
	"github.com/LingByte/LingVoice/pkg/protocol/streamconfig"
	"github.com/LingByte/LingVoice/pkg/protocol/webrtc"
	"github.com/LingByte/LingVoice/pkg/protocol/whep"
	"github.com/LingByte/LingVoice/pkg/protocol/whip"
	"github.com/LingByte/LingVoice/pkg/protocol/ws"
	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

// DemoEventHandler 是一个简单的事件处理器，打印所有事件和媒体帧统计
type DemoEventHandler struct {
	log *zap.Logger
}

func (h *DemoEventHandler) OnEvent(event common.ProtocolEvent) error {
	switch event.Type {
	case common.EventIncomingCall:
		h.log.Info(">> 来电",
			zap.String("protocol", string(event.Protocol)),
			zap.String("session", event.SessionID),
			zap.String("from", event.From),
			zap.String("to", event.To),
		)
	case common.EventRinging:
		h.log.Info(">> 振铃", zap.String("session", event.SessionID))
	case common.EventAnswered:
		h.log.Info(">> 接听", zap.String("session", event.SessionID))
	case common.EventHangup:
		h.log.Info(">> 挂断", zap.String("session", event.SessionID))
	case common.EventTrackAdded:
		if event.Track != nil {
			h.log.Info(">> 轨道就绪",
				zap.String("session", event.SessionID),
				zap.String("trackID", string(event.Track.ID)),
				zap.String("kind", event.Track.Kind.String()),
				zap.String("codec", event.Track.Codec.String()),
				zap.Uint32("sampleRate", event.Track.SampleRate),
				zap.Uint16("channels", event.Track.Channels),
				zap.String("direction", fmt.Sprintf("%d", event.Track.Direction)),
			)
		} else {
			h.log.Info(">> 轨道就绪", zap.String("session", event.SessionID))
		}
	case common.EventTrackRemoved:
		h.log.Info(">> 轨道移除", zap.String("session", event.SessionID))
	case common.EventDataChannel:
		h.log.Info(">> 数据通道", zap.String("session", event.SessionID))
	case common.EventReconnect:
		h.log.Info(">> 重连", zap.String("session", event.SessionID))
	case common.EventError:
		h.log.Error(">> 错误", zap.String("session", event.SessionID), zap.Error(event.Err))
	}
	return nil
}

func (h *DemoEventHandler) OnMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	// 只打印前几帧和统计，避免日志爆炸
	h.log.Debug("<< 媒体帧",
		zap.String("session", sessionID),
		zap.String("trackID", string(trackID)),
		zap.Uint8("type", uint8(frame.Type)),
		zap.String("codec", frame.Codec.String()),
		zap.Uint16("seq", frame.Sequence),
		zap.Uint32("ts", frame.Timestamp),
		zap.Int("payloadLen", len(frame.Payload)),
	)
	return nil
}

func (h *DemoEventHandler) OnData(sessionID string, msg common.DataMessage) error {
	h.log.Info("<< 数据通道消息",
		zap.String("session", sessionID),
		zap.String("channel", msg.Channel),
		zap.Int("len", len(msg.Data)),
		zap.Bool("isString", msg.IsString),
	)
	return nil
}

func main() {
	var (
		wsAddr      = flag.String("ws-addr", ":8080", "WebSocket 监听地址")
		wsPath      = flag.String("ws-path", "/ws/voice", "WebSocket 路径")
		sipAddr     = flag.String("sip-addr", "0.0.0.0:5060", "SIP 监听地址")
		webrtcAddr  = flag.String("webrtc-addr", ":8081", "WebRTC 信令监听地址")
		rtmpAddr    = flag.String("rtmp-addr", ":1935", "RTMP 监听地址")
		whipAddr    = flag.String("whip-addr", ":8082", "WHIP 监听地址")
		whepAddr    = flag.String("whep-addr", ":8083", "WHEP 监听地址")
		apiAddr     = flag.String("api-addr", ":8090", "REST API 监听地址")
		mqttBroker  = flag.String("mqtt-broker", "tcp://localhost:1883", "MQTT broker 地址")
		enableWS    = flag.Bool("ws", true, "启用 WebSocket 协议")
		enableSIP   = flag.Bool("sip", true, "启用 SIP 协议")
		enableRTC   = flag.Bool("webrtc", true, "启用 WebRTC 协议")
		enableRTMP  = flag.Bool("rtmp", true, "启用 RTMP 协议")
		enableWHIP  = flag.Bool("whip", true, "启用 WHIP 协议")
		enableWHEP  = flag.Bool("whep", true, "启用 WHEP 协议")
		enableMQTT  = flag.Bool("mqtt", false, "启用 MQTT 协议（需先启动 broker）")
		enableAPI   = flag.Bool("api", true, "启用 REST API 控制面")
		authMode    = flag.String("auth", "disabled", "鉴权模式: disabled/token/sign")
		authToken   = flag.String("auth-token", "", "鉴权 token (token 模式)")
		authSecret  = flag.String("auth-secret", "", "签名密钥 (sign 模式)")
		streamCfg   = flag.String("stream-config", "", "静态流配置文件路径 (YAML)")
	)
	flag.Parse()

	// 初始化 ling-base logger
	_ = logger.Init(&logger.LogConfig{
		Level:    "debug",
		Filename: "logs/protocol-server.log",
		MaxSize:  100,
		MaxAge:   30,
		Daily:    true,
	}, "dev")
	defer logger.Sync()

	log := logger.Lg

	handler := &DemoEventHandler{log: log}

	// 创建协议管理器
	mgr := protocol.NewManager(handler, log)

	// 配置各协议
	if *enableWS {
		wsConfig := ws.DefaultConfig()
		wsConfig.Addr = *wsAddr
		wsConfig.Path = *wsPath
		mgr.WithWebSocket(wsConfig)
		log.Info("WebSocket 协议已启用", zap.String("addr", *wsAddr), zap.String("path", *wsPath))
	}

	if *enableSIP {
		sipConfig := sip.DefaultConfig()
		sipConfig.Addr = *sipAddr
		sipConfig.AuthFunc = nil // 开发模式：不鉴权
		sipConfig.RouteFunc = func(to string) (string, bool) {
			log.Info("SIP 路由查询", zap.String("to", to))
			return to, true // 默认接受所有
		}
		if _, err := mgr.WithSIP(sipConfig); err != nil {
			log.Error("SIP 初始化失败", zap.Error(err))
		} else {
			log.Info("SIP 协议已启用", zap.String("addr", *sipAddr))
		}
	}

	if *enableRTC {
		rtcConfig := webrtc.DefaultConfig()
		rtcConfig.Addr = *webrtcAddr
		mgr.WithWebRTC(rtcConfig)
		log.Info("WebRTC 协议已启用", zap.String("addr", *webrtcAddr))
	}

	if *enableRTMP {
		rtmpConfig := rtmp.DefaultConfig()
		rtmpConfig.Addr = *rtmpAddr
		mgr.WithRTMP(rtmpConfig)
		log.Info("RTMP 协议已启用", zap.String("addr", *rtmpAddr))
	}

	if *enableWHIP {
		whipConfig := whip.DefaultConfig()
		whipConfig.Addr = *whipAddr
		mgr.WithWHIP(whipConfig)
		log.Info("WHIP 协议已启用", zap.String("addr", *whipAddr))
	}

	if *enableWHEP {
		whepConfig := whep.DefaultConfig()
		whepConfig.Addr = *whepAddr
		mgr.WithWHEP(whepConfig)
		log.Info("WHEP 协议已启用", zap.String("addr", *whepAddr))
	}

	if *enableAPI {
		apiConfig := api.DefaultConfig()
		apiConfig.Addr = *apiAddr
		mgr.WithAPI(apiConfig)

		// 设置转码控制器
		tc := transcodectl.New()
		mgr.SetTranscodeController(tc)
		log.Info("REST API 已启用", zap.String("addr", *apiAddr))

		// 鉴权配置
		if *authMode != "disabled" {
			authCfg := auth.DefaultConfig()
			switch *authMode {
			case "token":
				authCfg.Mode = auth.ModeToken
				authCfg.Tokens = map[string]bool{*authToken: true}
			case "sign":
				authCfg.Mode = auth.ModeSign
				authCfg.Secret = *authSecret
			}
			log.Info("鉴权已启用", zap.String("mode", *authMode))
		}
	}

	// 静态流配置
	if *streamCfg != "" {
		cfg, err := streamconfig.LoadFromFile(*streamCfg)
		if err != nil {
			log.Error("加载流配置失败", zap.String("file", *streamCfg), zap.Error(err))
		} else {
			log.Info("流配置已加载", zap.Int("streams", len(cfg.Streams)))
			// TODO: 注册拉流启动器后调用 StartAutoStart
		}
	}

	if *enableMQTT {
		mqttConfig := mqtt.DefaultConfig()
		mqttConfig.Broker = *mqttBroker
		mgr.WithMQTT(mqttConfig)
		log.Info("MQTT 协议已启用", zap.String("broker", *mqttBroker))
	}

	// 启动
	log.Info("LingVoice 协议层启动中...")
	if err := mgr.Start(); err != nil {
		log.Error("启动失败", zap.Error(err))
		os.Exit(1)
	}

	log.Info("========================================")
	log.Info("LingVoice 协议层已启动")
	log.Info("========================================")
	if *enableWS {
		log.Info("WebSocket:  ws://localhost" + *wsAddr + *wsPath)
	}
	if *enableSIP {
		log.Info("SIP:        udp://" + *sipAddr)
	}
	if *enableRTC {
		log.Info("WebRTC:     ws://localhost" + *webrtcAddr)
	}
	if *enableRTMP {
		log.Info("RTMP:       rtmp://localhost" + *rtmpAddr + "/live/<stream>")
	}
	if *enableWHIP {
		log.Info("WHIP:       http://localhost" + *whipAddr + "/whip")
	}
	if *enableWHEP {
		log.Info("WHEP:       http://localhost" + *whepAddr + "/whep")
	}
	if *enableMQTT {
		log.Info("MQTT:       " + *mqttBroker + " (topic: lingvoice/{session}/...)")
	}
	if *enableAPI {
		log.Info("REST API:   http://localhost" + *apiAddr + "/api")
	}
	log.Info("========================================")
	log.Info("等待连接...")

	// 等待退出信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Info("正在关闭...")
	mgr.Close()
	time.Sleep(200 * time.Millisecond) // 让 goroutine 退出
	log.Info("已关闭")
}
