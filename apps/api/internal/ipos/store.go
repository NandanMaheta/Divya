package ipos

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

// Store is the data access of the ipos table, one method per query of data-model section 6.
// Every method filters by org and runs on the transaction carried by the context.
type Store struct{}

// FactsUpdate carries the columns UpdateIPOFacts writes.
type FactsUpdate struct {
	Draft   Draft
	Version int32
	Now     time.Time
}

// StatusUpdate carries the columns SetIPOStatus writes.
type StatusUpdate struct {
	Status     Status
	Source     StatusSource
	Version    int32
	Now        time.Time
	OverrideAt *time.Time
}

// ErrBadCursor is returned for a cursor that does not decode.
var ErrBadCursor = errors.New("ipos: invalid cursor")

func (s *Store) q(ctx context.Context) (*dbgen.Queries, error) {
	tx, err := db.MustTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("ipos: %w", err)
	}
	return dbgen.New(tx), nil
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("ipos: query: %w", err)
}

func stale(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleVersion
	}
	return fmt.Errorf("ipos: query: %w", err)
}

// InsertIPO inserts a draft; created is false when (org, idemKey) already exists.
func (s *Store) InsertIPO(ctx context.Context, org uuid.UUID, d Draft, idemKey *string, now time.Time) (IPO, bool, error) {
	q, err := s.q(ctx)
	if err != nil {
		return IPO{}, false, err
	}
	row, err := q.InsertIPO(ctx, dbgen.InsertIPOParams{
		OrgID: org, Name: d.Name, Exchanges: exchangeStrings(d.Exchanges),
		NseSymbol: textOf(d.NSESymbol), IssueSizePaise: int8Of(d.IssueSizePaise),
		PriceBandFloorPaise: int8Of(d.PriceBandFloorPaise), PriceBandCapPaise: int8Of(d.PriceBandCapPaise),
		IssuePricePaise: int8Of(d.IssuePricePaise), LotSize: int4Of(d.LotSize),
		OpenDate: dateOf(d.Dates.Open), CloseDate: dateOf(d.Dates.Close), AllotmentDate: dateOf(d.Dates.Allotment),
		RefundDate: dateOf(d.Dates.Refund), DematCreditDate: dateOf(d.Dates.DematCredit), ListingDate: dateOf(d.Dates.Listing),
		Registrar: textOf(d.Registrar), LeadManagers: nonNilStrings(d.LeadManagers),
		OfferDocumentUrl: textOf(d.OfferDocumentURL), CreateIdempotencyKey: textOf(idemKey),
		Now: db.Timestamptz(now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return IPO{}, false, nil
	}
	if err != nil {
		return IPO{}, false, fmt.Errorf("ipos: insert: %w", err)
	}
	return toIPO(row), true, nil
}

// GetIPOByCreateKey finds the IPO a create key already produced.
func (s *Store) GetIPOByCreateKey(ctx context.Context, org uuid.UUID, key string) (IPO, error) {
	q, err := s.q(ctx)
	if err != nil {
		return IPO{}, err
	}
	row, err := q.GetIPOByCreateKey(ctx, dbgen.GetIPOByCreateKeyParams{OrgID: org, Key: db.Text(key)})
	if err != nil {
		return IPO{}, notFound(err)
	}
	return toIPO(row), nil
}

// GetIPO reads one IPO of the org.
func (s *Store) GetIPO(ctx context.Context, org, id uuid.UUID) (IPO, error) {
	q, err := s.q(ctx)
	if err != nil {
		return IPO{}, err
	}
	row, err := q.GetIPO(ctx, dbgen.GetIPOParams{OrgID: org, ID: id})
	if err != nil {
		return IPO{}, notFound(err)
	}
	return toIPO(row), nil
}

// GetIPOForUpdate reads and row-locks one IPO (every mutation and step handler).
func (s *Store) GetIPOForUpdate(ctx context.Context, org, id uuid.UUID) (IPO, error) {
	q, err := s.q(ctx)
	if err != nil {
		return IPO{}, err
	}
	row, err := q.GetIPOForUpdate(ctx, dbgen.GetIPOForUpdateParams{OrgID: org, ID: id})
	if err != nil {
		return IPO{}, notFound(err)
	}
	return toIPO(row), nil
}

// GetIPOForShare reads one IPO under a shared row lock (RequireEligible).
func (s *Store) GetIPOForShare(ctx context.Context, org, id uuid.UUID) (IPO, error) {
	q, err := s.q(ctx)
	if err != nil {
		return IPO{}, err
	}
	row, err := q.GetIPOForShare(ctx, dbgen.GetIPOForShareParams{OrgID: org, ID: id})
	if err != nil {
		return IPO{}, notFound(err)
	}
	return toIPO(row), nil
}

// GetPublishedIPO reads one published IPO; drafts look unknown.
func (s *Store) GetPublishedIPO(ctx context.Context, org, id uuid.UUID) (IPO, error) {
	q, err := s.q(ctx)
	if err != nil {
		return IPO{}, err
	}
	row, err := q.GetPublishedIPO(ctx, dbgen.GetPublishedIPOParams{OrgID: org, ID: id})
	if err != nil {
		return IPO{}, notFound(err)
	}
	return toIPO(row), nil
}

// UpdateIPOFacts replaces the facts when version matches.
func (s *Store) UpdateIPOFacts(ctx context.Context, org, id uuid.UUID, u FactsUpdate) (IPO, error) {
	q, err := s.q(ctx)
	if err != nil {
		return IPO{}, err
	}
	d := u.Draft
	row, err := q.UpdateIPOFacts(ctx, dbgen.UpdateIPOFactsParams{
		Name: d.Name, Exchanges: exchangeStrings(d.Exchanges),
		NseSymbol: textOf(d.NSESymbol), IssueSizePaise: int8Of(d.IssueSizePaise),
		PriceBandFloorPaise: int8Of(d.PriceBandFloorPaise), PriceBandCapPaise: int8Of(d.PriceBandCapPaise),
		IssuePricePaise: int8Of(d.IssuePricePaise), LotSize: int4Of(d.LotSize),
		OpenDate: dateOf(d.Dates.Open), CloseDate: dateOf(d.Dates.Close), AllotmentDate: dateOf(d.Dates.Allotment),
		RefundDate: dateOf(d.Dates.Refund), DematCreditDate: dateOf(d.Dates.DematCredit), ListingDate: dateOf(d.Dates.Listing),
		Registrar: textOf(d.Registrar), LeadManagers: nonNilStrings(d.LeadManagers),
		OfferDocumentUrl: textOf(d.OfferDocumentURL),
		Now:              db.Timestamptz(u.Now), OrgID: org, ID: id, Version: u.Version,
	})
	if err != nil {
		return IPO{}, stale(err)
	}
	return toIPO(row), nil
}

// SetIPOFlag sets or clears the flag and note when version matches.
func (s *Store) SetIPOFlag(ctx context.Context, org, id uuid.UUID, version int32, flag *ApplyFlag, note *string, now time.Time) (IPO, error) {
	q, err := s.q(ctx)
	if err != nil {
		return IPO{}, err
	}
	var f *string
	if flag != nil {
		v := string(*flag)
		f = &v
	}
	row, err := q.SetIPOFlag(ctx, dbgen.SetIPOFlagParams{
		ApplyFlag: textOf(f), FlagNote: textOf(note), Now: db.Timestamptz(now), OrgID: org, ID: id, Version: version,
	})
	if err != nil {
		return IPO{}, stale(err)
	}
	return toIPO(row), nil
}

// SetIPOPublished publishes or unpublishes when version matches.
func (s *Store) SetIPOPublished(ctx context.Context, org, id uuid.UUID, version int32, published bool, now time.Time) (IPO, error) {
	q, err := s.q(ctx)
	if err != nil {
		return IPO{}, err
	}
	row, err := q.SetIPOPublished(ctx, dbgen.SetIPOPublishedParams{
		Published: published, Now: db.Timestamptz(now), OrgID: org, ID: id, Version: version,
	})
	if err != nil {
		return IPO{}, stale(err)
	}
	return toIPO(row), nil
}

// SetIPOStatus moves the status when version matches.
func (s *Store) SetIPOStatus(ctx context.Context, org, id uuid.UUID, u StatusUpdate) (IPO, error) {
	q, err := s.q(ctx)
	if err != nil {
		return IPO{}, err
	}
	row, err := q.SetIPOStatus(ctx, dbgen.SetIPOStatusParams{
		Status: string(u.Status), StatusSource: string(u.Source), Now: db.Timestamptz(u.Now),
		OverrideAt: db.NullTimestamptz(u.OverrideAt), OrgID: org, ID: id, Version: u.Version,
	})
	if err != nil {
		return IPO{}, stale(err)
	}
	return toIPO(row), nil
}

// DeleteIPO deletes a never-published IPO of the given version; it returns the rows deleted.
func (s *Store) DeleteIPO(ctx context.Context, org, id uuid.UUID, version int32) (int64, error) {
	q, err := s.q(ctx)
	if err != nil {
		return 0, err
	}
	n, err := q.DeleteIPO(ctx, dbgen.DeleteIPOParams{OrgID: org, ID: id, Version: version})
	if err != nil {
		return 0, fmt.Errorf("ipos: delete: %w", err)
	}
	return n, nil
}

// ListIPOsAdmin returns up to Limit+1 rows in admin order after the cursor.
func (s *Store) ListIPOsAdmin(ctx context.Context, org uuid.UUID, f AdminFilter) ([]IPO, error) {
	q, err := s.q(ctx)
	if err != nil {
		return nil, err
	}
	cur, err := decodeCursor(f.Cursor)
	if err != nil {
		return nil, err
	}
	p := dbgen.ListIPOsAdminParams{
		OrgID: org, MonthStart: dateOf(f.MonthStart), MonthEnd: dateOf(f.MonthEnd),
		CursorSet: cur.set, CursorNull: cur.null, CursorID: cur.id, CursorOpen: cur.open,
		RowLimit: rowLimit(f.Limit),
	}
	if f.Status != nil {
		p.Status = db.Text(string(*f.Status))
	}
	if f.Published != nil {
		p.Published = pgtype.Bool{Bool: *f.Published, Valid: true}
	}
	p.Flag = textOf(f.Flag)
	rows, err := q.ListIPOsAdmin(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("ipos: list: %w", err)
	}
	return toIPOs(rows), nil
}

// ListPublishedIPOs returns up to Limit+1 published rows after the cursor.
func (s *Store) ListPublishedIPOs(ctx context.Context, org uuid.UUID, f ClientFilter) ([]IPO, error) {
	q, err := s.q(ctx)
	if err != nil {
		return nil, err
	}
	cur, err := decodeCursor(f.Cursor)
	if err != nil {
		return nil, err
	}
	p := dbgen.ListPublishedIPOsParams{
		OrgID: org, CursorSet: cur.set, CursorNull: cur.null, CursorID: cur.id, CursorOpen: cur.open,
		RowLimit: rowLimit(f.Limit),
	}
	if f.Status != nil {
		p.Status = db.Text(string(*f.Status))
	}
	rows, err := q.ListPublishedIPOs(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("ipos: list published: %w", err)
	}
	return toIPOs(rows), nil
}

// FindIPOAuditByIdempotencyKey finds the audit row an earlier call with the same key wrote.
func (s *Store) FindIPOAuditByIdempotencyKey(ctx context.Context, org, id uuid.UUID, action, key string) (uuid.UUID, bool, error) {
	q, err := s.q(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	got, err := q.FindIPOAuditByIdempotencyKey(ctx, dbgen.FindIPOAuditByIdempotencyKeyParams{
		OrgID: org, EntityID: uuid.NullUUID{UUID: id, Valid: true}, Action: action, Key: key,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("ipos: find audit: %w", err)
	}
	return got, true, nil
}

func rowLimit(limit int) int32 {
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	return int32(limit) + 1 //nolint:gosec // bounded above
}

// cursor is the decoded keyset position (open_date NULLS FIRST, id) DESC.
type cursor struct {
	set, null bool
	open      pgtype.Date
	id        uuid.UUID
}

type cursorJSON struct {
	Open *string   `json:"o"`
	ID   uuid.UUID `json:"i"`
}

// encodeCursor is the opaque cursor that resumes after row i (used by the service list calls).
func encodeCursor(i IPO) string {
	c := cursorJSON{ID: i.ID}
	if i.Dates.Open != nil {
		o := i.Dates.Open.String()
		c.Open = &o
	}
	b, _ := json.Marshal(c) //nolint:errchkjson // plain struct cannot fail
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursor, error) {
	if s == "" {
		return cursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, ErrBadCursor
	}
	var c cursorJSON
	if err := json.Unmarshal(raw, &c); err != nil || c.ID == uuid.Nil {
		return cursor{}, ErrBadCursor
	}
	out := cursor{set: true, id: c.ID, null: c.Open == nil}
	if c.Open != nil {
		d, err := istdate.ParseDate(*c.Open)
		if err != nil {
			return cursor{}, ErrBadCursor
		}
		out.open = dateOf(&d)
	}
	return out, nil
}

func textOf(p *string) pgtype.Text {
	if p == nil {
		return pgtype.Text{}
	}
	return db.Text(*p)
}

func int8Of(p *int64) pgtype.Int8 {
	if p == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *p, Valid: true}
}

func int4Of(p *int32) pgtype.Int4 {
	if p == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *p, Valid: true}
}

func dateOf(d *istdate.Date) pgtype.Date {
	if d == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC), Valid: true}
}

