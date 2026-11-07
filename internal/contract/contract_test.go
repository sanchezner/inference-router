package contract_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/sanchezner/inference-router/internal/contract"
)

func rulePath(t *testing.T, err error) string {
	t.Helper()
	var re *contract.RuleError
	if !errors.As(err, &re) {
		t.Fatalf("error %v, want RuleError", err)
	}
	return re.Path
}

func TestStatsExample(t *testing.T) {
	const raw = `{
	  "contract_version": "0",
	  "instance_id": "7f3c0a1e-4b19-4e2a-9c11-0a6d2e8f5b40",
	  "model_id": "demo",
	  "dtype": "fp32",
	  "device": "cpu",
	  "block_size": 16,
	  "blocks_total": 128,
	  "blocks_free": 128,
	  "requests_waiting": 0,
	  "requests_running": 0,
	  "pending_prefill_tokens": 0,
	  "counters": {
	    "requests_finished": 0,
	    "requests_aborted": 0,
	    "requests_rejected": 0,
	    "tokens_generated": 0,
	    "prefix_cache_hit_tokens": 0,
	    "prefix_cache_query_tokens": 0
	  }
	}`
	got, err := contract.ParseStats([]byte(raw))
	if err != nil {
		t.Fatalf("ParseStats: %v", err)
	}
	if !got.Idle() {
		t.Fatal("example board is idle")
	}
	if got.ContractVersion != contract.Version || got.BlockSize != 16 || got.BlocksFree != 128 {
		t.Fatalf("parsed board = %+v", got)
	}
	out, err := got.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	again, err := contract.ParseStats(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if !reflect.DeepEqual(got, again) {
		t.Fatalf("round trip\n got %+v\nwant %+v", again, got)
	}
}

func TestStatsExtraFieldIsAllowed(t *testing.T) {
	m := statsMap(t)
	m["scheduler"] = "reserve-max"
	c := m["counters"].(map[string]any)
	c["future_counter"] = 1
	if _, err := contract.ParseStats(mustJSON(t, m)); err != nil {
		t.Fatalf("extra fields: %v", err)
	}
}

func TestStatsRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
		path   string
	}{
		{"missing instance_id", func(m map[string]any) { delete(m, "instance_id") }, "instance_id"},
		{"empty instance_id", func(m map[string]any) { m["instance_id"] = "" }, "instance_id"},
		{"version number", func(m map[string]any) { m["contract_version"] = 0 }, "contract_version"},
		{"wrong version", func(m map[string]any) { m["contract_version"] = "1" }, "contract_version"},
		{"block_size zero", func(m map[string]any) { m["block_size"] = 0 }, "block_size"},
		{"block_size fraction", func(m map[string]any) { m["block_size"] = 1.5 }, "block_size"},
		{"block_size string", func(m map[string]any) { m["block_size"] = "16" }, "block_size"},
		{"missing blocks_free", func(m map[string]any) { delete(m, "blocks_free") }, "blocks_free"},
		{"blocks_free above total", func(m map[string]any) { m["blocks_free"] = 129 }, "blocks_free"},
		{"negative running", func(m map[string]any) { m["requests_running"] = -1 }, "requests_running"},
		{"missing counters", func(m map[string]any) { delete(m, "counters") }, "counters"},
		{"missing counter", func(m map[string]any) {
			delete(m["counters"].(map[string]any), "tokens_generated")
		}, "counters.tokens_generated"},
		{"array body", func(m map[string]any) {}, "body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var raw []byte
			if tc.name == "array body" {
				raw = []byte("[]")
			} else {
				m := statsMap(t)
				tc.mutate(m)
				raw = mustJSON(t, m)
			}
			_, err := contract.ParseStats(raw)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := rulePath(t, err); got != tc.path {
				t.Fatalf("path %s, want %s (%v)", got, tc.path, err)
			}
		})
	}
}

func TestStatsMarshalRejectsEmptyIdentity(t *testing.T) {
	s, err := contract.ParseStats(mustJSON(t, statsMap(t)))
	if err != nil {
		t.Fatal(err)
	}
	s.InstanceID = ""
	if _, err := s.Marshal(); err == nil {
		t.Fatal("Marshal accepted an empty instance_id")
	}
}

