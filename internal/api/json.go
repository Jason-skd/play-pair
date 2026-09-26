package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/gin-gonic/gin"
)

func decodeJSON(c *gin.Context, dst any) bool {
	if c.ContentType() != "application/json" {
		writeError(c, 400, generated.InvalidInput, "Expected application/json")
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(c, 400, generated.InvalidInput, "Invalid request")
		return false
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		writeError(c, 400, generated.InvalidInput, "Invalid request")
		return false
	}
	return true
}
