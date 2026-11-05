package admin

import (
	"sync"
	"time"
)

// limiter — скользящее окно по ключу. Для входа используется и на адрес, и общий (защита от распределённого подбора).
type limiter struct {
	mu   sync.Mutex
	n    int
	win  time.Duration
	hits map[string][]time.Time
	now  func() time.Time
}

func newLimiter(n int, win time.Duration) *limiter {
	return &limiter{n: n, win: win, hits: map[string][]time.Time{}, now: time.Now}
}

// allow регистрирует попытку и сообщает, разрешена ли она.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.win {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.n {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	if len(l.hits) > 5000 {
		l.hits = map[string][]time.Time{key: l.hits[key]}
	}
	return true
}
