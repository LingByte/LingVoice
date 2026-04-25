// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package notification

import (
	"fmt"
	"mime"
	"net/mail"
	"strings"
	"unicode"
)

// ParsedSender is the envelope address (SMTP MAIL FROM / SendCloud `from`) plus display name and full RFC From header line.
type ParsedSender struct {
	Envelope   string // bare email only
	Display    string // UTF-8 display name for SendCloud fromName; may be empty
	HeaderFrom string // value for MIME "From:" header (includes encoded-word if needed)
}

// ParseMailSender parses MailConfig.from (optional "Name <email>") and optional from_name fallback.
func ParseMailSender(fromField, nameFallback string) (ParsedSender, error) {
	fromField = strings.TrimSpace(fromField)
	nameFallback = strings.TrimSpace(nameFallback)
	if fromField == "" {
		return ParsedSender{}, fmt.Errorf("empty from address")
	}
	if a, err := mail.ParseAddress(fromField); err == nil && a.Address != "" {
		envelope := strings.TrimSpace(a.Address)
		disp := strings.TrimSpace(a.Name)
		if disp == "" {
			disp = nameFallback
		}
		return ParsedSender{
			Envelope:   envelope,
			Display:    disp,
			HeaderFrom: formatMailFromHeader(disp, envelope),
		}, nil
	}
	if strings.Contains(fromField, "@") && !strings.ContainsAny(fromField, "<>") {
		envelope := strings.TrimSpace(fromField)
		return ParsedSender{
			Envelope:   envelope,
			Display:    nameFallback,
			HeaderFrom: formatMailFromHeader(nameFallback, envelope),
		}, nil
	}
	return ParsedSender{}, fmt.Errorf("invalid from address %q", fromField)
}

func formatMailFromHeader(displayName, email string) string {
	dn := strings.TrimSpace(displayName)
	em := strings.TrimSpace(email)
	if em == "" {
		return ""
	}
	if dn == "" {
		return em
	}
	if isASCIIString(dn) {
		esc := strings.ReplaceAll(dn, `\`, `\\`)
		esc = strings.ReplaceAll(esc, `"`, `\"`)
		return fmt.Sprintf("\"%s\" <%s>", esc, em)
	}
	return fmt.Sprintf("%s <%s>", mime.QEncoding.Encode("UTF-8", dn), em)
}

func isASCIIString(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}
