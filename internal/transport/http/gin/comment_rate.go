package ginhttp

import (
	"crypto/sha256"
	"sync"
	"time"
)

const maxCommentRateIdentities = 4096

// CommentRateLimiter is the process-local public guest abuse boundary.
type CommentRateLimiter interface {
	Allow(clientIdentity string) (bool, time.Duration)
}

type commentRateLimiter struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	clock     func() time.Time
	windows   map[[sha256.Size]byte][]time.Time
	calls     uint64
	lastSweep time.Time
}

// NewCommentRateLimiter constructs a deterministic sliding-window limiter. It
// hashes socket identities before retaining them in process memory.
func NewCommentRateLimiter(limit int, window time.Duration, clock func() time.Time) (CommentRateLimiter, error) {
	if limit <= 0 || window <= 0 {
		return nil, errInvalidCommentRateLimit
	}
	if clock == nil {
		clock = time.Now
	}
	return &commentRateLimiter{limit: limit, window: window, clock: clock, windows: make(map[[sha256.Size]byte][]time.Time)}, nil
}

func (limiter *commentRateLimiter) Allow(clientIdentity string) (bool, time.Duration) {
	key := sha256.Sum256([]byte(clientIdentity))
	now := limiter.clock()
	cutoff := now.Add(-limiter.window)
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.calls++
	capacitySweep := len(limiter.windows) >= maxCommentRateIdentities && (limiter.lastSweep.IsZero() || now.Sub(limiter.lastSweep) >= time.Second)
	if limiter.calls%256 == 0 || capacitySweep {
		for candidate, candidateInstants := range limiter.windows {
			if len(candidateInstants) == 0 || !candidateInstants[len(candidateInstants)-1].After(cutoff) {
				delete(limiter.windows, candidate)
			}
		}
		limiter.lastSweep = now
	}
	if _, known := limiter.windows[key]; !known && len(limiter.windows) >= maxCommentRateIdentities {
		return false, limiter.window
	}
	instants := limiter.windows[key]
	firstLive := 0
	for firstLive < len(instants) && !instants[firstLive].After(cutoff) {
		firstLive++
	}
	instants = append([]time.Time(nil), instants[firstLive:]...)
	if len(instants) >= limiter.limit {
		retry := instants[0].Add(limiter.window).Sub(now)
		if retry < 0 {
			retry = 0
		}
		limiter.windows[key] = instants
		return false, retry
	}
	limiter.windows[key] = append(instants, now)
	return true, 0
}
