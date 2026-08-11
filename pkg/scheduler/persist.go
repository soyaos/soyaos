// persist.go connects the in-memory TimeWheel to pkg/store and
// implements DD-007's three missed-fire policies plus a 24-hour
// idempotency-key dedup window.
//
// The scheduler can't reconstruct a Fire callback from disk — only the
// schedule spec. On startup the caller pairs each PersistedJob.ID back
// to its Fire function (typically by looking up the owning Agent's
// handler) and feeds them into LoadFromStore. That function then:
//
//   - replays missed triggers per Job.MissedFire (skip / once / backfill);
//   - rate-limits backfill to MaxBackfillPerSecond so a long downtime
//     doesn't pin a CPU core spamming retries;
//   - records LastFiredAt back to the store after every fire;
//   - de-duplicates by IdempotencyKey across a 24-hour window using the
//     dedup namespace, so a backfill that overlaps a manual replay
//     doesn't double-deliver.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/soyaos/soyaos/pkg/store"
)

// StoreNamespace is the bbolt bucket where persisted job specs live.
const StoreNamespace = "scheduler.jobs"

// DedupNamespace is the bbolt bucket where idempotency-key fingerprints
// live. Entries are written `key + "\x00" + RFC3339(time)`; LoadFromStore
// prunes anything older than DedupWindow.
const DedupNamespace = "scheduler.dedup"

// DedupWindow is how long an idempotency key blocks re-fires.
const DedupWindow = 24 * time.Hour

// MaxBackfillPerSecond caps how many backfill fires the loader runs per
// second so a long process downtime cannot stampede the system.
const MaxBackfillPerSecond = 10

// MissedFirePolicy enumerates how the scheduler should handle jobs whose
// trigger time elapsed while the process was down. DD-007 vocabulary.
type MissedFirePolicy string

const (
	MissedFireSkip     MissedFirePolicy = "skip"     // jump to the next match (default)
	MissedFireOnce     MissedFirePolicy = "once"     // fire once now, then continue
	MissedFireBackfill MissedFirePolicy = "backfill" // fire every missed match in order (rate-limited)
)