func TestBusyBoardIsNotIdle(t *testing.T) {
	m := statsMap(t)
	m["blocks_free"] = 127
	m["requests_running"] = 1
	s, err := contract.ParseStats(mustJSON(t, m))
	if err != nil {
		t.Fatal(err)
	}
	if s.Idle() {
		t.Fatal("a running request is not idle")
	}
}

func TestSSESampleStream(t *testing.T) {
	const stream = "" +
		"data: {\"id\":\"req_1\",\"object\":\"text_completion\",\"created\":1762100000,\"model\":\"demo\",\"choices\":[{\"index\":0,\"text\":\"Hi\",\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"req_1\",\"object\":\"text_completion\",\"created\":1762100000,\"model\":\"demo\",\"choices\":[{\"index\":0,\"text\":\"!\",\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"prompt_tokens_details\":{\"cached_tokens\":0}}}\n\n" +
		"data: [DONE]\n\n"
	r := bufio.NewReader(bytes.NewReader([]byte(stream)))
	ctx := context.Background()

	first, err := contract.ReadEvent(ctx, r)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.Kind != contract.KindChunk || first.Chunk.IsFinal() || first.Chunk.Text != "Hi" || first.Chunk.Usage != nil {
		t.Fatalf("first event = %+v", first)
	}

	second, err := contract.ReadEvent(ctx, r)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.Kind != contract.KindChunk || !second.Chunk.IsFinal() || second.Chunk.FinishReason != contract.FinishStop {
		t.Fatalf("second event = %+v", second)
	}
	if second.Chunk.Text != "!" || second.Chunk.Usage.PromptTokens != 3 || second.Chunk.Usage.CompletionTokens != 2 || second.Chunk.Usage.PromptTokensDetails.CachedTokens != 0 {
		t.Fatalf("second usage = %+v text %q", second.Chunk.Usage, second.Chunk.Text)
	}

	done, err := contract.ReadEvent(ctx, r)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if done.Kind != contract.KindDone {
		t.Fatalf("done kind %s", done.Kind)
	}
	if _, err := contract.ReadEvent(ctx, r); !errors.Is(err, io.EOF) {
		t.Fatalf("after stream: %v, want EOF", err)
	}

	const finalBody = `{"id":"req_1","object":"text_completion","created":1762100000,"model":"demo","choices":[{"index":0,"text":"!","finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":0}}}`
	final, err := contract.ParseFinalChunk([]byte(finalBody))
	if err != nil {
		t.Fatalf("non-streaming body: %v", err)
	}
	if final.Text != "!" || !final.IsFinal() {
		t.Fatalf("final = %+v", final)
	}
}

func TestChunkRoundTripAndRules(t *testing.T) {
	c := contract.Chunk{
		ID:           "req_1",
		Object:       contract.ObjectTextCompletion,
		Created:      1762100000,
		Model:        "demo",
		Text:         "",
		FinishReason: contract.FinishLength,
		Usage: &contract.Usage{
			PromptTokens:     4,
			CompletionTokens: 0,
			PromptTokensDetails: contract.PromptTokensDetails{
				CachedTokens: 4,
			},
		},
	}
	framed, err := c.MarshalSSE()
	if err != nil {
		t.Fatal(err)
	}
	ev, err := contract.ReadEvent(context.Background(), bufio.NewReader(bytes.NewReader(framed)))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != contract.KindChunk || ev.Chunk.Text != "" || ev.Chunk.FinishReason != contract.FinishLength || ev.Chunk.Usage.PromptTokensDetails.CachedTokens != 4 {
		t.Fatalf("round trip = %+v", ev.Chunk)
	}

	body, err := c.MarshalBody()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contract.ParseFinalChunk(body); err != nil {
		t.Fatal(err)
	}

	if _, err := contract.ParseChunk(mustRewriteUsage(t, body, 5)); rulePath(t, err) != "usage.prompt_tokens_details.cached_tokens" {
		t.Fatalf("cached_tokens: %v", err)
	}
}

