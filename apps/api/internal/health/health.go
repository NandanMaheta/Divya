// Package health is the golden reference module: every Divya module follows this shape.
//
//	service.go  - business logic behind small interfaces (no HTTP, no SQL types leak out)
//	handler.go  - Huma operations: typed input/output, registered via Register
//	*_test.go   - table tests for the service, humatest tests for the handlers
package health

import (
	"context"
	"time"
)

// Pinger is the only dependency this module needs; the pgx pool satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Service reports liveness and readiness.
type Service struct {
	db      Pinger
	version string
	now     func() time.Time
}

// NewService builds the health service. now is injectable so tests control time.
func NewService(db Pinger, version string, now func() time.Time) *Service {
	return &Service{db: db, version: version, now: now}
}

// Status is the readiness result.
type Status struct {
	Status   string    `json:"status" enum:"ok,degraded" doc:"Overall status"`
	Database string    `json:"database" enum:"ok,unreachable" doc:"Database connectivity"`
	Version  string    `json:"version" doc:"Build version"`
	Time     time.Time `json:"time" doc:"Server time (UTC)"`
}

// Ready checks dependencies with a short timeout.
func (s *Service) Ready(ctx context.Context) Status {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	st := Status{Status: "ok", Database: "ok", Version: s.version, Time: s.now().UTC()}
	if err := s.db.Ping(ctx); err != nil {
		st.Status, st.Database = "degraded", "unreachable"
	}
	return st
}
