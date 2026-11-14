package ipos

import (
	"context"

	"github.com/google/uuid"
)

// Get returns any IPO of the org (draft or published); ErrNotFound otherwise.
func (s *Service) Get(ctx context.Context, org, id uuid.UUID) (IPO, error) {
	var out IPO
	err := s.inTx(ctx, func(ctx context.Context) (err error) {
		out, err = s.store.GetIPO(ctx, org, id)
		return err
	})
	return out, err
}

// GetPublished returns a published IPO; drafts and unknown ids give the same ErrNotFound.
func (s *Service) GetPublished(ctx context.Context, org, id uuid.UUID) (IPO, error) {
	var out IPO
	err := s.inTx(ctx, func(ctx context.Context) (err error) {
		out, err = s.store.GetPublishedIPO(ctx, org, id)
		return err
	})
	return out, err
}

// List returns one keyset page of the admin list.
func (s *Service) List(ctx context.Context, org uuid.UUID, f AdminFilter) (Page[IPO], error) {
	var rows []IPO
	err := s.inTx(ctx, func(ctx context.Context) (err error) {
		rows, err = s.store.ListIPOsAdmin(ctx, org, f)
		return err
	})
	if err != nil {
		return Page[IPO]{}, err
	}
	return pageOf(rows, f.Limit), nil
}

// ListPublished returns one keyset page of published IPOs.
func (s *Service) ListPublished(ctx context.Context, org uuid.UUID, f ClientFilter) (Page[IPO], error) {
	var rows []IPO
	err := s.inTx(ctx, func(ctx context.Context) (err error) {
		rows, err = s.store.ListPublishedIPOs(ctx, org, f)
		return err
	})
	if err != nil {
		return Page[IPO]{}, err
	}
	return pageOf(rows, f.Limit), nil
}

// pageOf trims the Limit+1 probe row the store returns and sets the cursor when more remain.
func pageOf(rows []IPO, limit int) Page[IPO] {
	n := int(rowLimit(limit)) - 1
	if len(rows) <= n {
		return Page[IPO]{Items: rows}
	}
	rows = rows[:n]
	return Page[IPO]{Items: rows, NextCursor: encodeCursor(rows[n-1])}
}
