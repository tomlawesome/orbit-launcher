// Package starfield implements the animated background: a pure,
// tick-driven, deterministic star field model, separate from rendering
// (docs/implementation-plan.md section 5, Wave 1). Given a fixed seed,
// star positions and the orbit mark's dot position are a deterministic
// function of the tick count, independent of any terminal or wall clock —
// that determinism is what makes this package unit-testable without a
// real render target.
package starfield

import (
	"math"
	"math/rand"
)

// Star is one point of light. Depth controls both parallax drift speed
// (distant stars move slower) and twinkle rate.
type Star struct {
	X, Y         float64
	Depth        float64 // 0 (far, dim, slow) .. 1 (near, bright, fast)
	TwinklePhase float64
}

// Glyph is the rune drawn for a star, chosen by its current brightness.
func (s Star) Glyph() rune {
	switch b := s.brightness(); {
	case b > 0.85:
		return '✦'
	case b > 0.5:
		return '·'
	default:
		return '.'
	}
}

// brightness is a deterministic function of TwinklePhase: a slow sine-like
// oscillation via a triangle wave, cheap and dependency-free.
func (s Star) brightness() float64 {
	phase := s.TwinklePhase
	// Fold phase into a 0..1 triangle wave.
	frac := phase - float64(int(phase))
	if frac < 0 {
		frac++
	}
	tri := 1 - math.Abs(frac*2-1)
	return 0.3 + 0.7*tri*(0.4+0.6*s.Depth)
}

// Model is a full star field for a given viewport size.
type Model struct {
	Width, Height int
	Stars         []Star
	Tick          int

	// MarkDot is the current position of the small point of light that
	// orbits the ⟡ mark, in a unit circle (radius 1, centred on 0,0);
	// callers translate it to screen coordinates around the mark's cell.
	orbitPeriodTicks int
}

// density is stars per 100 terminal cells — deliberately sparse; the
// mockup's starfield reads as atmospheric, not busy.
const density = 1.2

// New builds a deterministic star field for the given viewport. The same
// seed always produces the same initial star positions.
func New(width, height int, seed int64) Model {
	rng := rand.New(rand.NewSource(seed))
	count := int(float64(width*height) / 100 * density)
	if count < 1 && width > 0 && height > 0 {
		count = 1
	}
	stars := make([]Star, count)
	for i := range stars {
		stars[i] = Star{
			X:            rng.Float64() * float64(width),
			Y:            rng.Float64() * float64(height),
			Depth:        rng.Float64(),
			TwinklePhase: rng.Float64(),
		}
	}
	return Model{
		Width:            width,
		Height:           height,
		Stars:            stars,
		orbitPeriodTicks: 96, // ~ a full orbit every few seconds at a 100ms tick
	}
}

// Advance returns a new Model one tick further on — pure, no mutation of
// the receiver, so a test can hold the original and the advanced model
// side by side.
func (m Model) Advance() Model {
	next := m
	next.Tick = m.Tick + 1
	next.Stars = make([]Star, len(m.Stars))
	for i, s := range m.Stars {
		// Parallax: deeper (higher Depth) stars drift faster, and always
		// in the same direction, so distant stars visibly lag near ones
		// rather than the whole field moving as one flat sheet.
		s.X -= 0.02 * (0.2 + s.Depth)
		if s.X < 0 {
			s.X += float64(m.Width)
		}
		s.TwinklePhase += 0.004 + 0.01*s.Depth
		next.Stars[i] = s
	}
	return next
}

// MarkDotOffset returns the orbiting dot's current (dx, dy) offset from
// the ⟡ mark's cell, on a small elliptical path sized for a single
// terminal cell's neighbourhood.
func (m Model) MarkDotOffset() (dx, dy int) {
	angle := 2 * math.Pi * float64(m.Tick%m.orbitPeriodTicks) / float64(m.orbitPeriodTicks)
	dx = int(math.Round(2 * math.Cos(angle)))
	dy = int(math.Round(1 * math.Sin(angle)))
	return dx, dy
}