func TestChunkRejects(t *testing.T) {
	final := map[string]any{
		"id": "req_1", "object": "text_completion", "created": 10, "model": "demo",
		"choices": []any{map[string]any{"index": 0, "text": "x", "finish_reason": "stop"}},
		"usage": map[string]any{
			"prompt_tokens": 1, "completion_tokens": 1,
			"prompt_tokens_details": map[string]any{"cached_tokens": 0},
		},
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
		path   string
	}{
		{"usage on partial", func(m map[string]any) {
			m["choices"].([]any)[0].(map[string]any)["finish_reason"] = nil
		}, "usage"},
		{"final without usage", func(m map[string]any) { delete(m, "usage") }, "usage"},
		{"bad finish", func(m map[string]any) {
			m["choices"].([]any)[0].(map[string]any)["finish_reason"] = "abort"
		}, "finish_reason"},
		{"two choices", func(m map[string]any) {
			m["choices"] = []any{
				map[string]any{"index": 0, "text": "a", "finish_reason": "stop"},
				map[string]any{"index": 0, "text": "b", "finish_reason": "stop"},
			}
		}, "choices"},
		{"index", func(m map[string]any) {
			m["choices"].([]any)[0].(map[string]any)["index"] = 1
		}, "choices.0.index"},
		{"object", func(m map[string]any) { m["object"] = "chat.completion" }, "object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := cloneMap(t, final)
			tc.mutate(m)
			_, err := contract.ParseChunk(mustJSON(t, m))
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := rulePath(t, err); got != tc.path {
				t.Fatalf("path %s, want %s (%v)", got, tc.path, err)
			}
		})
	}
}

