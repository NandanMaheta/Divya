package auth_test

// S08 - Store integration tests, sessions and refresh tokens (FR-040..FR-042, D-10, D-11,
// plan P-02, P-03; data-model.md sections 2 and 3).

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
)

func TestStoreSessions_CreateRoundTripAndLiveDeviceUniqueness(t *testing.T) {
	e := authtest.New(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	device := uuid.New()
	s := stNewSession(t, e, u.ID, device)

	got, err := e.Store.GetSession(t.Context(), s.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.UserID != u.ID || got.DeviceID != device || got.OrgID != e.Org || got.Platform != "android" || got.ClientType != "mobile" ||
		got.EndedAt != nil || got.EndReason != "" || got.MFAMethod != "" {
		t.Fatalf("session = %+v", got)
	}
	if !got.CreatedAt.Equal(e.Clock.Now()) || !got.LastActiveAt.Equal(e.Clock.Now()) ||
		!got.IdleExpiresAt.Equal(e.Clock.Now().Add(30*24*time.Hour)) || !got.AbsoluteExpiresAt.Equal(e.Clock.Now().Add(90*24*time.Hour)) {
		t.Fatalf("session times = %+v", got)
	}

	// D-11: one live session per (user, device).
	err = e.Tx.InTx(t.Context(), func(ctx context.Context) error {
		_, err := e.Store.CreateSession(ctx, auth.NewSession{
			OrgID: e.Org, UserID: u.ID, DeviceID: device, DeviceName: "Pixel 9", Platform: "android", ClientType: "mobile",
			Now: e.Clock.Now(), IdleExpiresAt: e.Clock.Now().Add(time.Hour), AbsoluteExpiresAt: e.Clock.Now().Add(2 * time.Hour),
		})
		return err
	})
	if !errors.Is(err, auth.ErrDeviceSessionExists) {
		t.Fatalf("second live session on the same device = %v, want ErrDeviceSessionExists", err)
	}

	// Once ended, the installation may sign in again; another user may share the device id.
	stInTx(t, e, func(ctx context.Context) error {
		ok, err := e.Store.EndSession(ctx, s.ID, "replaced", e.Clock.Now())
		if err != nil || !ok {
			t.Fatalf("EndSession = %v, %v", ok, err)
		}
		return nil
	})
	stNewSession(t, e, u.ID, device)
	other := e.UserWithStatus(t, auth.StatusActive)
	stNewSession(t, e, other.ID, device)
}

func TestStoreSessions_AdminSessionRecordsTheMFAMethod(t *testing.T) {
	e := authtest.New(t)
	a := e.NewAdmin(t)
	var s auth.SessionRow
	stInTx(t, e, func(ctx context.Context) (err error) {
		s, err = e.Store.CreateSession(ctx, auth.NewSession{
			OrgID: e.Org, UserID: a.ID, DeviceID: uuid.New(), DeviceName: "Chrome on Mac", Platform: "web", ClientType: "web", MFAMethod: "totp",
			Now: e.Clock.Now(), IdleExpiresAt: e.Clock.Now().Add(12 * time.Hour), AbsoluteExpiresAt: e.Clock.Now().Add(7 * 24 * time.Hour),
		})
		return err
	})
	got, _ := e.Store.GetSession(t.Context(), s.ID)
	if got.MFAMethod != "totp" || got.ClientType != "web" {
		t.Fatalf("session = %+v", got)
	}
}

func TestStoreSessions_NotFoundAndLockNeedsATransaction(t *testing.T) {
	e := authtest.New(t)
	if _, err := e.Store.GetSession(t.Context(), uuid.New()); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Errorf("GetSession(unknown) = %v", err)
	}
	if _, err := e.Store.GetSessionPrincipal(t.Context(), uuid.New()); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Errorf("GetSessionPrincipal(unknown) = %v", err)
	}
	u := e.UserWithStatus(t, auth.StatusActive)
	s := e.NewSession(t, u.ID, authtest.SessionSpec{})
	if _, err := e.Store.LockSession(t.Context(), s); !errors.Is(err, db.ErrNoTx) {
		t.Errorf("LockSession without a transaction = %v, want db.ErrNoTx", err)
	}
	stInTx(t, e, func(ctx context.Context) error {
		if got, err := e.Store.LockSession(ctx, s); err != nil || got.ID != s {
			t.Errorf("LockSession = %+v, %v", got, err)
		}
		if _, err := e.Store.LockSession(ctx, uuid.New()); !errors.Is(err, auth.ErrSessionNotFound) {
			t.Errorf("LockSession(unknown) = %v", err)
		}
		return nil
	})
}

