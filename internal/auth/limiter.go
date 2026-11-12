package auth

import (
	"sync"
	"time"
)

// loginLimiter 是有界的登录失败计数器。
//
// 键为客户端地址 + 用户名；窗口内达到上限后拒绝继续尝试。条目数上限固定，
// 超过上限时淘汰最久未使用的条目，避免无界内存增长。
type loginLimiter struct {
	mu          sync.Mutex
	entries     map[string]*loginAttempt
	maxEntries  int
	maxAttempts int
	window      time.Duration
}

type loginAttempt struct {
	count       int
	windowStart time.Time
	lastSeen    time.Time
}

func newLoginLimiter(maxEntries, maxAttempts int, window time.Duration) *loginLimiter {
	if maxEntries <= 0 {
		maxEntries = 1024
	}
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if window <= 0 {
		window = 5 * time.Minute
	}
	return &loginLimiter{
		entries:     make(map[string]*loginAttempt),
		maxEntries:  maxEntries,
		maxAttempts: maxAttempts,
		window:      window,
	}
}

func (l *loginLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.prune(now)
	entry, ok := l.entries[key]
	if !ok {
		return true
	}
	return entry.count < l.maxAttempts
}

func (l *loginLimiter) fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if entry, ok := l.entries[key]; ok && now.Sub(entry.windowStart) <= l.window {
		entry.count++
		entry.lastSeen = now
		return
	}

	if _, exists := l.entries[key]; !exists && len(l.entries) >= l.maxEntries {
		l.evictOldest()
	}
	l.entries[key] = &loginAttempt{count: 1, windowStart: now, lastSeen: now}
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

// prune 清理窗口外条目；调用方必须持有锁。
func (l *loginLimiter) prune(now time.Time) {
	for key, entry := range l.entries {
		if now.Sub(entry.windowStart) > l.window {
			delete(l.entries, key)
		}
	}
}

// evictOldest 淘汰最久未使用的条目；调用方必须持有锁。
func (l *loginLimiter) evictOldest() {
	oldestKey := ""
	var oldest time.Time
	for key, entry := range l.entries {
		if oldestKey == "" || entry.lastSeen.Before(oldest) {
			oldestKey, oldest = key, entry.lastSeen
		}
	}
	if oldestKey != "" {
		delete(l.entries, oldestKey)
	}
}
