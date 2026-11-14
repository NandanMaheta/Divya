package scheduler

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/outbox"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
)

// AlertTopic is the outbox topic of the grouped dead-item alert (contracts/outbox-events.md).
const AlertTopic = "admin.jobs_dead"

const maxAlertListLen = 10

// alertPayload is the content reference of an admin.jobs_dead message: counts and names only,
// never error texts, ids or secrets.
type alertPayload struct {
	TickID       uuid.UUID `json:"tick_id"`
	JobsDead     int       `json:"jobs_dead"`
	MessagesDead int       `json:"messages_dead"`
	Kinds        []string  `json:"kinds"`
	Topics       []string  `json:"topics"`
}

// sweepDead records one admin.jobs_dead message per organisation for the dead jobs and messages
// that no alert covered yet, and marks them alerted, all in one transaction: when it fails
// nothing is marked and the next tick tries again, so an alert is neither lost nor duplicated.
// Dead messages of topics "admin.*" are marked but never reported (no alert about an alert).
func (s *Service) sweepDead(ctx context.Context, tickID uuid.UUID) error {
	return s.txr.InTx(ctx, func(ctx context.Context) error {
		tx, err := db.MustTx(ctx)
		if err != nil {
			return err
		}
		q := dbgen.New(tx)
		jobs, err := q.SelectUnalertedDeadJobs(ctx)
		if err != nil {
			return fmt.Errorf("scheduler: select dead jobs: %w", err)
		}
		groups, err := s.ob.SweepDead(ctx)
		if err != nil {
			return fmt.Errorf("scheduler: sweep dead messages: %w", err)
		}

		payloads := map[uuid.UUID]*alertPayload{}
		get := func(org uuid.UUID) *alertPayload {
			p, ok := payloads[org]
			if !ok {
				p = &alertPayload{TickID: tickID, Kinds: []string{}, Topics: []string{}}
				payloads[org] = p
			}
			return p
		}
		ids := make([]uuid.UUID, 0, len(jobs))
		for _, j := range jobs {
			ids = append(ids, j.ID)
			p := get(j.OrgID)
			p.JobsDead++
			if !slices.Contains(p.Kinds, j.Kind) {
				p.Kinds = append(p.Kinds, j.Kind)
			}
		}
		for _, g := range groups {
			p := get(g.OrgID)
			p.MessagesDead += g.Count
			for _, t := range g.Topics {
				if !slices.Contains(p.Topics, t) {
					p.Topics = append(p.Topics, t)
				}
			}
		}

		orgs := make([]uuid.UUID, 0, len(payloads))
		for org := range payloads {
			orgs = append(orgs, org)
		}
		slices.SortFunc(orgs, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
		for _, org := range orgs {
			p := payloads[org]
			slices.Sort(p.Kinds)
			slices.Sort(p.Topics)
			p.Kinds, p.Topics = p.Kinds[:min(len(p.Kinds), maxAlertListLen)], p.Topics[:min(len(p.Topics), maxAlertListLen)]
			_, err := s.ob.Add(ctx, outbox.Message{
				OrgID: org, Topic: AlertTopic, Recipient: outbox.Recipient{Kind: "admin"},
				Subject: "scheduler:dead", DedupeKey: "jobs_dead:" + tickID.String(), Payload: p,
			})
			if err != nil {
				return fmt.Errorf("scheduler: record alert: %w", err)
			}
		}
		if len(ids) > 0 {
			if err := q.MarkJobsAlerted(ctx, dbgen.MarkJobsAlertedParams{Now: db.Timestamptz(s.now()), Ids: ids}); err != nil {
				return fmt.Errorf("scheduler: mark jobs alerted: %w", err)
			}
		}
		return nil
	})
}
