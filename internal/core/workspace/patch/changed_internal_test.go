package patch

import (
	"slices"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/policy"
)

const (
	oidA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	oidB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	oid0 = "0000000000000000000000000000000000000000"
)

func TestChangedFromOutput_Parses(t *testing.T) {
	raw := ":000000 100644 " + oid0 + " " + oidA + " A\x00a.txt\x00" +
		":100644 100755 " + oidA + " " + oidB + " M\x00m.sh\x00" +
		":100644 000000 " + oidA + " " + oid0 + " D\x00d\tx.txt\x00" +
		":100644 120000 " + oidA + " " + oidB + " T\x00t\x00" +
		":100644 100644 " + oidA + " " + oidB + " R090\x00old/n\x00new/n\x00"
	num := "3\t0\ta.txt\x00" + "0\t0\tm.sh\x00" + "-\t-\td\tx.txt\x00" + "1\t1\tt\x00" + "1\t1\t\x00old/n\x00new/n\x00"
	want := []policy.ChangedPath{
		{Status: policy.ChangeStatusAdded, Path: "a.txt", OldMode: "000000", NewMode: "100644", OldBlob: oid0, NewBlob: oidA, Added: 3},
		{Status: policy.ChangeStatusModified, Path: "m.sh", OldMode: "100644", NewMode: "100755", OldBlob: oidA, NewBlob: oidB},
		{Status: policy.ChangeStatusDeleted, Path: "d\tx.txt", OldMode: "100644", NewMode: "000000", OldBlob: oidA, NewBlob: oid0, Binary: true},
		{Status: policy.ChangeStatusTypeChanged, Path: "t", OldMode: "100644", NewMode: "120000", OldBlob: oidA, NewBlob: oidB, Added: 1, Deleted: 1},
		{Status: policy.ChangeStatusRenamed, Path: "new/n", OldPath: "old/n", OldMode: "100644", NewMode: "100644", OldBlob: oidA, NewBlob: oidB, Added: 1, Deleted: 1},
	}
	got, err := changedFromOutput([]byte(raw), []byte(num))
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("changedFromOutput = %+v, %v; want %+v", got, err, want)
	}
	if got, err := changedFromOutput(nil, nil); err != nil || got == nil || len(got) != 0 {
		t.Errorf("empty output = %#v, %v; want an empty list", got, err)
	}
}

func TestChangedFromOutput_Refuses(t *testing.T) {
	mod := ":100644 100644 " + oidA + " " + oidB + " M\x00m\x00"
	for _, c := range []struct{ name, raw, num string }{
		{"copy status C075", ":100644 100644 " + oidA + " " + oidB + " C075\x00a\x00b\x00", "1\t1\t\x00a\x00b\x00"},
		{"unmerged status U", ":100644 100644 " + oidA + " " + oidB + " U\x00m\x00", "1\t1\tm\x00"},
		{"rename without score", ":100644 100644 " + oidA + " " + oidB + " R\x00a\x00b\x00", "1\t1\t\x00a\x00b\x00"},
		{"truncated raw record", mod[:20], "1\t1\tm\x00"},
		{"raw not NUL-terminated", mod[:len(mod)-1], "1\t1\tm\x00"},
		{"missing raw path field", ":100644 100644 " + oidA + " " + oidB + " M\x00", "1\t1\tm\x00"},
		{"missing rename new path", ":100644 100644 " + oidA + " " + oidB + " R090\x00a\x00", "1\t1\t\x00a\x00b\x00"},
		{"empty raw path", ":100644 100644 " + oidA + " " + oidB + " M\x00\x00", "1\t1\tm\x00"},
		{"bad mode", ":100644 100664 " + oidA + " " + oidB + " M\x00m\x00", "1\t1\tm\x00"},
		{"abbreviated oid", ":100644 100644 aaaaaaa " + oidB + " M\x00m\x00", "1\t1\tm\x00"},
		{"truncated numstat record", mod, "1\t1"},
		{"missing numstat rename paths", mod, "1\t1\t\x00m\x00"},
		{"half-binary counts", mod, "-\t1\tm\x00"},
		{"count mismatch", mod, "1\t1\tm\x00" + "1\t1\tn\x00"},
		{"path mismatch", mod, "1\t1\tn\x00"},
		{"rename path mismatch", ":100644 100644 " + oidA + " " + oidB + " R090\x00a\x00b\x00", "1\t1\t\x00c\x00b\x00"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got, err := changedFromOutput([]byte(c.raw), []byte(c.num)); err == nil || got != nil {
				t.Errorf("changedFromOutput = %+v, %v; want nil and an error", got, err)
			}
		})
	}
}
