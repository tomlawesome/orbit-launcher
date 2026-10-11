package engine

// Pins #211 (prompt session, #199 WI-7; UI-10): the machine-prompt
// attempt limit has one name in engine, so the UI formats "attempt N of
// M" from it instead of typing 3. The constant is read from the
// package's own source by the type checker (see vocabulary_test.go), so
// a missing name fails this test alone instead of stopping the whole
// package from compiling.

import (
	"go/constant"
	"go/types"
	"testing"
)

// TestPromptMaxAttemptsIsNamed: the engine bounds each field at three
// answers (Prompt.Attempt is documented "bounded at 3 by the engine";
// the third rejection is a prompt-abort). The name is an exported
// integer constant usable with Prompt.Attempt.
func TestPromptMaxAttemptsIsNamed(t *testing.T) {
	c, ok := packageConstants(t)["PromptMaxAttempts"]
	if !ok {
		t.Fatal("engine.PromptMaxAttempts is not defined")
	}
	if !c.Exported() {
		t.Error("engine.PromptMaxAttempts is not exported")
	}
	v, exact := constant.Int64Val(constant.ToInt(c.Val()))
	if !exact || v != 3 {
		t.Errorf("engine.PromptMaxAttempts = %s, want 3", c.Val())
	}
	if b, ok := c.Type().Underlying().(*types.Basic); !ok || b.Info()&types.IsInteger == 0 {
		t.Errorf("engine.PromptMaxAttempts has type %s, want an integer type comparable with Prompt.Attempt", c.Type())
	}
}