func datePtr(p pgtype.Date) *istdate.Date {
	if !p.Valid {
		return nil
	}
	return &istdate.Date{Year: p.Time.Year(), Month: p.Time.Month(), Day: p.Time.Day()}
}

func exchangeStrings(in []Exchange) []string {
	out := make([]string, len(in))
	for i, e := range in {
		out[i] = string(e)
	}
	return out
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func toIPOs(rows []dbgen.Ipo) []IPO {
	out := make([]IPO, len(rows))
	for i, r := range rows {
		out[i] = toIPO(r)
	}
	return out
}

func toIPO(r dbgen.Ipo) IPO {
	ex := make([]Exchange, len(r.Exchanges))
	for i, e := range r.Exchanges {
		ex[i] = Exchange(e)
	}
	var flag *ApplyFlag
	if r.ApplyFlag.Valid {
		f := ApplyFlag(r.ApplyFlag.String)
		flag = &f
	}
	var lot *int32
	if r.LotSize.Valid {
		v := r.LotSize.Int32
		lot = &v
	}
	return IPO{
		ID: r.ID, OrgID: r.OrgID, Name: r.Name, Exchanges: ex,
		NSESymbol: db.TextPtr(r.NseSymbol), IssueSizePaise: int8Ptr(r.IssueSizePaise),
		PriceBandFloorPaise: int8Ptr(r.PriceBandFloorPaise), PriceBandCapPaise: int8Ptr(r.PriceBandCapPaise),
		IssuePricePaise: int8Ptr(r.IssuePricePaise), LotSize: lot,
		Dates: Dates{
			Open: datePtr(r.OpenDate), Close: datePtr(r.CloseDate), Allotment: datePtr(r.AllotmentDate),
			Refund: datePtr(r.RefundDate), DematCredit: datePtr(r.DematCreditDate), Listing: datePtr(r.ListingDate),
		},
		Registrar: db.TextPtr(r.Registrar), LeadManagers: r.LeadManagers, OfferDocumentURL: db.TextPtr(r.OfferDocumentUrl),
		Flag: flag, FlagNote: db.TextPtr(r.FlagNote), Published: r.Published,
		FirstPublishedAt: db.TimePtr(r.FirstPublishedAt), Status: Status(r.Status), StatusSource: StatusSource(r.StatusSource),
		StatusChangedAt: db.Time(r.StatusChangedAt), LastOverrideAt: db.TimePtr(r.LastOverrideAt),
		Source: r.Source, SourceRef: db.TextPtr(r.SourceRef), LastSyncedAt: db.TimePtr(r.LastSyncedAt),
		Version: r.Version, CreatedAt: db.Time(r.CreatedAt), UpdatedAt: db.Time(r.UpdatedAt),
	}
}

func int8Ptr(p pgtype.Int8) *int64 {
	if !p.Valid {
		return nil
	}
	v := p.Int64
	return &v
}
