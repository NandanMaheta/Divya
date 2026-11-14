package ipos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

// Shared helpers for the service tests (slices S08, S09, S12, S13). They use the real Store over
// dbtest.New, the real scheduler (schedulertest.Harness: fake clock, real Tick) and raw SQL to
// read state back, so no assertion depends on the Service read methods unless that is the subject.
//
// Assumptions the contracts leave open (the implementation must satisfy them):
//   - NewService(Deps{...}) alone is enough: the service builds its own Store.
//   - A blank override reason, and a flag note without a flag, give *ValidationError with Code
//     ipo.invalid_value (error-codes.md); publish-breaking edits give ipo.publish_incomplete.
//   - SetFlag(flag, note) replaces both values (nil clears).
//   - Get, GetPublished, List and ListPublished work without a caller transaction (the service
//     opens its own); Delete of an already deleted IPO with a known idempotency key replays.
//   - Audit `before`/`after` hold the changed fields only, with the key names of data-model
//     section 5 (status, status_source, step, scheduled_for, reason, apply_flag, flag_note,
//     eligible, published, idempotency_key) and ISO dates (2026-11-12).

// The four lifecycle step kinds as scheduled in the scheduler (data-model section 3).
var stepKinds = []string{jobOpen, jobClose, jobAllotment, jobList}

type svcOpts struct {
	jobs bool // register the real step handlers (ipos.RegisterJobs); otherwise no-op handlers
	spy  bool // wrap the scheduler in a call recorder
}

type spySched struct {
	inner     Scheduler
	mu        sync.Mutex
	schedules []scheduler.JobSpec
	cancels   []string
}

func (s *spySched) Schedule(ctx context.Context, spec scheduler.JobSpec) error {
	s.mu.Lock()
	s.schedules = append(s.schedules, spec)
	s.mu.Unlock()
	return s.inner.Schedule(ctx, spec)
}

func (s *spySched) Cancel(ctx context.Context, org uuid.UUID, kind, key string) (int, error) {
	s.mu.Lock()
	s.cancels = append(s.cancels, kind)
	s.mu.Unlock()
	return s.inner.Cancel(ctx, org, kind, key)
}

func (s *spySched) CancelSubject(ctx context.Context, org uuid.UUID, subject string) (int, error) {
	s.mu.Lock()
	s.cancels = append(s.cancels, "subject:"+subject)
	s.mu.Unlock()
	return s.inner.CancelSubject(ctx, org, subject)
}

func (s *spySched) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.schedules) + len(s.cancels)
}

type svcEnv struct {
	t     *testing.T
	h     *schedulertest.Harness
	svc   *Service
	org   uuid.UUID
	admin audit.Actor
	spy   *spySched
	keys  atomic.Int64
}

func newSvcEnv(t *testing.T) *svcEnv     { t.Helper(); return newSvcEnvWith(t, svcOpts{}) }
func newSvcEnvJobs(t *testing.T) *svcEnv { t.Helper(); return newSvcEnvWith(t, svcOpts{jobs: true}) }

func newSvcEnvWith(t *testing.T, o svcOpts) *svcEnv {
	t.Helper()
	h := schedulertest.NewHarness(t)
	e := &svcEnv{t: t, h: h, org: h.Org, admin: audit.Actor{ID: uuid.New(), Kind: audit.KindAdmin}}
	var sch Scheduler = h.Sched
	if o.spy {
		e.spy = &spySched{inner: h.Sched}
		sch = e.spy
	}
	e.svc = NewService(Deps{Tx: h.Txr, Scheduler: sch, Now: h.Clock.Now, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if o.jobs {
		guard(t, "RegisterJobs", func() { RegisterJobs(h.Sched, e.svc) })
	} else {
		for _, k := range stepKinds {
			h.Handle(k, func(context.Context, scheduler.Job) error { return nil })
		}
	}
	return e
}

// guard turns a skeleton panic into a test failure so one unimplemented function does not abort
// the whole test binary.
func guard(t testing.TB, what string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s: panicked: %v", what, r)
		}
	}()
	fn()
}

func (e *svcEnv) key() string { return fmt.Sprintf("idem-key-%d", e.keys.Add(1)) }

func (e *svcEnv) ctx() context.Context { return e.t.Context() }

func (e *svcEnv) at(day, hour, minute int) time.Time { return schedulertest.Nov(day, hour, minute) }

func (e *svcEnv) setClock(day, hour, minute int) { e.h.Clock.Set(e.at(day, hour, minute)) }

