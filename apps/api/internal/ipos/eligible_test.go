package ipos

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
)

// FR-023: RequireEligible (published && APPLY), FOR SHARE lock, transaction required.

func TestRequireEligible_BR_IPO_02_Table(t *testing.T) {
	e := newSvcEnv(t)
	draft := e.create(acme())
	dna := e.published(acme(), FlagDoNotApply)
	apply := e.published(acme(), FlagApply)
	unpub := e.published(acme(), FlagApply)
	if _, err := e.svc.Unpublish(e.ctx(), e.admin, e.org, unpub.ID, unpub.Version, e.key()); err != nil {
		t.Fatal(err)
	}
	other := e.h.NewOrg("Other")
	tests := []struct {
		name string
		org  uuid.UUID
		id   uuid.UUID
		want error
	}{
		{"draft", e.org, draft.ID, ErrNotEligible},
		{"do not apply", e.org, dna.ID, ErrNotEligible},
		{"unpublished apply", e.org, unpub.ID, ErrNotEligible},
		{"unknown", e.org, newID(), ErrNotFound},
		{"other org", other, apply.ID, ErrNotFound},
		{"eligible", e.org, apply.ID, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got IPO
			err := e.h.Txr.InTx(e.ctx(), func(ctx context.Context) error {
				var err error
				got, err = e.svc.RequireEligible(ctx, tc.org, tc.id)
				return err
			})
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tc.want == nil && (got.ID != apply.ID || !got.Eligible()) {
				t.Fatalf("returned %+v", got)
			}
		})
	}
}

func TestRequireEligible_BR_IPO_02_NeedsTransaction(t *testing.T) {
	e := newSvcEnv(t)
	i := e.published(acme(), FlagApply)
	if _, err := e.svc.RequireEligible(e.ctx(), e.org, i.ID); !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("err = %v, want db.ErrNoTx", err)
	}
}

func TestRequireEligible_BR_IPO_02_ShareLockBlocksConcurrentUnpublish(t *testing.T) {
	e := newSvcEnv(t)
	i := e.published(acme(), FlagApply)
	r := e.listen()
	held, release := make(chan struct{}), make(chan struct{})
	holder := make(chan error, 1)
	go func() {
		holder <- e.h.Txr.InTx(e.ctx(), func(ctx context.Context) error {
			if _, err := e.svc.RequireEligible(ctx, e.org, i.ID); err != nil {
				close(held)
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	unpub := make(chan error, 1)
	go func() {
		_, err := e.svc.Unpublish(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key())
		unpub <- err
	}()
	select {
	case err := <-unpub:
		t.Fatalf("Unpublish finished while a RequireEligible holder is open: %v", err)
	case <-time.After(400 * time.Millisecond):
	}
	close(release)
	if err := <-holder; err != nil {
		t.Fatalf("holder: %v", err)
	}
	if err := <-unpub; err != nil {
		t.Fatalf("Unpublish after the holder committed: %v", err)
	}
	if len(r.Elig) != 1 || r.Elig[0].Eligible {
		t.Fatalf("the waiting unpublish must signal after the holder: %+v", r.Elig)
	}
}
