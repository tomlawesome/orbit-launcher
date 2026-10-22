package ui

import (
	"math"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// planLine is a plan line on one row, unwrapped: the tests' view of
// planRows, which is what the screen draws. It lived in repair.go while
// only tests called it.
func planLine(p engine.PlanAction) string {
	return planRows(p, math.MaxInt)[0]
}
