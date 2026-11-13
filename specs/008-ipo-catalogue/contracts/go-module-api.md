# Contract: Go API of `internal/ipos` for other modules

Who uses it:

- 009 (GMP)
- 010/011 (deals)
- 012 (tasks)
- 013 (reminders)
- 018 (due dates)
- 019 (payments → SETTLED)
- 026 (import)
- `cmd/divya` (wiring)

No module reads or writes the `ipos` table directly (constitution IV). The exported names and signatures below are binding; unexported helpers are not. Ids are `uuid.UUID` and dates are `istdate.Date` (002). No pgx or HTTP types appear.

## Construction and wiring (`cmd/divya` only)

```go
type Scheduler interface { // satisfied by *scheduler.Service (002)
    Schedule(ctx context.Context, spec scheduler.JobSpec) error
    Cancel(ctx context.Context, org uuid.UUID, kind, key string) (int, error)
    CancelSubject(ctx context.Context, org uuid.UUID, subject string) (int, error)
}
type JobRegistrar interface { // satisfied by *scheduler.Service
    Register(kind string, h scheduler.Handler, opts scheduler.KindOptions)
}

type Deps struct {
    Tx        db.TxRunner     // 002
    Scheduler Scheduler
    Now       clock.Func      // 002; clock.Real in production, (*clock.Fake).Now in tests
    Log       *slog.Logger
}

func NewService(d Deps) *Service
// RegisterJobs registers ipo.open, ipo.close, ipo.allotment, ipo.list (KindOptions{} = 8 attempts, not recurring).
func RegisterJobs(r JobRegistrar, s *Service)
// Register adds the HTTP operations (admin + client); call after authz.Install (003).
func Register(api huma.API, s *Service)
```

Listener registration happens at wiring time, before serving. It is not safe to call concurrently with requests.

```go
func (s *Service) OnDatesChanged(l DatesChangedListener)
func (s *Service) OnEligibilityChanged(l EligibilityChangedListener)
func (s *Service) OnStatusChanged(l StatusChangedListener)
```

## Signals (ADR 0008)

```go
type DatesChangedListener interface {
    OnIPODatesChanged(ctx context.Context, e DatesChanged) error
}
type EligibilityChangedListener interface {
    OnIPOEligibilityChanged(ctx context.Context, e EligibilityChanged) error
}
type StatusChangedListener interface {
    OnIPOStatusChanged(ctx context.Context, e StatusChanged) error
}

type DatesChanged struct {
    OrgID, IPOID uuid.UUID
    Old, New     Dates        // all six dates, before and after (FR-042)
    Actor        audit.Actor
    At           time.Time
}
type EligibilityChanged struct {
    OrgID, IPOID uuid.UUID
    Eligible     bool         // new value (published && APPLY)
    Cause        string       // "publish" | "unpublish" | "flag"
    Actor        audit.Actor
    At           time.Time
}
type StatusChanged struct {
    OrgID, IPOID uuid.UUID
    From, To     Status       // may be non-adjacent (override, catch-up after a retried step); may go backwards (override)
    Source       StatusSource // schedule | admin | settlement
    Actor        audit.Actor  // system for schedule and settlement
    At           time.Time
    ScheduledFor *time.Time   // step instant, Source == schedule only
}
```

Rules for listeners:

1. They are called synchronously, in registration order, with the emitting transaction in `ctx`. Write through `db.MustTx(ctx)` or your own service's `InTx`, which joins the transaction. Schedule or cancel your own jobs with 002's tx-only calls.
2. An error or panic rolls back the whole change: the IPO row, its lifecycle steps, the audit entry and every other listener's writes. To reject with a user-facing reason, return `*ipos.RejectedError{Code, Detail}`; the admin then gets 409 with that code. Any other error gives 500 `internal`. A failing listener inside a step job makes 002 retry the job (backoff, then `dead` plus an `admin.jobs_dead` alert).
3. Never call an `ipos` mutation from a listener (no re-entrancy). Reads (`Get`) are allowed and see the new state.
4. When signals fire:
   - DatesChanged fires only when at least one of the six dates changed. It does not fire on create.
   - EligibilityChanged fires only when `Eligible()` flips.
   - StatusChanged fires on every real status change. A no-op step sends nothing.

## Reads and capabilities for dependents

