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
// and secondary buckets are distinct windows even under one limit id,
// so deduplication keys on (limit id, bucket role). The historical
// single-bucket view and the same entry under rateLimitsByLimitId must
// not duplicate each other. Unknown upstream fields are ignored by the
// decoder; missing data stays unknown.
func mapQuota(res rateLimitsResult, now time.Time) QuotaSnapshot {
	snap := QuotaSnapshot{Provider: "codex", UpdatedAt: now}
	seen := map[[2]string]bool{}
	emit := func(limitID, limitName, role string, rw *rateLimitWindow) {
		w := mapRateWindow(limitID, limitName, rw)
		if w == nil {
			return
		}
		key := [2]string{limitID, role}
		if limitID != "" {
			if seen[key] {
				return
			}
			seen[key] = true
		}
		snap.Windows = append(snap.Windows, *w)
	}
	if res.RateLimits != nil {
		emit(deref(res.RateLimits.LimitID), deref(res.RateLimits.LimitName), "primary", res.RateLimits.Primary)
		emit(deref(res.RateLimits.LimitID), deref(res.RateLimits.LimitName), "secondary", res.RateLimits.Secondary)
		for id, bucket := range res.RateLimitsByLimitID {
			b := bucket
			name := id
			if b.LimitName != nil && *b.LimitName != "" {
				name = *b.LimitName
			}
			emit(id, name, "primary", b.Primary)
			emit(id, name, "secondary", b.Secondary)
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

// ReadRateLimits returns the quota snapshot, reusing the in-memory
// cache while it is fresh (quotaCacheTTL) so ordinary UI polling does
// not spawn an app-server per view. A stale result is served only on
// live failure; with no cache at all the error propagates. Snapshots
// are never persisted to SQLite.
func (s *Service) ReadRateLimits(ctx context.Context, accountID string) (QuotaSnapshot, error) {
	if _, err := s.accounts.Get(ctx, accountID); err != nil {
		return QuotaSnapshot{}, err
	}
	if snap, ok := s.freshQuota(accountID); ok {
		return snap, nil
	}
	return s.refreshQuotaLocked(ctx, accountID)
}

// RefreshRateLimits always performs a live read, bypassing the cache
// TTL, and updates the cache on success. On live failure with a cached
// snapshot it returns the snapshot marked stale; without cache the
// error propagates.
func (s *Service) RefreshRateLimits(ctx context.Context, accountID string) (QuotaSnapshot, error) {
	if _, err := s.accounts.Get(ctx, accountID); err != nil {
		return QuotaSnapshot{}, err
	}
	return s.refreshQuotaLocked(ctx, accountID)
}
func (s *Service) freshQuota(accountID string) (QuotaSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cached, ok := s.quotas[accountID]
	if !ok || time.Since(cached.at) > quotaCacheTTL {
		return QuotaSnapshot{}, false
	}
	return cached.snapshot, true
}

// dropQuotaCache forgets the snapshot for an account. Called after a
// successful connection commit and after logout: one local profile may
// later authenticate as a different ChatGPT identity, and the previous
// identity's quota must never be served fresh within its TTL.
func (s *Service) dropQuotaCache(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.quotas, accountID)
}

func (s *Service) refreshQuotaLocked(ctx context.Context, accountID string) (QuotaSnapshot, error) {
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
	cached.snapshot.Stale = true
	return cached.snapshot, nil
}

func (s *Service) readRateLimitsLive(ctx context.Context, accountID string) (QuotaSnapshot, error) {
	c, _, err := s.spawn(ctx, ctx, accountID, nil)
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
