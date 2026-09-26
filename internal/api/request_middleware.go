package api

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/gin-gonic/gin"
)

func requestMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		var id [16]byte
		_, _ = rand.Read(id[:])
		c.Header("X-Request-ID", hex.EncodeToString(id[:]))
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("request panic", "request_id", c.Writer.Header().Get("X-Request-ID"))
				if !c.Writer.Written() {
					writeError(c, http.StatusInternalServerError, generated.InternalError, "Internal server error")
				}
				c.Abort()
			}
		}()
		c.Next()
	}
}
