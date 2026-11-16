package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

// ErrNotFound reports a missing row; store errors wrap it.
var ErrNotFound = errors.New("store: not found")

// ErrDecode reports a stored value that does not decode: an unknown enum, a
// flag other than 0 or 1, a bad payload or a record that fails Validate.
// Every read error of that kind wraps it (spec §4.5 SQL rules, #73).
var ErrDecode = errors.New("store: decode")

// scanner is a *sql.Row or *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// decodeErr names the table and column of a value that failed to decode.
func decodeErr(table, column string, err error) error {
	return fmt.Errorf("%w: %s.%s: %w", ErrDecode, table, column, err)
}

// parseCol decodes an enum or key column through its model Parse function.
func parseCol[T ~string](table, column, v string, parse func(string) (T, error)) (T, error) {
	out, err := parse(v)
	if err != nil {
		return "", decodeErr(table, column, err)
	}
	return out, nil
}

// parseNullCol decodes a nullable enum or key column: NULL is the zero
// value, "" and unknown values are errors.
func parseNullCol[T ~string](table, column string, v sql.NullString, parse func(string) (T, error)) (T, error) {
	if !v.Valid {
		return "", nil
	}
	return parseCol(table, column, v.String, parse)
}

// parseBool decodes a 0/1 column; any other value is an error.
func parseBool(table, column string, v int64) (bool, error) {
	if v != 0 && v != 1 {
		return false, decodeErr(table, column, fmt.Errorf("value %d, want 0 or 1", v))
	}
	return v == 1, nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// ms encodes a time as INTEGER Unix milliseconds UTC (spec §4.5 SQL rules).
func ms(t time.Time) int64 { return t.UTC().UnixMilli() }

// fromMS decodes Unix milliseconds into a UTC time.
func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }

// nullMS encodes an optional time; nil is SQL NULL.
func nullMS(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ms(*t)
}

// fromNullMS decodes a nullable time column.
func fromNullMS(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMS(v.Int64)
	return &t
}

// ptrMS and fromPtrMS carry optional times inside payloads as Unix ms.
func ptrMS(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := ms(*t)
	return &v
}

func fromPtrMS(v *int64) *time.Time {
	if v == nil {
		return nil
	}
	t := fromMS(*v)
	return &t
}

// nullText encodes an optional text value; "" is SQL NULL.
func nullText[T ~string](v T) any {
	if v == "" {
		return nil
	}
	return string(v)
}

// encodePayload marshals a payload object for table.
func encodePayload(table string, v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode %s.payload: %w", table, err)
	}
	return string(b), nil
}

// decodePayload decodes exactly one JSON object into dst, refusing unknown
// fields, trailing data and a schema ID other than want.
func decodePayload(table, raw string, dst any, schema *model.SchemaID, want model.SchemaID) error {
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeErr(table, "payload", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return decodeErr(table, "payload", errors.New("trailing data after the object"))
	}
	if *schema != want {
		return decodeErr(table, "payload", fmt.Errorf("schema %q, want %q", *schema, want))
	}
	return nil
}

// validRecord wraps a Validate failure of a decoded record.
func validRecord(table string, err error) error {
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrDecode, table, err)
	}
	return nil
}
