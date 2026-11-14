//nolint:unparam,revive,unused // S01 compile skeleton; removed as slices land
package ipos

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Store is the data access of the ipos table, one method per query of data-model section 6.
// Every method filters by org. Domain types only; the sqlc-backed body lands in S05.
type Store struct{}

// FactsUpdate carries the columns UpdateIPOFacts writes.
type FactsUpdate struct {
	Draft   Draft
	Version int32
	Now     time.Time
}

// StatusUpdate carries the columns SetIPOStatus writes.
type StatusUpdate struct {
	Status     Status
	Source     StatusSource
	Version    int32
	Now        time.Time
	OverrideAt *time.Time
}

func (s *Store) InsertIPO(ctx context.Context, org uuid.UUID, d Draft, idemKey *string, now time.Time) (IPO, bool, error) {
	return IPO{}, false, errNotImplemented
}
func (s *Store) GetIPOByCreateKey(ctx context.Context, org uuid.UUID, key string) (IPO, error) {
	return IPO{}, errNotImplemented
}
func (s *Store) GetIPO(ctx context.Context, org, id uuid.UUID) (IPO, error) {
	return IPO{}, errNotImplemented
}
func (s *Store) GetIPOForUpdate(ctx context.Context, org, id uuid.UUID) (IPO, error) {
	return IPO{}, errNotImplemented
}
func (s *Store) GetIPOForShare(ctx context.Context, org, id uuid.UUID) (IPO, error) {
	return IPO{}, errNotImplemented
}
func (s *Store) GetPublishedIPO(ctx context.Context, org, id uuid.UUID) (IPO, error) {
	return IPO{}, errNotImplemented
}
func (s *Store) UpdateIPOFacts(ctx context.Context, org, id uuid.UUID, u FactsUpdate) (IPO, error) {
	return IPO{}, errNotImplemented
}
func (s *Store) SetIPOFlag(ctx context.Context, org, id uuid.UUID, version int32, flag *ApplyFlag, note *string, now time.Time) (IPO, error) {
	return IPO{}, errNotImplemented
}
func (s *Store) SetIPOPublished(ctx context.Context, org, id uuid.UUID, version int32, published bool, now time.Time) (IPO, error) {
	return IPO{}, errNotImplemented
}
func (s *Store) SetIPOStatus(ctx context.Context, org, id uuid.UUID, u StatusUpdate) (IPO, error) {
	return IPO{}, errNotImplemented
}
func (s *Store) DeleteIPO(ctx context.Context, org, id uuid.UUID, version int32) (int64, error) {
	return 0, errNotImplemented
}
func (s *Store) ListIPOsAdmin(ctx context.Context, org uuid.UUID, f AdminFilter) ([]IPO, error) {
	return nil, errNotImplemented
}
func (s *Store) ListPublishedIPOs(ctx context.Context, org uuid.UUID, f ClientFilter) ([]IPO, error) {
	return nil, errNotImplemented
}
func (s *Store) FindIPOAuditByIdempotencyKey(ctx context.Context, org, id uuid.UUID, action, key string) (uuid.UUID, bool, error) {
	return uuid.Nil, false, errNotImplemented
}
