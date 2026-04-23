package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"runtime/debug"

	"github.com/LingByte/LingVoice/pkg/utils"
	"github.com/gin-gonic/gin"
)

const (
	RequestIdKey = "X-Oneapi-Request-Id"
)

var _bp = func() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Path != "" {
		h := sha256.Sum256([]byte(bi.Main.Path))
		return hex.EncodeToString(h[:4])
	}
	return utils.RandString(8)
}()

func RequestId() func(c *gin.Context) {
	return func(c *gin.Context) {
		id := utils.GetTimeString() + _bp + utils.RandString(8)
		c.Set(RequestIdKey, id)
		ctx := context.WithValue(c.Request.Context(), RequestIdKey, id)
		c.Request = c.Request.WithContext(ctx)
		c.Header(RequestIdKey, id)
		c.Next()
	}
}
