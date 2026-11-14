# Contract: Go APIs that other modules use

These are the only entry points owning features (008 IPOs, 011 deals, 012 tasks, 013 reminders, 014 delivery, 018 settlement, 019 payments, 021 engagement) may call. No module reads or writes `scheduled_jobs`, `outbox_messages`, `quiet_hours` or `tick_runs` directly (constitution IV). Signatures are binding for the implementation; names of unexported helpers are not.

Common rules:

- "Requires a transaction in ctx" means the call must happen inside `db.TxRunner.InTx(ctx, fn)`; otherwise it returns `db.ErrNoTx` and writes nothing (FR-008, FR-023, US1.10, US3.1).
- Ids are `uuid.UUID` (`github.com/google/uuid`, see plan "Dependencies requiring owner approval"; fallback: `pgtype.UUID` confined to `store.go`).
- No pgx or HTTP types appear in these signatures.

## `internal/platform/clock`

```go
// Clock is the single source of "now" (constitution III). Production: clock.Real.
type Func = func() time.Time

func Real() time.Time                    // time.Now().UTC(); only wired in cmd/swastik

type Fake struct{ /* mutex-guarded */ }
func NewFake(t time.Time) *Fake
func (f *Fake) Now() time.Time           // pass f.Now wherever a clock.Func is expected
func (f *Fake) Set(t time.Time)
func (f *Fake) Advance(d time.Duration)
```

## `internal/platform/istdate` (BR-SET-08, GS-04, FR-033)

```go
var IST *time.Location                   // Asia/Kolkata via embedded time/tzdata; init panics if it cannot load

type Date struct{ Year int; Month time.Month; Day int }
func DateOf(t time.Time) Date            // IST business date of an instant, independent of time.Local
func (d Date) At(h, m int) time.Time     // instant of d at h:m IST (returned in UTC)
func (d Date) Weekday() time.Weekday
func (d Date) AddDays(n int) Date
func (d Date) String() string            // "2026-11-13"
func ParseDate(s string) (Date, error)

// AddWeekdays returns the date n weekdays after d, skipping only Saturday and Sunday.
// n == 0 returns d unchanged. n < 0 returns an error (programmer error, not a business case).
func AddWeekdays(d Date, n int) (Date, error)

// PaymentDue = AddWeekdays(listing, 2) at 23:59:00 IST (BR-SET-08, D1, D2).
func PaymentDue(listing Date) time.Time

type WallClock struct{ Hour, Minute int }          // IST wall-clock time, 00:00..23:59
type QuietHours struct{ Start, End WallClock }      // [Start, End) in IST, wraps midnight if Start > End
var DefaultQuietHours = QuietHours{Start: WallClock{21, 30}, End: WallClock{8, 0}}
func (q QuietHours) Contains(t time.Time) bool
func (q QuietHours) Defer(t time.Time) time.Time   // t if outside the window, else the next End instant
func NextDailyAt(now time.Time, at WallClock) time.Time // first instant > now that is `at` IST (for recurring jobs)
```

## `internal/platform/db` (additions)

```go
var ErrNoTx = errors.New("db: operation requires a transaction in context")

type TxRunner struct{ /* *pgxpool.Pool */ }
func NewTxRunner(pool *pgxpool.Pool) TxRunner
// InTx runs fn in one transaction stored in ctx. A nested InTx joins the outer transaction.
// fn's error or a panic rolls back.
func (r TxRunner) InTx(ctx context.Context, fn func(ctx context.Context) error) error
// Conn returns the transaction in ctx, or the pool when there is none (read-only callers).
func (r TxRunner) Conn(ctx context.Context) dbgen.DBTX
// MustTx returns the transaction in ctx or ErrNoTx.
func MustTx(ctx context.Context) (dbgen.DBTX, error)
```

## `internal/platform/audit`

```go
type Actor struct{ ID uuid.UUID; Kind string } // Kind: "admin" | "client" | "system"
type Entry struct{ OrgID uuid.UUID; Actor Actor; Entity string; EntityID uuid.UUID; Action string; Before, After any }
func Write(ctx context.Context, e Entry) error  // requires a transaction in ctx
```

## `internal/scheduler`

```go
// Handler performs one job. ctx carries the job's transaction: every DB write made through
// db.MustTx(ctx) commits atomically with the job's "done" mark and is rolled back if the
// lease was lost. Handlers MUST be idempotent and MUST read current business data
// (payload holds ids only) (FR-012). Returning an error schedules a retry (FR-017).
type Handler func(ctx context.Context, job Job) error

type KindOptions struct {
    MaxAttempts int                                // 0 = default 8
    Recurring   func(now time.Time) time.Time      // non-nil = recurring kind; returns next run_at (FR-015)
}

type JobSpec struct {
    OrgID     uuid.UUID
    Kind      string          // must be registered, else ErrUnknownKind
    Key       string          // dedupe key, stable across reschedules, e.g. "ipo:<id>:open"
    Subject   string          // business item, e.g. "ipo:<id>"
    RunAt     time.Time       // required
    ExpiresAt *time.Time      // nil = state-change work that always runs, however late (FR-014)
    Payload   any             // JSON-encoded; ids only; ≤ 8 KB
}

type Job struct {
    ID uuid.UUID; OrgID uuid.UUID; Kind, Key, Subject string
    Payload json.RawMessage; RunAt time.Time; ExpiresAt *time.Time; Attempt int
}

func (s *Service) Register(kind string, h Handler, opts KindOptions)          // at wiring time; panics on duplicate kind
func (s *Service) Schedule(ctx context.Context, spec JobSpec) error           // requires tx; upsert (FR-008, FR-009)
func (s *Service) Reschedule(ctx context.Context, org uuid.UUID, kind, key string, runAt time.Time) (bool, error) // requires tx; never creates (US5.7)
func (s *Service) Cancel(ctx context.Context, org uuid.UUID, kind, key string) (int, error)        // requires tx
func (s *Service) CancelSubject(ctx context.Context, org uuid.UUID, subject string) (int, error)   // requires tx (FR-013)

// Tick is called only by the tick handler (and tests).
func (s *Service) Tick(ctx context.Context) (TickSummary, error)
func (s *Service) Status(ctx context.Context, org uuid.UUID) (Status, error)  // FR-021, dead counts for FR-019

// Admin capabilities (service level now; HTTP in Phase B after 003).
func (s *Service) ListFailedJobs(ctx context.Context, org uuid.UUID, f FailedFilter) (Page[FailedJob], error)
func (s *Service) RetryJob(ctx context.Context, actor audit.Actor, org, id uuid.UUID, idemKey string) (FailedJob, error)  // dead only, else ErrConflict
func (s *Service) CancelJob(ctx context.Context, actor audit.Actor, org, id uuid.UUID, idemKey string) (FailedJob, error) // dead only

var ErrUnknownKind, ErrNotFound, ErrConflict, ErrInvalid error
```

