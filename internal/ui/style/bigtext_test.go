package style

import (
	"strings"
	"testing"
)

func TestBigText_ThreeRowsOfHalfBlocksOnly(t *testing.T) {
	rows := BigText("ORBIT")
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	for i, row := range rows {
		if row == "" {
			t.Fatalf("row %d is empty", i)
		}
		for _, r := range row {
			switch r {
			case '█', '▀', '▄', ' ':
			default:
				t.Fatalf("row %d contains unexpected rune %q", i, r)
			}
		}
	}
}

func TestBigText_IsDeterministicAndDistinctPerLetter(t *testing.T) {
	a := strings.Join(BigText("ORBIT"), "\n")
	b := strings.Join(BigText("ORBIT"), "\n")
	if a != b {
		t.Fatal("BigText is not deterministic")
	}
	if strings.Join(BigText("O"), "\n") == strings.Join(BigText("T"), "\n") {
		t.Fatal("different letters rendered identically")
	}
}

func TestBigText_SkipsUnknownRunesInsteadOfPanicking(t *testing.T) {
	rows := BigText("O?Z")
	if rows[0] != BigText("O")[0] {
		t.Fatalf("unknown runes should be skipped; got %q", rows[0])
	}
}
