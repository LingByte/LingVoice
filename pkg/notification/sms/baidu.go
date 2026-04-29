package sms

import (
	"context"
	"fmt"
	"strings"
)

type BaiduConfig struct {
	AK       string
	SK       string
	InvokeID string
	Domain   string
}

type BaiduProvider struct {
	cfg BaiduConfig
}

func NewBaidu(cfg BaiduConfig) (*BaiduProvider, error) {
	if strings.TrimSpace(cfg.AK) == "" || strings.TrimSpace(cfg.SK) == "" || strings.TrimSpace(cfg.InvokeID) == "" {
		return nil, fmt.Errorf("%w: baidu requires ak/sk/invokeId", ErrInvalidConfig)
	}
	return &BaiduProvider{cfg: cfg}, nil
}

func (p *BaiduProvider) Kind() ProviderKind { return ProviderBaidu }

func (p *BaiduProvider) Send(ctx context.Context, req SendRequest) (*SendResult, error) {
	_ = ctx
	if err := ValidateBasic(req); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Message.Template) == "" {
		return nil, fmt.Errorf("%w: baidu requires template", ErrInvalidArgument)
	}
	return &SendResult{Provider: p.Kind(), Accepted: false, Error: ErrNotImplemented.Error(), SentAtUnix: nowUnix()}, ErrNotImplemented
}

