package schema

// Option configures the optional, per-node fields consumed by Object,
// String, Number, Integer, Boolean, and Array. A single Option type is
// shared across every builder rather than one distinct type per builder:
// each builder reads only the options fields meaningful for its own
// SchemaType (per doc.go's "supported subset" list) and silently ignores
// the rest, so passing e.g. WithEnum to Boolean is a no-op, not a compile
// error or a runtime panic — the same "invalid option is a no-op"
// convention every functional-options type in this codebase follows.
type Option func(*options)

// options is the unexported target struct every Option mutates. Builders
// translate the populated struct into the generated schemav1.Schema
// fields they each care about.
type options struct {
	description string
	enumValues  []string
	required    []string
}

// WithDescription sets the human-readable description shown to the model
// during tool selection and in plan-diff UI. Meaningful on every builder.
func WithDescription(description string) Option {
	return func(o *options) { o.description = description }
}

// WithEnum constrains a String schema's value to one of values. Meaningful
// only on String — per model/data-types.md#tool-schema, enum applies to a
// STRING-typed node, not to a distinct type of its own. A no-op on every
// other builder.
func WithEnum(values ...string) Option {
	return func(o *options) { o.enumValues = values }
}

// WithRequired marks the given property names as required. Meaningful
// only on Object, where every name MUST already be a key of the
// properties map passed to Object — Object returns an error otherwise. A
// no-op on every other builder.
func WithRequired(names ...string) Option {
	return func(o *options) { o.required = names }
}

// resolve applies every non-nil opt in order and returns the resulting
// options value. Defaults are the options zero value: empty description,
// no enum values, no required names.
func resolve(opts []Option) options {
	var o options
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}
