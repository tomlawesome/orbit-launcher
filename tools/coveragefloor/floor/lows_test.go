package floor

import (
	"strings"
	"testing"
)

// T2-Q6 / X-Q2 (#195): ParseFloat accepts NaN, Inf and negative numbers,
// and a NaN floor never fails the `got < floor` comparison. A floor that is
// not a usable percentage is an error naming the line, not a silent pass.
func TestParseFloors_RejectsNaNAndNegativeFloors(t *testing.T) {
	for _, bad := range []string{"NaN", "nan", "-NaN", "-1", "-0.5", "-1e-3", "-Inf", "-100"} {
		t.Run(bad, func(t *testing.T) {
			got, err := ParseFloors(strings.NewReader("example.com/mod/ok 50.0\nexample.com/mod/pkg " + bad + "\n"))
			if err == nil {
				t.Fatalf("ParseFloors accepted a floor of %q: %+v", bad, got)
			}
			if !strings.Contains(err.Error(), "example.com/mod/pkg") && !strings.Contains(err.Error(), "line 2") {
				t.Errorf("err = %v; want it to say which line or package is wrong", err)
			}
		})
	}
}

// The check above must not turn into refusing real floors.
func TestParseFloors_StillAcceptsZeroAndOrdinaryFloors(t *testing.T) {
	got, err := ParseFloors(strings.NewReader("a/zero 0\na/zerodot 0.0\na/mid 84.9\na/full 100\na/fulldot 100.0\n"))
	if err != nil {
		t.Fatalf("ParseFloors: %v", err)
	}
	if len(got) != 5 || got["a/zero"].Percent != 0 || got["a/mid"].Percent != 84.9 || got["a/full"].Percent != 100 {
		t.Errorf("got %+v", got)
	}
}