// tickAt moves the fake clock and runs one real scheduler tick.
func (e *svcEnv) tickAt(day, hour, minute int) {
	e.t.Helper()
	e.setClock(day, hour, minute)
	e.h.Tick()
}

// acme is the spec's example IPO (open Wed 11 Nov 2026 ... listing Wed 18 Nov); minimum
// application value 14 x 105,000 paise = Rs 14,700.
func acme() Draft {
	return Draft{
		Name:                "Acme Ltd",
		Exchanges:           []Exchange{ExchangeNSE, ExchangeBSE},
		NSESymbol:           ptrStr("ACME"),
		IssueSizePaise:      p64(50_000_000_000),
		PriceBandFloorPaise: p64(100_000),
		PriceBandCapPaise:   p64(105_000),
		LotSize:             p32(14),
		Dates:               Dates{Open: dt(11), Close: dt(13), Allotment: dt(16), Refund: dt(17), DematCredit: dt(17), Listing: dt(18)},
		Registrar:           ptrStr("Link Intime"),
		LeadManagers:        []string{"Axis Capital"},
	}
}

func minimalDraft(name string) Draft {
	return Draft{Name: name, Exchanges: []Exchange{ExchangeNSE}}
}

// draftOf copies the editable facts of an IPO (the PUT body that changes nothing).
func draftOf(i IPO) Draft {
	return Draft{
		Name: i.Name, Exchanges: i.Exchanges, NSESymbol: i.NSESymbol, IssueSizePaise: i.IssueSizePaise,
		PriceBandFloorPaise: i.PriceBandFloorPaise, PriceBandCapPaise: i.PriceBandCapPaise, IssuePricePaise: i.IssuePricePaise,
		LotSize: i.LotSize, Dates: i.Dates, Registrar: i.Registrar, LeadManagers: i.LeadManagers,
		OfferDocumentURL: i.OfferDocumentURL, FlagNote: i.FlagNote,
	}
}

func flagPtr(f ApplyFlag) *ApplyFlag { return &f }

func (e *svcEnv) create(d Draft) IPO {
	e.t.Helper()
	res, err := e.svc.Create(e.ctx(), e.admin, e.org, d, e.key())
	if err != nil {
		e.t.Fatalf("Create(%q): %v", d.Name, err)
	}
	return res.IPO
}

func (e *svcEnv) update(i IPO, d Draft) IPO {
	e.t.Helper()
	res, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, d, e.key())
	if err != nil {
		e.t.Fatalf("Update: %v", err)
	}
	return res.IPO
}

func (e *svcEnv) setFlag(i IPO, f ApplyFlag, note *string) IPO {
	e.t.Helper()
	res, err := e.svc.SetFlag(e.ctx(), e.admin, e.org, i.ID, i.Version, &f, note, e.key())
	if err != nil {
		e.t.Fatalf("SetFlag: %v", err)
	}
	return res.IPO
}

func (e *svcEnv) publish(i IPO) IPO {
	e.t.Helper()
	res, err := e.svc.Publish(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key())
	if err != nil {
		e.t.Fatalf("Publish: %v", err)
	}
	return res.IPO
}

func (e *svcEnv) override(i IPO, to Status, reason string) IPO {
	e.t.Helper()
	res, err := e.svc.OverrideStatus(e.ctx(), e.admin, e.org, i.ID, i.Version, to, reason, e.key())
	if err != nil {
		e.t.Fatalf("OverrideStatus(%s): %v", to, err)
	}
	return res.IPO
}

// published creates, flags and publishes a complete IPO (acme facts unless d says otherwise).
func (e *svcEnv) published(d Draft, flag ApplyFlag) IPO {
	e.t.Helper()
	return e.publish(e.setFlag(e.create(d), flag, nil))
}

// current reads the row back through raw SQL.

func (e *svcEnv) currentID(id uuid.UUID) IPO {
	e.t.Helper()
	got, err := e.svc.Get(e.ctx(), e.org, id)
	if err != nil {
		e.t.Fatalf("Get(%s): %v", id, err)
	}
	return got
}

// rowState is a raw view of the ipos row; independent of the Service read methods.
type rowState struct {
	Name, Status, StatusSource string
	Version                    int32
	Published                  bool
	Flag, Note                 *string
	FirstPublished             *time.Time
	LastOverride               *time.Time
	Open, Close, Allotment     *string
	Refund, Demat, Listing     *string
}

