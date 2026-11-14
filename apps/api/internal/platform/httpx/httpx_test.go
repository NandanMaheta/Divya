// Tests for the httpx additions of feature 002 (plan.md, "Tick handler": the tick route extends
// its response write deadline to 90 s while the global WriteTimeout stays 30 s).
//
// Contract assumed (go-module-api.md is silent; plan.md names the pieces):
//
//	func (r *statusRecorder) Unwrap() http.ResponseWriter
//	func ExtendWriteDeadline(ctx huma.Context, d time.Duration) error
//
// ExtendWriteDeadline sets the response write deadline to d from now through
// http.NewResponseController, so it returns the controller's error, which wraps
// http.ErrNotSupported when the underlying writer has no deadline support (for example an
// httptest.ResponseRecorder). The caller decides whether that matters.
//
// No sleeps: the real-server tests start the server with a WriteTimeout of 1 ns, so the response
// deadline has already passed when the handler runs. Without an extension the response cannot be
// written (the client sees a dropped connection); with the extension it arrives.
package httpx

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

type pingOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestStatusRecorder_Unwrap_ReturnsTheUnderlyingWriter(t *testing.T) {
	under := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: under, status: http.StatusOK}

	if got := rec.Unwrap(); got != http.ResponseWriter(under) {
		t.Fatalf("Unwrap() = %v, want the wrapped writer", got)
	}
}

func TestStatusRecorder_Unwrap_KeepsResponseControllerWorking(t *testing.T) {
	under := httptest.NewRecorder() // supports Flush, not deadlines
	rec := &statusRecorder{ResponseWriter: under, status: http.StatusOK}
	rc := http.NewResponseController(rec)

	if err := rc.Flush(); err != nil {
		t.Fatalf("Flush through the recorder: %v (Unwrap missing?)", err)
	}
	if !under.Flushed {
		t.Fatal("Flush did not reach the underlying writer")
	}
	// A writer without deadline support still reports that honestly through the wrapper.
	if err := rc.SetWriteDeadline(time.Now().Add(time.Minute)); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("SetWriteDeadline on a recorder: got %v, want http.ErrNotSupported", err)
	}
}

func TestStatusRecorder_StillRecordsTheStatus(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	rec.WriteHeader(http.StatusTeapot)
	if rec.status != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", rec.status, http.StatusTeapot)
	}
}

func TestNewServer_GlobalWriteTimeoutStays30Seconds(t *testing.T) {
	srv := NewServer(":0", http.NewServeMux(), discardLogger())
	if srv.WriteTimeout != 30*time.Second {
		t.Fatalf("WriteTimeout = %v, want 30s (only the tick route extends it)", srv.WriteTimeout)
	}
}

func TestExtendWriteDeadline_ReportsWritersWithoutDeadlineSupport(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := humago.NewContext(&huma.Operation{}, req, httptest.NewRecorder())

	err := ExtendWriteDeadline(ctx, 90*time.Second)
	if !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("got %v, want an error wrapping http.ErrNotSupported", err)
	}
}

// serve starts the real HTTP stack (NewServer's logging wrapper, which puts a statusRecorder in
// front of the writer, around the Huma mux) with a write timeout that has already expired.
// extend is called from Huma middleware for GET /ping; a nil extend leaves the deadline alone.
func serve(t *testing.T, extend func(ctx huma.Context) error) (url string, extendErr *atomic.Pointer[error]) {
	t.Helper()
	mux, api := NewAPI()
	extendErr = &atomic.Pointer[error]{}
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		if extend != nil {
			err := extend(ctx)
			extendErr.Store(&err)
		}
		next(ctx)
	})
	huma.Register(api, huma.Operation{OperationID: "ping", Method: http.MethodGet, Path: "/ping"},
		func(_ context.Context, _ *struct{}) (*pingOutput, error) {
			out := &pingOutput{}
			out.Body.Status = "ok"
			return out, nil
		})

	ts := httptest.NewUnstartedServer(NewServer(":0", mux, discardLogger()).Handler)
	ts.Config.WriteTimeout = time.Nanosecond
	ts.Start()
	t.Cleanup(ts.Close)
	return ts.URL, extendErr
}

func get(t *testing.T, url string) (*http.Response, error) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	t.Cleanup(client.CloseIdleConnections)
	return client.Get(url + "/ping")
}

// Control: proves the harness really has an expired response deadline.
func TestExtendWriteDeadline_ControlWithoutExtensionTheResponseIsDropped(t *testing.T) {
	url, _ := serve(t, nil)
	resp, err := get(t, url)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("got status %d; the harness deadline was not expired, so the extension tests prove nothing", resp.StatusCode)
	}
}

func TestExtendWriteDeadline_MovesTheResponseDeadlineOnARealServer(t *testing.T) {
	url, extendErr := serve(t, func(ctx huma.Context) error { return ExtendWriteDeadline(ctx, 90*time.Second) })

	resp, err := get(t, url)
	if err != nil {
		t.Fatalf("request failed although the deadline was extended: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) == "" {
		t.Fatal("empty body")
	}
	if p := extendErr.Load(); p == nil || *p != nil {
		t.Fatalf("ExtendWriteDeadline returned %v, want nil on a real server", p)
	}
}

// The duration is honoured: extending to 1 ns is as good as not extending.
func TestExtendWriteDeadline_UsesTheGivenDuration(t *testing.T) {
	url, extendErr := serve(t, func(ctx huma.Context) error { return ExtendWriteDeadline(ctx, time.Nanosecond) })

	resp, err := get(t, url)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("got status %d; a 1 ns extension should still leave the deadline expired", resp.StatusCode)
	}
	if p := extendErr.Load(); p == nil || *p != nil {
		t.Fatalf("ExtendWriteDeadline returned %v, want nil (the call itself is valid)", p)
	}
}
