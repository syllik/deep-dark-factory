package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

const (
	tableReviews  = "reviews"
	tableFindings = "findings"

	// sigVersionPrefix is the text form of a signature version without its
	// number (spec §6.6): version n is stored as sigVersionPrefix + n.
	sigVersionPrefix = "ddf.review-signature/v"

	reviewColumns = "review_id, task_key, change_id, role, head_sha, round, verdict, blocking_count, findings_digest, " +
		"raise_risk, sig_version, key_id, signature, run_id, created_at, payload"
	insertReviewSQL = "INSERT INTO reviews (" + reviewColumns + ") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	getReviewSQL    = "SELECT " + reviewColumns + " FROM reviews WHERE review_id = ?"
	listReviewsSQL  = "SELECT " + reviewColumns + " FROM reviews WHERE task_key = ? AND change_id = ? ORDER BY role, round, review_id"
	maxRoundSQL     = "SELECT COALESCE(MAX(round), 0) FROM reviews WHERE task_key = ? AND change_id = ? AND role = ?"

	findingColumns = "review_id, finding_id, task_key, role, head_sha, severity, category, security_level, blocking, " +
		"prior_id, routed, payload"
	insertFindingSQL = "INSERT INTO findings (" + findingColumns + ") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	listFindingsSQL  = "SELECT " + findingColumns + " FROM findings WHERE review_id = ? ORDER BY finding_id"
)

// ErrReviewRound reports a review whose round is not the next round of its
// role on its change (spec §6.6 round rule).
var ErrReviewRound = errors.New("store: review round not next")

// reviewPayload is the ddf.review/v1 payload: the verbatim hard-stop
// checklist and the summary.
type reviewPayload struct {
	Schema            model.SchemaID  `json:"schema"`
	HardStopChecklist json.RawMessage `json:"hardStopChecklist,omitempty"`
	Summary           string          `json:"summary,omitempty"`
}

// findingPayload is the ddf.finding/v1 payload: the finding's text fields.
type findingPayload struct {
	Schema        model.SchemaID `json:"schema"`
	Evidence      string         `json:"evidence"`
	Location      string         `json:"location,omitempty"`
	FixHint       string         `json:"fixHint,omitempty"`
	WontFixReason string         `json:"wontFixReason,omitempty"`
}

// NextReviewRound returns 1 + the highest stored round of role on the change
// changeID of task key, or 1 when there is none. The daemon reads it before
// it signs the review (spec §6.6 round rule).
func (s *Store) NextReviewRound(ctx context.Context, key model.TaskKey, changeID string, role model.Role) (int, error) {
	n, err := nextRound(s.db.QueryRowContext(ctx, maxRoundSQL, string(key), changeID, string(role)))
	if err != nil {
		return 0, fmt.Errorf("next review round %s %s: %w", changeID, role, err)
	}
	return n, nil
}

