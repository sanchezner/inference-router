package contract

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
)

const (
	// SSE object field on every content event
	ObjectTextCompletion = "text_completion"
	// the model stopped.
	FinishStop = "stop"
	// max_tokens was hit
	FinishLength = "length"
)

// which SSE event a payload was
type Kind string

const (
	KindChunk Kind = "chunk"
	KindDone  Kind = "done"
	KindError Kind = "error"
)

// one server-sent event
// a clean finish is a final chunk, then a done event
// an error event is how a stream dies after it has already started
type Event struct {
	Kind  Kind
	Chunk Chunk
	Error ErrorInfo
}

// one content event
// Text is that token, including the empty token a worker emits when it generates nothing
// FinishReason is empty until the last content event
type Chunk struct {
	ID           string
	Object       string
	Created      int64
	Model        string
	Text         string
	FinishReason string
	Usage        *Usage
}

// token accounting; only present on the last content event
type Usage struct {
	PromptTokens        int                 `json:"prompt_tokens"`
	CompletionTokens    int                 `json:"completion_tokens"`
	PromptTokensDetails PromptTokensDetails `json:"prompt_tokens_details"`
}

// holds how much of the prompt was reused
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// reports whether this chunk ends the completion
func (c Chunk) IsFinal() bool { return c.FinishReason != "" }

// reads one content event, either an intermediate token or the last
func ParseChunk(b []byte) (Chunk, error) {
	m, err := object(b, "body")
	if err != nil {
		return Chunk{}, err
	}
	return parseChunkObject(m)
}

// reads a non-streaming completion body
// body is the last content event, with no [DONE] line
func ParseFinalChunk(b []byte) (Chunk, error) {
	c, err := ParseChunk(b)
	if err != nil {
		return Chunk{}, err
	}
	if !c.IsFinal() {
		return Chunk{}, fieldErr("finish_reason", "is required")
	}
	return c, nil
}

// reads the text after "data: "
// done payload is the literal [DONE], not JSON
func ParseDataPayload(payload string) (Event, error) {
	if payload == "[DONE]" {
		return Event{Kind: KindDone}, nil
	}
	m, err := object([]byte(payload), "data")
	if err != nil {
		return Event{}, err
	}
	_, hasErr := m["error"]
	_, hasChoices := m["choices"]
	switch {
	case hasErr && hasChoices:
		return Event{}, fieldErr("data", "event is both a chunk and an error")
	case hasErr:
		info, err := parseErrorInfo(m["error"], "error")
		if err != nil {
			return Event{}, err
		}
		return Event{Kind: KindError, Error: info}, nil
	default:
		ch, err := parseChunkObject(m)
		if err != nil {
			return Event{}, err
		}
		return Event{Kind: KindChunk, Chunk: ch}, nil
	}
}

// reads one SSE event: a "data: " line and the blank line after it
// if context is already canceled, it returns before reading
// a read blocked inside r ends when the caller closes the underlying body
func ReadEvent(ctx context.Context, r *bufio.Reader) (Event, error) {
	for {
		line, err := readLine(ctx, r)
		if err != nil {
			return Event{}, err
		}
		if line == "" {
			continue
		}
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			return Event{}, fieldErr("sse", "line must start with data:")
		}
		if len(payload) > 0 && payload[0] == ' ' {
			payload = payload[1:]
		}
		blank, err := readLine(ctx, r)
		if err == io.EOF {
			return Event{}, fieldErr("sse", "event ended without a blank line")
		}
		if err != nil {
			return Event{}, err
		}
		if blank != "" {
			return Event{}, fieldErr("sse", "event must end with a blank line")
		}
		return ParseDataPayload(payload)
	}
}

