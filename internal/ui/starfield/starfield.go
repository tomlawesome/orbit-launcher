// Package starfield implements the animated background: a pure,
// tick-driven, deterministic model of the sky, separate from rendering
// (design/mockups-v5.html). Given a fixed seed, every star and planet
// position is a deterministic function of the tick count, independent of
// any terminal or wall clock — that determinism is what makes this
// package unit-testable without a real render target, and what makes
// ORBIT_LAUNCHER_NO_ANIMATION's tick-0 frame stable for PTY and visual
// tests.
package starfield

import (
	"math"
	"math/rand"
)

// tickSeconds is the wall-clock duration one Advance() represents. It
// must match internal/ui's tick interval (120ms); all periods below are
// expressed in seconds and converted through it.
const tickSeconds = 0.12

// The sky moves as ONE body. Rotation is quantized: every stepTicks
// ticks the whole field advances in a single synchronized step — the
// near layer by a larger angle than the far layer, which is the
// parallax. Between steps, only twinkle changes. Continuous rotation
// with per-star cell rounding made stars cross cell boundaries one at a
// time, which read as stars wandering individually — stars are fixed
// points; it's the viewer that turns (design/mockups-v5.html, section
// 01).
const (
	stepTicks         = 21 // ≈2.5s per synchronized step at a 120ms tick
	nearRevolutionSec = 360
	farRevolutionSec  = 520
)

// Star is one fixed point of light, held in polar coordinates around the
// screen centre so the whole field can rotate rigidly.
type Star struct {
	R            float64 // radius from screen centre, in column units
	A0           float64 // base angle
	Far          bool    // far-layer stars sweep slower (parallax)
	TwinklePhase float64
	TwinkleRate  float64
}

// Cell is a renderable background element at a terminal cell.
type Cell struct {
	X, Y  int
	Glyph rune
}

// PlanetKind identifies which body a planet cell belongs to, so the
// renderer can colour it — the model stays colour-free.
type PlanetKind int

const (
	PlanetIce PlanetKind = iota
	PlanetRose
	PlanetPale
	PlanetEmber
)

// Planet is one planetary-system body at a terminal cell.
type Planet struct {
	X, Y  int
	Glyph rune
	Kind  PlanetKind
}

// Model is the full sky for a given viewport size.
type Model struct {
	Width, Height int
	Tick          int
	Stars         []Star
}

// density is stars per 100 terminal cells — deliberately sparse; the
// field reads as atmosphere, not weather.
const density = 1.2

// New builds a deterministic sky for the given viewport. The same seed
// always produces the same stars.
func New(width, height int, seed int64) Model {
	rng := rand.New(rand.NewSource(seed))
	count := int(float64(width*height) / 100 * density)
	if count < 1 && width > 0 && height > 0 {
		count = 1
	}
	maxR := float64(width)/2 + 6
	stars := make([]Star, count)
	for i := range stars {
		stars[i] = Star{
			R:            6 + rng.Float64()*(maxR-6),
			A0:           rng.Float64() * 2 * math.Pi,
			Far:          i%2 == 1,
			TwinklePhase: rng.Float64(),
			TwinkleRate:  0.004 + rng.Float64()*0.01,
		}
	}
	return Model{Width: width, Height: height, Stars: stars}
}

// Advance returns a new Model one tick further on — pure, no mutation of
// the receiver.
func (m Model) Advance() Model {
	next := m
	next.Tick = m.Tick + 1
	return next
}

// rotation returns the current field angles for the near and far layers.
// Both derive from the same quantized step counter, so every star's cell
// position changes in the same tick or not at all.
func (m Model) rotation() (near, far float64) {
	steps := float64(m.Tick / stepTicks)
	stepDur := float64(stepTicks) * tickSeconds
	near = 2 * math.Pi * stepDur / nearRevolutionSec * steps
	far = 2 * math.Pi * stepDur / farRevolutionSec * steps
	return near, far
}

// brightness is a deterministic slow triangle-wave twinkle.
func (s Star) brightness(tick int) float64 {
	phase := s.TwinklePhase + float64(tick)*s.TwinkleRate
	frac := phase - math.Floor(phase)
	tri := 1 - math.Abs(frac*2-1)
	return 0.3 + 0.7*tri
}

func glyphFor(b float64) rune {
	switch {
	case b > 0.85:
		return '✦'
	case b > 0.5:
		return '·'
	default:
		return '.'
	}
}

// StarCells returns every star's current cell position and glyph.
// Terminal cells are ~1:2 (wide:tall), so the vertical component is
// halved to keep the field circular on screen.
func (m Model) StarCells() []Cell {
	near, far := m.rotation()
	cx, cy := float64(m.Width)/2, float64(m.Height)/2
	cells := make([]Cell, 0, len(m.Stars))
	for _, s := range m.Stars {
		a := s.A0 + near
		if s.Far {
			a = s.A0 + far
		}
		x := int(math.Round(cx + s.R*math.Cos(a)))
		y := int(math.Round(cy + s.R*math.Sin(a)/2))
		if x < 0 || x >= m.Width || y < 0 || y >= m.Height {
			continue
		}
		cells = append(cells, Cell{X: x, Y: y, Glyph: glyphFor(s.brightness(m.Tick))})
	}
	return cells
}

// Planets returns the free planetary systems' current cells: a binary
// pair circling a shared centre upper-right, and a still planet with a
// small moon lower-left — single-cell bodies on slow clocks, placed
// clear of the centred text column (design/mockups-v5.html section 01).
func (m Model) Planets() []Planet {
	if m.Width < 40 || m.Height < 12 {
		return nil // too cramped for ornament; the menu comes first
	}
	t := float64(m.Tick) * tickSeconds
	scale := float64(m.Width) / 80
	if scale < 1 {
		scale = 1
	}

	var planets []Planet
	put := func(x, y float64, glyph rune, kind PlanetKind) {
		xi, yi := int(math.Round(x)), int(math.Round(y))
		if xi >= 0 && xi < m.Width && yi >= 0 && yi < m.Height {
			planets = append(planets, Planet{X: xi, Y: yi, Glyph: glyph, Kind: kind})
		}
	}

	// Binary pair: two bodies opposite each other on one ellipse.
	bx, by := float64(m.Width)*0.78, float64(m.Height)*0.20
	ba := 2 * math.Pi * t / 34
	brx, bry := 6*scale, 3*scale
	put(bx+brx*math.Cos(ba), by+bry*math.Sin(ba), '●', PlanetIce)
	put(bx+brx*math.Cos(ba+math.Pi), by+bry*math.Sin(ba+math.Pi), '●', PlanetRose)

	// Still planet with a circling moon.
	px, py := float64(m.Width)*0.16, float64(m.Height)*0.78
	put(px, py, '●', PlanetPale)
	ma := 2 * math.Pi * t / 13
	put(px+3.5*scale*math.Cos(ma), py+1.6*scale*math.Sin(ma), '·', PlanetEmber)

	return planets
}
