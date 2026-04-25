// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package notification

import (
	"crypto/tls"
	"fmt"
	"net/smtp"
	"time"
)

// SMTPConfig holds SMTP connection settings.
type SMTPConfig struct {
	Host     string
	Port     int64
	Username string
	Password string
	From     string
	FromName string
}

// SMTPClient implements MailProvider over SMTP.
type SMTPClient struct {
	Config SMTPConfig
	sender ParsedSender
}

// NewSMTPClient builds an SMTP mail provider.
func NewSMTPClient(config SMTPConfig) (*SMTPClient, error) {
	p, err := ParseMailSender(config.From, config.FromName)
	if err != nil {
		return nil, err
	}
	return &SMTPClient{Config: config, sender: p}, nil
}

// Kind implements MailProvider.
func (s *SMTPClient) Kind() ProviderKind {
	return ProviderSMTP
}

// SendHTML sends a MIME HTML message. Returns a synthetic message id for logging (SMTP has no provider id).
func (s *SMTPClient) SendHTML(to, subject, htmlBody string) (string, error) {
	msg := "MIME-Version: 1.0\r\n"
	msg += "Content-Type: text/html; charset=\"UTF-8\"\r\n"
	msg += fmt.Sprintf("From: %s\r\n", s.sender.HeaderFrom)
	msg += fmt.Sprintf("To: %s\r\n", to)
	msg += fmt.Sprintf("Subject: %s\r\n", subject)
	msg += "\r\n" + htmlBody

	addr := fmt.Sprintf("%s:%d", s.Config.Host, s.Config.Port)
	auth := smtp.PlainAuth("", s.Config.Username, s.Config.Password, s.Config.Host)
	env := s.sender.Envelope

	tlsConfig := &tls.Config{
		ServerName:         s.Config.Host,
		InsecureSkipVerify: false,
	}

	if s.Config.Port == 465 {
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return "", fmt.Errorf("smtp dial: %w", err)
		}
		defer conn.Close()

		client, err := smtp.NewClient(conn, s.Config.Host)
		if err != nil {
			return "", fmt.Errorf("smtp client: %w", err)
		}
		defer client.Close()

		if err = client.Auth(auth); err != nil {
			return "", fmt.Errorf("smtp auth: %w", err)
		}
		if err = client.Mail(env); err != nil {
			return "", fmt.Errorf("smtp mail from: %w", err)
		}
		if err = client.Rcpt(to); err != nil {
			return "", fmt.Errorf("smtp rcpt: %w", err)
		}
		w, err := client.Data()
		if err != nil {
			return "", fmt.Errorf("smtp data: %w", err)
		}
		if _, err = w.Write([]byte(msg)); err != nil {
			_ = w.Close()
			return "", fmt.Errorf("smtp write: %w", err)
		}
		if err = w.Close(); err != nil {
			return "", fmt.Errorf("smtp close writer: %w", err)
		}
		_ = client.Quit()
	} else {
		if err := smtp.SendMail(addr, auth, env, []string{to}, []byte(msg)); err != nil {
			return "", fmt.Errorf("smtp send: %w", err)
		}
	}

	return fmt.Sprintf("smtp-%d", time.Now().UnixNano()), nil
}