func readLine(ctx context.Context, r *bufio.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s, err := r.ReadString('\n')
	if err == io.EOF {
		if s == "" {
			return "", io.EOF
		}
		return "", fieldErr("sse", "event ended without a newline")
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

func parseChunkObject(m map[string]json.RawMessage) (Chunk, error) {
	var c Chunk
	var err error
	if c.ID, err = requiredString(m, "id", "id", true); err != nil {
		return Chunk{}, err
	}
	if c.Object, err = requiredString(m, "object", "object", true); err != nil {
		return Chunk{}, err
	}
	if c.Created, err = requiredInt64(m, "created", "created", 0); err != nil {
		return Chunk{}, err
	}
	if c.Model, err = requiredString(m, "model", "model", true); err != nil {
		return Chunk{}, err
	}
	rawChoices, err := required(m, "choices", "choices")
	if err != nil {
		return Chunk{}, err
	}
	items, err := array(rawChoices, "choices")
	if err != nil {
		return Chunk{}, err
	}
	if len(items) != 1 {
		return Chunk{}, fieldErr("choices", "must hold one choice")
	}
	choice, err := object(items[0], "choices.0")
	if err != nil {
		return Chunk{}, err
	}
	index, err := requiredInt(choice, "index", "choices.0.index", 0)
	if err != nil {
		return Chunk{}, err
	}
	if index != 0 {
		return Chunk{}, fieldErr("choices.0.index", "must be 0")
	}
	if c.Text, err = requiredString(choice, "text", "choices.0.text", false); err != nil {
		return Chunk{}, err
	}
	if raw, ok := choice["finish_reason"]; ok && string(trimSpace(raw)) != "null" {
		reason, err := str(raw, "choices.0.finish_reason", true)
		if err != nil {
			return Chunk{}, err
		}
		c.FinishReason = reason
	}
	if raw, ok := m["usage"]; ok {
		u, err := parseUsage(raw)
		if err != nil {
			return Chunk{}, err
		}
		c.Usage = u
	}
	if err := c.validate(); err != nil {
		return Chunk{}, err
	}
	return c, nil
}

func parseUsage(raw json.RawMessage) (*Usage, error) {
	m, err := object(raw, "usage")
	if err != nil {
		return nil, err
	}
	var u Usage
	if u.PromptTokens, err = requiredInt(m, "prompt_tokens", "usage.prompt_tokens", 0); err != nil {
		return nil, err
	}
	if u.CompletionTokens, err = requiredInt(m, "completion_tokens", "usage.completion_tokens", 0); err != nil {
		return nil, err
	}
	details, err := required(m, "prompt_tokens_details", "usage.prompt_tokens_details")
	if err != nil {
		return nil, err
	}
	dm, err := object(details, "usage.prompt_tokens_details")
	if err != nil {
		return nil, err
	}
	if u.PromptTokensDetails.CachedTokens, err = requiredInt(dm, "cached_tokens", "usage.prompt_tokens_details.cached_tokens", 0); err != nil {
		return nil, err
	}
	if err := u.validate(); err != nil {
		return nil, err
	}
	return &u, nil
}

func (c Chunk) validate() error {
	if c.ID == "" {
		return fieldErr("id", "must be non-empty")
	}
	if c.Object != ObjectTextCompletion {
		return fieldErr("object", "must be text_completion")
	}
	if c.Created < 0 {
		return fieldErr("created", "must be >= 0")
	}
	if c.Model == "" {
		return fieldErr("model", "must be non-empty")
	}
	if c.IsFinal() {
		if c.FinishReason != FinishStop && c.FinishReason != FinishLength {
			return fieldErr("finish_reason", "must be stop or length")
		}
		if c.Usage == nil {
			return fieldErr("usage", "is required on the final chunk")
		}
		return c.Usage.validate()
	}
	if c.Usage != nil {
		return fieldErr("usage", "is omitted until the final chunk")
	}
	return nil
}

func (u Usage) validate() error {
	if u.PromptTokens < 0 {
		return fieldErr("usage.prompt_tokens", "must be >= 0")
	}
	if u.CompletionTokens < 0 {
		return fieldErr("usage.completion_tokens", "must be >= 0")
	}
	if u.PromptTokensDetails.CachedTokens < 0 {
		return fieldErr("usage.prompt_tokens_details.cached_tokens", "must be >= 0")
	}
	if u.PromptTokensDetails.CachedTokens > u.PromptTokens {
		return fieldErr("usage.prompt_tokens_details.cached_tokens", "exceeds prompt_tokens")
	}
	return nil
}

type choiceWire struct {
	Index        int     `json:"index"`
	Text         string  `json:"text"`
	FinishReason *string `json:"finish_reason"`
}

type chunkWire struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []choiceWire `json:"choices"`
	Usage   *Usage       `json:"usage,omitempty"`
}

// writes one content event, including the blank line that ends it
func (c Chunk) MarshalSSE() ([]byte, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(c.wire())
	if err != nil {
		return nil, err
	}
	return frame(payload), nil
}

// writes a non-streaming completion; the chunk must be final
func (c Chunk) MarshalBody() ([]byte, error) {
	if !c.IsFinal() {
		return nil, fieldErr("finish_reason", "is required")
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(c.wire())
}

func (c Chunk) wire() chunkWire {
	w := chunkWire{
		ID:      c.ID,
		Object:  c.Object,
		Created: c.Created,
		Model:   c.Model,
		Choices: []choiceWire{{
			Index: 0,
			Text:  c.Text,
		}},
		Usage: c.Usage,
	}
	if c.IsFinal() {
		reason := c.FinishReason
		w.Choices[0].FinishReason = &reason
	}
	return w
}

// event that follows the final content event
func DoneSSE() []byte { return []byte("data: [DONE]\n\n") }

func frame(payload []byte) []byte {
	out := make([]byte, 0, len(payload)+8)
	out = append(out, "data: "...)
	out = append(out, payload...)
	out = append(out, "\n\n"...)
	return out
}

func trimSpace(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}
