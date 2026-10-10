package deploy

import (
	"context"
	"time"
)

// limitApplied is how long a call whose own limit is own may run under
// ctx: own, or what is left of ctx's deadline when that comes first.
// Error text names this, never own alone, so a caller's shorter
// deadline is not reported as the launcher's (#207).
func limitApplied(ctx context.Context, own time.Duration) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return own
	}
	left := time.Until(deadline)
	if left >= own {
		return own
	}
	if left >= time.Second {
		return left.Round(time.Second)
	}
	return left.Round(time.Millisecond)
}
