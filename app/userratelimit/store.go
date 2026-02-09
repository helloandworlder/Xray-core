package userratelimit

import (
	"strings"
	"sync"
)

type UserRateLimit struct {
	UplinkBps   int64
	DownlinkBps int64
}

var (
	storeMu sync.RWMutex
	store   = make(map[string]UserRateLimit)
)

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func normalizeBps(v int64) int64 {
	if v <= 0 {
		return 0
	}
	return v
}

func Set(email string, uplinkBps int64, downlinkBps int64) bool {
	normalized := normalizeEmail(email)
	if normalized == "" {
		return false
	}
	uplink := normalizeBps(uplinkBps)
	downlink := normalizeBps(downlinkBps)
	if uplink == 0 && downlink == 0 {
		Delete(normalized)
		return true
	}
	storeMu.Lock()
	store[normalized] = UserRateLimit{UplinkBps: uplink, DownlinkBps: downlink}
	storeMu.Unlock()
	return true
}

func Delete(email string) {
	normalized := normalizeEmail(email)
	if normalized == "" {
		return
	}
	storeMu.Lock()
	delete(store, normalized)
	storeMu.Unlock()
}

func Get(email string) (UserRateLimit, bool) {
	normalized := normalizeEmail(email)
	if normalized == "" {
		return UserRateLimit{}, false
	}
	storeMu.RLock()
	item, ok := store[normalized]
	storeMu.RUnlock()
	return item, ok
}

func List() map[string]UserRateLimit {
	storeMu.RLock()
	items := make(map[string]UserRateLimit, len(store))
	for email, item := range store {
		items[email] = item
	}
	storeMu.RUnlock()
	return items
}
