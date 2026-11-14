// Shared helpers of the S08 Store integration tests (specs/003-auth-core/data-model.md, plan P-03,
// contracts in store.go). Everything runs against a real PostgreSQL 18 through authtest.New.
package auth_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
)

// stInTx runs fn in one transaction and fails the test when it returns an error.
func stInTx(t *testing.T, e *authtest.Env, fn func(ctx context.Context) error) {
	t.Helper()
	if err := e.Tx.InTx(t.Context(), fn); err != nil {
		t.Fatalf("transaction: %v", err)
	}
}

// stRace runs fn(i) in n goroutines released together and returns their errors by index.
func stRace(n int, fn func(i int) error) []error {
	start := make(chan struct{})
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = fn(i)
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

// stNewUser is a valid CreateUser input; override fields on the copy.
func stNewUser(e *authtest.Env, email, mobile string) auth.NewUser {
	return auth.NewUser{
		OrgID: e.Org, Role: "client", Status: auth.StatusPendingVerification, Name: "Asha Mehta",
		Email: email, Mobile: mobile, PasswordHash: validPHC, Now: authtest.T0,
	}
}

func stCreate(t *testing.T, e *authtest.Env, in auth.NewUser) auth.UserRow {
	t.Helper()
	var u auth.UserRow
	stInTx(t, e, func(ctx context.Context) (err error) {
		u, err = e.Store.CreateUser(ctx, in)
		return err
	})
	return u
}

// stNewSession creates a session through the Store for an existing user.
func stNewSession(t *testing.T, e *authtest.Env, user uuid.UUID, device uuid.UUID) auth.SessionRow {
	t.Helper()
	var s auth.SessionRow
	stInTx(t, e, func(ctx context.Context) (err error) {
		s, err = e.Store.CreateSession(ctx, auth.NewSession{
			OrgID: e.Org, UserID: user, DeviceID: device, DeviceName: "Pixel 9", Platform: "android", ClientType: "mobile",
			Now: e.Clock.Now(), IdleExpiresAt: e.Clock.Now().Add(30 * 24 * time.Hour), AbsoluteExpiresAt: e.Clock.Now().Add(90 * 24 * time.Hour),
		})
		return err
	})
	return s
}

func stHash(b byte) []byte {
	d := make([]byte, 32)
	for i := range d {
		d[i] = b
	}
	return d
}

// stBlocked starts fn in a goroutine and reports whether it is still running after wait. The
// returned func waits for fn to finish and returns its error.
func stBlocked(wait time.Duration, fn func() error) (stillRunning bool, finish func() error) {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return false, func() error { return err }
	case <-time.After(wait):
		return true, func() error { return <-done }
	}
}
