package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/gin-gonic/gin"
)

func decodeJSON(c *gin.Context, dst any) bool {
	return decodeJSONLimit(c, dst, 8<<10)
}

func decodeJSONLimit(c *gin.Context, dst any, limit int64) bool {
	if c.ContentType() != "application/json" {
		writeError(c, 400, generated.InvalidInput, "Expected application/json")
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
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

// decodeReplacement enforces presence separately from nullability. Generated Go
// pointers alone cannot distinguish an omitted nullable field from explicit null.
// Paths include parent objects so JSON null cannot silently become a zero struct.
func decodeReplacement(c *gin.Context, dst any, required, nullable []string) bool {
	var raw json.RawMessage
	if !decodeJSONLimit(c, &raw, 32<<10) {
		return false
	}
	for _, path := range required {
		value := raw
		for _, key := range strings.Split(path, ".") {
			var object map[string]json.RawMessage
			if json.Unmarshal(value, &object) != nil {
				value = nil
				break
			}
			value = object[key]
		}
		allowNull := false
		for _, key := range nullable {
			if key == path {
				allowNull = true
				break
			}
		}
		if len(value) == 0 || (!allowNull && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			writeError(c, 400, generated.InvalidInput, "Missing or null required field")
			return false
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(c, 400, generated.InvalidInput, "Invalid request")
		return false
	}
	return true
}