func TestMidStreamErrorAndFraming(t *testing.T) {
	ev, err := contract.ParseDataPayload(`{"error":{"type":"internal","message":"boom"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != contract.KindError || ev.Error.Type != "internal" || ev.Error.Message != "boom" {
		t.Fatalf("event = %+v", ev)
	}
	framed, err := ev.Error.MarshalSSE()
	if err != nil {
		t.Fatal(err)
	}
	again, err := contract.ReadEvent(context.Background(), bufio.NewReader(bytes.NewReader(framed)))
	if err != nil {
		t.Fatal(err)
	}
	if again.Error.Type != "internal" {
		t.Fatalf("round trip = %+v", again)
	}

	_, err = contract.ReadEvent(context.Background(), bufio.NewReader(bytes.NewReader([]byte("data: [DONE]\n"))))
	if rulePath(t, err) != "sse" {
		t.Fatalf("truncated event: %v", err)
	}
	_, err = contract.ReadEvent(context.Background(), bufio.NewReader(bytes.NewReader([]byte("event: ping\n\n"))))
	if rulePath(t, err) != "sse" {
		t.Fatalf("non-data line: %v", err)
	}
	_, err = contract.ParseDataPayload("[DONE] ")
	if err == nil {
		t.Fatal("[DONE] with a trailing space is not the done event")
	}
}

func TestReadEventCanceledBeforeRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := contract.ReadEvent(ctx, bufio.NewReader(denyRead{}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestHTTPErrorRules(t *testing.T) {
	body := []byte(`{"error":{"type":"queue_full","message":"waiting queue is full"}}`)
	got, err := contract.ParseHTTPError(429, body, "1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasRetryAfter || got.RetryAfter != 1 || got.Info.Type != contract.TypeQueueFull {
		t.Fatalf("parsed = %+v", got)
	}
	out, err := got.Info.MarshalBody()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contract.ParseHTTPError(429, out, "0"); err != nil {
		t.Fatalf("retry 0: %v", err)
	}

	cases := []struct {
		name   string
		status int
		body   string
		retry  string
		path   string
	}{
		{"type mismatches status", 400, `{"error":{"type":"queue_full","message":"x"}}`, "", "error.type"},
		{"429 needs retry", 429, `{"error":{"type":"queue_full","message":"x"}}`, "", "Retry-After"},
		{"400 rejects retry", 400, `{"error":{"type":"invalid_request","message":"x"}}`, "5", "Retry-After"},
		{"date header", 503, `{"error":{"type":"unavailable","message":"x"}}`, "Wed, 21 Oct 2015 07:28:00 GMT", "Retry-After"},
		{"unknown status", 500, `{"error":{"type":"internal","message":"x"}}`, "", "status"},
		{"413", 413, `{"error":{"type":"request_too_large","message":"x"}}`, "1", "Retry-After"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := contract.ParseHTTPError(tc.status, []byte(tc.body), tc.retry)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := rulePath(t, err); got != tc.path {
				t.Fatalf("path %s, want %s (%v)", got, tc.path, err)
			}
		})
	}

	if _, err := contract.ParseHTTPError(413, []byte(`{"error":{"type":"request_too_large","message":"never fits"}}`), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := contract.ParseHTTPError(503, []byte(`{"error":{"type":"unavailable","message":"draining"},"extra":true}`), "2"); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionRequest(t *testing.T) {
	got, err := contract.ParseCompletionRequest([]byte(`{"model":"demo","prompt":"","max_tokens":1,"top_p":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Stream || got.Prompt != "" || got.MaxTokens != 1 || !got.MatchesModel("demo") || got.MatchesModel("other") {
		t.Fatalf("parsed = %+v", got)
	}
	withTemp, err := contract.ParseCompletionRequest([]byte(`{"model":"demo","prompt":"hi","max_tokens":2,"stream":true,"temperature":0.0,"user":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !withTemp.Stream || withTemp.User != "a" {
		t.Fatalf("parsed = %+v", withTemp)
	}
	zero, err := contract.ParseCompletionRequest([]byte(`{"model":"demo","prompt":"hi","max_tokens":2,"temperature":0}`))
	if err != nil {
		t.Fatal(err)
	}
	if zero.Stream {
		t.Fatal("omitted stream means false")
	}

	cases := []struct {
		body string
		path string
	}{
		{`{"model":"","prompt":"hi","max_tokens":1}`, "model"},
		{`{"prompt":"hi","max_tokens":1}`, "model"},
		{`{"model":"demo","max_tokens":1}`, "prompt"},
		{`{"model":"demo","prompt":1,"max_tokens":1}`, "prompt"},
		{`{"model":"demo","prompt":"hi","max_tokens":0}`, "max_tokens"},
		{`{"model":"demo","prompt":"hi","max_tokens":1.5}`, "max_tokens"},
		{`{"model":"demo","prompt":"hi","max_tokens":1,"stream":"true"}`, "stream"},
		{`{"model":"demo","prompt":"hi","max_tokens":1,"temperature":0.7}`, "temperature"},
		{`{"model":"demo","prompt":"hi","max_tokens":1,"temperature":"0"}`, "temperature"},
		{`[]`, "body"},
	}
	for _, tc := range cases {
		t.Run(tc.path+" "+tc.body, func(t *testing.T) {
			_, err := contract.ParseCompletionRequest([]byte(tc.body))
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := rulePath(t, err); got != tc.path {
				t.Fatalf("path %s, want %s (%v)", got, tc.path, err)
			}
		})
	}
}

func TestHealth(t *testing.T) {
	ok, err := contract.ParseHealth([]byte(`{"status":"ok","extra":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if ok.StatusCode() != 200 {
		t.Fatalf("status %d", ok.StatusCode())
	}
	for _, status := range []string{contract.HealthStarting, contract.HealthDraining} {
		h, err := contract.ParseHealth([]byte(`{"status":"` + status + `"}`))
		if err != nil {
			t.Fatal(err)
		}
		if h.StatusCode() != 503 {
			t.Fatalf("%s status %d", status, h.StatusCode())
		}
		out, err := h.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := contract.ParseHealth(out); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := contract.ParseHealth([]byte(`{"status":"down"}`)); rulePath(t, err) != "status" {
		t.Fatalf("down: %v", err)
	}
	if _, err := contract.ParseHealth([]byte(`{}`)); rulePath(t, err) != "status" {
		t.Fatalf("missing: %v", err)
	}
}

func statsMap(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(`{
	  "contract_version": "0",
	  "instance_id": "id-1",
	  "model_id": "demo",
	  "dtype": "fp32",
	  "device": "cpu",
	  "block_size": 16,
	  "blocks_total": 128,
	  "blocks_free": 128,
	  "requests_waiting": 0,
	  "requests_running": 0,
	  "pending_prefill_tokens": 0,
	  "counters": {
	    "requests_finished": 0,
	    "requests_aborted": 0,
	    "requests_rejected": 0,
	    "tokens_generated": 0,
	    "prefix_cache_hit_tokens": 0,
	    "prefix_cache_query_tokens": 0
	  }
	}`), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func cloneMap(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(mustJSON(t, m), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func mustRewriteUsage(t *testing.T, body []byte, cached int) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	m["usage"].(map[string]any)["prompt_tokens_details"].(map[string]any)["cached_tokens"] = cached
	return mustJSON(t, m)
}

type denyRead struct{}

func (denyRead) Read([]byte) (int, error) {
	return 0, errors.New("should not read")
}