func (e *svcEnv) row(id uuid.UUID) (rowState, bool) {
	e.t.Helper()
	var r rowState
	err := e.h.Pool.QueryRow(e.ctx(), `SELECT name, status, status_source, version, published, apply_flag, flag_note,
		first_published_at, last_override_at, open_date::text, close_date::text, allotment_date::text,
		refund_date::text, demat_credit_date::text, listing_date::text FROM ipos WHERE id = $1`, id).
		Scan(&r.Name, &r.Status, &r.StatusSource, &r.Version, &r.Published, &r.Flag, &r.Note,
			&r.FirstPublished, &r.LastOverride, &r.Open, &r.Close, &r.Allotment, &r.Refund, &r.Demat, &r.Listing)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return rowState{}, false
		}
		e.t.Fatalf("read row: %v", err)
	}
	return r, true
}

func (e *svcEnv) mustRow(id uuid.UUID) rowState {
	e.t.Helper()
	r, ok := e.row(id)
	if !ok {
		e.t.Fatalf("ipo %s does not exist", id)
	}
	return r
}

func (e *svcEnv) iposCount() int { return e.h.Count("ipos", "true") }

type auditRow struct {
	Action, ActorKind   string
	ActorID             *uuid.UUID
	BeforeRaw, AfterRaw []byte
	Before, After       map[string]any
}

func (e *svcEnv) audits(id uuid.UUID) []auditRow {
	e.t.Helper()
	rows, err := e.h.Pool.Query(e.ctx(), `SELECT action, actor_kind, actor_id, before, after FROM audit_log
		WHERE entity = 'ipo' AND entity_id = $1 ORDER BY id`, id)
	if err != nil {
		e.t.Fatalf("read audit: %v", err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var a auditRow
		if err := rows.Scan(&a.Action, &a.ActorKind, &a.ActorID, &a.BeforeRaw, &a.AfterRaw); err != nil {
			e.t.Fatalf("scan audit: %v", err)
		}
		if a.BeforeRaw != nil {
			_ = json.Unmarshal(a.BeforeRaw, &a.Before)
		}
		if a.AfterRaw != nil {
			_ = json.Unmarshal(a.AfterRaw, &a.After)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatalf("audit rows: %v", err)
	}
	return out
}

func (e *svcEnv) actions(id uuid.UUID) []string {
	var out []string
	for _, a := range e.audits(id) {
		out = append(out, a.Action)
	}
	return out
}

func (e *svcEnv) auditCount(id uuid.UUID) int { return len(e.audits(id)) }

func (e *svcEnv) statusAudits(id uuid.UUID) []auditRow {
	var out []auditRow
	for _, a := range e.audits(id) {
		if a.Action == "ipo.status_changed" {
			out = append(out, a)
		}
	}
	return out
}

func (e *svcEnv) wantActions(id uuid.UUID, want ...string) {
	e.t.Helper()
	got := e.actions(id)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		e.t.Fatalf("audit actions = %v, want %v", got, want)
	}
}

// stepJobs returns the pending lifecycle jobs of an IPO: kind -> run_at.
func (e *svcEnv) stepJobs(id uuid.UUID) map[string]time.Time {
	e.t.Helper()
	rows, err := e.h.Pool.Query(e.ctx(), `SELECT kind, run_at FROM scheduled_jobs
		WHERE org_id = $1 AND subject = $2 AND status = 'pending'`, e.org, "ipo:"+id.String())
	if err != nil {
		e.t.Fatalf("read jobs: %v", err)
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var k string
		var at time.Time
		if err := rows.Scan(&k, &at); err != nil {
			e.t.Fatalf("scan job: %v", err)
		}
		out[k] = at
	}
	return out
}

func (e *svcEnv) jobRows(id uuid.UUID) int {
	return e.h.Count("scheduled_jobs", "org_id = $1 AND subject = $2", e.org, "ipo:"+id.String())
}

func stepKey(id uuid.UUID, kind string) string {
	suffix := map[string]string{jobOpen: "open", jobClose: "close", jobAllotment: "allotment", jobList: "list"}[kind]
	return "ipo:" + id.String() + ":" + suffix
}

func wantStatus(t *testing.T, e *svcEnv, id uuid.UUID, want Status) {
	t.Helper()
	if got := e.mustRow(id).Status; got != string(want) {
		t.Fatalf("status = %s, want %s", got, want)
	}
}

func wantValidation2(t testing.TB, err error, code string) *ValidationError {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want *ValidationError(%s)", err, code)
	}
	if ve.Code != code {
		t.Fatalf("validation code = %s, want %s (issues %v)", ve.Code, code, ve.Issues)
	}
	return ve
}

