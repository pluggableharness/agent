package contextassembly

import "errors"

// ErrMissingModelTarget is returned by Assemble when in.ModelTarget is
// nil. context/data-types.md#contextrequest requires model_target on
// every ContextRequest — a nil target here is a caller bug (the model
// routing that resolves it happens one layer up, before Assemble is
// ever called), not a per-provider condition this package can isolate.
var ErrMissingModelTarget = errors.New("contextassembly: model target is required")
