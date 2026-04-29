package recognizer

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"time"

	media2 "github.com/LingByte/LingVoice/pkg/utils/media"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming/types"
	"github.com/sirupsen/logrus"
)

type AwsASR struct {
	handler     media2.MediaHandler
	transStream *transcribestreaming.StartStreamTranscriptionEventStream
	sentence    string
	words       []byte
	ttfbDone    bool
	sendReqTime time.Time
}

type AwsASROption struct {
	AppID       string              `json:"appId" yaml:"app_id"`
	Region      string              `json:"region" yaml:"region"`
	Encoding    types.MediaEncoding `json:"encoding" yaml:"encoding"`
	SampleRate  int32               `json:"sampleRate" yaml:"sample_rate"`
	ReqChanSize int                 `json:"reqChanSize" yaml:"req_chan_size" default:"128"`
}

func NewAwsASROption(appId, region string, language string) AwsASROption {
	return AwsASROption{
		AppID:       appId,
		Region:      region,
		ReqChanSize: 128,
	}
}

func WithAwsASR(opt AwsASROption) media2.MediaHandlerFunc {
	executor := media2.NewAsyncTaskRunner[[]byte](opt.ReqChanSize)

	aws := &AwsASR{}

	executor.ConcurrentMode = false
	executor.RequestBuilder = func(h media2.MediaHandler, packet media2.MediaPacket) (*media2.PacketRequest[[]byte], error) {
		audioPacket, ok := packet.(*media2.AudioPacket)
		if !ok {
			h.EmitPacket(aws, packet)
			return nil, nil
		}
		if aws.handler == nil {
			aws.handler = h
		}
		decoded, _ := media2.ResamplePCM(audioPacket.Payload, h.GetSession().Codec().SampleRate, 16000)
		req := media2.PacketRequest[[]byte]{
			Req:       decoded,
			Interrupt: true,
		}
		return &req, nil
	}

	executor.InitCallback = func(h media2.MediaHandler) error {
		client := transcribestreaming.New(transcribestreaming.Options{
			AppID:  opt.AppID,
			Region: opt.Region,
		})
		transInput := transcribestreaming.StartStreamTranscriptionInput{
			MediaEncoding:        opt.Encoding,
			MediaSampleRateHertz: &opt.SampleRate,
		}
		transOutput, err := client.StartStreamTranscription(context.Background(), &transInput, func(options *transcribestreaming.Options) {
			logrus.WithFields(logrus.Fields{
				"sessionID": aws.handler.GetSession().ID,
				"options":   options,
			}).Info("Invoke aws options.")
		})
		if err != nil {
			return err
		}
		aws.transStream = transOutput.GetStream()
		go aws.recvEvents()
		return nil
	}

	executor.TerminateCallback = func(h media2.MediaHandler) error {
		return aws.transStream.Close()
	}

	executor.StateCallback = func(h media2.MediaHandler, event media2.StateChange) error {
		switch event.State {
		case media2.Hangup:
			return aws.transStream.Close()
		}
		return nil
	}

	executor.TaskExecutor = func(ctx context.Context, h media2.MediaHandler, req media2.PacketRequest[[]byte]) error {
		return aws.transStream.Writer.Send(ctx, &types.AudioStreamMemberAudioEvent{Value: types.AudioEvent{AudioChunk: req.Req}})
	}

	return executor.HandleMediaData
}

func (aws *AwsASR) recvEvents() {
	eventChan := aws.transStream.Events()
	select {
	case event, ok := <-eventChan:
		if !ok {
			logrus.Error("aws stream closed.")
			return
		}
		transcriptEvent, ok := event.(*types.TranscriptResultStreamMemberTranscriptEvent)
		if !ok {
			logrus.Error("known aws stream.")
			return
		}

		if !aws.ttfbDone {
			aws.ttfbDone = true
			aws.handler.AddMetric("asr.aws.ttfb", time.Since(aws.sendReqTime))
		}

		for _, result := range transcriptEvent.Value.Transcript.Results {
			if result.IsPartial {
				logrus.Info("aws partial result:", result)
			} else {
				for _, alternative := range result.Alternatives {
					aws.words = append(aws.words, []byte(*alternative.Transcript)...)
				}
				aws.sentence = string(aws.words)
				aws.handler.EmitPacket(aws, &media2.TextPacket{Text: aws.sentence, IsTranscribed: true})
				aws.handler.EmitState(aws, media2.Transcribing, aws.sentence)
			}
		}
	case <-aws.handler.GetContext().Done():
		return
	}
	aws.handler.AddMetric("asr.aws", time.Since(aws.sendReqTime))
}
