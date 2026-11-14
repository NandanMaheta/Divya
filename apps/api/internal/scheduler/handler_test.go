package scheduler_test

// T055 - the hidden POST /internal/tick operation (US2.1-2.4, 2.7; FR-002, FR-004, FR-007;
// contracts/internal-tick.openapi.yaml). No database: the tick itself is a counting fake.
//
// Assumptions (plan "Wiring" and "Tick handler"):
//   - scheduler.RegisterTick(api huma.API, t scheduler.Ticker, auth tickauth.Authenticator), where
//     Ticker is satisfied by *scheduler.Service (Tick(ctx) (TickSummary, error)).
//   - The API is built by httpx.NewAPI (humago), as in cmd/swastik; the handler extends the write
//     deadline through httpx.ExtendWriteDeadline and does not fail the tick when the writer cannot
//     extend it (the humatest recorder cannot).
//   - The handler logs through slog's default logger (or not at all); whatever it logs must never
//     contain the credential.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/httpx"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/tickauth"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
)

const devToken = "dev-tick-token-9f8e7d6c5b4a"

// fakeTicker counts calls and returns a fixed result.
type fakeTicker struct {
	mu    sync.Mutex
	calls int
	sum   scheduler.TickSummary
	err   error
}

func (f *fakeTicker) Tick(context.Context) (scheduler.TickSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.sum, f.err
}

func (f *fakeTicker) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var _ scheduler.Ticker = (*scheduler.Service)(nil)

// authFunc adapts a function to tickauth.Authenticator.
type authFunc func(ctx context.Context, header string) error

func (f authFunc) Authenticate(ctx context.Context, header string) error { return f(ctx, header) }

var sampleSummary = func() scheduler.TickSummary {
	s := scheduler.TickSummary{
		TickID:          uuid.MustParse("0197a000-0000-7000-8000-0000000000a1"),
		StartedAt:       time.Date(2026, 11, 14, 4, 30, 0, 0, time.UTC),
		FinishedAt:      time.Date(2026, 11, 14, 4, 30, 2, 0, time.UTC),
		BudgetExhausted: true,
	}
	s.Jobs.Run, s.Jobs.Failed, s.Jobs.Dead, s.Jobs.Expired = 3, 1, 0, 2
	s.Messages.Sent, s.Messages.Failed, s.Messages.Dead, s.Messages.Expired = 4, 0, 1, 0
	return s
}()

// newTickAPI registers the tick on a fresh httpx API and wraps it for humatest.
func newTickAPI(t *testing.T, tk scheduler.Ticker, auth tickauth.Authenticator) humatest.TestAPI {
	t.Helper()
	_, api := httpx.NewAPI()
	scheduler.RegisterTick(api, tk, auth)
	return humatest.Wrap(t, api)
}

// captureLogs routes slog's default logger to a buffer for the test.
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func wantProblem(t *testing.T, resp *httptest.ResponseRecorder, status int) {
	t.Helper()
	if resp.Code != status {
		t.Fatalf("status = %d, want %d: %s", resp.Code, status, resp.Body.String())
	}
	if ct := resp.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("Content-Type = %q, want application/problem+json (RFC 9457)", ct)
	}
	var p map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &p); err != nil || p["status"] != float64(status) {
		t.Errorf("body %s is not a problem document with status %d", resp.Body.String(), status)
	}
}

func TestHandler_UnauthenticatedRequestsGet401AndDoNoWork(t *testing.T) {
	logs := captureLogs(t)
	tests := []struct {
		name   string
		auth   tickauth.Authenticator
		header []any
	}{
		{"no Authorization header", tickauth.NewDev(devToken), nil},
		{"empty header", tickauth.NewDev(devToken), []any{"Authorization: "}},
		{"not a bearer credential", tickauth.NewDev(devToken), []any{"Authorization: Basic " + devToken}},
		{"wrong token", tickauth.NewDev(devToken), []any{"Authorization: Bearer wrong-" + devToken}},
		{"tick auth not configured", tickauth.Disabled(), []any{"Authorization: Bearer " + devToken}},
		{"wrapped unauthenticated error", authFunc(func(context.Context, string) error {
			return fmt.Errorf("oidc: token expired: %w", tickauth.ErrUnauthenticated)
		}), []any{"Authorization: Bearer " + devToken}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tk := &fakeTicker{sum: sampleSummary}
			resp := newTickAPI(t, tk, tt.auth).Post("/internal/tick", tt.header...)
			wantProblem(t, resp, http.StatusUnauthorized)
			if got := resp.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Errorf("WWW-Authenticate = %q, want Bearer", got)
			}
			if tk.Calls() != 0 {
				t.Fatalf("the tick ran %d times for an unauthenticated request, want 0 (US2.1)", tk.Calls())
			}
			if strings.Contains(resp.Body.String(), devToken) || strings.Contains(resp.Body.String(), "expired") {
				t.Errorf("401 body %q reveals the credential or why it was rejected; it must be generic", resp.Body.String())
			}
		})
	}
	if strings.Contains(logs.String(), devToken) {
		t.Errorf("logs contain the credential:\n%s", logs.String())
	}
}

func TestHandler_AValidTokenForAnotherIdentityGets403AndDoesNoWork(t *testing.T) {
	tk := &fakeTicker{sum: sampleSummary}
	auth := authFunc(func(context.Context, string) error {
		return fmt.Errorf("email other@example.iam.gserviceaccount.com: %w", tickauth.ErrForbidden)
	})
	resp := newTickAPI(t, tk, auth).Post("/internal/tick", "Authorization: Bearer "+devToken)
	wantProblem(t, resp, http.StatusForbidden)
	if tk.Calls() != 0 {
		t.Fatalf("the tick ran %d times for a forbidden caller, want 0 (US2.3)", tk.Calls())
	}
	if strings.Contains(resp.Body.String(), "other@example") {
		t.Errorf("403 body %q echoes the caller identity", resp.Body.String())
	}
}

