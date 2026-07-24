package content

// Option configures an optional field on a content-block builder that
// takes one. DocumentBlock.filename (proto/v1/content.pb.go's
// DocumentBlock.Filename) is the only such field today: it's a MAY per
// docs/specifications/model/data-types.md's canonical-schema note ("MUST
// be supported ... carrying data: bytes, media_type: string, and an
// optional filename"), so Document does not take it as a required
// positional argument.
type Option func(*documentOptions)

// documentOptions collects the optional fields Document accepts.
type documentOptions struct {
	filename *string
}

// WithFilename sets DocumentBlock's optional filename — the document's
// original filename, when known, which several vendors surface to the
// model as a citation/reference label (proto/v1/content.pb.go's
// DocumentBlock.Filename doc comment).
func WithFilename(name string) Option {
	return func(o *documentOptions) {
		o.filename = &name
	}
}
