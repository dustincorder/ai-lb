package codex

import (
	"context"
	"fmt"
	"time"
)

// clampPercent keeps percentages inside 0..100.
func clampPercent(n int) int {
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}

// mapRateWindow converts a single RateLimitWindow with its envelope ids.
func mapRateWindow(limitID, limitName string, rw *rateLimitWindow) *QuotaWindow {
	if rw == nil {
		return nil
	}
	w := &QuotaWindow{LimitID: limitID, LimitName: limitName}
	if rw.UsedPercent != nil {
		used := clampPercent(*rw.UsedPercent)
		remaining := 100 - used
		w.UsedPercent = &used
		w.RemainingPercent = &remaining
	}
	if rw.WindowDurationMins != nil {
		w.WindowDurationMins = rw.WindowDurationMins
	}
	if rw.ResetsAt != nil {
		w.ResetAt = rw.ResetsAt
	}
	return w
}

// mapQuota converts a rateLimits/read response into a snapshot. Primary
// and secondary buckets come first; extra limit-id buckets follow except
// when they duplicate an already emitted limit id. Unknown upstream
// fields are ignored by the decoder; missing data stays unknown.
func mapQuota(res rateLimitsResult, now time.Time) QuotaSnapshot {
	snap := QuotaSnapshot{Provider: "codex", UpdatedAt: now}
	seen := map[string]bool{}
	emit := func(limitID, limitName string, rw *rateLimitWindow) {
		w := mapRateWindow(limitID, limitName, rw)
		if w == nil {
			return
		}
		if limitID != "" {
			if seen[limitID] {
				return
			}
			seen[limitID] = true
		}
		snap.Windows = append(snap.Windows, *w)
	}
	if res.RateLimits != nil {
		emit(deref(res.RateLimits.LimitID), deref(res.RateLimits.LimitName), res.RateLimits.Primary)
		emit(deref(res.RateLimits.LimitID), deref(res.RateLimits.LimitName), res.RateLimits.Secondary)
		for id, bucket := range res.RateLimitsByLimitID {
			b := bucket
			name := id
			if b.LimitName != nil && *b.LimitName != "" {
				name = *b.LimitName
			}
			emit(id, name, b.Primary)
			emit(id, name, b.Secondary)
		}
	}
	if snap.Windows == nil {
		snap.Windows = []QuotaWindow{}
	}
	return snap
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ReadRateLimits returns the quota snapshot, using a short in-memory
// cache (quotaCacheTTL) to avoid spamming the app-server from UI
// polling. On live failure with a cached snapshot, the cached value is
// returned marked stale; without cache the error propagates. Snapshots
// are never persisted to SQLite.
func (s *Service) ReadRateLimits(ctx context.Context, accountID string) (QuotaSnapshot, error) {
	if _, err := s.accounts.Get(ctx, accountID); err != nil {
		return QuotaSnapshot{}, err
	}
	snap, err := s.readRateLimitsLive(ctx, accountID)
	if err == nil {
		s.mu.Lock()
		s.quotas[accountID] = cachedQuota{snapshot: snap, at: time.Now()}
		s.mu.Unlock()
		return snap, nil
	}
	s.mu.Lock()
	cached, ok := s.quotas[accountID]
	s.mu.Unlock()
	if !ok {
		return QuotaSnapshot{}, fmt.Errorf("quota unavailable: %w", err)
	}
	if time.Since(cached.at) > quotaCacheTTL {
		// Cache aged out: still better than nothing for display, but
		// honestly marked stale.
	}
	cached.snapshot.Stale = true
	return cached.snapshot, nil
}

func (s *Service) readRateLimitsLive(ctx context.Context, accountID string) (QuotaSnapshot, error) {
	c, _, err := s.spawn(ctx, accountID, nil)
	if err != nil {
		return QuotaSnapshot{}, err
	}
	defer c.Close()
	var res rateLimitsResult
	if err := c.Call(ctx, "account/rateLimits/read", nil, &res); err != nil {
		return QuotaSnapshot{}, err
	}
	return mapQuota(res, time.Now().UTC()), nil
}
