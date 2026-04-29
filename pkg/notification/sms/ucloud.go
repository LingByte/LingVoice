package sms

import (
	"context"
	"fmt"
	"strings"
)

type UCloudConfig struct {
	PublicKey  string
	PrivateKey string
	ProjectID  string
}

type UCloudProvider struct {
	cfg UCloudConfig
}

func NewUCloud(cfg UCloudConfig) (*UCloudProvider, error) {
	if strings.TrimSpace(cfg.PublicKey) == "" || strings.TrimSpace(cfg.PrivateKey) == "" {
		return nil, fmt.Errorf("%w: ucloud requires publicKey/privateKey", ErrInvalidConfig)
	}
	return &UCloudProvider{cfg: cfg}, nil
}

func (p *UCloudProvider) Kind() ProviderKind { return ProviderUCloud }

func (p *UCloudProvider) Send(ctx context.Context, req SendRequest) (*SendResult, error) {
	_ = ctx
	if err := ValidateBasic(req); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Message.Template) == "" && strings.TrimSpace(req.Message.Content) == "" {
		return nil, fmt.Errorf("%w: ucloud requires template or content", ErrInvalidArgument)
	}
	return &SendResult{Provider: p.Kind(), Accepted: false, Error: ErrNotImplemented.Error(), SentAtUnix: nowUnix()}, ErrNotImplemented
}

