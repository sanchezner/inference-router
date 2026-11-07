package contract

import "encoding/json"

// a worker's GET /stats board
// counts are since process start
// extra JSON fields are ignored: a worker may add a counter without breaking a reader.
type Stats struct {
	ContractVersion      string   `json:"contract_version"`
	InstanceID           string   `json:"instance_id"`
	ModelID              string   `json:"model_id"`
	Dtype                string   `json:"dtype"`
	Device               string   `json:"device"`
	BlockSize            int      `json:"block_size"`
	BlocksTotal          int      `json:"blocks_total"`
	BlocksFree           int      `json:"blocks_free"`
	RequestsWaiting      int      `json:"requests_waiting"`
	RequestsRunning      int      `json:"requests_running"`
	PendingPrefillTokens int      `json:"pending_prefill_tokens"`
	Counters             Counters `json:"counters"`
}

// totals since the worker process started
// non decreasing
// comparison takes two observations so a single board cannot check it
type Counters struct {
	RequestsFinished       int `json:"requests_finished"`
	RequestsAborted        int `json:"requests_aborted"`
	RequestsRejected       int `json:"requests_rejected"`
	TokensGenerated        int `json:"tokens_generated"`
	PrefixCacheHitTokens   int `json:"prefix_cache_hit_tokens"`
	PrefixCacheQueryTokens int `json:"prefix_cache_query_tokens"`
}

// reports the board of a worker with nothing in flight
// finished requests leave the counters above zero
func (s Stats) Idle() bool {
	return s.BlocksFree == s.BlocksTotal &&
		s.RequestsRunning == 0 &&
		s.RequestsWaiting == 0 &&
		s.PendingPrefillTokens == 0
}

// reads a GET /stats body
func ParseStats(b []byte) (Stats, error) {
	m, err := object(b, "body")
	if err != nil {
		return Stats{}, err
	}
	var s Stats
	if s.ContractVersion, err = requiredString(m, "contract_version", "contract_version", true); err != nil {
		return Stats{}, err
	}
	if s.InstanceID, err = requiredString(m, "instance_id", "instance_id", true); err != nil {
		return Stats{}, err
	}
	if s.ModelID, err = requiredString(m, "model_id", "model_id", true); err != nil {
		return Stats{}, err
	}
	if s.Dtype, err = requiredString(m, "dtype", "dtype", true); err != nil {
		return Stats{}, err
	}
	if s.Device, err = requiredString(m, "device", "device", true); err != nil {
		return Stats{}, err
	}
	if s.BlockSize, err = requiredInt(m, "block_size", "block_size", 1); err != nil {
		return Stats{}, err
	}
	if s.BlocksTotal, err = requiredInt(m, "blocks_total", "blocks_total", 0); err != nil {
		return Stats{}, err
	}
	if s.BlocksFree, err = requiredInt(m, "blocks_free", "blocks_free", 0); err != nil {
		return Stats{}, err
	}
	if s.RequestsWaiting, err = requiredInt(m, "requests_waiting", "requests_waiting", 0); err != nil {
		return Stats{}, err
	}
	if s.RequestsRunning, err = requiredInt(m, "requests_running", "requests_running", 0); err != nil {
		return Stats{}, err
	}
	if s.PendingPrefillTokens, err = requiredInt(m, "pending_prefill_tokens", "pending_prefill_tokens", 0); err != nil {
		return Stats{}, err
	}
	raw, err := required(m, "counters", "counters")
	if err != nil {
		return Stats{}, err
	}
	if s.Counters, err = parseCounters([]byte(raw)); err != nil {
		return Stats{}, err
	}
	if err := s.Validate(); err != nil {
		return Stats{}, err
	}
	return s, nil
}

func parseCounters(raw []byte) (Counters, error) {
	m, err := object(raw, "counters")
	if err != nil {
		return Counters{}, err
	}
	var c Counters
	if c.RequestsFinished, err = requiredInt(m, "requests_finished", "counters.requests_finished", 0); err != nil {
		return Counters{}, err
	}
	if c.RequestsAborted, err = requiredInt(m, "requests_aborted", "counters.requests_aborted", 0); err != nil {
		return Counters{}, err
	}
	if c.RequestsRejected, err = requiredInt(m, "requests_rejected", "counters.requests_rejected", 0); err != nil {
		return Counters{}, err
	}
	if c.TokensGenerated, err = requiredInt(m, "tokens_generated", "counters.tokens_generated", 0); err != nil {
		return Counters{}, err
	}
	if c.PrefixCacheHitTokens, err = requiredInt(m, "prefix_cache_hit_tokens", "counters.prefix_cache_hit_tokens", 0); err != nil {
		return Counters{}, err
	}
	if c.PrefixCacheQueryTokens, err = requiredInt(m, "prefix_cache_query_tokens", "counters.prefix_cache_query_tokens", 0); err != nil {
		return Counters{}, err
	}
	return c, nil
}

// checks a board a process built in memory
func (s Stats) Validate() error {
	if s.ContractVersion != Version {
		return fieldErr("contract_version", "must be "+Version)
	}
	if s.InstanceID == "" {
		return fieldErr("instance_id", "must be non-empty")
	}
	if s.ModelID == "" {
		return fieldErr("model_id", "must be non-empty")
	}
	if s.Dtype == "" {
		return fieldErr("dtype", "must be non-empty")
	}
	if s.Device == "" {
		return fieldErr("device", "must be non-empty")
	}
	if s.BlockSize < 1 {
		return fieldErr("block_size", "must be >= 1")
	}
	if s.BlocksTotal < 0 {
		return fieldErr("blocks_total", "must be >= 0")
	}
	if s.BlocksFree < 0 {
		return fieldErr("blocks_free", "must be >= 0")
	}
	if s.BlocksFree > s.BlocksTotal {
		return fieldErr("blocks_free", "exceeds blocks_total")
	}
	if s.RequestsWaiting < 0 || s.RequestsRunning < 0 || s.PendingPrefillTokens < 0 {
		return fieldErr("stats", "counts must be >= 0")
	}
	if err := s.Counters.validate(); err != nil {
		return err
	}
	return nil
}

func (c Counters) validate() error {
	if c.RequestsFinished < 0 || c.RequestsAborted < 0 || c.RequestsRejected < 0 ||
		c.TokensGenerated < 0 || c.PrefixCacheHitTokens < 0 || c.PrefixCacheQueryTokens < 0 {
		return fieldErr("counters", "counts must be >= 0")
	}
	return nil
}

// writes a board; it refuses a board that fails Validate
func (s Stats) Marshal() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(s)
}