// PersistedJob is the on-disk shape of a Job. Fire callbacks can't be
// JSON-encoded — callers re-register the Fire function on startup via
// LoadFromStore. The store is the source of truth for what schedules
// exist; the in-memory callback is its handler.
type PersistedJob struct {
	ID             string    `json:"id"`
	Cron           string    `json:"cron,omitempty"`
	RunAt          time.Time `json:"run_at,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	MissedFire     string    `json:"missed_fire,omitempty"`
	LastFiredAt    time.Time `json:"last_fired_at,omitempty"`
}

// SavePersistent writes a job spec to the store (callback excluded).
// Idempotent — repeated writes overwrite. The supplied policy is what
// gets recorded on disk; LoadFromStore reads it back into Job.MissedFire.
func SavePersistent(ctx context.Context, s store.Store, j Job, policy MissedFirePolicy) error {
	if s == nil {
		return errors.New("scheduler: nil store")
	}
	if policy == "" {
		policy = MissedFireSkip
	}
	body, err := json.Marshal(PersistedJob{
		ID:             j.ID,
		Cron:           j.Cron,
		RunAt:          j.RunAt,
		IdempotencyKey: j.IdempotencyKey,
		MissedFire:     string(policy),
		LastFiredAt:    j.LastFiredAt,
	})
	if err != nil {
		return err
	}
	return s.Put(ctx, StoreNamespace, []byte(j.ID), body)
}

// DeletePersistent removes a job spec from the store. Missing IDs are
// a no-op.
func DeletePersistent(ctx context.Context, s store.Store, id string) error {
	if s == nil {
		return nil
	}
	return s.Delete(ctx, StoreNamespace, []byte(id))
}

// LoadPersistent reads every persisted job spec from the store. Callers
// pair each PersistedJob.ID back to a Fire callback and call
// TimeWheel.Add to reactivate it. For the full hydration flow that also
// applies missed-fire policies, use LoadFromStore.
func LoadPersistent(ctx context.Context, s store.Store) ([]PersistedJob, error) {
	if s == nil {
		return nil, errors.New("scheduler: nil store")
	}
	pairs, err := s.List(ctx, StoreNamespace, nil)
	if err != nil {
		return nil, err
	}
	out := make([]PersistedJob, 0, len(pairs))
	for _, p := range pairs {
		var j PersistedJob
		if err := json.Unmarshal(p.Value, &j); err != nil {
			continue
		}
		out = append(out, j)
	}
	return out, nil
}

// HandlerFor maps a PersistedJob ID to its Fire callback. Returning
// (nil, false) tells LoadFromStore to skip that job (it was probably
// owned by an Agent that has since been uninstalled).
type HandlerFor func(p PersistedJob) (Fire, bool)

// LoadFromStore re-hydrates every job in the store into tw, applying
// the per-job missed-fire policy against now. The handler resolver
// re-attaches Fire callbacks (which can't be persisted).
//
// Behavior per policy when the trigger time elapsed during downtime:
//
//   - MissedFireSkip: do nothing; the next cron match (or RunAt > now)
//     will fire as usual.
//   - MissedFireOnce: fire exactly once at hydration time, regardless of
//     how many cron matches were missed.
//   - MissedFireBackfill: replay every missed cron match in chronological
//     order, capped at MaxBackfillPerSecond firings per second.
//
// Idempotency keys are honored across the whole hydration: a key that
// fired within DedupWindow is not re-fired.
func LoadFromStore(ctx context.Context, s store.Store, tw *TimeWheel, resolver HandlerFor, now time.Time) error {
	if s == nil {
		return errors.New("scheduler: nil store")
	}
	if tw == nil {
		return errors.New("scheduler: nil time wheel")
	}
	if resolver == nil {
		return errors.New("scheduler: nil handler resolver")
	}
	if now.IsZero() {
		now = time.Now()
	}

	// Prune stale dedup fingerprints before honoring them.
	if err := pruneDedup(ctx, s, now); err != nil {
		return err
	}

	persisted, err := LoadPersistent(ctx, s)
	if err != nil {
		return err
	}

	for _, p := range persisted {
		fire, ok := resolver(p)
		if !ok {
			continue
		}
		j := Job{
			ID:             p.ID,
			Cron:           p.Cron,
			RunAt:          p.RunAt,
			IdempotencyKey: p.IdempotencyKey,
			MissedFire:     MissedFirePolicy(p.MissedFire),
			LastFiredAt:    p.LastFiredAt,
		}
		if j.MissedFire == "" {
			j.MissedFire = MissedFireSkip
		}

		// Apply missed-fire policy synchronously before adding to the
		// time wheel, so the first live tick after Add doesn't race
		// with the catch-up logic.
		if err := applyMissedFire(ctx, s, j, fire, now); err != nil {
			return fmt.Errorf("scheduler: apply missed-fire for %q: %w", j.ID, err)
		}

		// Reattach the live handler for future ticks.
		j.Fire = wrapFireWithDedup(s, j, fire)
		if err := tw.Add(j); err != nil && !errors.Is(err, ErrEmptyJob) {
			return fmt.Errorf("scheduler: add %q: %w", j.ID, err)
		}
	}
	return nil
}

// applyMissedFire executes the missed-fire policy for a single job. It
// uses time.Sleep to honor the backfill rate limit; callers that want
// to skip this can pass MissedFireSkip.
func applyMissedFire(ctx context.Context, s store.Store, j Job, fire Fire, now time.Time) error {
	if j.MissedFire == MissedFireSkip || j.MissedFire == "" {
		return nil
	}
	// One-shot jobs whose RunAt is in the past are a special case: skip
	// surfaces them once if they never fired before.
	if j.Cron == "" {
		if j.RunAt.Before(now) && j.LastFiredAt.IsZero() {
			return tryFire(ctx, s, j, fire, now)
		}
		return nil
	}

	spec, err := parseCron(j.Cron)
	if err != nil {
		return err
	}

	switch j.MissedFire {
	case MissedFireOnce:
		// Fire once "now" if any match occurred in the missed window.
		from := j.LastFiredAt
		if from.IsZero() {
			from = now.Add(-DedupWindow)
		}
		if anyMatchBetween(spec, from, now) {
			return tryFire(ctx, s, j, fire, now)
		}
	case MissedFireBackfill:
		from := j.LastFiredAt
		if from.IsZero() {
			// Avoid stampedes when LastFiredAt was never set — bound
			// the backfill window to one DedupWindow.
			from = now.Add(-DedupWindow)
		}
		matches := cronMatchesBetween(spec, from, now)
		// Rate-limit per the package constant. Sleep between fires so
		// we never exceed MaxBackfillPerSecond.
		gap := time.Second / time.Duration(MaxBackfillPerSecond)
		for i, t := range matches {
			if err := tryFire(ctx, s, j, fire, t); err != nil {
				return err
			}
			if i < len(matches)-1 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(gap):
				}
			}
		}
	}
	return nil
}

// tryFire runs fire(ctx) unless an idempotency-key dedup record from the
// past DedupWindow says we already did. On success we both update
// PersistedJob.LastFiredAt and write a dedup fingerprint.
func tryFire(ctx context.Context, s store.Store, j Job, fire Fire, when time.Time) error {
	if j.IdempotencyKey != "" {
		dup, err := dedupSeen(ctx, s, j.IdempotencyKey, when)
		if err != nil {
			return err
		}
		if dup {
			return nil
		}
	}
	fire(ctx)
	if j.IdempotencyKey != "" {
		if err := dedupRecord(ctx, s, j.IdempotencyKey, when); err != nil {
			return err
		}
	}
	// Persist new LastFiredAt so a second restart doesn't repeat the same
	// fire on backfill mode.
	j.LastFiredAt = when
	body, err := json.Marshal(PersistedJob{
		ID:             j.ID,
		Cron:           j.Cron,
		RunAt:          j.RunAt,
		IdempotencyKey: j.IdempotencyKey,
		MissedFire:     string(j.MissedFire),
		LastFiredAt:    j.LastFiredAt,
	})
	if err != nil {
		return err
	}
	return s.Put(ctx, StoreNamespace, []byte(j.ID), body)
}

// wrapFireWithDedup wraps the caller-supplied Fire so live (non-missed)
// fires also honor the dedup window. This protects against the case
// where a manual `Add` and a cron tick land within the same second.
func wrapFireWithDedup(s store.Store, j Job, fire Fire) Fire {
	if j.IdempotencyKey == "" {
		return fire
	}
	return func(ctx context.Context) {
		now := time.Now()
		dup, err := dedupSeen(ctx, s, j.IdempotencyKey, now)
		if err != nil || dup {
			return
		}
		fire(ctx)
		_ = dedupRecord(ctx, s, j.IdempotencyKey, now)
	}
}

// --- dedup primitives ------------------------------------------------------

func dedupSeen(ctx context.Context, s store.Store, key string, now time.Time) (bool, error) {
	raw, err := s.Get(ctx, DedupNamespace, []byte(key))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	last, perr := time.Parse(time.RFC3339Nano, string(raw))
	if perr != nil {
		return false, nil
	}
	return now.Sub(last) < DedupWindow, nil
}

func dedupRecord(ctx context.Context, s store.Store, key string, when time.Time) error {
	return s.Put(ctx, DedupNamespace, []byte(key), []byte(when.UTC().Format(time.RFC3339Nano)))
}

func pruneDedup(ctx context.Context, s store.Store, now time.Time) error {
	pairs, err := s.List(ctx, DedupNamespace, nil)
	if err != nil {
		return err
	}
	cutoff := now.Add(-DedupWindow)
	for _, p := range pairs {
		t, err := time.Parse(time.RFC3339Nano, string(p.Value))
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			_ = s.Delete(ctx, DedupNamespace, p.Key)
		}
	}
	return nil
}

// --- cron match expansion --------------------------------------------------

// cronMatchesBetween enumerates the times at which spec matches in the
// half-open interval (from, to]. Granularity is one minute, mirroring
// the 5-field cron grammar.
func cronMatchesBetween(spec cronSpec, from, to time.Time) []time.Time {
	if !to.After(from) {
		return nil
	}
	// Start at the next minute boundary after `from`.
	cur := from.Add(time.Minute - time.Duration(from.Second())*time.Second).Truncate(time.Minute)
	var out []time.Time
	for !cur.After(to) {
		if spec.matches(cur) {
			out = append(out, cur)
		}
		cur = cur.Add(time.Minute)
		// Safety: bound the loop at ~1 week of minutes (10k+ iterations).
		if len(out) >= 24*60*7 {
			break
		}
	}
	return out
}

func anyMatchBetween(spec cronSpec, from, to time.Time) bool {
	return len(cronMatchesBetween(spec, from, to)) > 0
}
