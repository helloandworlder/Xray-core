package rayipruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

type Direction string

const (
	DirectionEgress  Direction = "EGRESS"
	DirectionIngress Direction = "INGRESS"
)

type AbuseAction string

const (
	ReportOnly       AbuseAction = "REPORT_ONLY"
	DisableAndReport AbuseAction = "DISABLE_AND_REPORT"
)

type AccountPolicy struct {
	Email              string      `json:"email"`
	EgressLimitBPS     int64       `json:"egress_limit_bps"`
	IngressLimitBPS    int64       `json:"ingress_limit_bps"`
	MaxConnections     int         `json:"max_connections"`
	Priority           int         `json:"priority"`
	Generation         uint64      `json:"generation"`
	Disabled           bool        `json:"disabled"`
	AbuseBytesPerMin   int64       `json:"abuse_bytes_per_min"`
	AbuseDisablePolicy AbuseAction `json:"abuse_disable_policy"`
}

type Usage struct {
	Email             string `json:"email"`
	RxBytes           int64  `json:"rx_bytes"`
	TxBytes           int64  `json:"tx_bytes"`
	ActiveConnections int    `json:"active_connections"`
}

type FairnessState struct {
	EgressPoolBPS     int64 `json:"egress_pool_bps"`
	IngressPoolBPS    int64 `json:"ingress_pool_bps"`
	WindowSeconds     int64 `json:"window_seconds"`
	LossRatePPM       int64 `json:"loss_rate_ppm"`
	RetransmitRatePPM int64 `json:"retransmit_rate_ppm"`
	TargetLossPPM     int64 `json:"target_loss_ppm"`
	TargetRetransPPM  int64 `json:"target_retrans_ppm"`
	MinCongestionBPS  int64 `json:"min_congestion_bps"`
}

type AbuseEvent struct {
	Email       string      `json:"email"`
	Action      AbuseAction `json:"action"`
	WindowBytes int64       `json:"window_bytes"`
	Threshold   int64       `json:"threshold"`
	At          time.Time   `json:"at"`
}

type Digest struct {
	AccountCount  uint64 `json:"account_count"`
	EnabledCount  uint64 `json:"enabled_count"`
	DisabledCount uint64 `json:"disabled_count"`
	MaxGeneration uint64 `json:"max_generation"`
	Hash          string `json:"hash"`
}

type Manager struct {
	mu          sync.Mutex
	policies    map[string]*policyState
	fairPoolBPS int64
	fairness    FairnessState
}

var defaultManager = NewManager()

func DefaultManager() *Manager {
	return defaultManager
}

func (*Manager) Type() interface{} {
	return (*Manager)(nil)
}

func (*Manager) Start() error {
	return nil
}

func (*Manager) Close() error {
	return nil
}

type policyState struct {
	Policy       AccountPolicy
	Egress       tokenBucket
	Ingress      tokenBucket
	Usage        Usage
	MinuteWindow trafficWindow
}

type tokenBucket struct {
	limitBPS int64
	tokens   int64
	last     time.Time
}

type trafficWindow struct {
	start time.Time
	bytes int64
}

func NewManager() *Manager {
	return &Manager{
		policies:    map[string]*policyState{},
		fairPoolBPS: 0,
	}
}

func (m *Manager) SetFairPool(bytesPerSecond int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fairPoolBPS = maxInt64(0, bytesPerSecond)
	m.fairness.EgressPoolBPS = m.fairPoolBPS
	m.fairness.IngressPoolBPS = m.fairPoolBPS
	if m.fairness.WindowSeconds <= 0 {
		m.fairness.WindowSeconds = 60
	}
}

func (m *Manager) SetFairnessState(state FairnessState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state.TargetLossPPM <= 0 {
		state.TargetLossPPM = 5000
	}
	if state.TargetRetransPPM <= 0 {
		state.TargetRetransPPM = 10000
	}
	if state.WindowSeconds <= 0 {
		state.WindowSeconds = 300
	}
	state.EgressPoolBPS = maxInt64(0, state.EgressPoolBPS)
	state.IngressPoolBPS = maxInt64(0, state.IngressPoolBPS)
	state.LossRatePPM = maxInt64(0, state.LossRatePPM)
	state.RetransmitRatePPM = maxInt64(0, state.RetransmitRatePPM)
	state.MinCongestionBPS = maxInt64(0, state.MinCongestionBPS)
	m.fairness = state
	if state.EgressPoolBPS == state.IngressPoolBPS {
		m.fairPoolBPS = state.EgressPoolBPS
	}
}

