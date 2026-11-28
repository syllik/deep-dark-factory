package store_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/store"
)

func mustSetPause(t *testing.T, s *store.Store, r model.PauseReason, detail string, at time.Time) bool {
	t.Helper()
	set, err := s.SetPauseReason(t.Context(), r, detail, at)
	if err != nil {
		t.Fatalf("SetPauseReason %s: %v", r, err)
	}
	return set
}

func mustListPause(t *testing.T, s *store.Store) []store.PauseReason {
	t.Helper()
	got, err := s.ListPauseReasons(t.Context())
	if err != nil {
		t.Fatalf("ListPauseReasons: %v", err)
	}
	return got
}

func TestPauseReasons_SetListRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	if got := mustListPause(t, s); len(got) != 0 {
		t.Fatalf("empty list = %v", got)
	}
	t1 := t0.Add(time.Second)
	if !mustSetPause(t, s, model.PauseReasonRuntime, "colima down", t1) {
		t.Fatal("first set of runtime returned false")
	}
	if !mustSetPause(t, s, model.PauseReasonOwner, "", t0) {
		t.Fatal("first set of owner returned false")
	}
	if !mustSetPause(t, s, model.PauseReasonCredential, "", t1) {
		t.Fatal("first set of credential returned false")
	}
	want := []store.PauseReason{
		{Reason: model.PauseReasonOwner, SetAt: t0.UTC()},
		{Reason: model.PauseReasonCredential, SetAt: t1.UTC()},
		{Reason: model.PauseReasonRuntime, SetAt: t1.UTC(), Detail: "colima down"},
	}
	if got := mustListPause(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("list = %+v, want %+v", got, want)
	}
}

func TestPauseReasons_SetIsIdempotent(t *testing.T) {
	s, _ := newTestStore(t)
	mustSetPause(t, s, model.PauseReasonSecurity, "first", t0)
	if mustSetPause(t, s, model.PauseReasonSecurity, "second", t0.Add(time.Hour)) {
		t.Fatal("second set returned true")
	}
	want := []store.PauseReason{{Reason: model.PauseReasonSecurity, SetAt: t0.UTC(), Detail: "first"}}
	if got := mustListPause(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("list = %+v, want %+v", got, want)
	}
}

func TestPauseReasons_SurviveReopen(t *testing.T) {
	s, path := newTestStore(t)
	mustSetPause(t, s, model.PauseReasonOwner, "ddf pause", t0)
	mustSetPause(t, s, model.PauseReasonRevertRed, "revert of #12 red", t0.Add(time.Minute))
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	got := mustListPause(t, s2)
	want := []store.PauseReason{
		{Reason: model.PauseReasonOwner, SetAt: t0.UTC(), Detail: "ddf pause"},
		{Reason: model.PauseReasonRevertRed, SetAt: t0.Add(time.Minute).UTC(), Detail: "revert of #12 red"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after reopen = %+v, want %+v", got, want)
	}
	for i := range got {
		if got[i].SetAt.UnixMilli() != want[i].SetAt.UnixMilli() || got[i].SetAt.Location() != time.UTC {
			t.Errorf("set_at %d = %v, want %v UTC", i, got[i].SetAt, want[i].SetAt)
		}
	}
}

func TestPauseReasons_ClearReturnsPresentSorted(t *testing.T) {
	s, _ := newTestStore(t)
	mustSetPause(t, s, model.PauseReasonRuntime, "", t0)
	mustSetPause(t, s, model.PauseReasonOwner, "", t0)
	mustSetPause(t, s, model.PauseReasonSecurity, "", t0)
	got, err := s.ClearPauseReasons(t.Context(),
		model.PauseReasonSecurity, model.PauseReasonCredential, model.PauseReasonRuntime, model.PauseReasonSecurity)
	if err != nil {
		t.Fatalf("ClearPauseReasons: %v", err)
	}
	want := []model.PauseReason{model.PauseReasonRuntime, model.PauseReasonSecurity}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cleared = %v, want %v", got, want)
	}
	left := []store.PauseReason{{Reason: model.PauseReasonOwner, SetAt: t0.UTC()}}
	if l := mustListPause(t, s); !reflect.DeepEqual(l, left) {
		t.Fatalf("left = %+v, want %+v", l, left)
	}
	got, err = s.ClearPauseReasons(t.Context(), model.PauseReasonCredential)
	if err != nil || len(got) != 0 {
		t.Fatalf("clear of an unset reason = %v, %v; want empty, nil", got, err)
	}
}

func TestPauseReasons_ClearWithoutReasonsFails(t *testing.T) {
	s, _ := newTestStore(t)
	mustSetPause(t, s, model.PauseReasonOwner, "", t0)
	if _, err := s.ClearPauseReasons(t.Context()); err == nil {
		t.Fatal("clear with no reasons succeeded")
	}
	if got := mustListPause(t, s); len(got) != 1 {
		t.Fatalf("list = %+v, want the owner reason kept", got)
	}
}

func TestPauseReasons_InvalidReasonWritesNothing(t *testing.T) {
	s, _ := newTestStore(t)
	for _, r := range []model.PauseReason{"", "bogus", "Owner"} {
		if set, err := s.SetPauseReason(t.Context(), r, "x", t0); err == nil || set {
			t.Errorf("set %q = %v, %v; want false, error", r, set, err)
		}
	}
	if got := mustListPause(t, s); len(got) != 0 {
		t.Fatalf("list after invalid sets = %+v", got)
	}
	mustSetPause(t, s, model.PauseReasonOwner, "", t0)
	if _, err := s.ClearPauseReasons(t.Context(), model.PauseReasonOwner, "bogus"); err == nil {
		t.Fatal("clear with an invalid reason succeeded")
	}
	if got := mustListPause(t, s); len(got) != 1 || got[0].Reason != model.PauseReasonOwner {
		t.Fatalf("list after invalid clear = %+v, want owner kept", got)
	}
}

func TestPauseReasons_DecodeFailsClosed(t *testing.T) {
	cases := []struct{ name, stmt string }{
		{"unknown reason", "UPDATE pause_reasons SET reason = 'bogus'"},
		{"non-JSON payload", "UPDATE pause_reasons SET payload = 'not json'"},
		{"wrong schema", `UPDATE pause_reasons SET payload = '{"schema":"ddf.task/v1"}'`},
		{"unknown payload field", `UPDATE pause_reasons SET payload = '{"schema":"ddf.pause-reason/v1","x":1}'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, path := newTestStore(t)
			mustSetPause(t, s, model.PauseReasonOwner, "d", t0)
			mustSetPause(t, s, model.PauseReasonRuntime, "", t0.Add(time.Second))
			if _, err := rawDB(t, path).ExecContext(t.Context(), tc.stmt+" WHERE reason = 'runtime'"); err != nil {
				t.Fatalf("raw update: %v", err)
			}
			got, err := s.ListPauseReasons(t.Context())
			if !errors.Is(err, store.ErrDecode) {
				t.Fatalf("err = %v, want ErrDecode", err)
			}
			if got != nil {
				t.Fatalf("got = %+v, want nil", got)
			}
		})
	}
}