func TestStoreSessions_GetSessionPrincipalJoinsUserAndSession(t *testing.T) {
	e := authtest.New(t)
	admin := e.NewAdmin(t)
	s := e.NewSession(t, admin.ID, authtest.SessionSpec{ClientType: "web", Platform: "web", MFAMethod: "totp", Idle: 12 * time.Hour, Absolute: 7 * 24 * time.Hour})

	p, err := e.Store.GetSessionPrincipal(t.Context(), s)
	if err != nil {
		t.Fatalf("GetSessionPrincipal: %v", err)
	}
	if p.SessionID != s || p.UserID != admin.ID || p.OrgID != e.Org || p.Role != "admin" || p.Status != auth.StatusActive || p.EndedAt != nil {
		t.Fatalf("principal = %+v", p)
	}
	if !p.IdleExpiresAt.Equal(e.Clock.Now().Add(12*time.Hour)) || !p.AbsoluteExpiresAt.Equal(e.Clock.Now().Add(7*24*time.Hour)) {
		t.Fatalf("principal expiry = %v / %v", p.IdleExpiresAt, p.AbsoluteExpiresAt)
	}
}

func TestStoreSessions_PrincipalReflectsTheCurrentUserStatusAndEndedSession(t *testing.T) {
	e := authtest.New(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	s := e.NewSession(t, u.ID, authtest.SessionSpec{})

	stInTx(t, e, func(ctx context.Context) error {
		return e.Store.SetStatus(ctx, u.ID, auth.StatusDisabled, auth.StatusActive, e.Clock.Now())
	})
	p, _ := e.Store.GetSessionPrincipal(t.Context(), s)
	if p.Status != auth.StatusDisabled {
		t.Fatalf("principal status = %s, want the user's current status disabled", p.Status)
	}

	endedAt := e.Clock.Now().Add(time.Minute)
	stInTx(t, e, func(ctx context.Context) error {
		_, err := e.Store.EndSession(ctx, s, "disabled", endedAt)
		return err
	})
	p, _ = e.Store.GetSessionPrincipal(t.Context(), s)
	if p.EndedAt == nil || !p.EndedAt.Equal(endedAt) {
		t.Fatalf("principal ended_at = %v, want %v", p.EndedAt, endedAt)
	}
}

func TestStoreSessions_PrincipalLiveBoundaries(t *testing.T) {
	idle := authtest.T0.Add(30 * time.Minute)
	abs := authtest.T0.Add(2 * time.Hour)
	ended := authtest.T0.Add(-time.Second)
	base := auth.SessionPrincipal{
		SessionID: uuid.New(), UserID: uuid.New(), OrgID: uuid.New(), Role: "client", Status: auth.StatusActive,
		IdleExpiresAt: idle, AbsoluteExpiresAt: abs,
	}
	tests := []struct {
		name string
		mod  func(p *auth.SessionPrincipal)
		now  time.Time
		want bool
	}{
		{"live", func(*auth.SessionPrincipal) {}, authtest.T0, true},
		{"one nanosecond before idle expiry", func(*auth.SessionPrincipal) {}, idle.Add(-time.Nanosecond), true},
		{"exactly at idle expiry", func(*auth.SessionPrincipal) {}, idle, false},
		{"after idle expiry", func(*auth.SessionPrincipal) {}, idle.Add(time.Second), false},
		{"idle after absolute: one nanosecond before absolute", func(p *auth.SessionPrincipal) { p.IdleExpiresAt = abs.Add(time.Hour) }, abs.Add(-time.Nanosecond), true},
		{"exactly at absolute expiry", func(p *auth.SessionPrincipal) { p.IdleExpiresAt = abs.Add(time.Hour) }, abs, false},
		{"ended session", func(p *auth.SessionPrincipal) { p.EndedAt = &ended }, authtest.T0, false},
		{"user pending_verification", func(p *auth.SessionPrincipal) { p.Status = auth.StatusPendingVerification }, authtest.T0, false},
		{"user pending_approval", func(p *auth.SessionPrincipal) { p.Status = auth.StatusPendingApproval }, authtest.T0, false},
		{"user disabled", func(p *auth.SessionPrincipal) { p.Status = auth.StatusDisabled }, authtest.T0, false},
		{"admin active", func(p *auth.SessionPrincipal) { p.Role = "admin" }, authtest.T0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.mod(&p)
			if got := p.Live(tc.now); got != tc.want {
				t.Fatalf("Live(%v) = %v, want %v", tc.now, got, tc.want)
			}
		})
	}
}

