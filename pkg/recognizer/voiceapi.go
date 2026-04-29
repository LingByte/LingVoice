package recognizer

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"encoding/json"
	"time"

	media2 "github.com/LingByte/LingVoice/pkg/utils/media"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

type VoiceapiASR struct {
	handler     media2.MediaHandler
	conn        *websocket.Conn
	sendReqTime *time.Time
	endReqTime  *time.Time
	Sentence    string
}

type VoiceapiASROption struct {
	Url         string `json:"url" yaml:"url" env:"ASR_VOICEAPI_URL"`
	ReqChanSize int    `json:"reqChanSize" yaml:"req_chan_size" default:"128"`
}

type VoiceapiResponse struct {
	Idx      int    `json:"idx"`
	Finished bool   `json:"finished"`
	Text     string `json:"text"`
}

func NewVoiceapiASROption(url string) VoiceapiASROption {
	return VoiceapiASROption{
		Url:         url,
		ReqChanSize: 128,
	}
}

func WithVoiceapiASR(opt VoiceapiASROption) media2.MediaHandlerFunc {
	executor := media2.NewAsyncTaskRunner[[]byte](opt.ReqChanSize)

	vapi := &VoiceapiASR{}
	executor.RequestBuilder = func(h media2.MediaHandler, packet media2.MediaPacket) (*media2.PacketRequest[[]byte], error) {
		audioPacket, ok := packet.(*media2.AudioPacket)
		if !ok {
			h.EmitPacket(vapi, packet)
			return nil, nil
		}
		if vapi.handler == nil {
			vapi.handler = h
		}
		audioPacket.Payload, _ = media2.ResamplePCM(audioPacket.Payload, h.GetSession().Codec().SampleRate, 16000)
		req := media2.PacketRequest[[]byte]{
			Req:       audioPacket.Payload,
			Interrupt: true,
		}
		return &req, nil
	}

	executor.InitCallback = func(h media2.MediaHandler) error {
		conn, _, err := websocket.DefaultDialer.Dial(opt.Url, nil)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"handler": h,
				"url":     opt.Url,
			}).WithError(err).Error("voiceapi asr: failed to dial websocket")
			return err
		}
		vapi.conn = conn
		go vapi.recvFrames()
		return err
	}

	executor.TerminateCallback = func(h media2.MediaHandler) error {
		return vapi.conn.Close()
	}

	executor.StateCallback = func(h media2.MediaHandler, event media2.StateChange) error {
		switch event.State {
		case media2.StartSpeaking:
			n := time.Now()
			vapi.sendReqTime = &n
		case media2.StartSilence:
			n := time.Now()
			vapi.endReqTime = &n
		case media2.Hangup:
			return vapi.conn.Close()
		}
		return nil
	}

	executor.TaskExecutor = func(ctx context.Context, h media2.MediaHandler, req media2.PacketRequest[[]byte]) error {
		return vapi.conn.WriteMessage(websocket.BinaryMessage, req.Req)
	}

	return executor.HandleMediaData
}

func (vapi *VoiceapiASR) recvFrames() {
	for {
		messageType, message, err := vapi.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				logrus.WithFields(logrus.Fields{
					"sessionID": vapi.handler.GetSession().ID,
				}).Info("voiceapi asr: recv close message, connection closed")
			} else {
				logrus.WithFields(logrus.Fields{
					"sessionID":   vapi.handler.GetSession().ID,
					"message":     string(message),
					"messageType": messageType,
				}).WithError(err).Error("voiceapi asr: recv error, connection closed")
				vapi.handler.CauseError(vapi, err)
			}
			if vapi.Sentence != "" {
				vapi.handler.EmitPacket(vapi, &media2.TextPacket{Text: vapi.Sentence, IsTranscribed: true})
				vapi.handler.EmitState(vapi, media2.Completed, &media2.CompletedData{
					SenderName: "asr.voiceapi",
					Result:     vapi.Sentence,
				})
				if vapi.sendReqTime != nil {
					vapi.handler.AddMetric("asr.voiceapi", time.Since(*vapi.sendReqTime))
				}
			}
			return
		}

		var res VoiceapiResponse
		if err = json.Unmarshal(message, &res); err != nil {
			logrus.WithFields(logrus.Fields{
				"sessionID": vapi.handler.GetSession().ID,
				"message":   string(message),
			}).WithError(err).Error("voiceapi asr: failed to unmarshal message")
			vapi.handler.CauseError(vapi, err)
			return
		}

		vapi.Sentence = res.Text
		vapi.handler.EmitState(vapi, media2.Transcribing, &media2.TranscribingData{
			SenderName: "asr.voiceapi",
			Result:     vapi.Sentence,
		})

		if res.Finished {
			vapi.handler.EmitState(vapi, media2.Completed, &media2.CompletedData{
				SenderName: "asr.voiceapi",
				Result:     res.Text,
			})
			vapi.handler.EmitPacket(vapi, &media2.TextPacket{Text: vapi.Sentence, IsTranscribed: true})
			if vapi.endReqTime != nil {
				vapi.handler.AddMetric("asr.voiceapi.end", time.Since(*vapi.endReqTime))
			}
		}
		if vapi.sendReqTime != nil {
			vapi.handler.AddMetric("asr.voiceapi", time.Since(*vapi.sendReqTime))
		}
	}
}
