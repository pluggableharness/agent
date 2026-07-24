package eventbus

// Event is one message published onto a Bus: a caller-chosen Topic routing
// it to every current Subscription on that Topic, and an arbitrary Payload.
//
// The Bus does not defensive-copy Payload: the exact same value (or, for a
// reference type, the exact same underlying data) reaches every subscriber
// of Topic. Publish's caller MUST NOT mutate Payload after Publish returns,
// and every Handler MUST treat the Payload it receives as read-only — a
// Handler that mutates a shared Payload corrupts what every other, possibly
// concurrently-running, Handler for the same Event sees.
type Event struct {
	// Topic routes this Event to every Subscription registered for the
	// same Topic string. Topic is unbounded and caller-defined — see
	// internal/telemetry's EventBusTopicKey doc comment for why it never
	// appears as a metric attribute.
	Topic string

	// Payload is this Event's arbitrary content. See the type doc
	// comment's read-only contract.
	Payload any
}

// validate reports whether e is well-formed enough to publish. The only
// requirement is a non-empty Topic — Payload may legitimately be nil (an
// Event that only signals a topic occurred, carrying no data).
func (e Event) validate() error {
	if e.Topic == "" {
		return ErrEmptyTopic
	}
	return nil
}