// CreateReview writes review r and all its findings in one transaction;
// findings are never written separately. r and every finding must be valid,
// each finding must carry r's key, ID, role and head SHA, BlockingCount must
// equal the number of blocking findings, the change must exist (else
// ErrNotFound), the run r.RunID must belong to r's task and role, and
// r.Round must be NextReviewRound (else ErrReviewRound). On any failure,
// including a duplicate finding ID, nothing is written.
func (s *Store) CreateReview(ctx context.Context, r model.Review, findings []model.Finding) error {
	if err := checkReview(r, findings); err != nil {
		return fmt.Errorf("create review %s: %w", r.ID, err)
	}
	payload, err := encodePayload(tableReviews, reviewPayload{
		Schema: model.SchemaIDReview, HardStopChecklist: r.HardStopChecklist, Summary: r.Summary,
	})
	if err != nil {
		return fmt.Errorf("create review %s: %w", r.ID, err)
	}
	fpayloads := make([]string, len(findings))
	for i, f := range findings {
		if fpayloads[i], err = encodePayload(tableFindings, findingPayload{
			Schema: model.SchemaIDFinding, Evidence: f.Evidence, Location: f.Location, FixHint: f.FixHint,
			WontFixReason: f.WontFixReason,
		}); err != nil {
			return fmt.Errorf("create review %s: finding %s: %w", r.ID, f.ID, err)
		}
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := scanChange(tx.QueryRowContext(ctx, getChangeSQL, string(r.Key), r.ChangeID)); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("change %s: %w", r.ChangeID, ErrNotFound)
			}
			return err
		}
		run, err := scanRun(tx.QueryRowContext(ctx, getRunSQL, r.RunID))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("run %s: %w", r.RunID, ErrNotFound)
		}
		if err != nil {
			return err
		}
		if run.Key != r.Key || run.Role != r.Role {
			return fmt.Errorf("run %s belongs to task %s role %s, not the review's", r.RunID, run.Key, run.Role)
		}
		next, err := nextRound(tx.QueryRowContext(ctx, maxRoundSQL, string(r.Key), r.ChangeID, string(r.Role)))
		if err != nil {
			return err
		}
		if r.Round != next {
			return fmt.Errorf("%w: round %d, want %d", ErrReviewRound, r.Round, next)
		}
		if _, err := tx.ExecContext(ctx, insertReviewSQL,
			r.ID, string(r.Key), r.ChangeID, string(r.Role), r.HeadSHA, r.Round, string(r.Verdict), r.BlockingCount,
			r.FindingsDigest, nullText(r.RaiseRisk), sigVersionPrefix+strconv.Itoa(r.SigVersion), r.KeyID, r.Signature,
			r.RunID, ms(r.CreatedAt), payload); err != nil {
			return err
		}
		for i, f := range findings {
			if _, err := tx.ExecContext(ctx, insertFindingSQL,
				f.ReviewID, f.ID, string(f.Key), string(f.Role), f.HeadSHA, string(f.Severity), string(f.Category),
				nullText(f.SecurityLevel), boolInt(f.Blocking), nullText(f.PriorID), nullText(f.Routed),
				fpayloads[i]); err != nil {
				return fmt.Errorf("finding %s: %w", f.ID, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("create review %s: %w", r.ID, err)
	}
	return nil
}

// GetReview returns review id. A missing review is ErrNotFound.
func (s *Store) GetReview(ctx context.Context, id string) (model.Review, error) {
	r, err := scanReview(s.db.QueryRowContext(ctx, getReviewSQL, id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return model.Review{}, fmt.Errorf("get review %s: %w", id, err)
	}
	return r, nil
}

// ListReviews returns the reviews of the change changeID of task key
// ordered by role, round, then review_id. Any undecodable row fails the
// whole read.
func (s *Store) ListReviews(ctx context.Context, key model.TaskKey, changeID string) (reviews []model.Review, err error) {
	rows, err := s.db.QueryContext(ctx, listReviewsSQL, string(key), changeID)
	if err != nil {
		return nil, fmt.Errorf("list reviews %s: %w", changeID, err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			reviews, err = nil, fmt.Errorf("close reviews rows: %w", cerr)
		}
	}()
	for rows.Next() {
		r, err := scanReview(rows)
		if err != nil {
			return nil, fmt.Errorf("list reviews %s: %w", changeID, err)
		}
		reviews = append(reviews, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list reviews %s: %w", changeID, err)
	}
	return reviews, nil
}

// ListFindings returns the findings of review reviewID ordered by
// finding_id. Any undecodable row fails the whole read.
func (s *Store) ListFindings(ctx context.Context, reviewID string) (findings []model.Finding, err error) {
	rows, err := s.db.QueryContext(ctx, listFindingsSQL, reviewID)
	if err != nil {
		return nil, fmt.Errorf("list findings %s: %w", reviewID, err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			findings, err = nil, fmt.Errorf("close findings rows: %w", cerr)
		}
	}()
	for rows.Next() {
		f, err := scanFinding(rows)
		if err != nil {
			return nil, fmt.Errorf("list findings %s: %w", reviewID, err)
		}
		findings = append(findings, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list findings %s: %w", reviewID, err)
	}
	return findings, nil
}

// checkReview validates a review and its findings before a write.
func checkReview(r model.Review, findings []model.Finding) error {
	if err := r.Validate(); err != nil {
		return err
	}
	for _, f := range []struct{ name, v string }{
		{"id", r.ID}, {"changeId", r.ChangeID}, {"headSha", r.HeadSHA}, {"findingsDigest", r.FindingsDigest},
		{"keyId", r.KeyID}, {"signature", r.Signature}, {"runId", r.RunID},
	} {
		if f.v == "" {
			return fmt.Errorf("empty %s", f.name)
		}
	}
	switch {
	case r.SigVersion < 1:
		return fmt.Errorf("sigVersion %d, want >= 1", r.SigVersion)
	case r.Round < 1:
		return fmt.Errorf("round %d, want >= 1", r.Round)
	case r.BlockingCount < 0:
		return fmt.Errorf("blockingCount %d, want >= 0", r.BlockingCount)
	}
	blocking := 0
	for _, f := range findings {
		if err := f.Validate(); err != nil {
			return fmt.Errorf("finding %s: %w", f.ID, err)
		}
		switch {
		case f.ID == "":
			return errors.New("finding with an empty id")
		case f.Key != r.Key:
			return fmt.Errorf("finding %s: key differs from the review's", f.ID)
		case f.ReviewID != r.ID:
			return fmt.Errorf("finding %s: reviewId %q differs from the review's", f.ID, f.ReviewID)
		case f.Role != r.Role:
			return fmt.Errorf("finding %s: role %q differs from the review's", f.ID, f.Role)
		case f.HeadSHA != r.HeadSHA:
			return fmt.Errorf("finding %s: headSha %q differs from the review's", f.ID, f.HeadSHA)
		}
		if f.Blocking {
			blocking++
		}
	}
	if r.BlockingCount != blocking {
		return fmt.Errorf("blockingCount %d, but %d findings are blocking", r.BlockingCount, blocking)
	}
	return nil
}

// nextRound scans the highest stored round and returns the next one.
func nextRound(row *sql.Row) (int, error) {
	var highest int64
	if err := row.Scan(&highest); err != nil {
		return 0, err
	}
	return int(highest) + 1, nil
}

// parseSigVersion decodes the text form of a signature version: the prefix
// followed by a decimal n >= 1 with no sign and no leading zero.
func parseSigVersion(v string) (int, error) {
	digits, ok := strings.CutPrefix(v, sigVersionPrefix)
	if !ok || digits == "" || digits[0] == '0' {
		return 0, decodeErr(tableReviews, "sig_version", fmt.Errorf("value %q", v))
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, decodeErr(tableReviews, "sig_version", fmt.Errorf("value %q", v))
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0, decodeErr(tableReviews, "sig_version", err)
	}
	return n, nil
}

// scanReview decodes one reviews row, failing closed on any unknown value.
func scanReview(row scanner) (model.Review, error) {
	var (
		r                                       model.Review
		key, role, verdict, sigVersion, payload string
		raiseRisk                               sql.NullString
		round, blockingCount, created           int64
		err                                     error
	)
	if err := row.Scan(&r.ID, &key, &r.ChangeID, &role, &r.HeadSHA, &round, &verdict, &blockingCount,
		&r.FindingsDigest, &raiseRisk, &sigVersion, &r.KeyID, &r.Signature, &r.RunID, &created, &payload); err != nil {
		return model.Review{}, err
	}
	if r.Key, err = parseCol(tableReviews, "task_key", key, model.ParseTaskKey); err != nil {
		return model.Review{}, err
	}
	if r.Role, err = parseCol(tableReviews, "role", role, model.ParseRole); err != nil {
		return model.Review{}, err
	}
	if r.Verdict, err = parseCol(tableReviews, "verdict", verdict, model.ParseReviewVerdict); err != nil {
		return model.Review{}, err
	}
	if r.RaiseRisk, err = parseNullCol(tableReviews, "raise_risk", raiseRisk, model.ParseRiskClass); err != nil {
		return model.Review{}, err
	}
	if r.SigVersion, err = parseSigVersion(sigVersion); err != nil {
		return model.Review{}, err
	}
	if round < 1 {
		return model.Review{}, decodeErr(tableReviews, "round", fmt.Errorf("value %d, want >= 1", round))
	}
	if blockingCount < 0 {
		return model.Review{}, decodeErr(tableReviews, "blocking_count", fmt.Errorf("value %d, want >= 0", blockingCount))
	}
	r.Round, r.BlockingCount, r.CreatedAt = int(round), int(blockingCount), fromMS(created)
	var p reviewPayload
	if err := decodePayload(tableReviews, payload, &p, &p.Schema, model.SchemaIDReview); err != nil {
		return model.Review{}, err
	}
	r.HardStopChecklist, r.Summary = p.HardStopChecklist, p.Summary
	if err := validRecord(tableReviews, r.Validate()); err != nil {
		return model.Review{}, err
	}
	return r, nil
}

// scanFinding decodes one findings row, failing closed on any unknown value.
func scanFinding(row scanner) (model.Finding, error) {
	var (
		f                                      model.Finding
		key, role, severity, category, payload string
		securityLevel, priorID, routed         sql.NullString
		blocking                               int64
		err                                    error
	)
	if err := row.Scan(&f.ReviewID, &f.ID, &key, &role, &f.HeadSHA, &severity, &category, &securityLevel,
		&blocking, &priorID, &routed, &payload); err != nil {
		return model.Finding{}, err
	}
	if f.Key, err = parseCol(tableFindings, "task_key", key, model.ParseTaskKey); err != nil {
		return model.Finding{}, err
	}
	if f.Role, err = parseCol(tableFindings, "role", role, model.ParseRole); err != nil {
		return model.Finding{}, err
	}
	if f.Severity, err = parseCol(tableFindings, "severity", severity, model.ParseSeverity); err != nil {
		return model.Finding{}, err
	}
	if f.Category, err = parseCol(tableFindings, "category", category, model.ParseFindingCategory); err != nil {
		return model.Finding{}, err
	}
	if f.SecurityLevel, err = parseNullCol(tableFindings, "security_level", securityLevel, model.ParseSecurityLevel); err != nil {
		return model.Finding{}, err
	}
	if f.Routed, err = parseNullCol(tableFindings, "routed", routed, model.ParseFindingRoute); err != nil {
		return model.Finding{}, err
	}
	if f.Blocking, err = parseBool(tableFindings, "blocking", blocking); err != nil {
		return model.Finding{}, err
	}
	f.PriorID = priorID.String
	var p findingPayload
	if err := decodePayload(tableFindings, payload, &p, &p.Schema, model.SchemaIDFinding); err != nil {
		return model.Finding{}, err
	}
	f.Evidence, f.Location, f.FixHint, f.WontFixReason = p.Evidence, p.Location, p.FixHint, p.WontFixReason
	if err := validRecord(tableFindings, f.Validate()); err != nil {
		return model.Finding{}, err
	}
	return f, nil
}
