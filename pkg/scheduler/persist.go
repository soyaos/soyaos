package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/soyaos/soyaos/pkg/store"
)

// StoreNamespace is the bbolt bucket where persisted jobs live.
const StoreNamespace = "scheduler.jobs"

// MissedFirePolicy enumerates how the scheduler should handle jobs whose
// trigger time elapsed while the process was down. DD-007 vocabulary.
type MissedFirePolicy string

const (
	MissedFireSkip     MissedFirePolicy = "skip"     // jump to the next match (default)
	MissedFireOnce     MissedFirePolicy = "once"     // fire once now, then continue
	MissedFireBackfill MissedFirePolicy = "backfill" // fire every missed match in order
)

// PersistedJob is the on-disk shape of a Job. Fire callbacks can't be
// JSON-encoded — callers re-register the Fire function on startup via
// LoadFromStore + Add. The store is the source of truth for what
// schedules exist; the in-memory callback is its handler.
type PersistedJob struct {
	ID             string    `json:"id"`
	Cron           string    `json:"cron,omitempty"`
	RunAt          time.Time `json:"run_at,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	MissedFire     string    `json:"missed_fire,omitempty"`
	LastFiredAt    time.Time `json:"last_fired_at,omitempty"`
}

// SavePersistent writes a job spec to the store (callback excluded).
// Idempotent — repeated writes overwrite.
func SavePersistent(ctx context.Context, s store.Store, j Job, policy MissedFirePolicy) error {
	if s == nil {
		return errors.New("scheduler: nil store")
	}
	body, err := json.Marshal(PersistedJob{
		ID:             j.ID,
		Cron:           j.Cron,
		RunAt:          j.RunAt,
		IdempotencyKey: j.IdempotencyKey,
		MissedFire:     string(policy),
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
// pair each PersistedJob.ID back to a Fire callback (typically by looking
// up the owning Agent's handler) and call TimeWheel.Add to reactivate it.
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
