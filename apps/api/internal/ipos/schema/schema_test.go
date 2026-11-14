// Package schema_test proves the CHECK constraints, partial unique indexes and the delete trigger
// of migrations/00004_ipos.sql with raw SQL, bypassing the service (defence in depth:
// BR-IPO-01 FR-011, BR-IPO-02 FR-020, BR-IPO-03 FR-040, FR-008). It is RED until S03 adds the migration.
package schema_test

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/dbtest"
)

var now = time.Date(2026, 11, 14, 4, 30, 0, 0, time.UTC)

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	org  uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := dbtest.New(t)
	var org uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO orgs (name) VALUES ('Test') RETURNING id`).Scan(&org); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	return &env{t: t, pool: pool, org: org}
}

// base is the smallest valid draft row; overrides replace or add columns (nil value = NULL).
func (e *env) base() map[string]any {
	return map[string]any{
		"org_id":            e.org,
		"name":              "Acme Industries",
		"exchanges":         []string{"NSE"},
		"status_changed_at": now,
	}
}

func with(m map[string]any, kv ...any) map[string]any {
	out := make(map[string]any, len(m)+len(kv)/2)
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

// publishable is a valid published row (FR-020 satisfied).
func (e *env) publishable(kv ...any) map[string]any {
	return with(e.base(),
		append([]any{
			"apply_flag", "APPLY", "open_date", "2026-11-18", "close_date", "2026-11-20",
			"lot_size", 100, "price_band_floor_paise", 14000, "price_band_cap_paise", 14700,
			"published", true, "first_published_at", now,
		}, kv...)...)
}

func (e *env) insert(cols map[string]any) (uuid.UUID, error) {
	e.t.Helper()
	names := make([]string, 0, len(cols))
	for k := range cols {
		names = append(names, k)
	}
	sort.Strings(names)
	holders := make([]string, len(names))
	args := make([]any, len(names))
	for i, n := range names {
		cast := ""
		switch {
		case strings.HasSuffix(n, "_date"):
			cast = "::date"
		case n == "exchanges" || n == "lead_managers":
			cast = "::text[]"
		case strings.HasSuffix(n, "_at"):
			cast = "::timestamptz"
		}
		holders[i] = fmt.Sprintf("$%d%s", i+1, cast)
		args[i] = cols[n]
	}
	var id uuid.UUID
	err := e.pool.QueryRow(e.t.Context(),
		fmt.Sprintf(`INSERT INTO ipos (%s) VALUES (%s) RETURNING id`, strings.Join(names, ", "), strings.Join(holders, ", ")),
		args...).Scan(&id)
	return id, err
}

func (e *env) mustInsert(cols map[string]any) uuid.UUID {
	e.t.Helper()
	id, err := e.insert(cols)
	if err != nil {
		e.t.Fatalf("insert should succeed: %v", err)
	}
	return id
}

// wantCode asserts a PostgreSQL error with the SQLSTATE; anything else (including a missing
// table, 42P01) is a failure for the wrong reason.
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		t.Fatalf("want PostgreSQL error %s, got %v", code, err)
	}
	if pe.Code != code {
		t.Fatalf("want SQLSTATE %s, got %s (%s)", code, pe.Code, pe.Message)
	}
}

const (
	checkViolation  = "23514"
	uniqueViolation = "23505"
	raiseException  = "P0001"
)

type tc struct {
	name string
	cols func(e *env) map[string]any
	ok   bool
}

func run(t *testing.T, cases []tc) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			_, err := e.insert(c.cols(e))
			if c.ok {
				if err != nil {
					t.Fatalf("want accepted, got %v", err)
				}
				return
			}
			wantCode(t, err, checkViolation)
		})
	}
}

func TestSchema_Baseline_DraftRowDefaults(t *testing.T) {
	e := newEnv(t)
	id := e.mustInsert(e.base())
	var board, status, source string
	var published bool
	var version int32
	var lead []string
	if err := e.pool.QueryRow(t.Context(),
		`SELECT board, status, status_source, source, published, version, lead_managers FROM ipos WHERE id = $1`, id).
		Scan(&board, &status, new(string), &source, &published, &version, &lead); err != nil {
		t.Fatal(err)
	}
	if board != "MAINBOARD" || status != "UPCOMING" || source != "manual" || published || version != 1 || len(lead) != 0 {
		t.Fatalf("defaults wrong: board=%s status=%s source=%s published=%v version=%d lead=%v", board, status, source, published, version, lead)
	}
}

func TestSchema_BR_IPO_01_BoardAndExchanges(t *testing.T) {
	run(t, []tc{
		{"mainboard NSE", func(e *env) map[string]any { return e.base() }, true},
		{"mainboard BSE", func(e *env) map[string]any { return with(e.base(), "exchanges", []string{"BSE"}) }, true},
		{"both stored sorted", func(e *env) map[string]any { return with(e.base(), "exchanges", []string{"BSE", "NSE"}) }, true},
		{"board SME", func(e *env) map[string]any { return with(e.base(), "board", "SME") }, false},
		{"board lowercase", func(e *env) map[string]any { return with(e.base(), "board", "mainboard") }, false},
		{"NSE Emerge token", func(e *env) map[string]any { return with(e.base(), "exchanges", []string{"NSE_EMERGE"}) }, false},
		{"BSE SME token", func(e *env) map[string]any { return with(e.base(), "exchanges", []string{"BSE_SME"}) }, false},
		{"mainboard mixed with SME token", func(e *env) map[string]any { return with(e.base(), "exchanges", []string{"NSE", "BSE_SME"}) }, false},
		{"duplicate exchange", func(e *env) map[string]any { return with(e.base(), "exchanges", []string{"NSE", "NSE"}) }, false},
		{"three exchanges", func(e *env) map[string]any { return with(e.base(), "exchanges", []string{"NSE", "BSE", "NSE"}) }, false},
		{"empty exchanges", func(e *env) map[string]any { return with(e.base(), "exchanges", []string{}) }, false},
	})
}

func TestSchema_BR_IPO_02_PublishRequiresFlagDatesLotAndPrice(t *testing.T) {
	run(t, []tc{
		{"complete with band", func(e *env) map[string]any { return e.publishable() }, true},
		{"complete with final price only", func(e *env) map[string]any {
			return e.publishable("price_band_floor_paise", nil, "price_band_cap_paise", nil, "issue_price_paise", 14700)
		}, true},
		{"draft without any of them", func(e *env) map[string]any { return e.base() }, true},
		{"published without flag", func(e *env) map[string]any { return e.publishable("apply_flag", nil) }, false},
		{"published without open date", func(e *env) map[string]any { return e.publishable("open_date", nil) }, false},
		{"published without close date", func(e *env) map[string]any { return e.publishable("close_date", nil) }, false},
		{"published without lot size", func(e *env) map[string]any { return e.publishable("lot_size", nil) }, false},
		{"published without any price", func(e *env) map[string]any {
			return e.publishable("price_band_floor_paise", nil, "price_band_cap_paise", nil)
		}, false},
		{"published without first_published_at", func(e *env) map[string]any { return e.publishable("first_published_at", nil) }, false},
		{"flag DO_NOT_APPLY published", func(e *env) map[string]any { return e.publishable("apply_flag", "DO_NOT_APPLY") }, true},
		{"unknown flag", func(e *env) map[string]any { return e.publishable("apply_flag", "MAYBE") }, false},
	})
}

func TestSchema_BR_IPO_02_FlagNote(t *testing.T) {
	run(t, []tc{
		{"flag and note", func(e *env) map[string]any {
			return with(e.base(), "apply_flag", "APPLY", "flag_note", "Strong GMP")
		}, true},
		{"note of 500 characters", func(e *env) map[string]any {
			return with(e.base(), "apply_flag", "APPLY", "flag_note", strings.Repeat("n", 500))
		}, true},
		{"note of 501 characters", func(e *env) map[string]any {
			return with(e.base(), "apply_flag", "APPLY", "flag_note", strings.Repeat("n", 501))
		}, false},
		{"empty note", func(e *env) map[string]any { return with(e.base(), "apply_flag", "APPLY", "flag_note", "") }, false},
		{"note without flag", func(e *env) map[string]any { return with(e.base(), "flag_note", "orphan") }, false},
	})
}

func TestSchema_BR_IPO_03_DateOrder(t *testing.T) {
	d := func(kv ...any) func(e *env) map[string]any {
		return func(e *env) map[string]any { return with(e.base(), kv...) }
	}
	run(t, []tc{
		{"full valid order", d("open_date", "2026-11-18", "close_date", "2026-11-20", "allotment_date", "2026-11-23",
			"refund_date", "2026-11-24", "demat_credit_date", "2026-11-24", "listing_date", "2026-11-25"), true},
		{"open equals close", d("open_date", "2026-11-18", "close_date", "2026-11-18"), true},
		{"open after close", d("open_date", "2026-11-19", "close_date", "2026-11-18"), false},
		{"allotment equals close", d("close_date", "2026-11-20", "allotment_date", "2026-11-20"), false},
		{"allotment before close", d("close_date", "2026-11-20", "allotment_date", "2026-11-19"), false},
		{"refund before allotment", d("allotment_date", "2026-11-23", "refund_date", "2026-11-22"), false},
		{"refund equals allotment", d("allotment_date", "2026-11-23", "refund_date", "2026-11-23"), true},
		{"refund after listing", d("refund_date", "2026-11-26", "listing_date", "2026-11-25"), false},
		{"demat before allotment", d("allotment_date", "2026-11-23", "demat_credit_date", "2026-11-22"), false},
		{"demat after listing", d("demat_credit_date", "2026-11-26", "listing_date", "2026-11-25"), false},
		{"demat equals listing", d("demat_credit_date", "2026-11-25", "listing_date", "2026-11-25"), true},
		{"listing equals allotment", d("allotment_date", "2026-11-23", "listing_date", "2026-11-23"), false},
		{"listing before allotment", d("allotment_date", "2026-11-23", "listing_date", "2026-11-20"), false},
		{"only listing set", d("listing_date", "2026-11-25"), true},
		{"weekend dates are allowed", d("open_date", "2026-11-14", "close_date", "2026-11-15"), true},
	})
}

func TestSchema_FR003_PriceBandAndValues(t *testing.T) {
	d := func(kv ...any) func(e *env) map[string]any {
		return func(e *env) map[string]any { return with(e.base(), kv...) }
	}
	run(t, []tc{
		{"band floor below cap", d("price_band_floor_paise", 14000, "price_band_cap_paise", 14700), true},
		{"fixed price band floor equals cap", d("price_band_floor_paise", 14700, "price_band_cap_paise", 14700), true},
		{"band floor above cap", d("price_band_floor_paise", 14800, "price_band_cap_paise", 14700), false},
		{"only floor", d("price_band_floor_paise", 14000), false},
		{"only cap", d("price_band_cap_paise", 14700), false},
		{"floor zero", d("price_band_floor_paise", 0, "price_band_cap_paise", 14700), false},
		{"cap above one crore rupees", d("price_band_floor_paise", 14000, "price_band_cap_paise", 1000000001), false},
		{"cap at one crore rupees", d("price_band_floor_paise", 14000, "price_band_cap_paise", 1000000000), true},
		{"issue price outside band is allowed", d("price_band_floor_paise", 14000, "price_band_cap_paise", 14700, "issue_price_paise", 20000), true},
		{"issue price zero", d("issue_price_paise", 0), false},
		{"issue price negative", d("issue_price_paise", -1), false},
		{"issue price too large", d("issue_price_paise", 1000000001), false},
		{"issue size zero", d("issue_size_paise", 0), false},
		{"issue size at maximum", d("issue_size_paise", int64(1000000000000000)), true},
		{"issue size above maximum", d("issue_size_paise", int64(1000000000000001)), false},
		{"lot size zero", d("lot_size", 0), false},
		{"lot size at maximum", d("lot_size", 1000000), true},
		{"lot size above maximum", d("lot_size", 1000001), false},
		{"name blank after trim", d("name", "   "), false},
		{"name empty", d("name", ""), false},
		{"name of 200 characters", d("name", strings.Repeat("a", 200)), true},
		{"name of 201 characters", d("name", strings.Repeat("a", 201)), false},
		{"name containing SME is a normal name", d("name", "SME Solar Limited"), true},
		{"nse symbol valid", d("nse_symbol", "M&M-FIN1"), true},
		{"nse symbol lowercase", d("nse_symbol", "acme"), false},
		{"nse symbol too long", d("nse_symbol", strings.Repeat("A", 21)), false},
		{"registrar empty", d("registrar", ""), false},
		{"registrar set", d("registrar", "KFin Technologies"), true},
		{"twenty lead managers", d("lead_managers", make([]string, 20)), true},
		{"twenty-one lead managers", d("lead_managers", make([]string, 21)), false},
	})
}

func TestSchema_FR002_StatusAndSourceValues(t *testing.T) {
	d := func(kv ...any) func(e *env) map[string]any {
		return func(e *env) map[string]any { return with(e.base(), kv...) }
	}
	run(t, []tc{
		{"status SETTLED", d("status", "SETTLED"), true},
		{"status unknown", d("status", "WITHDRAWN"), false},
		{"status lowercase", d("status", "open"), false},
		{"status_source admin", d("status_source", "admin"), true},
		{"status_source settlement", d("status_source", "settlement"), true},
		{"status_source unknown", d("status_source", "robot"), false},
		{"version zero", d("version", 0), false},
	})
}

func TestSchema_SourceConsistency(t *testing.T) {
	d := func(kv ...any) func(e *env) map[string]any {
		return func(e *env) map[string]any { return with(e.base(), kv...) }
	}
	run(t, []tc{
		{"manual without ref", d(), true},
		{"manual with ref", d("source_ref", "abc"), false},
		{"import with ref", d("source", "import:chittorgarh", "source_ref", "ipo-123"), true},
		{"import without ref", d("source", "import:chittorgarh"), false},
		{"import with empty ref", d("source", "import:chittorgarh", "source_ref", ""), false},
		{"import with ref over 200", d("source", "import:chittorgarh", "source_ref", strings.Repeat("r", 201)), false},
		{"import source uppercase", d("source", "import:Chittorgarh", "source_ref", "x"), false},
		{"unknown source prefix", d("source", "scrape:foo", "source_ref", "x"), false},
		{"import source over 32 chars", d("source", "import:"+strings.Repeat("a", 33), "source_ref", "x"), false},
		{"import with last_synced_at", d("source", "import:chittorgarh", "source_ref", "x", "last_synced_at", now), true},
		{"manual with last_synced_at", d("last_synced_at", now), false},
		{"field_sources object", d("field_sources", `{"lot_size":"import:chittorgarh"}`), true},
		{"field_sources array", d("field_sources", `[]`), false},
	})
}

func TestSchema_OfferDocumentURL(t *testing.T) {
	d := func(kv ...any) func(e *env) map[string]any {
		return func(e *env) map[string]any { return with(e.base(), kv...) }
	}
	run(t, []tc{
		{"https url", d("offer_document_url", "https://example.com/rhp.pdf"), true},
		{"http url", d("offer_document_url", "http://example.com/rhp.pdf"), false},
		{"javascript url", d("offer_document_url", "javascript:alert(1)"), false},
		{"https url of 2000 characters", d("offer_document_url", "https://"+strings.Repeat("a", 1992)), true},
		{"https url of 2001 characters", d("offer_document_url", "https://"+strings.Repeat("a", 1993)), false},
	})
}

func TestSchema_FR007_CreateIdempotencyKeyIsUniquePerOrg(t *testing.T) {
	e := newEnv(t)
	e.mustInsert(with(e.base(), "create_idempotency_key", "key-12345678"))
	_, err := e.insert(with(e.base(), "create_idempotency_key", "key-12345678"))
	wantCode(t, err, uniqueViolation)

	// Another org may reuse the key; NULL keys never collide.
	var other uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO orgs (name) VALUES ('Other') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	e.mustInsert(with(e.base(), "org_id", other, "create_idempotency_key", "key-12345678"))
	e.mustInsert(e.base())
	e.mustInsert(e.base())
}

func TestSchema_FR007_CreateIdempotencyKeyLength(t *testing.T) {
	run(t, []tc{
		{"8 characters", func(e *env) map[string]any { return with(e.base(), "create_idempotency_key", "12345678") }, true},
		{"7 characters", func(e *env) map[string]any { return with(e.base(), "create_idempotency_key", "1234567") }, false},
		{"200 characters", func(e *env) map[string]any {
			return with(e.base(), "create_idempotency_key", strings.Repeat("k", 200))
		}, true},
		{"201 characters", func(e *env) map[string]any {
			return with(e.base(), "create_idempotency_key", strings.Repeat("k", 201))
		}, false},
	})
}

func TestSchema_SourceRefIsUniquePerOrgAndSource(t *testing.T) {
	e := newEnv(t)
	imp := with(e.base(), "source", "import:chittorgarh", "source_ref", "ipo-123")
	e.mustInsert(imp)
	_, err := e.insert(imp)
	wantCode(t, err, uniqueViolation)
	e.mustInsert(with(imp, "source", "import:other"))
}

func TestSchema_FR008_DeleteTrigger(t *testing.T) {
	t.Run("never-published draft is deletable", func(t *testing.T) {
		e := newEnv(t)
		id := e.mustInsert(e.base())
		if _, err := e.pool.Exec(t.Context(), `DELETE FROM ipos WHERE id = $1`, id); err != nil {
			t.Fatalf("delete draft: %v", err)
		}
	})
	t.Run("published row is not deletable", func(t *testing.T) {
		e := newEnv(t)
		id := e.mustInsert(e.publishable())
		_, err := e.pool.Exec(t.Context(), `DELETE FROM ipos WHERE id = $1`, id)
		wantCode(t, err, raiseException)
		if !strings.Contains(err.Error(), "published IPOs can only be unpublished") {
			t.Fatalf("unexpected message: %v", err)
		}
	})
	t.Run("ever-published but now unpublished row is not deletable", func(t *testing.T) {
		e := newEnv(t)
		id := e.mustInsert(e.publishable())
		if _, err := e.pool.Exec(t.Context(), `UPDATE ipos SET published = false WHERE id = $1`, id); err != nil {
			t.Fatalf("unpublish: %v", err)
		}
		_, err := e.pool.Exec(t.Context(), `DELETE FROM ipos WHERE id = $1`, id)
		wantCode(t, err, raiseException)
		var n int
		if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM ipos WHERE id = $1`, id).Scan(&n); err != nil || n != 1 {
			t.Fatalf("row must survive: n=%d err=%v", n, err)
		}
	})
}

func TestSchema_UpdateStillEnforcesChecks(t *testing.T) {
	e := newEnv(t)
	id := e.mustInsert(e.publishable())
	for name, sql := range map[string]string{
		"clear flag while published":   `UPDATE ipos SET apply_flag = NULL WHERE id = $1`,
		"clear lot size while pub":     `UPDATE ipos SET lot_size = NULL WHERE id = $1`,
		"move SME board":               `UPDATE ipos SET board = 'SME' WHERE id = $1`,
		"close before open":            `UPDATE ipos SET close_date = '2026-11-17' WHERE id = $1`,
		"exchange to emerge":           `UPDATE ipos SET exchanges = '{NSE_EMERGE}' WHERE id = $1`,
		"publish flag cleared by note": `UPDATE ipos SET apply_flag = NULL, flag_note = 'x', published = false WHERE id = $1`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.pool.Exec(t.Context(), sql, id)
			wantCode(t, err, checkViolation)
		})
	}
}
