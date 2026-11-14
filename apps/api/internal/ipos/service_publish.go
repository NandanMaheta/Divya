package ipos

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
)

const maxFlagNoteLen = 500

// SetFlag replaces the APPLY / DO NOT APPLY flag and its note (nil clears; a draft only).
func (s *Service) SetFlag(ctx context.Context, by audit.Actor, org, id uuid.UUID, version int32, flag *ApplyFlag, note *string, idemKey string) (Result, error) {
	if err := validateFlag(flag, note); err != nil {
		return Result{}, err
	}
	var res Result
	err := s.inTx(ctx, func(ctx context.Context) error {
		cur, err := s.store.GetIPOForUpdate(ctx, org, id)
		if err != nil {
			return err
		}
		if again, err := s.replayed(ctx, org, id, actFlagSet, idemKey); err != nil || again {
			res = Result{IPO: cur, Warnings: warnings(cur), Replayed: again}
			return err
		}
		if cur.Version != version {
			return ErrStaleVersion
		}
		if flag == nil && cur.Published {
			return &ValidationError{Code: "ipo.publish_incomplete", Issues: []FieldIssue{
				{Field: "apply_flag", Message: "a published IPO needs a flag"}}}
		}
		if sameFlag(cur.Flag, flag) && sameText(cur.FlagNote, note) {
			res = Result{IPO: cur, Warnings: warnings(cur)}
			return nil
		}
		now := s.d.Now()
		upd, err := s.store.SetIPOFlag(ctx, org, id, version, flag, note, now)
		if err != nil {
			return err
		}
		before := fields{"apply_flag": deref(cur.Flag), "flag_note": deref(cur.FlagNote)}
		after := fields{"apply_flag": deref(upd.Flag), "flag_note": deref(upd.FlagNote), "eligible": upd.Eligible()}
		if err := s.writeAudit(ctx, by, org, id, actFlagSet, before, withKey(after, idemKey)); err != nil {
			return err
		}
		if cur.Eligible() != upd.Eligible() {
			err := s.emitEligibility(ctx, EligibilityChanged{OrgID: org, IPOID: id, Eligible: upd.Eligible(), Cause: "flag", Actor: by, At: now})
			if err != nil {
				return err
			}
		}
		res = Result{IPO: upd, Warnings: warnings(upd)}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

func validateFlag(flag *ApplyFlag, note *string) error {
	var is []FieldIssue
	if flag != nil && *flag != FlagApply && *flag != FlagDoNotApply {
		is = append(is, FieldIssue{Field: "apply_flag", Message: "must be APPLY or DO_NOT_APPLY"})
	}
	if note != nil {
		switch n := utf8.RuneCountInString(strings.TrimSpace(*note)); {
		case flag == nil:
			is = append(is, FieldIssue{Field: "flag_note", Message: "a note needs a flag"})
		case n < 1 || utf8.RuneCountInString(*note) > maxFlagNoteLen:
			is = append(is, FieldIssue{Field: "flag_note", Message: "must be 1 to 500 characters"})
		}
	}
	if len(is) == 0 {
		return nil
	}
	return &ValidationError{Code: "ipo.invalid_value", Issues: is}
}

func sameFlag(a, b *ApplyFlag) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }
func sameText(a, b *string) bool    { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

// Publish makes the IPO visible to clients; it needs the FR-020 facts and a flag.
func (s *Service) Publish(ctx context.Context, by audit.Actor, org, id uuid.UUID, version int32, idemKey string) (Result, error) {
	return s.setPublished(ctx, by, org, id, version, idemKey, true)
}

// Unpublish hides the IPO from clients; status and steps are untouched.
func (s *Service) Unpublish(ctx context.Context, by audit.Actor, org, id uuid.UUID, version int32, idemKey string) (Result, error) {
	return s.setPublished(ctx, by, org, id, version, idemKey, false)
}

func (s *Service) setPublished(ctx context.Context, by audit.Actor, org, id uuid.UUID, version int32, idemKey string, publish bool) (Result, error) {
	action, cause := actUnpublished, "unpublish"
	if publish {
		action, cause = actPublished, "publish"
	}
	var res Result
	err := s.inTx(ctx, func(ctx context.Context) error {
		cur, err := s.store.GetIPOForUpdate(ctx, org, id)
		if err != nil {
			return err
		}
		if again, err := s.replayed(ctx, org, id, action, idemKey); err != nil || again {
			res = Result{IPO: cur, Warnings: warnings(cur), Replayed: again}
			return err
		}
		if cur.Version != version {
			return ErrStaleVersion
		}
		if cur.Published == publish {
			return ErrNoChange
		}
		if publish {
			if err := validatePublishable(cur); err != nil {
				return err
			}
		}
		now := s.d.Now()
		upd, err := s.store.SetIPOPublished(ctx, org, id, version, publish, now)
		if err != nil {
			return err
		}
		before := fields{"published": cur.Published, "eligible": cur.Eligible()}
		after := fields{"published": upd.Published, "eligible": upd.Eligible()}
		if err := s.writeAudit(ctx, by, org, id, action, before, withKey(after, idemKey)); err != nil {
			return err
		}
		if cur.Eligible() != upd.Eligible() {
			err := s.emitEligibility(ctx, EligibilityChanged{OrgID: org, IPOID: id, Eligible: upd.Eligible(), Cause: cause, Actor: by, At: now})
			if err != nil {
				return err
			}
		}
		res = Result{IPO: upd, Warnings: warnings(upd)}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// RequireEligible locks the row FOR SHARE in the caller's transaction and returns
// ErrNotEligible unless it is published with the APPLY flag (FR-023).
func (s *Service) RequireEligible(ctx context.Context, org, id uuid.UUID) (IPO, error) {
	i, err := s.store.GetIPOForShare(ctx, org, id)
	if err != nil {
		return IPO{}, err
	}
	if !i.Eligible() {
		return IPO{}, ErrNotEligible
	}
	return i, nil
}
