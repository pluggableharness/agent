package kernel

import (
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
)

// structFromAttrs converts a flat list of slog.Attr into a
// google.protobuf.Struct — the inverse direction of internal/log's
// AttrsFromStruct, for LogEntry.fields (kernel-callbacks.md#log:
// "structured attributes, mirroring slog.Attr's key/value model"). Later
// duplicate keys win, matching slog's own last-value-wins behavior for
// repeated keys with the same name.
func structFromAttrs(attrs []slog.Attr) (*structpb.Struct, error) {
	if len(attrs) == 0 {
		return nil, nil
	}
	fields := make(map[string]any, len(attrs))
	for _, a := range attrs {
		if a.Equal(slog.Attr{}) {
			continue // slog's own convention: a zero Attr is skipped, never rendered
		}
		v, err := attrValueToAny(a.Value)
		if err != nil {
			return nil, fmt.Errorf("kernel: attr %q: %w", a.Key, err)
		}
		fields[a.Key] = v
	}
	return structpb.NewStruct(fields)
}

// attrValueToAny converts one slog.Value into a structpb.NewValue-
// compatible Go value, resolving a slog.LogValuer first (Handle callers
// are responsible for this — slog does not do it automatically) and
// recursing into a group's own attrs. Kinds structpb.NewValue has no
// direct representation for (Duration, Time, arbitrary Any) render to
// their string form rather than failing the whole batch over one
// unsupported attribute.
func attrValueToAny(v slog.Value) (any, error) {
	v = v.Resolve()
	switch v.Kind() {
	case slog.KindBool:
		return v.Bool(), nil
	case slog.KindInt64:
		return v.Int64(), nil
	case slog.KindUint64:
		return v.Uint64(), nil
	case slog.KindFloat64:
		return v.Float64(), nil
	case slog.KindString:
		return v.String(), nil
	case slog.KindDuration:
		return v.Duration().String(), nil
	case slog.KindTime:
		return v.Time().Format(time.RFC3339Nano), nil
	case slog.KindGroup:
		group := v.Group()
		if len(group) == 0 {
			return map[string]any{}, nil
		}
		nested := make(map[string]any, len(group))
		for _, a := range group {
			if a.Equal(slog.Attr{}) {
				continue
			}
			nv, err := attrValueToAny(a.Value)
			if err != nil {
				return nil, err
			}
			nested[a.Key] = nv
		}
		return nested, nil
	default: // slog.KindAny, and anything else slog might add later
		if err, ok := v.Any().(error); ok {
			return err.Error(), nil
		}
		return fmt.Sprintf("%v", v.Any()), nil
	}
}
