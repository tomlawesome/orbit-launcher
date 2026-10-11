package deploy

// Error text convention. A deploy error reaches the screen verbatim, so
// its shape is part of the screen's grammar, and ui follows the same
// rule for the errors it makes:
//
//   - lowercase, and no full stop;
//   - `what: cause` to wrap, the cause after a colon (%w where it wraps);
//   - " — " (a spaced em dash) before advice, and never ";" to join
//     clauses;
//   - errors.New for fixed text; fmt.Errorf only when it has a verb.
//
// The stderr diagnostics in ui say `<area> stream: closed before the
// <run|step> finished`.
