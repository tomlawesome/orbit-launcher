package starfield

import (
	"math"
	"testing"
)

func TestNew_IsDeterministicForASeed(t *testing.T) {
	a := New(80, 24, 7)
	b := New(80, 24, 7)
	if len(a.Stars) != len(b.Stars) {
		t.Fatalf("star counts differ: %d vs %d", len(a.Stars), len(b.Stars))
	}
	for i := range a.Stars {
		if a.Stars[i] != b.Stars[i] {
			t.Fatalf("star %d differs across identical seeds: %+v vs %+v", i, a.Stars[i], b.Stars[i])
		}
	}
}

func TestAdvance_IsPureAndOnlyMovesTheTick(t *testing.T) {
	original := New(80, 24, 7)
	advanced := original.Advance()

	if original.Tick != 0 {
		t.Fatalf("Advance mutated the receiver's tick: %d", original.Tick)
	}
	if advanced.Tick != 1 {
		t.Fatalf("advanced tick = %d, want 1", advanced.Tick)
	}
	for i := range original.Stars {
		if original.Stars[i] != advanced.Stars[i] {
			t.Fatalf("Advance changed star %d's stored state; positions must derive from Tick only", i)
		}
	}
}

// The sky is rigid: any two stars in the same layer keep a constant
// angular separation forever — no star ever wanders on its own.
func TestStars_AreRigid_AngularSeparationIsConstant(t *testing.T) {
	m := New(120, 40, 3)
	var nearIdx []int
	for i, s := range m.Stars {
		if !s.Far {
			nearIdx = append(nearIdx, i)
		}
	}
	if len(nearIdx) < 2 {
		t.Skip("need at least two near-layer stars")
	}
	sep := m.Stars[nearIdx[0]].A0 - m.Stars[nearIdx[1]].A0
	for i := 0; i < 500; i++ {
		m = m.Advance()
	}
	after := m.Stars[nearIdx[0]].A0 - m.Stars[nearIdx[1]].A0
	if math.Abs(sep-after) > 1e-12 {
		t.Fatalf("angular separation drifted: %v -> %v", sep, after)
	}
}

// Rotation is quantized: cell positions change only when the shared step
// counter increments, so the whole field shifts in the same tick — the
// design's "locked in their permanence" rule.
func TestStarCells_MoveOnlyOnSynchronizedSteps(t *testing.T) {
	m := New(100, 30, 11)
	prev := m.StarCells()
	moves := 0
	for tick := 1; tick <= stepTicks*3; tick++ {
		m = m.Advance()
		cur := m.StarCells()
		moved := false
		for i := range cur {
			if i < len(prev) && (cur[i].X != prev[i].X || cur[i].Y != prev[i].Y) {
				moved = true
				break
			}
		}
		if moved {
			if tick%stepTicks != 0 {
				t.Fatalf("stars moved at tick %d, which is not a step boundary (step=%d)", tick, stepTicks)
			}
			moves++
		}
		prev = cur
	}
	if moves == 0 {
		t.Fatal("stars never moved across three full steps; rotation is broken")
	}
}

func TestStarCells_StayInBounds(t *testing.T) {
	m := New(80, 24, 5)
	for i := 0; i < 1000; i++ {
		for _, c := range m.StarCells() {
			if c.X < 0 || c.X >= m.Width || c.Y < 0 || c.Y >= m.Height {
				t.Fatalf("tick %d: star cell out of bounds: %+v", m.Tick, c)
			}
		}
		m = m.Advance()
	}
}

func TestPlanets_AllFourBodiesOrbitDeterministically(t *testing.T) {
	m := New(80, 26, 1)
	first := m.Planets()
	if len(first) != 4 {
		t.Fatalf("planets at tick 0 = %d, want 4 (binary pair + planet + moon)", len(first))
	}
	replay := New(80, 26, 99).Planets() // seed only affects stars, never planets
	for i := range first {
		if first[i] != replay[i] {
			t.Fatalf("planet %d depends on the star seed: %+v vs %+v", i, first[i], replay[i])
		}
	}

	// The binary pair genuinely orbits: over one full 34s period the ice
	// body must occupy more than one cell position.
	seen := map[[2]int]bool{}
	for i := 0; i < 34*10; i++ { // > one full 34s binary period at 120ms ticks
		for _, p := range m.Planets() {
			if p.Kind == PlanetLead {
				seen[[2]int{p.X, p.Y}] = true
			}
		}
		m = m.Advance()
	}
	if len(seen) < 4 {
		t.Fatalf("ice body visited only %d cells over a full period; expected an orbit", len(seen))
	}
}

func TestPlanets_ClearOfTheCentredTextColumn(t *testing.T) {
	// The identity block and menu occupy the centre of the screen; the
	// design places both systems clear of it. Verify across a whole
	// binary period at the floor size.
	m := New(80, 26, 1)
	for i := 0; i < 34*10; i++ { // > one full 34s binary period at 120ms ticks
		for _, p := range m.Planets() {
			if p.X > 26 && p.X < 54 {
				t.Fatalf("tick %d: planet %+v inside the centre text band (cols 27-53)", m.Tick, p)
			}
		}
		m = m.Advance()
	}
}

func TestPlanets_DisappearOnCrampedTerminals(t *testing.T) {
	if got := New(38, 20, 1).Planets(); got != nil {
		t.Fatalf("expected no planets on a too-narrow terminal, got %d", len(got))
	}
	if got := New(80, 10, 1).Planets(); got != nil {
		t.Fatalf("expected no planets on a too-short terminal, got %d", len(got))
	}
}
