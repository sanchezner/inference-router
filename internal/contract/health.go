package contract

import (
	"encoding/json"
	"net/http"
)

const (
	// worker is serving; HTTP status 200
	HealthOK = "ok"
	// process is up and not ready; HTTP status 503
	HealthStarting = "starting"
	// worker is finishing in-flight work; HTTP status 503
	HealthDraining = "draining"
)

// GET /health body
// starting and draining still answer /health, POST /v1/completions turns those states into an error
type Health struct {
	Status string `json:"status"`
}

// reads a health body
func ParseHealth(b []byte) (Health, error) {
	m, err := object(b, "body")
	if err != nil {
		return Health{}, err
	}
	status, err := requiredString(m, "status", "status", true)
	if err != nil {
		return Health{}, err
	}
	h := Health{Status: status}
	if err := h.Validate(); err != nil {
		return Health{}, err
	}
	return h, nil
}

// checks the status string
func (h Health) Validate() error {
	switch h.Status {
	case HealthOK, HealthStarting, HealthDraining:
		return nil
	default:
		return fieldErr("status", "must be ok, starting, or draining")
	}
}

// 200 for ok and 503 for starting or draining
func (h Health) StatusCode() int {
	if h.Status == HealthOK {
		return http.StatusOK
	}
	return http.StatusServiceUnavailable
}

// writes a health body
func (h Health) Marshal() ([]byte, error) {
	if err := h.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(h)
}
