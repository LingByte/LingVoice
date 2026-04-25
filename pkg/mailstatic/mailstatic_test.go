package mailstatic

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderHTML_EmailVerification(t *testing.T) {
	html, err := RenderHTML(TplEmailVerification, struct {
		Username  string
		VerifyURL string
	}{
		Username:  "alice",
		VerifyURL: "https://example.com/login?verifyToken=abc",
	})
	require.NoError(t, err)
	require.Contains(t, html, "alice")
	require.Contains(t, html, "verifyToken=abc")
	require.NotContains(t, strings.ToLower(html), "{{.")
}

func TestUsernameFromEmail(t *testing.T) {
	require.Equal(t, "bob", UsernameFromEmail("Bob@X.COM"))
	require.Equal(t, "", UsernameFromEmail("@x.com"))
}

func TestRenderHTML_EmailLoginCode(t *testing.T) {
	html, err := RenderHTML(TplEmailLoginCode, struct {
		Username   string
		Code       string
		ExpireHint string
	}{
		Username:   "alice",
		Code:       "123456",
		ExpireHint: "10 分钟",
	})
	require.NoError(t, err)
	require.Contains(t, html, "123456")
	require.Contains(t, html, "alice")
	require.Contains(t, html, "验证码")
	require.NotContains(t, strings.ToLower(html), "{{.")
}
