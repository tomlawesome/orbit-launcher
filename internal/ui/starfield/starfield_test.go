package starfield

import "testing"

func TestNew_DeterministicForFixedSeed(t *testing.T) {
	a := New(80, 24, 42)
	b := New(80, 24, 42)

	if len(a.Stars) != len(b.Stars) {
		t.Fatalf("different star counts for the same seed: %d vs %d", len(a.Stars), len(b.Stars))
	}
	for i := range a.Stars {
		if a.Stars[i] != b.Stars[i] {
			t.Fatalf("star %d differs for the same seed: %+v vs %+v", i, a.Stars[i], b.Stars[i])
		}
	}
}

func TestNew_DifferentSeedsDiffer(t *testing.T) {
	a := New(80, 24, 1)
	b := New(80, 24, 2)

	same := true
	for i := range a.Stars {
		if a.Stars[i] != b.Stars[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("expected different seeds to produce different star fields")
	}
}

func TestAdvance_IsPureAndDeterministic(t *testing.T) {
	original := New(80, 24, 7)
	snapshot := make([]Star, len(original.Stars))
	copy(snapshot, original.Stars)

	advanced := original.Advance()

	for i := range original.Stars {
		if original.Stars[i] != snapshot[i] {
			t.Fatalf("Advance mutated the receiver's star %d", i)
		}
	}

	replay := New(80, 24, 7).Advance()
	for i := range advanced.Stars {
		if advanced.Stars[i] != replay.Stars[i] {
			t.Fatalf("Advance is not deterministic at star %d: %+v vs %+v", i, advanced.Stars[i], replay.Stars[i])
		}
	}
	if advanced.Tick != 1 {
		t.Errorf("Tick = %d, want 1", advanced.Tick)
	}
}

func TestAdvance_WrapsStarsAcrossTheLeftEdge(t *testing.T) {
	m := Model{Width: 10, Height: 10, Stars: []Star{{X: 0, Y: 0, Depth: 1}}}
	next := m.Advance()
	if next.Stars[0].X < 0 || next.Stars[0].X > float64(m.Width) {
		t.Errorf("star X = %v, want wrapped within [0, %d]", next.Stars[0].X, m.Width)
	}
}

func TestStar_GlyphRespondsToBrightness(t *testing.T) {
	tests := []struct {
		name  string
		phase float64
		want  rune
	}{
		{"peak of the triangle wave", 0.5, '✦'},
		{"trough of the triangle wave", 0.0, '.'},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Star{Depth: 1, TwinklePhase: tt.phase}
			if got := s.Glyph(); got != tt.want {
				t.Errorf("Glyph() = %q, want %q (brightness=%v)", got, tt.want, s.brightness())
			}
		})
	}
}

func TestMarkDotOffset_IsDeterministicAndBounded(t *testing.T) {
	m := New(10, 10, 1)
	for tick := 0; tick < 200; tick++ {
		dx, dy := m.MarkDotOffset()
		if dx < -2 || dx > 2 || dy < -1 || dy > 1 {
			t.Fatalf("tick %d: offset (%d,%d) out of expected bounds", tick, dx, dy)
		}
		m = m.Advance()
	}
}

func TestMarkDotOffset_RepeatsEveryOrbitPeriod(t *testing.T) {
	m := New(10, 10, 1)
	m.orbitPeriodTicks = 8
	first := m
	for i := 0; i < 8; i++ {
		m = m.Advance()
	}
	fdx, fdy := first.MarkDotOffset()
	ldx, ldy := m.MarkDotOffset()
	if fdx != ldx || fdy != ldy {
		t.Errorf("offset after a full orbit period = (%d,%d), want it to repeat (%d,%d)", ldx, ldy, fdx, fdy)
	}
}
