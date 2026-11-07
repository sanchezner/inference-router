package contract

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
)

// one wire document that breaks a rule
// Path is the field the rule applies to in dotted form
type RuleError struct {
	Path   string
	Detail string
}

func (e *RuleError) Error() string {
	if e.Path == "" {
		return e.Detail
	}
	return e.Path + ": " + e.Detail
}

func fieldErr(path, detail string) error {
	return &RuleError{Path: path, Detail: detail}
}

func object(raw []byte, path string) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fieldErr(path, "must be an object")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var m map[string]json.RawMessage
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, fieldErr(path, "must be an object")
	}
	if err := noTrailing(dec, path, "must be an object"); err != nil {
		return nil, err
	}
	return m, nil
}

func array(raw json.RawMessage, path string) ([]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var items []json.RawMessage
	if err := dec.Decode(&items); err != nil {
		return nil, fieldErr(path, "must be an array")
	}
	if err := noTrailing(dec, path, "must be an array"); err != nil {
		return nil, err
	}
	return items, nil
}

func required(m map[string]json.RawMessage, key, path string) (json.RawMessage, error) {
	raw, ok := m[key]
	if !ok {
		return nil, fieldErr(path, "is required")
	}
	return raw, nil
}

func str(raw json.RawMessage, path string, nonempty bool) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var s string
	if err := dec.Decode(&s); err != nil {
		return "", fieldErr(path, "must be a string")
	}
	if err := noTrailing(dec, path, "must be a string"); err != nil {
		return "", err
	}
	if nonempty && s == "" {
		return "", fieldErr(path, "must be non-empty")
	}
	return s, nil
}

func requiredString(m map[string]json.RawMessage, key, path string, nonempty bool) (string, error) {
	raw, err := required(m, key, path)
	if err != nil {
		return "", err
	}
	return str(raw, path, nonempty)
}

func boolean(raw json.RawMessage, path string) (bool, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var v bool
	if err := dec.Decode(&v); err != nil {
		return false, fieldErr(path, "must be a bool")
	}
	if err := noTrailing(dec, path, "must be a bool"); err != nil {
		return false, err
	}
	return v, nil
}

func requiredInt(m map[string]json.RawMessage, key, path string, min int) (int, error) {
	i, err := requiredInt64(m, key, path, int64(min))
	if err != nil {
		return 0, err
	}
	if int64(int(i)) != i {
		return 0, fieldErr(path, "integer overflows")
	}
	return int(i), nil
}

func requiredInt64(m map[string]json.RawMessage, key, path string, min int64) (int64, error) {
	raw, err := required(m, key, path)
	if err != nil {
		return 0, err
	}
	return int64Value(raw, path, min)
}

func int64Value(raw json.RawMessage, path string, min int64) (int64, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return 0, fieldErr(path, "must be an integer")
	}
	if err := noTrailing(dec, path, "must be an integer"); err != nil {
		return 0, err
	}
	n, ok := v.(json.Number)
	if !ok {
		return 0, fieldErr(path, "must be an integer")
	}
	i, err := n.Int64()
	if err != nil {
		return 0, fieldErr(path, "must be an integer")
	}
	if i < min {
		return 0, fieldErr(path, "must be >= "+strconv.FormatInt(min, 10))
	}
	return i, nil
}

// accepts only a non-negative JSON number whose value is 0
// 0 and 0.0 are greedy, other temps are rejected
func zeroTemp(raw json.RawMessage) error {
	s := bytes.TrimSpace(raw)
	if len(s) > 0 && s[0] == '-' {
		return fieldErr("temperature", "must be 0")
	}
	dec := json.NewDecoder(bytes.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return fieldErr("temperature", "must be 0")
	}
	if err := noTrailing(dec, "temperature", "must be 0"); err != nil {
		return err
	}
	n, ok := v.(json.Number)
	if !ok {
		return fieldErr("temperature", "must be 0")
	}
	f, err := n.Float64()
	if err != nil || f != 0 {
		return fieldErr("temperature", "must be 0")
	}
	return nil
}

func noTrailing(dec *json.Decoder, path, detail string) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fieldErr(path, detail)
	}
	return nil
}
