package contract

// one POST /v1/completions body
// value returned by ParseCompletionRequest is greedy
// temp was omitted or zero, any other temp is a parse error
// max_tokens of 0 is a bad request, 413 is a later decision made by a worker that knows its pool
type CompletionRequest struct {
	Model     string
	Prompt    string
	MaxTokens int
	Stream    bool
	User      string
}

// reads a completion body
// unknown fields are ignored, a present field with the wrong JSON type fails
// stream defaults to false, temperature defaults to 0
func ParseCompletionRequest(b []byte) (CompletionRequest, error) {
	m, err := object(b, "body")
	if err != nil {
		return CompletionRequest{}, err
	}
	var r CompletionRequest
	if r.Model, err = requiredString(m, "model", "model", true); err != nil {
		return CompletionRequest{}, err
	}
	if r.Prompt, err = requiredString(m, "prompt", "prompt", false); err != nil {
		return CompletionRequest{}, err
	}
	if r.MaxTokens, err = requiredInt(m, "max_tokens", "max_tokens", 1); err != nil {
		return CompletionRequest{}, err
	}
	if raw, ok := m["stream"]; ok {
		if r.Stream, err = boolean(raw, "stream"); err != nil {
			return CompletionRequest{}, err
		}
	}
	if raw, ok := m["temperature"]; ok {
		if err := zeroTemp(raw); err != nil {
			return CompletionRequest{}, err
		}
	}
	if raw, ok := m["user"]; ok {
		if r.User, err = str(raw, "user", false); err != nil {
			return CompletionRequest{}, err
		}
	}
	return r, nil
}

// reports whether the request names the model this worker loaded
// a mismatch is a bad request
func (r CompletionRequest) MatchesModel(modelID string) bool {
	return r.Model == modelID
}