func (m *Manager) SetPolicy(policy AccountPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if policy.Priority <= 0 {
		policy.Priority = 1
	}
	state := m.policies[policy.Email]
	if state == nil {
		state = &policyState{Usage: Usage{Email: policy.Email}}
		m.policies[policy.Email] = state
	}
	state.Policy = policy
	state.Egress.setLimit(policy.EgressLimitBPS)
	state.Ingress.setLimit(policy.IngressLimitBPS)
}

func (m *Manager) RemovePolicy(email string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.policies, email)
}

func (m *Manager) Policy(email string) (AccountPolicy, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.policies[email]
	if state == nil {
		return AccountPolicy{}, false
	}
	return state.Policy, true
}

func (m *Manager) AcquireConnection(email string) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.requireState(email)
	if err != nil {
		return nil, err
	}
	if state.Policy.Disabled {
		return nil, errors.New("account disabled")
	}
	if state.Policy.MaxConnections > 0 && state.Usage.ActiveConnections >= state.Policy.MaxConnections {
		return nil, errors.New("connection limit exceeded")
	}
	state.Usage.ActiveConnections++
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if current := m.policies[email]; current != nil && current.Usage.ActiveConnections > 0 {
				current.Usage.ActiveConnections--
			}
		})
	}, nil
}

func (m *Manager) AllowBytes(email string, direction Direction, requested int64) int64 {
	return m.AllowBytesAt(email, direction, requested, time.Now())
}

func (m *Manager) AllowBytesAt(email string, direction Direction, requested int64, now time.Time) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.requireState(email)
	if err != nil || requested <= 0 || state.Policy.Disabled {
		return 0
	}
	limit := state.Policy.EgressLimitBPS
	bucket := &state.Egress
	if direction == DirectionIngress {
		limit = state.Policy.IngressLimitBPS
		bucket = &state.Ingress
	}
	if limit <= 0 && m.fairnessPoolBPSLocked(direction) > 0 {
		limit = m.fairShareBPSLocked(email, direction, now)
		bucket.setLimit(limit)
	}
	if limit <= 0 {
		return requested
	}
	return bucket.take(requested, now)
}

func (m *Manager) RecordTraffic(email string, direction Direction, bytes int64) *AbuseEvent {
	return m.RecordTrafficAt(email, direction, bytes, time.Now())
}

func (m *Manager) RecordTrafficAt(email string, direction Direction, bytes int64, now time.Time) *AbuseEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.requireState(email)
	if err != nil || bytes <= 0 {
		return nil
	}
	if direction == DirectionIngress {
		state.Usage.RxBytes += bytes
	} else {
		state.Usage.TxBytes += bytes
	}
	state.MinuteWindow.add(bytes, now)
	if state.Policy.AbuseBytesPerMin <= 0 || state.MinuteWindow.bytes <= state.Policy.AbuseBytesPerMin {
		return nil
	}
	action := state.Policy.AbuseDisablePolicy
	if action == "" {
		action = ReportOnly
	}
	return &AbuseEvent{
		Email:       email,
		Action:      action,
		WindowBytes: state.MinuteWindow.bytes,
		Threshold:   state.Policy.AbuseBytesPerMin,
		At:          now,
	}
}

func (m *Manager) FairShareBPS(email string, direction Direction, now time.Time) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fairShareBPSLocked(email, direction, now)
}

func (m *Manager) Usage(email string) Usage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state := m.policies[email]; state != nil {
		return state.Usage
	}
	return Usage{Email: email}
}

func (m *Manager) ListUsage() []Usage {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]Usage, 0, len(m.policies))
	for _, state := range m.policies {
		items = append(items, state.Usage)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Email < items[j].Email })
	return items
}

