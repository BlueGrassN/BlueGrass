package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"bluegrass/client-api/internal/model"
)

const maxBodyBytes = 4096

func invalidInput() error {
	return model.Fail("validation_failed", http.StatusUnprocessableEntity, "Invalid request")
}

func decodeBody(r *http.Request, destination any) error {
	if r.Body == nil {
		return invalidInput()
	}
	if r.ContentLength > maxBodyBytes {
		return model.Fail("payload_too_large", http.StatusRequestEntityTooLarge, "Payload too large")
	}
	// Read one sentinel byte beyond the limit, including trailing whitespace.
	// This also bounds memory for chunked requests with no Content-Length.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if len(body) > maxBodyBytes {
		return model.Fail("payload_too_large", http.StatusRequestEntityTooLarge, "Payload too large")
	}
	if canceled := r.Context().Err(); canceled != nil {
		return canceled
	}
	if err != nil {
		return invalidInput()
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' || !utf8.Valid(body) {
		return invalidInput()
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return invalidInput()
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return invalidInput()
	}
	return nil
}

func validLogin(input model.LoginInput) bool {
	// Match the auth service's byte limits and rune limit without modifying
	// passwords, usernames, or the device label passed to that service.
	return strings.TrimSpace(input.Username) != "" && len(input.Username) <= 255 &&
		utf8.ValidString(input.Username) && !hasControl(input.Username) &&
		len(input.Password) > 0 && len(input.Password) <= 1024 && utf8.ValidString(input.Password) &&
		validUUID(input.DeviceID) && len(input.DeviceName) <= 480 &&
		utf8.ValidString(input.DeviceName) && utf8.RuneCountInString(input.DeviceName) <= 120 && !hasControl(input.DeviceName)
}

func hasControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	nonzero := false
	for i := 0; i < len(value); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if value[i] != '-' {
				return false
			}
			continue
		}
		c := value[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
		if c != '0' {
			nonzero = true
		}
	}
	return nonzero
}

func validToken(value string) bool {
	if len(value) == 0 || len(value) > 2048 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] <= ' ' || value[i] >= 127 || value[i] == ',' {
			return false
		}
	}
	return true
}
