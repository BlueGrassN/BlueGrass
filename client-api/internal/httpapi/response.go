package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"bluegrass/client-api/internal/model"
)

type publicError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type responseEnvelope struct {
	Success    bool         `json:"success"`
	Data       any          `json:"data,omitempty"`
	Error      *publicError `json:"error,omitempty"`
	ServerTime time.Time    `json:"server_time"`
}

func writeSuccess(w http.ResponseWriter, data any) {
	if data == nil {
		data = struct{}{}
	}
	writeResponse(w, http.StatusOK, responseEnvelope{Success: true, Data: data})
}

func writeFailure(w http.ResponseWriter, status int, code string) {
	// Dependency errors may contain database details or submitted credentials.
	// Only their public status/code cross this boundary, never their message.
	writeResponse(w, status, responseEnvelope{Error: &publicError{Code: code, Message: http.StatusText(status)}})
}

func writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeFailure(w, http.StatusServiceUnavailable, "service_unavailable")
		return
	}
	var apiError *model.APIError
	if errors.As(err, &apiError) && apiError != nil && apiError.Status >= 400 && apiError.Status <= 599 && validErrorCode(apiError.Code) {
		writeFailure(w, apiError.Status, apiError.Code)
		return
	}
	if errors.Is(err, model.ErrNotFound) {
		writeFailure(w, http.StatusNotFound, "not_found")
		return
	}
	// Includes transient I/O errors and unexpected errors, with no auth writes.
	writeFailure(w, http.StatusServiceUnavailable, "service_unavailable")
}

func validErrorCode(code string) bool {
	if len(code) == 0 || len(code) > 128 {
		return false
	}
	for _, c := range code {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func writeResponse(w http.ResponseWriter, status int, response responseEnvelope) {
	response.ServerTime = time.Now().UTC()
	if response.Error != nil && response.Error.Message == "" {
		response.Error.Message = "Request failed"
	}
	body, err := json.Marshal(response)
	if err != nil {
		status = http.StatusServiceUnavailable
		body, _ = json.Marshal(responseEnvelope{
			Error:      &publicError{Code: "service_unavailable", Message: "Service Unavailable"},
			ServerTime: time.Now().UTC(),
		})
	}
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "1")
	} else if status == http.StatusTooManyRequests && w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", "60")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}