func TestHandler_AuthenticationHappensBeforeAnyWorkAndSeesTheHeader(t *testing.T) {
	var got []string
	tk := &fakeTicker{sum: sampleSummary}
	auth := authFunc(func(_ context.Context, header string) error {
		if tk.Calls() != 0 {
			t.Error("the tick ran before the caller was authenticated")
		}
		got = append(got, header)
		return nil
	})
	resp := newTickAPI(t, tk, auth).Post("/internal/tick", "Authorization: Bearer "+devToken)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
	}
	if !slices.Equal(got, []string{"Bearer " + devToken}) || tk.Calls() != 1 {
		t.Fatalf("authenticator saw %q, tick calls %d; want the raw header once and one tick", got, tk.Calls())
	}
}

func TestHandler_AuthenticatedTickReturns200WithTheTickSummary(t *testing.T) {
	logs := captureLogs(t)
	tk := &fakeTicker{sum: sampleSummary}
	resp := newTickAPI(t, tk, tickauth.NewDev(devToken)).Post("/internal/tick", "Authorization: Bearer "+devToken)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body.String())
	}
	if tk.Calls() != 1 {
		t.Fatalf("tick calls = %d, want 1", tk.Calls())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %s: %v", resp.Body.String(), err)
	}
	if _, hasSchema := body["$schema"]; hasSchema {
		delete(body, "$schema") // Huma may link the schema; it is not part of the contract
	}
	wantKeys := []string{"budget_exhausted", "finished_at", "jobs", "messages", "started_at", "tick_id"}
	var keys []string
	for k := range body {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, wantKeys) {
		t.Fatalf("TickSummary keys = %v, want exactly %v (additionalProperties: false)", keys, wantKeys)
	}
	if body["tick_id"] != sampleSummary.TickID.String() || body["started_at"] != "2026-11-14T04:30:00Z" ||
		body["finished_at"] != "2026-11-14T04:30:02Z" || body["budget_exhausted"] != true {
		t.Errorf("body = %v", body)
	}
	jobs, _ := body["jobs"].(map[string]any)
	msgs, _ := body["messages"].(map[string]any)
	wantJobs := map[string]any{"run": float64(3), "failed": float64(1), "dead": float64(0), "expired": float64(2)}
	wantMsgs := map[string]any{"sent": float64(4), "failed": float64(0), "dead": float64(1), "expired": float64(0)}
	for k, v := range wantJobs {
		if jobs[k] != v {
			t.Errorf("jobs.%s = %v, want %v (jobs %v)", k, jobs[k], v, jobs)
		}
	}
	for k, v := range wantMsgs {
		if msgs[k] != v {
			t.Errorf("messages.%s = %v, want %v (messages %v)", k, msgs[k], v, msgs)
		}
	}
	if len(jobs) != 4 || len(msgs) != 4 {
		t.Errorf("jobs %v / messages %v carry extra properties", jobs, msgs)
	}
	if strings.Contains(logs.String(), devToken) {
		t.Errorf("logs contain the credential:\n%s", logs.String())
	}
}

func TestHandler_ADatabaseFailureIs503WithoutInternalDetails(t *testing.T) {
	tk := &fakeTicker{err: errors.New("tick: insert tick run: dial tcp db.internal:5432: password authentication failed for user swastik")}
	resp := newTickAPI(t, tk, tickauth.NewDev(devToken)).Post("/internal/tick", "Authorization: Bearer "+devToken)
	wantProblem(t, resp, http.StatusServiceUnavailable)
	for _, leak := range []string{"password", "db.internal", "5432", "swastik"} {
		if strings.Contains(resp.Body.String(), leak) {
			t.Errorf("503 body %q leaks %q", resp.Body.String(), leak)
		}
	}
}

func TestHandler_TheOperationIsHiddenFromTheOpenAPIDocument(t *testing.T) {
	_, api := httpx.NewAPI()
	scheduler.RegisterTick(api, &fakeTicker{}, tickauth.Disabled())
	doc := api.OpenAPI()
	for path := range doc.Paths {
		if strings.HasPrefix(path, "/internal") {
			t.Errorf("OpenAPI paths contain %s; the tick must be Hidden (FR-004, US2.6)", path)
		}
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "scheduler-tick") || strings.Contains(string(raw), "TickSummary") {
		t.Error("the OpenAPI document mentions the tick operation or its schema")
	}
}

func TestHandler_OnlyPOSTRunsTheTick(t *testing.T) {
	tk := &fakeTicker{sum: sampleSummary}
	resp := newTickAPI(t, tk, tickauth.NewDev(devToken)).Get("/internal/tick", "Authorization: Bearer "+devToken)
	if resp.Code == http.StatusOK || tk.Calls() != 0 {
		t.Fatalf("GET /internal/tick = %d with %d ticks, want it rejected without work", resp.Code, tk.Calls())
	}
}

// Through a real HTTP server the write deadline can be extended; the tick still answers 200.
func TestHandler_WorksBehindARealServerWithTheWriteDeadlineExtended(t *testing.T) {
	mux, api := httpx.NewAPI()
	tk := &fakeTicker{sum: sampleSummary}
	scheduler.RegisterTick(api, tk, tickauth.NewDev(devToken))
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.WriteTimeout = 30 * time.Second
	srv.Start()
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/internal/tick", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+devToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || tk.Calls() != 1 {
		t.Fatalf("status = %d, ticks = %d; want 200 and one tick", resp.StatusCode, tk.Calls())
	}
}
