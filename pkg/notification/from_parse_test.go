package notification

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseMailSender_PlainEmailWithName(t *testing.T) {
	p, err := ParseMailSender("hello@example.com", "解忧造物")
	require.NoError(t, err)
	require.Equal(t, "hello@example.com", p.Envelope)
	require.Equal(t, "解忧造物", p.Display)
	require.Contains(t, p.HeaderFrom, "hello@example.com")
	require.Contains(t, p.HeaderFrom, "=?UTF-8?")
}

func TestParseMailSender_RFCAddress(t *testing.T) {
	p, err := ParseMailSender(`解忧造物 <noreply@example.com>`, "")
	require.NoError(t, err)
	require.Equal(t, "noreply@example.com", p.Envelope)
	require.Equal(t, "解忧造物", p.Display)
}
