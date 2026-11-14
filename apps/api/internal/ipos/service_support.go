package ipos

import (
	"context"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
)

// Audit actions (data-model section 5).
const (
	actCreated     = "ipo.created"
	actUpdated     = "ipo.updated"
	actFlagSet     = "ipo.flag_set"
	actPublished   = "ipo.published"
	actUnpublished = "ipo.unpublished"
	actStatus      = "ipo.status_changed"
	actDeleted     = "ipo.deleted"
)

var systemActor = audit.Actor{Kind: audit.KindSystem}

// fields is the changed-field map stored in audit before/after.
type fields map[string]any

func (s *Service) writeAudit(ctx context.Context, by audit.Actor, org, id uuid.UUID, action string, before, after fields) error {
	e := audit.Entry{OrgID: org, Actor: by, Entity: "ipo", EntityID: id, Action: action}
	if before != nil {
		e.Before = before
	}
	if after != nil {
		e.After = after
	}
	return audit.Write(ctx, e)
}

// withKey adds the idempotency key (the replay lookup of research R-04) when one was sent.
func withKey(f fields, key string) fields {
	if key != "" && f != nil {
		f["idempotency_key"] = key
	}
	return f
}

// replayed reports whether an earlier call with this key already wrote this action for the IPO.
func (s *Service) replayed(ctx context.Context, org, id uuid.UUID, action, key string) (bool, error) {
	if key == "" {
		return false, nil
	}
	_, ok, err := s.store.FindIPOAuditByIdempotencyKey(ctx, org, id, action, key)
	return ok, err
}

func optKey(key string) *string {
	if key == "" {
		return nil
	}
	return &key
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func dateStr(d *istdate.Date) any {
	if d == nil {
		return nil
	}
	return d.String()
}

// facts is the editable content of a draft in audit form (ISO dates, plain values).
func facts(d Draft) fields {
	ex := make([]string, len(d.Exchanges))
	for i, e := range d.Exchanges {
		ex[i] = string(e)
	}
	return fields{
		"name": d.Name, "exchanges": ex, "nse_symbol": deref(d.NSESymbol),
		"issue_size_paise": deref(d.IssueSizePaise), "price_band_floor_paise": deref(d.PriceBandFloorPaise),
		"price_band_cap_paise": deref(d.PriceBandCapPaise), "issue_price_paise": deref(d.IssuePricePaise),
		"lot_size":  deref(d.LotSize),
		"open_date": dateStr(d.Dates.Open), "close_date": dateStr(d.Dates.Close),
		"allotment_date": dateStr(d.Dates.Allotment), "refund_date": dateStr(d.Dates.Refund),
		"demat_credit_date": dateStr(d.Dates.DematCredit), "listing_date": dateStr(d.Dates.Listing),
		"registrar": deref(d.Registrar), "lead_managers": nonNilStrings(d.LeadManagers),
		"offer_document_url": deref(d.OfferDocumentURL),
	}
}

func factsOfIPO(i IPO) Draft {
	return Draft{
		Name: i.Name, Exchanges: i.Exchanges, NSESymbol: i.NSESymbol, IssueSizePaise: i.IssueSizePaise,
		PriceBandFloorPaise: i.PriceBandFloorPaise, PriceBandCapPaise: i.PriceBandCapPaise,
		IssuePricePaise: i.IssuePricePaise, LotSize: i.LotSize, Dates: i.Dates, Registrar: i.Registrar,
		LeadManagers: i.LeadManagers, OfferDocumentURL: i.OfferDocumentURL,
	}
}

// snapshot is the full audit picture of an IPO (created, deleted).
func snapshot(i IPO) fields {
	f := facts(factsOfIPO(i))
	f["apply_flag"], f["flag_note"] = deref(i.Flag), deref(i.FlagNote)
	f["published"], f["status"], f["status_source"] = i.Published, string(i.Status), string(i.StatusSource)
	return f
}

// factsDiff returns the changed facts only: old values and new values.
func factsDiff(old, updated Draft) (before, after fields) {
	o, n := facts(old), facts(updated)
	before, after = fields{}, fields{}
	for k, v := range n {
		if !reflect.DeepEqual(o[k], v) {
			before[k], after[k] = o[k], v
		}
	}
	return before, after
}

func applyDraft(cur IPO, d Draft) IPO {
	cur.Name, cur.Exchanges, cur.NSESymbol, cur.IssueSizePaise = d.Name, d.Exchanges, d.NSESymbol, d.IssueSizePaise
	cur.PriceBandFloorPaise, cur.PriceBandCapPaise, cur.IssuePricePaise = d.PriceBandFloorPaise, d.PriceBandCapPaise, d.IssuePricePaise
	cur.LotSize, cur.Dates, cur.Registrar = d.LotSize, d.Dates, d.Registrar
	cur.LeadManagers, cur.OfferDocumentURL = d.LeadManagers, d.OfferDocumentURL
	return cur
}

func datesEqual(a, b Dates) bool { return reflect.DeepEqual(a, b) }

func jobKey(id uuid.UUID, kind string) string {
	suffix := map[string]string{jobOpen: "open", jobClose: "close", jobAllotment: "allotment", jobList: "list"}[kind]
	return "ipo:" + id.String() + ":" + suffix
}

func jobSubject(id uuid.UUID) string { return "ipo:" + id.String() }

// syncSteps schedules the steps whose date was set or moved and cancels those whose date was
// cleared; unchanged dates touch nothing (FR-041). It runs in the caller's transaction.
func (s *Service) syncSteps(ctx context.Context, org, id uuid.UUID, old, updated Dates, now time.Time) error {
	schedule, cancel := stepDiff(old, updated)
	for _, st := range schedule {
		at, ok := stepInstant(st, IPO{Dates: updated})
		if !ok {
			continue
		}
		err := s.d.Scheduler.Schedule(ctx, schedulerSpec(org, id, st.Kind, at, now))
		if err != nil {
			return err
		}
	}
	for _, st := range cancel {
		if _, err := s.d.Scheduler.Cancel(ctx, org, st.Kind, jobKey(id, st.Kind)); err != nil {
			return err
		}
	}
	return nil
}

func schedulerSpec(org, id uuid.UUID, kind string, at, armed time.Time) scheduler.JobSpec {
	return scheduler.JobSpec{
		OrgID: org, Kind: kind, Key: jobKey(id, kind), Subject: jobSubject(id), RunAt: at,
		Payload: stepPayload{IPOID: id, ArmedAt: armed},
	}
}
