package api

import (
	"net/http"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/gin-gonic/gin"
)

func crossOriginProtection() generated.MiddlewareFunc {
	cop := http.NewCrossOriginProtection()
	return func(c *gin.Context) {
		if err := cop.Check(c.Request); err != nil {
			writeError(c, http.StatusForbidden, generated.CsrfRejected, "Cross-origin request rejected")
		}
	}
}