func TestStoreSessions_ListLiveSessionsExcludesEndedExpiredAndOthers(t *testing.T) {
	e := authtest.New(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	other := e.UserWithStatus(t, auth.StatusActive)

	live1 := e.NewSession(t, u.ID, authtest.SessionSpec{})
	e.Clock.Advance(time.Minute)
	live2 := e.NewSession(t, u.ID, authtest.SessionSpec{})
	idleDead := e.NewSession(t, u.ID, authtest.SessionSpec{Idle: time.Minute})
	absDead := e.NewSession(t, u.ID, authtest.SessionSpec{Absolute: time.Minute})
	ended := e.NewSession(t, u.ID, authtest.SessionSpec{})
	e.NewSession(t, other.ID, authtest.SessionSpec{})
	e.Exec(t, `UPDATE device_sessions SET ended_at=$2, end_reason='signed_out' WHERE id=$1`, ended, e.Clock.Now())
	_ = idleDead
	_ = absDead

	now := e.Clock.Now().Add(time.Minute) // idleDead and absDead expire at exactly this instant: expired
	got, err := e.Store.ListLiveSessions(t.Context(), u.ID, now)
	if err != nil {
		t.Fatalf("ListLiveSessions: %v", err)
	}
	var ids []uuid.UUID
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []uuid.UUID{live2, live1}) {
		t.Fatalf("live sessions = %v, want [%v %v] (most recently active first)", ids, live2, live1)
	}
	justBefore, _ := e.Store.ListLiveSessions(t.Context(), u.ID, now.Add(-time.Nanosecond))
	if len(justBefore) != 4 {
		t.Fatalf("one nanosecond before the boundary %d sessions are live, want 4", len(justBefore))
	}
}