// recorder records signals, optionally failing or panicking, and reads the row back inside the
// emitting transaction (listeners may call Get, contract rule 3).
type recorder struct {
	mu                         sync.Mutex
	svc                        *Service
	name                       string
	log                        *[]string // shared order log across recorders
	Dates                      []DatesChanged
	Elig                       []EligibilityChanged
	Status                     []StatusChanged
	EligRows, StatusRows       []IPO
	errDates, errElig, errStat error
	panicElig                  bool
}

func (e *svcEnv) listen() *recorder {
	e.t.Helper()
	r := &recorder{svc: e.svc, name: "r"}
	e.attach(r)
	return r
}

func (e *svcEnv) attach(r *recorder) {
	e.t.Helper()
	guard(e.t, "OnDatesChanged", func() { e.svc.OnDatesChanged(r) })
	guard(e.t, "OnEligibilityChanged", func() { e.svc.OnEligibilityChanged(r) })
	guard(e.t, "OnStatusChanged", func() { e.svc.OnStatusChanged(r) })
}

func (r *recorder) note(kind string) {
	if r.log != nil {
		*r.log = append(*r.log, r.name+":"+kind)
	}
}

func (r *recorder) failStatus(err error) {
	r.mu.Lock()
	r.errStat = err
	r.mu.Unlock()
}

func (r *recorder) OnIPODatesChanged(_ context.Context, ev DatesChanged) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Dates = append(r.Dates, ev)
	r.note("dates")
	return r.errDates
}

func (r *recorder) OnIPOEligibilityChanged(ctx context.Context, ev EligibilityChanged) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Elig = append(r.Elig, ev)
	r.note("eligibility")
	if got, err := r.svc.Get(ctx, ev.OrgID, ev.IPOID); err == nil {
		r.EligRows = append(r.EligRows, got)
	}
	if r.panicElig {
		panic("listener boom")
	}
	return r.errElig
}

func (r *recorder) OnIPOStatusChanged(ctx context.Context, ev StatusChanged) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Status = append(r.Status, ev)
	r.note("status")
	if got, err := r.svc.Get(ctx, ev.OrgID, ev.IPOID); err == nil {
		r.StatusRows = append(r.StatusRows, got)
	}
	return r.errStat
}

func (r *recorder) counts() (dates, elig, status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.Dates), len(r.Elig), len(r.Status)
}

func (r *recorder) wantDatesOnly(t *testing.T, dates int) {
	t.Helper()
	d, el, s := r.counts()
	if d != dates || el != 0 || s != 0 {
		t.Fatalf("signals (dates, eligibility, status) = (%d, %d, %d), want (%d, 0, 0)", d, el, s, dates)
	}
}

func (r *recorder) wantNoSignals(t *testing.T) { t.Helper(); r.wantDatesOnly(t, 0) }

// captureRegistrar records what RegisterJobs registers so tests can run a step handler directly.
type captureRegistrar struct {
	handlers map[string]scheduler.Handler
	opts     map[string]scheduler.KindOptions
}

func newCapture() *captureRegistrar {
	return &captureRegistrar{handlers: map[string]scheduler.Handler{}, opts: map[string]scheduler.KindOptions{}}
}

func (c *captureRegistrar) Register(kind string, h scheduler.Handler, opts scheduler.KindOptions) {
	c.handlers[kind] = h
	c.opts[kind] = opts
}

// captureHandlers registers the jobs of e.svc on a capturing registrar.
func (e *svcEnv) captureHandlers() *captureRegistrar {
	e.t.Helper()
	c := newCapture()
	guard(e.t, "RegisterJobs", func() { RegisterJobs(c, e.svc) })
	return c
}

// runStep runs the captured handler for kind against the stored (pending or done) job row of the
// IPO, in its own transaction, like a tick would.
func (e *svcEnv) runStep(c *captureRegistrar, id uuid.UUID, kind string) error {
	e.t.Helper()
	h := c.handlers[kind]
	if h == nil {
		e.t.Fatalf("no handler registered for %s", kind)
	}
	row := e.h.Job(kind, stepKey(id, kind))
	job := scheduler.Job{ID: row.ID, OrgID: row.Org, Kind: row.Kind, Key: row.Key, Subject: row.Subject, Payload: row.Payload, RunAt: row.RunAt}
	return e.h.Txr.InTx(e.ctx(), func(ctx context.Context) error { return h(ctx, job) })
}

func newID() uuid.UUID { return uuid.New() }
