package auth_test

// S13 - Service tests B, concurrency (plan P-03 "Concurrency", D-10, D-11; US-3 s3). A start
// barrier releases N goroutines together on the real Store and a real PostgreSQL.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
)

func TestConcurrency_SameRefreshTokenExactlyOneWins(t *testing.T) {
	for _, n := range []int{2, 4, 8} {
		t.Run(fmt.Sprintf("%d goroutines", n), func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			u := e.UserWithStatus(t, auth.StatusActive)
			device := uuid.New()
			tok := svSignIn(t, s, u, device)

			results := make([]auth.SessionTokens, n)
			errs := stRace(n, func(i int) (err error) {
				results[i], err = s.Refresh(t.Context(), svRefreshIn(device, tok.RefreshToken))
				return err
			})
			var winner = -1
			wins := 0
			for i, err := range errs {
				switch {
				case err == nil:
					wins++
					winner = i
				case !errors.Is(err, auth.ErrRefreshRejected):
					t.Errorf("goroutine %d: err = %v, want ErrRefreshRejected", i, err)
				}
			}
			if wins != 1 {
				t.Fatalf("successes = %d, want exactly 1", wins)
			}
			if ss := svSession(t, e, tok.SessionID); ss.EndedAt == nil || ss.EndReason != "refresh_reuse" {
				t.Errorf("session ended=%v reason=%q, want ended by refresh_reuse (the loser counts as reuse)", ss.EndedAt, ss.EndReason)
			}
			if got := len(e.AuditOf(t, auth.ActionRefreshReuseDetected)); got != 1 {
				t.Errorf("reuse audit rows = %d, want exactly 1", got)
			}
			if got := len(e.AuditOf(t, auth.ActionSessionRefreshed)); got != 1 {
				t.Errorf("refreshed audit rows = %d, want exactly 1", got)
			}
			if got := svCount(t, e, `SELECT count(*) FROM refresh_tokens WHERE session_id = $1`, tok.SessionID); got != 2 {
				t.Errorf("refresh tokens in the family = %d, want R1 and one successor", got)
			}
			if _, err := s.Refresh(t.Context(), svRefreshIn(device, results[winner].RefreshToken)); !errors.Is(err, auth.ErrRefreshRejected) {
				t.Errorf("the winner's R2 after the family ended: err = %v, want ErrRefreshRejected", err)
			}
			if err := svVerify(t, s.BearerVerifier(), results[winner].AccessToken); err == nil {
				t.Errorf("the winner's access token must die with the session")
			}
		})
	}
}

func TestConcurrency_ParallelSignInFromOneInstallationLeavesOneLiveSession(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	device := uuid.New()
	const n = 6
	results := make([]auth.SessionTokens, n)
	errs := stRace(n, func(i int) error {
		res, err := s.SignIn(t.Context(), svInput(u, device))
		if err == nil && res.Session != nil {
			results[i] = *res.Session
		}
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("sign-in %d: %v (a correct sign-in must never fail on a device clash)", i, err)
		}
	}
	if got := svLiveSessions(t, e, u.ID); got != 1 {
		t.Errorf("live sessions = %d, want exactly 1 for one installation (D-11)", got)
	}
	if got := svAllSessions(t, e, u.ID); got != n {
		t.Errorf("sessions = %d, want %d", got, n)
	}
	if got := svCount(t, e, `SELECT count(*) FROM device_sessions WHERE user_id = $1 AND end_reason = 'replaced'`, u.ID); got != n-1 {
		t.Errorf("replaced sessions = %d, want %d", got, n-1)
	}
	seen := map[uuid.UUID]bool{}
	for _, r := range results {
		seen[r.SessionID] = true
	}
	if len(seen) != n {
		t.Errorf("distinct session ids = %d, want %d", len(seen), n)
	}
	if got := len(e.AuditOf(t, auth.ActionSignInSucceeded)); got != n {
		t.Errorf("sign_in.succeeded rows = %d, want %d", got, n)
	}
}

func TestConcurrency_ParallelSignInOfDifferentUsers(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	users := make([]authtest.User, 8)
	for i := range users {
		users[i] = e.UserWithStatus(t, auth.StatusActive)
	}
	errs := stRace(len(users), func(i int) error {
		_, err := s.SignIn(t.Context(), svInput(users[i], uuid.New()))
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Errorf("user %d: %v", i, err)
		}
	}
}

func TestConcurrency_ParallelRefreshOfDifferentSessionsOfOneUser(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	const n = 6
	devices := make([]uuid.UUID, n)
	toks := make([]auth.SessionTokens, n)
	for i := range n {
		devices[i] = uuid.New()
		toks[i] = svSignIn(t, s, u, devices[i])
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	errs := stRace(n, func(i int) error {
		_, err := s.Refresh(ctx, svRefreshIn(devices[i], toks[i].RefreshToken))
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Errorf("session %d: %v (independent sessions must not reject each other, nor deadlock)", i, err)
		}
	}
	if got := svLiveSessions(t, e, u.ID); got != n {
		t.Errorf("live sessions = %d, want %d", got, n)
	}
}

func TestConcurrency_RefreshRacingSignOutEverywhereEndsConsistently(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	dA, dB := uuid.New(), uuid.New()
	a := svSignIn(t, s, u, dA)
	b := svSignIn(t, s, u, dB)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	errs := stRace(3, func(i int) error {
		switch i {
		case 0:
			_, err := s.Refresh(ctx, svRefreshIn(dA, a.RefreshToken))
			return err
		case 1:
			_, err := s.Refresh(ctx, svRefreshIn(dB, b.RefreshToken))
			return err
		default:
			_, err := s.SignOutEverywhere(ctx, svPrincipal(e, u, a.SessionID))
			return err
		}
	})
	for i := range 2 {
		if errs[i] != nil && !errors.Is(errs[i], auth.ErrRefreshRejected) {
			t.Errorf("refresh %d: err = %v, want success or ErrRefreshRejected", i, errs[i])
		}
	}
	if errs[2] != nil {
		t.Errorf("SignOutEverywhere: %v", errs[2])
	}
	if got := svLiveSessions(t, e, u.ID); got != 0 {
		t.Errorf("live sessions = %d, want 0 (nothing may survive sign-out everywhere)", got)
	}
}