func TestStoreSessions_TouchEndAndEndAll(t *testing.T) {
	e := authtest.New(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	s1 := e.NewSession(t, u.ID, authtest.SessionSpec{})
	s2 := e.NewSession(t, u.ID, authtest.SessionSpec{})
	s3 := e.NewSession(t, u.ID, authtest.SessionSpec{})
	later := e.Clock.Now().Add(time.Hour)

	stInTx(t, e, func(ctx context.Context) error {
		return e.Store.TouchSession(ctx, s1, later, later.Add(30*24*time.Hour))
	})
	got, _ := e.Store.GetSession(t.Context(), s1)
	if !got.LastActiveAt.Equal(later) || !got.IdleExpiresAt.Equal(later.Add(30*24*time.Hour)) || !got.CreatedAt.Equal(e.Clock.Now()) {
		t.Fatalf("after Touch = %+v", got)
	}

	var first, second bool
	stInTx(t, e, func(ctx context.Context) (err error) {
		if first, err = e.Store.EndSession(ctx, s1, "signed_out", later); err != nil {
			return err
		}
		second, err = e.Store.EndSession(ctx, s1, "refresh_reuse", later.Add(time.Minute))
		return err
	})
	if !first || second {
		t.Fatalf("EndSession twice = %v, %v; want true then false", first, second)
	}
	got, _ = e.Store.GetSession(t.Context(), s1)
	if got.EndedAt == nil || !got.EndedAt.Equal(later) || got.EndReason != "signed_out" {
		t.Fatalf("the first end must stand, got %+v", got)
	}

	err := e.Tx.InTx(t.Context(), func(ctx context.Context) error {
		_, err := e.Store.EndSession(ctx, s2, "not_a_reason", later)
		return err
	})
	if err == nil {
		t.Fatal("EndSession with an unknown reason must fail (CHECK list)")
	}

	var ended []uuid.UUID
	stInTx(t, e, func(ctx context.Context) (err error) {
		ended, err = e.Store.EndUserSessions(ctx, u.ID, s3, "signed_out_everywhere", later.Add(time.Hour))
		return err
	})
	if !slices.Equal(ended, []uuid.UUID{s2}) {
		t.Fatalf("EndUserSessions(except s3) = %v, want only the live s2 [%v] (s1 was already ended)", ended, s2)
	}
	stInTx(t, e, func(ctx context.Context) (err error) {
		ended, err = e.Store.EndUserSessions(ctx, u.ID, uuid.Nil, "password_changed", later.Add(2*time.Hour))
		return err
	})
	if !slices.Equal(ended, []uuid.UUID{s3}) {
		t.Fatalf("EndUserSessions(except none) = %v, want [%v]", ended, s3)
	}
	stInTx(t, e, func(ctx context.Context) (err error) {
		ended, err = e.Store.EndUserSessions(ctx, u.ID, uuid.Nil, "password_changed", later.Add(3*time.Hour))
		return err
	})
	if len(ended) != 0 {
		t.Fatalf("EndUserSessions with nothing live = %v, want empty", ended)
	}
}

func TestStoreSessions_EndUserSessionsReturnsIDsAscending(t *testing.T) {
	e := authtest.New(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	var want []uuid.UUID
	for range 5 {
		want = append(want, e.NewSession(t, u.ID, authtest.SessionSpec{}))
	}
	slices.SortFunc(want, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	var got []uuid.UUID
	stInTx(t, e, func(ctx context.Context) (err error) {
		got, err = e.Store.EndUserSessions(ctx, u.ID, uuid.Nil, "disabled", e.Clock.Now())
		return err
	})
	if !slices.Equal(got, want) {
		t.Fatalf("ended ids = %v, want ascending %v", got, want)
	}
}

func TestStoreSessions_InsertRefreshRoundTripAndDomainErrors(t *testing.T) {
	e := authtest.New(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	s := e.NewSession(t, u.ID, authtest.SessionSpec{})
	s2 := e.NewSession(t, u.ID, authtest.SessionSpec{})

	insert := func(session uuid.UUID, h byte) (auth.RefreshRow, error) {
		var r auth.RefreshRow
		err := e.Tx.InTx(t.Context(), func(ctx context.Context) (err error) {
			r, err = e.Store.InsertRefresh(ctx, e.Org, session, stHash(h), e.Clock.Now())
			return err
		})
		return r, err
	}
	r, err := insert(s, 1)
	if err != nil {
		t.Fatalf("InsertRefresh: %v", err)
	}
	if r.ID == uuid.Nil || r.SessionID != s || r.OrgID != e.Org || r.UsedAt != nil || r.ReplacedBy != uuid.Nil || !r.IssuedAt.Equal(e.Clock.Now()) {
		t.Fatalf("row = %+v", r)
	}
	found, err := e.Store.FindRefreshByHash(t.Context(), stHash(1))
	if err != nil || found.ID != r.ID {
		t.Fatalf("FindRefreshByHash = %+v, %v", found, err)
	}
	if _, err := e.Store.FindRefreshByHash(t.Context(), stHash(2)); !errors.Is(err, auth.ErrRefreshNotFound) {
		t.Errorf("FindRefreshByHash(unknown) = %v, want ErrRefreshNotFound", err)
	}
	if _, err := insert(s, 2); !errors.Is(err, auth.ErrRefreshExists) {
		t.Errorf("a second unused token in the family = %v, want ErrRefreshExists", err)
	}
	if _, err := insert(s2, 1); !errors.Is(err, auth.ErrRefreshExists) {
		t.Errorf("a duplicate digest = %v, want ErrRefreshExists", err)
	}
}

func TestStoreSessions_RotateRefreshLinksOldToNewAtomically(t *testing.T) {
	e := authtest.New(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	s := e.NewSession(t, u.ID, authtest.SessionSpec{})
	var r1 auth.RefreshRow
	stInTx(t, e, func(ctx context.Context) (err error) {
		r1, err = e.Store.InsertRefresh(ctx, e.Org, s, stHash(1), e.Clock.Now())
		return err
	})

	if _, err := e.Store.RotateRefresh(t.Context(), r1.ID, stHash(2), e.Clock.Now()); !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("RotateRefresh without a transaction = %v, want db.ErrNoTx", err)
	}

	later := e.Clock.Now().Add(15 * time.Minute)
	var r2 auth.RefreshRow
	stInTx(t, e, func(ctx context.Context) (err error) {
		r2, err = e.Store.RotateRefresh(ctx, r1.ID, stHash(2), later)
		return err
	})
	if r2.ID == r1.ID || r2.SessionID != s || r2.UsedAt != nil || !r2.IssuedAt.Equal(later) || string(r2.TokenHash) != string(stHash(2)) {
		t.Fatalf("successor = %+v", r2)
	}
	old, _ := e.Store.FindRefreshByHash(t.Context(), stHash(1))
	if old.UsedAt == nil || !old.UsedAt.Equal(later) || old.ReplacedBy != r2.ID {
		t.Fatalf("old token = %+v, want used at %v and replaced_by %v", old, later, r2.ID)
	}
	var unused int
	if err := e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM refresh_tokens WHERE session_id=$1 AND used_at IS NULL`, s).Scan(&unused); err != nil || unused != 1 {
		t.Fatalf("unused tokens in the family = %d (%v), want exactly 1", unused, err)
	}

	// Reuse: rotating the used token again is refused and adds nothing.
	err := e.Tx.InTx(t.Context(), func(ctx context.Context) error {
		_, err := e.Store.RotateRefresh(ctx, r1.ID, stHash(3), later.Add(time.Minute))
		return err
	})
	if !errors.Is(err, auth.ErrRefreshUsed) {
		t.Fatalf("rotating a used token = %v, want ErrRefreshUsed", err)
	}
	err = e.Tx.InTx(t.Context(), func(ctx context.Context) error {
		_, err := e.Store.RotateRefresh(ctx, uuid.New(), stHash(3), later)
		return err
	})
	if !errors.Is(err, auth.ErrRefreshNotFound) {
		t.Fatalf("rotating an unknown token = %v, want ErrRefreshNotFound", err)
	}
	var total int
	if err := e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM refresh_tokens WHERE session_id=$1`, s).Scan(&total); err != nil || total != 2 {
		t.Fatalf("family size = %d (%v), want 2", total, err)
	}
}

func TestStoreSessions_RotateRefreshFailureLeavesTheOldTokenUnusedAfterRollback(t *testing.T) {
	e := authtest.New(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	s := e.NewSession(t, u.ID, authtest.SessionSpec{})
	other := e.NewSession(t, u.ID, authtest.SessionSpec{})
	var r1 auth.RefreshRow
	stInTx(t, e, func(ctx context.Context) (err error) {
		r1, err = e.Store.InsertRefresh(ctx, e.Org, s, stHash(1), e.Clock.Now())
		if err != nil {
			return err
		}
		_, err = e.Store.InsertRefresh(ctx, e.Org, other, stHash(9), e.Clock.Now())
		return err
	})
	err := e.Tx.InTx(t.Context(), func(ctx context.Context) error {
		_, err := e.Store.RotateRefresh(ctx, r1.ID, stHash(9), e.Clock.Now()) // digest already taken
		return err
	})
	if err == nil {
		t.Fatal("rotating onto an existing digest must fail")
	}
	got, _ := e.Store.FindRefreshByHash(t.Context(), stHash(1))
	if got.UsedAt != nil || got.ReplacedBy != uuid.Nil {
		t.Fatalf("a failed rotation left the old token %+v", got)
	}
}

func TestStoreSessions_LockRefreshNeedsATransaction(t *testing.T) {
	e := authtest.New(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	s := e.NewSession(t, u.ID, authtest.SessionSpec{})
	var r auth.RefreshRow
	stInTx(t, e, func(ctx context.Context) (err error) {
		r, err = e.Store.InsertRefresh(ctx, e.Org, s, stHash(1), e.Clock.Now())
		return err
	})
	if _, err := e.Store.LockRefresh(t.Context(), r.ID); !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("LockRefresh without a transaction = %v, want db.ErrNoTx", err)
	}
	stInTx(t, e, func(ctx context.Context) error {
		if got, err := e.Store.LockRefresh(ctx, r.ID); err != nil || got.ID != r.ID {
			t.Errorf("LockRefresh = %+v, %v", got, err)
		}
		if _, err := e.Store.LockRefresh(ctx, uuid.New()); !errors.Is(err, auth.ErrRefreshNotFound) {
			t.Errorf("LockRefresh(unknown) = %v", err)
		}
		return nil
	})
}

// TestStoreSessions_ConcurrentRefreshOfOneTokenRotatesExactlyOnce is the store half of D-10 /
// plan P-03: N transactions present R1 in the documented lock order; the first rotates it and the
// others, re-reading after the lock, find it used.
func TestStoreSessions_ConcurrentRefreshOfOneTokenRotatesExactlyOnce(t *testing.T) {
	for _, n := range []int{2, 5, 8} {
		t.Run(string(rune('0'+n))+" goroutines", func(t *testing.T) {
			e := authtest.New(t)
			u := e.UserWithStatus(t, auth.StatusActive)
			s := e.NewSession(t, u.ID, authtest.SessionSpec{})
			var r1 auth.RefreshRow
			stInTx(t, e, func(ctx context.Context) (err error) {
				r1, err = e.Store.InsertRefresh(ctx, e.Org, s, stHash(1), e.Clock.Now())
				return err
			})

			rotated := make([]bool, n)
			errs := stRace(n, func(i int) error {
				return e.Tx.InTx(t.Context(), func(ctx context.Context) error {
					if _, err := e.Store.LockUser(ctx, u.ID); err != nil {
						return err
					}
					if _, err := e.Store.LockSession(ctx, s); err != nil {
						return err
					}
					cur, err := e.Store.LockRefresh(ctx, r1.ID)
					if err != nil {
						return err
					}
					if cur.UsedAt != nil {
						return nil // reuse branch: the Service would end the family here
					}
					_, err = e.Store.RotateRefresh(ctx, r1.ID, stHash(byte(100+i)), e.Clock.Now())
					rotated[i] = err == nil
					return err
				})
			})
			stNoDeadlock(t, errs)
			wins := 0
			for _, ok := range rotated {
				if ok {
					wins++
				}
			}
			if wins != 1 {
				t.Fatalf("%d goroutines rotated R1, want exactly 1", wins)
			}
			var total, unused int
			if err := e.Pool.QueryRow(t.Context(), `SELECT count(*), count(*) FILTER (WHERE used_at IS NULL) FROM refresh_tokens WHERE session_id=$1`, s).Scan(&total, &unused); err != nil {
				t.Fatal(err)
			}
			if total != 2 || unused != 1 {
				t.Fatalf("family = %d tokens, %d unused; want 2 and 1", total, unused)
			}
		})
	}
}
