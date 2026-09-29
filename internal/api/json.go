package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

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
	return decodeReplacementLimit(c, dst, required, nullable, 32<<10)
}

func decodeReplacementLimit(c *gin.Context, dst any, required, nullable []string, limit int64) bool {
	var raw json.RawMessage
	if !decodeJSONLimit(c, &raw, limit) {
		return false
	}
	if !utf8.Valid(raw) {
		writeError(c, 400, generated.InvalidInput, "Invalid UTF-8")
		return false
	}
	requiredFields, nullableFields := make(map[string]bool, len(required)), make(map[string]bool, len(nullable))
	for _, path := range required {
		requiredFields[path] = true
	}
	for _, path := range nullable {
		nullableFields[path] = true
	}
	if !replacementValue(json.NewDecoder(bytes.NewReader(raw)), "", requiredFields, nullableFields) {
		writeError(c, 400, generated.InvalidInput, "Missing, duplicate or invalid field")
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(c, 400, generated.InvalidInput, "Invalid request")
		return false
	}
	return true
}

// replacementValue checks exact names and presence while rejecting duplicate keys.
// encoding/json's struct decoder alone accepts case-insensitive keys and merges
// repeated objects, which would make full-replacement semantics ambiguous.
func replacementValue(dec *json.Decoder, path string, required, nullable map[string]bool) bool {
	token, err := dec.Token()
	if err != nil {
		return false
	}
	if token == nil {
		return nullable[path]
	}
	if delim, ok := token.(json.Delim); ok {
		if delim != '{' {
			return false
		}
		prefix := path
		if prefix != "" {
			prefix += "."
		}
		seen := map[string]bool{}
		for dec.More() {
			token, err := dec.Token()
			if err != nil {
				return false
			}
			key, ok := token.(string)
			if !ok {
				return false
			}
			child := prefix + key
			if seen[child] || !required[child] {
				return false
			}
			seen[child] = true
			if !replacementValue(dec, child, required, nullable) {
				return false
			}
		}
		if end, err := dec.Token(); err != nil || end != json.Delim('}') {
			return false
		}
		for key := range required {
			if suffix, ok := strings.CutPrefix(key, prefix); ok && !strings.Contains(suffix, ".") && !seen[key] {
				return false
			}
		}
		return true
	}
	// Root and nested objects cannot be supplied as primitives.
	if path == "" {
		return false
	}
	for key := range required {
		if strings.HasPrefix(key, path+".") {
			return false
		}
	}
	return true
}
