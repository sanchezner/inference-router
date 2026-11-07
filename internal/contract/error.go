package contract

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

const (
	// HTTP 400; body is malformed or a parameter is unsupported
	TypeInvalidRequest = "invalid_request"
	// HTTP 413; request can't fit even in an empty block pool
	// different than busy pool
	TypeRequestTooLarge = "request_too_large"
	// HTTP 429; waiting queue is full
	TypeQueueFull = "queue_full"
	// HTTP 503; worker is starting or draining
	TypeUnavailable = "unavailable"
)

// object inside an error body and inside a mid-stream error event
type ErrorInfo struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// failure before any completion byte has been sent
// HasRetryAfter is true for 429 and 503
type HTTPError struct {
	Status        int
	Info          ErrorInfo
	RetryAfter    int
	HasRetryAfter bool
}

type errorWire struct {
	Error ErrorInfo `json:"error"`
}

// reads an error body and checks it against status and Retry-After header
// retryAfter is the raw header, empty string means header is absent
func ParseHTTPError(status int, body []byte, retryAfter string) (HTTPError, error) {
	m, err := object(body, "body")
	if err != nil {
		return HTTPError{}, err
	}
	raw, err := required(m, "error", "error")
	if err != nil {
		return HTTPError{}, err
	}
	info, err := parseErrorInfo(raw, "error")
	if err != nil {
		return HTTPError{}, err
	}
	seconds, present, err := parseRetryAfter(retryAfter)
	if err != nil {
		return HTTPError{}, err
	}
	e := HTTPError{
		Status:        status,
		Info:          info,
		RetryAfter:    seconds,
		HasRetryAfter: present,
	}
	if err := e.Validate(); err != nil {
		return HTTPError{}, err
	}
	return e, nil
}

func parseErrorInfo(raw json.RawMessage, path string) (ErrorInfo, error) {
	m, err := object(raw, path)
	if err != nil {
		return ErrorInfo{}, err
	}
	var info ErrorInfo
	if info.Type, err = requiredString(m, "type", path+".type", true); err != nil {
		return ErrorInfo{}, err
	}
	if info.Message, err = requiredString(m, "message", path+".message", true); err != nil {
		return ErrorInfo{}, err
	}
	return info, nil
}

func parseRetryAfter(h string) (int, bool, error) {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0, false, nil
	}
	for _, c := range h {
		if c < '0' || c > '9' {
			return 0, false, fieldErr("Retry-After", "must be an integer count of seconds")
		}
	}
	n, err := strconv.Atoi(h)
	if err != nil {
		return 0, false, fieldErr("Retry-After", "must be an integer count of seconds")
	}
	return n, true, nil
}

// checks status, type string, and whether Retry-After belongs
func (e HTTPError) Validate() error {
	want, ok := typeForStatus(e.Status)
	if !ok {
		return fieldErr("status", "is not a v0 error status")
	}
	if e.Info.Type != want {
		return fieldErr("error.type", "does not match status")
	}
	if e.Info.Message == "" {
		return fieldErr("error.message", "must be non-empty")
	}
	need := e.Status == http.StatusTooManyRequests || e.Status == http.StatusServiceUnavailable
	if need && !e.HasRetryAfter {
		return fieldErr("Retry-After", "is required")
	}
	if !need && e.HasRetryAfter {
		return fieldErr("Retry-After", "must be absent")
	}
	if e.HasRetryAfter && e.RetryAfter < 0 {
		return fieldErr("Retry-After", "must be >= 0")
	}
	return nil
}

func typeForStatus(status int) (string, bool) {
	switch status {
	case http.StatusBadRequest:
		return TypeInvalidRequest, true
	case http.StatusRequestEntityTooLarge:
		return TypeRequestTooLarge, true
	case http.StatusTooManyRequests:
		return TypeQueueFull, true
	case http.StatusServiceUnavailable:
		return TypeUnavailable, true
	default:
		return "", false
	}
}

// writes JSON error object
func (e ErrorInfo) MarshalBody() ([]byte, error) {
	if e.Type == "" || e.Message == "" {
		return nil, fieldErr("error", "type and message must be non-empty")
	}
	return json.Marshal(errorWire{Error: e})
}

// writes a mid-stream error event
// HTTP status is already 200, so no HTTPError
func (e ErrorInfo) MarshalSSE() ([]byte, error) {
	body, err := e.MarshalBody()
	if err != nil {
		return nil, err
	}
	return frame(body), nil
}