func (m *Manager) Digest() Digest {
	m.mu.Lock()
	defer m.mu.Unlock()
	policies := make([]AccountPolicy, 0, len(m.policies))
	digest := Digest{AccountCount: uint64(len(m.policies))}
	for _, state := range m.policies {
		policies = append(policies, state.Policy)
		if state.Policy.Disabled {
			digest.DisabledCount++
		} else {
			digest.EnabledCount++
		}
		if state.Policy.Generation > digest.MaxGeneration {
			digest.MaxGeneration = state.Policy.Generation
		}
	}
	sort.Slice(policies, func(i, j int) bool { return policies[i].Email < policies[j].Email })
	payload, _ := json.Marshal(policies)
	sum := sha256.Sum256(payload)
	digest.Hash = hex.EncodeToString(sum[:])
	return digest
}

func (m *Manager) requireState(email string) (*policyState, error) {
	if email == "" {
		return nil, errors.New("email is required")
	}
	state := m.policies[email]
	if state == nil {
		return nil, errors.New("account policy not found")
	}
	return state, nil
}

func (m *Manager) fairShareBPSLocked(email string, direction Direction, now time.Time) int64 {
	pool := m.fairnessPoolBPSLocked(direction)
	if pool <= 0 {
		return 0
	}
	window := time.Duration(maxInt64(1, m.fairness.WindowSeconds)) * time.Second
	totalWeight := int64(0)
	weights := map[string]int64{}
	for candidate, state := range m.policies {
		if state.Policy.Disabled {
			continue
		}
		weight := int64(maxInt(1, state.Policy.Priority)) * 1024
		if !state.MinuteWindow.start.IsZero() && now.Sub(state.MinuteWindow.start) < window {
			usagePressure := state.MinuteWindow.bytes / maxInt64(1, pool)
			if usagePressure > 0 {
				weight = maxInt64(1, weight/(1+usagePressure))
			}
		}
		weights[candidate] = weight
		totalWeight += weight
	}
	if totalWeight <= 0 {
		return 0
	}
	return maxInt64(1, pool*weights[email]/totalWeight)
}

func (m *Manager) fairnessPoolBPSLocked(direction Direction) int64 {
	pool := m.fairPoolBPS
	if direction == DirectionEgress && m.fairness.EgressPoolBPS > 0 {
		pool = m.fairness.EgressPoolBPS
	}
	if direction == DirectionIngress && m.fairness.IngressPoolBPS > 0 {
		pool = m.fairness.IngressPoolBPS
	}
	if pool <= 0 {
		return 0
	}
	targetLoss := maxInt64(1, m.fairness.TargetLossPPM)
	targetRetrans := maxInt64(1, m.fairness.TargetRetransPPM)
	lossPressure := m.fairness.LossRatePPM / targetLoss
	retransPressure := m.fairness.RetransmitRatePPM / targetRetrans
	pressure := maxInt64(lossPressure, retransPressure)
	if pressure > 0 {
		pool = pool / (1 + pressure)
	}
	if m.fairness.MinCongestionBPS > 0 {
		pool = maxInt64(m.fairness.MinCongestionBPS, pool)
	}
	return pool
}

func (b *tokenBucket) setLimit(limitBPS int64) {
	limitBPS = maxInt64(0, limitBPS)
	if b.limitBPS == limitBPS {
		return
	}
	b.limitBPS = limitBPS
	b.tokens = limitBPS
	b.last = time.Time{}
}

func (b *tokenBucket) take(requested int64, now time.Time) int64 {
	if b.limitBPS <= 0 {
		return requested
	}
	if b.last.IsZero() {
		b.last = now
		if b.tokens <= 0 {
			b.tokens = b.limitBPS
		}
	} else if now.After(b.last) {
		refill := int64(now.Sub(b.last).Seconds() * float64(b.limitBPS))
		b.tokens = minInt64(b.limitBPS, b.tokens+refill)
		b.last = now
	}
	allowed := minInt64(requested, b.tokens)
	b.tokens -= allowed
	return allowed
}

func (w *trafficWindow) add(bytes int64, now time.Time) {
	if w.start.IsZero() || now.Sub(w.start) >= time.Minute {
		w.start = now
		w.bytes = 0
	}
	w.bytes += bytes
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
