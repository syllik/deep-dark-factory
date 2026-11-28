package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

const (
	tablePauseReasons = "pause_reasons"

	insertPauseReasonSQL = "INSERT INTO pause_reasons (reason, set_at, payload) VALUES (?, ?, ?) ON CONFLICT (reason) DO NOTHING"
	listPauseReasonsSQL  = "SELECT reason, set_at, payload FROM pause_reasons ORDER BY set_at, reason"
	deletePauseReasonSQL = "DELETE FROM pause_reasons WHERE reason = ?"
)

// PauseReason is one stored reason of the global pause set (spec §9 Global
// pause): the reason, when it was first set and an optional detail.
type PauseReason struct {
	Reason model.PauseReason
	SetAt  time.Time
	Detail string
}

// pauseReasonPayload is the ddf.pause-reason/v1 payload.
type pauseReasonPayload struct {
	Schema model.SchemaID `json:"schema"`
	Detail string         `json:"detail,omitempty"`
}

// SetPauseReason adds reason to the global pause set (spec §9 Global pause),
// which survives a daemon restart. It returns true only when the reason was
// not set before; an already set reason keeps its first set_at and detail
// and returns false, nil. An invalid reason fails and writes nothing. The
// store does not enforce the lift rule (only `ddf resume` clears reasons
// other than runtime); the daemon does. No event is appended.
func (s *Store) SetPauseReason(ctx context.Context, reason model.PauseReason, detail string, at time.Time) (set bool, err error) {
	if _, err := model.ParsePauseReason(string(reason)); err != nil {
		return false, fmt.Errorf("set pause reason: %w", err)
	}
	payload, err := encodePayload(tablePauseReasons, pauseReasonPayload{Schema: model.SchemaIDPauseReason, Detail: detail})
	if err != nil {
		return false, fmt.Errorf("set pause reason %s: %w", reason, err)
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, insertPauseReasonSQL, string(reason), ms(at), payload)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		set = n == 1
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("set pause reason %s: %w", reason, err)
	}
	return set, nil
}

// ListPauseReasons returns the global pause set (spec §9 Global pause)
// ordered by set_at, then reason; an empty set is a nil slice. Any
// undecodable row fails the whole read with an error wrapping ErrDecode.
func (s *Store) ListPauseReasons(ctx context.Context) (reasons []PauseReason, err error) {
	rows, err := s.db.QueryContext(ctx, listPauseReasonsSQL)
	if err != nil {
		return nil, fmt.Errorf("list pause reasons: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			reasons, err = nil, fmt.Errorf("close pause_reasons rows: %w", cerr)
		}
	}()
	for rows.Next() {
		r, err := scanPauseReason(rows)
		if err != nil {
			return nil, fmt.Errorf("list pause reasons: %w", err)
		}
		reasons = append(reasons, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pause reasons: %w", err)
	}
	return reasons, nil
}

// ClearPauseReasons removes the named reasons from the global pause set
// (spec §9 Global pause) in one transaction and returns those that were
// present, deduplicated and sorted ascending. A reason that is not set is
// not an error and is not returned. No reasons, or any invalid reason, fails
// and deletes nothing. The store does not enforce the lift rule (only `ddf
// resume` clears reasons other than runtime); the daemon does. No event is
// appended.
func (s *Store) ClearPauseReasons(ctx context.Context, reasons ...model.PauseReason) ([]model.PauseReason, error) {
	if len(reasons) == 0 {
		return nil, errors.New("clear pause reasons: no reasons")
	}
	want := make([]model.PauseReason, 0, len(reasons))
	for _, r := range reasons {
		if _, err := model.ParsePauseReason(string(r)); err != nil {
			return nil, fmt.Errorf("clear pause reasons: %w", err)
		}
		want = append(want, r)
	}
	slices.Sort(want)
	want = slices.Compact(want)
	var cleared []model.PauseReason
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		cleared = nil
		for _, r := range want {
			res, err := tx.ExecContext(ctx, deletePauseReasonSQL, string(r))
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n == 1 {
				cleared = append(cleared, r)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("clear pause reasons: %w", err)
	}
	return cleared, nil
}

// scanPauseReason decodes one pause_reasons row, failing closed on any
// unknown value.
func scanPauseReason(row scanner) (PauseReason, error) {
	var (
		r           PauseReason
		reason, raw string
		setAt       int64
		err         error
	)
	if err := row.Scan(&reason, &setAt, &raw); err != nil {
		return PauseReason{}, err
	}
	if r.Reason, err = parseCol(tablePauseReasons, "reason", reason, model.ParsePauseReason); err != nil {
		return PauseReason{}, err
	}
	r.SetAt = fromMS(setAt)
	var p pauseReasonPayload
	if err := decodePayload(tablePauseReasons, raw, &p, &p.Schema, model.SchemaIDPauseReason); err != nil {
		return PauseReason{}, err
	}
	r.Detail = p.Detail
	return r, nil
}