Built-in kind registered by 002: `scheduler.retention` (recurring, daily 03:00 IST per org; purges per data-model §7). The tick also ensures one pending row of every recurring kind per org (`dedupe_key = "recurring"`), so recurring work self-heals after a dead row.

Kind and key naming: `<module>.<action>` kinds; keys `<entity>:<id>:<step>`; reminder keys include the slot (`task:<id>:slot:2026-11-14T10:00`) so catch-up only runs still-valid slots.

## `internal/outbox`

```go
type Recipient struct{ Kind string; ID *uuid.UUID } // "client" (ID required) | "admin" (ID nil = all admins)

type Message struct {
    OrgID                uuid.UUID
    Topic                string      // "<audience>.<event>", e.g. "task.reminder", "admin.jobs_dead"
    Recipient            Recipient
    Subject              string      // business item, e.g. "task:<id>"
    DedupeKey            string
    Payload              any         // content reference; never PAN/tokens/secrets
    ExemptFromQuietHours bool        // zero value = respects quiet hours (FR-031)
    ExpiresAt            *time.Time
    MaxAttempts          int         // 0 = topic default
}

// Envelope is what a Sender receives.
type Envelope struct {
    ID uuid.UUID; OrgID uuid.UUID; Topic string; Recipient Recipient
    Subject, DedupeKey string; Payload json.RawMessage; Attempt int; CreatedAt time.Time
}

// Sender delivers one message. It must treat DedupeKey as an idempotency/collapse key (FR-026).
type Sender interface{ Send(ctx context.Context, e Envelope) error }

func (s *Service) RegisterSender(topic string, snd Sender, maxAttempts int)       // wiring time; 014 registers real senders
func (s *Service) Add(ctx context.Context, m Message) (inserted bool, err error)   // requires tx; duplicate key → (false, nil) (FR-024)
func (s *Service) Withdraw(ctx context.Context, org uuid.UUID, topic, key string) (int, error)  // requires tx
func (s *Service) WithdrawSubject(ctx context.Context, org uuid.UUID, subject string) (int, error) // requires tx (FR-027)
func (s *Service) QuietHours(ctx context.Context, org uuid.UUID) (istdate.QuietHours, error)
func (s *Service) SetQuietHours(ctx context.Context, actor audit.Actor, org uuid.UUID, q istdate.QuietHours) error // audit_log

// Called by scheduler.Tick only.
func (s *Service) Relay(ctx context.Context, budget RelayBudget) (RelaySummary, error)
func (s *Service) SweepDead(ctx context.Context) ([]DeadGroup, error) // unalerted dead messages per org (topics "admin.*" excluded)

// Admin (service level now; HTTP in Phase B).
func (s *Service) ListFailedMessages(ctx context.Context, org uuid.UUID, f FailedFilter) (Page[FailedMessage], error)
func (s *Service) RetryMessage(ctx context.Context, actor audit.Actor, org, id uuid.UUID, idemKey string) (FailedMessage, error)
func (s *Service) CancelMessage(ctx context.Context, actor audit.Actor, org, id uuid.UUID, idemKey string) (FailedMessage, error)
```

Test double (non-test package so other modules' tests can use it): `internal/outbox/outboxtest.Sender` records envelopes, can fail on demand (`FailNext(n)`, `FailAlways()`), and counts deliveries per dedupe key (FR-029).

## `internal/platform/tickauth`

```go
var ErrUnauthenticated = errors.New("tickauth: unauthenticated") // → 401
var ErrForbidden       = errors.New("tickauth: forbidden")       // → 403

type Authenticator interface{ Authenticate(ctx context.Context, authorizationHeader string) error }

// NewOIDC verifies a Google-signed ID token: RS256 signature against Google's JWKS, issuer
// (https://accounts.google.com or accounts.google.com), audience == audience, not expired (clock
// injected), then email == serviceAccount and email_verified == true (else ErrForbidden).
func NewOIDC(ctx context.Context, audience, serviceAccount string, now func() time.Time, opts ...OIDCOption) Authenticator
func WithJWKSURL(url string) OIDCOption          // tests only (httptest server with a local RSA key)
func WithHTTPClient(c *http.Client) OIDCOption

// NewDev accepts "Bearer <token>" with a constant-time compare. Empty token → rejects everything.
// Constructed only when config allows it (APP_ENV local|ci).
func NewDev(token string) Authenticator

// Disabled rejects everything with ErrUnauthenticated (OIDC mode without audience/service account).
func Disabled() Authenticator
```