```go
type Status string        // StatusUpcoming … StatusSettled; func (Status) Rank() int
type StatusSource string  // SourceSchedule, SourceAdmin, SourceSettlement
type ApplyFlag string     // FlagApply, FlagDoNotApply
type Exchange string      // ExchangeNSE, ExchangeBSE
type Dates struct{ Open, Close, Allotment, Refund, DematCredit, Listing *istdate.Date }

type IPO struct {
    ID, OrgID                    uuid.UUID
    Name                         string
    Exchanges                    []Exchange
    NSESymbol                    *string
    IssueSizePaise               *int64
    PriceBandFloorPaise, PriceBandCapPaise, IssuePricePaise *int64
    LotSize                      *int32
    Dates                        Dates
    Registrar                    *string
    LeadManagers                 []string
    OfferDocumentURL             *string
    Flag                         *ApplyFlag
    FlagNote                     *string
    Published                    bool
    FirstPublishedAt             *time.Time
    Status                       Status
    StatusSource                 StatusSource
    StatusChangedAt              time.Time
    Source                       string
    SourceRef                    *string
    LastSyncedAt                 *time.Time
    Version                      int32
    CreatedAt, UpdatedAt         time.Time
}
func (i IPO) Eligible() bool                       // Published && Flag == APPLY (FR-023)
func (i IPO) MinApplicationPaise() (int64, bool)   // lot × (issue price, else band cap); false when unknown (FR-004)
func (s Status) Rank() int

// Get returns any IPO of the org (draft or published). ErrNotFound.
func (s *Service) Get(ctx context.Context, org, id uuid.UUID) (IPO, error)
// RequireEligible locks the row FOR SHARE in the caller's transaction (db.ErrNoTx otherwise) and
// returns ErrNotEligible unless published && APPLY. 010/012 call it before creating deals/tasks so a
// concurrent unpublish/flag change waits for them (and then signals them).
func (s *Service) RequireEligible(ctx context.Context, org, id uuid.UUID) (IPO, error)
// MarkSettled (019): LISTED → SETTLED, actor system, source settlement, audit + StatusChanged.
// Already SETTLED → nil, no write. Any other status → *NotListedError{Current} (errors.Is ErrNotListed), nothing changes (FR-033, US6.7).
// Joins the caller's transaction if there is one.
func (s *Service) MarkSettled(ctx context.Context, org, id uuid.UUID) error
```

## Mutations (used by the HTTP handlers; 026 later adds `Upsert` on top of the same validation)

Every mutation takes `by audit.Actor`, `org uuid.UUID` and `idemKey string` (empty means no key). It runs in one transaction: row lock → idempotency check → validate → write → steps → audit → listeners.

```go
type Result struct { IPO IPO; Warnings []Warning; Replayed bool }

func (s *Service) Create(ctx, by, org, in Draft, idemKey) (Result, error)
func (s *Service) Update(ctx, by, org, id uuid.UUID, version int32, in Draft, idemKey) (Result, error)
func (s *Service) SetFlag(ctx, by, org, id, version, flag *ApplyFlag, note *string, idemKey) (Result, error)
func (s *Service) Publish(ctx, by, org, id, version, idemKey) (Result, error)
func (s *Service) Unpublish(ctx, by, org, id, version, idemKey) (Result, error)
func (s *Service) OverrideStatus(ctx, by, org, id, version, to Status, reason string, idemKey) (Result, error)
func (s *Service) Delete(ctx, by, org, id, version, idemKey) (replayed bool, err error)
func (s *Service) List(ctx, org, f AdminFilter) (Page[IPO], error)
func (s *Service) ListPublished(ctx, org, f ClientFilter) (Page[IPO], error)
func (s *Service) GetPublished(ctx, org, id) (IPO, error)       // ErrNotFound for drafts and unknown ids alike
func SuggestDates(close istdate.Date) DateSuggestion              // pure; no I/O (FR-043)
```

## Errors

```go
var (
    ErrNotFound              = errors.New("ipos: not found")
    ErrStaleVersion          = errors.New("ipos: stale version")
    ErrNoChange              = errors.New("ipos: no change")
    ErrPublishedNotDeletable = errors.New("ipos: published IPOs can only be unpublished")
    ErrNotListed             = errors.New("ipos: not listed yet")
    ErrNotEligible           = errors.New("ipos: not eligible")
)
type FieldIssue struct{ Field, Message string }
type ValidationError struct { Code string; Issues []FieldIssue } // Code ∈ ipo.sme_not_allowed, ipo.invalid_segment, ipo.invalid_value, ipo.date_order, ipo.publish_incomplete
type NotListedError struct { Current Status }                    // Is(ErrNotListed)
type RejectedError struct { Code, Detail string }                // returned by listeners
```

HTTP mapping: [error-codes.md](./error-codes.md).
