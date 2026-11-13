package health

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

var fixedNow = func() time.Time { return time.Date(2026, 11, 13, 4, 30, 0, 0, time.UTC) }

func TestServiceReady(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status string
		db     string
	}{
		{name: "database up", status: "ok", db: "ok"},
		{name: "database down", err: errors.New("boom"), status: "degraded", db: "unreachable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewService(fakePinger{tt.err}, "test", fixedNow).Ready(context.Background())
			if got.Status != tt.status || got.Database != tt.db {
				t.Fatalf("Ready() = %+v, want status=%s database=%s", got, tt.status, tt.db)
			}
			if !got.Time.Equal(fixedNow()) {
				t.Errorf("Time = %v, want %v", got.Time, fixedNow())
			}
		})
	}
}

func TestHandlers(t *testing.T) {
	tests := []struct {
		name string
		path string
		err  error
		want int
	}{
		{name: "live", path: "/healthz", want: http.StatusOK},
		{name: "ready", path: "/readyz", want: http.StatusOK},
		{name: "not ready", path: "/readyz", err: errors.New("down"), want: http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, api := humatest.New(t)
			Register(api, NewService(fakePinger{tt.err}, "test", fixedNow))
			if resp := api.Get(tt.path); resp.Code != tt.want {
				t.Fatalf("GET %s = %d, want %d: %s", tt.path, resp.Code, tt.want, resp.Body.String())
			}
		})
	}
}
