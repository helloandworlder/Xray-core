package ratelimit

import (
	"sync"

	"golang.org/x/time/rate"
)

// Manager manages multi-level rate limiters.
type Manager struct {
	mu       sync.RWMutex
	global   *Limiter
	inbound  sync.Map // tag -> *Limiter
	outbound sync.Map // tag -> *Limiter
	level    sync.Map // level (uint32) -> *Limiter
	user     sync.Map // email -> *Limiter
}

// NewManager creates a new rate limit manager.
func NewManager() *Manager {
	return &Manager{}
}

// SetGlobal sets the global rate limiter.
func (m *Manager) SetGlobal(uplink, downlink int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if uplink > 0 || downlink > 0 {
		m.global = NewLimiter(uplink, downlink)
	}
}

// SetInbound sets rate limiter for an inbound tag.
func (m *Manager) SetInbound(tag string, uplink, downlink int64) {
	if tag != "" && (uplink > 0 || downlink > 0) {
		m.inbound.Store(tag, NewLimiter(uplink, downlink))
	}
}

// SetOutbound sets rate limiter for an outbound tag.
func (m *Manager) SetOutbound(tag string, uplink, downlink int64) {
	if tag != "" && (uplink > 0 || downlink > 0) {
		m.outbound.Store(tag, NewLimiter(uplink, downlink))
	}
}

// SetLevel sets rate limiter for a user level.
func (m *Manager) SetLevel(level uint32, uplink, downlink int64) {
	if uplink > 0 || downlink > 0 {
		m.level.Store(level, NewLimiter(uplink, downlink))
	}
}

// SetUser sets rate limiter for a specific user (by email).
func (m *Manager) SetUser(email string, uplink, downlink int64) {
	if email != "" && (uplink > 0 || downlink > 0) {
		m.user.Store(email, NewLimiter(uplink, downlink))
	}
}

// GetUplinkLimiters returns all applicable uplink limiters for a connection.
func (m *Manager) GetUplinkLimiters(inboundTag, outboundTag, userEmail string, userLevel uint32) []*rate.Limiter {
	var limiters []*rate.Limiter

	// Global
	m.mu.RLock()
	if m.global != nil && m.global.Uplink != nil {
		limiters = append(limiters, m.global.Uplink)
	}
	m.mu.RUnlock()

	// Inbound
	if v, ok := m.inbound.Load(inboundTag); ok {
		if l := v.(*Limiter); l.Uplink != nil {
			limiters = append(limiters, l.Uplink)
		}
	}

	// Outbound
	if v, ok := m.outbound.Load(outboundTag); ok {
		if l := v.(*Limiter); l.Uplink != nil {
			limiters = append(limiters, l.Uplink)
		}
	}

	// Level
	if v, ok := m.level.Load(userLevel); ok {
		if l := v.(*Limiter); l.Uplink != nil {
			limiters = append(limiters, l.Uplink)
		}
	}

	// User
	if v, ok := m.user.Load(userEmail); ok {
		if l := v.(*Limiter); l.Uplink != nil {
			limiters = append(limiters, l.Uplink)
		}
	}

	return limiters
}

// GetDownlinkLimiters returns all applicable downlink limiters for a connection.
func (m *Manager) GetDownlinkLimiters(inboundTag, outboundTag, userEmail string, userLevel uint32) []*rate.Limiter {
	var limiters []*rate.Limiter

	// Global
	m.mu.RLock()
	if m.global != nil && m.global.Downlink != nil {
		limiters = append(limiters, m.global.Downlink)
	}
	m.mu.RUnlock()

	// Inbound
	if v, ok := m.inbound.Load(inboundTag); ok {
		if l := v.(*Limiter); l.Downlink != nil {
			limiters = append(limiters, l.Downlink)
		}
	}

	// Outbound
	if v, ok := m.outbound.Load(outboundTag); ok {
		if l := v.(*Limiter); l.Downlink != nil {
			limiters = append(limiters, l.Downlink)
		}
	}

	// Level
	if v, ok := m.level.Load(userLevel); ok {
		if l := v.(*Limiter); l.Downlink != nil {
			limiters = append(limiters, l.Downlink)
		}
	}

	// User
	if v, ok := m.user.Load(userEmail); ok {
		if l := v.(*Limiter); l.Downlink != nil {
			limiters = append(limiters, l.Downlink)
		}
	}

	return limiters
}

// Global singleton manager
var globalManager = NewManager()

// GetManager returns the global rate limit manager.
func GetManager() *Manager {
	return globalManager
}
