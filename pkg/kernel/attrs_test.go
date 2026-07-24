package kernel

import (
	"errors"
	"log/slog"
	"testing"
	"time"
)

func TestStructFromAttrs_empty(t *testing.T) {
	t.Parallel()
	got, err := structFromAttrs(nil)
	if err != nil {
		t.Fatalf("structFromAttrs(nil): %v", err)
	}
	if got != nil {
		t.Fatalf("structFromAttrs(nil) = %v, want nil", got)
	}
}

func TestStructFromAttrs_scalars(t *testing.T) {
	t.Parallel()

	attrs := []slog.Attr{
		slog.Bool("flag", true),
		slog.Int64("count", 42),
		slog.Uint64("uc", 7),
		slog.Float64("ratio", 0.5),
		slog.String("name", "hello"),
	}
	got, err := structFromAttrs(attrs)
	if err != nil {
		t.Fatalf("structFromAttrs: %v", err)
	}
	fields := got.GetFields()
	if !fields["flag"].GetBoolValue() {
		t.Error("flag != true")
	}
	if fields["count"].GetNumberValue() != 42 {
		t.Error("count != 42")
	}
	if fields["name"].GetStringValue() != "hello" {
		t.Error("name != hello")
	}
}

func TestStructFromAttrs_durationAndTime(t *testing.T) {
	t.Parallel()

	d := 5 * time.Second
	tm := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	attrs := []slog.Attr{
		slog.Duration("d", d),
		slog.Time("t", tm),
	}
	got, err := structFromAttrs(attrs)
	if err != nil {
		t.Fatalf("structFromAttrs: %v", err)
	}
	fields := got.GetFields()
	if fields["d"].GetStringValue() != d.String() {
		t.Errorf("d = %q, want %q", fields["d"].GetStringValue(), d.String())
	}
	if fields["t"].GetStringValue() != tm.Format(time.RFC3339Nano) {
		t.Errorf("t = %q, want %q", fields["t"].GetStringValue(), tm.Format(time.RFC3339Nano))
	}
}

func TestStructFromAttrs_group(t *testing.T) {
	t.Parallel()

	attrs := []slog.Attr{
		slog.Group("req", slog.String("method", "GET"), slog.Int("status", 200)),
	}
	got, err := structFromAttrs(attrs)
	if err != nil {
		t.Fatalf("structFromAttrs: %v", err)
	}
	nested := got.GetFields()["req"].GetStructValue()
	if nested == nil {
		t.Fatal("req is not a nested struct")
	}
	if nested.GetFields()["method"].GetStringValue() != "GET" {
		t.Errorf("req.method = %v, want GET", nested.GetFields()["method"])
	}
	if nested.GetFields()["status"].GetNumberValue() != 200 {
		t.Errorf("req.status = %v, want 200", nested.GetFields()["status"])
	}
}

func TestStructFromAttrs_error(t *testing.T) {
	t.Parallel()

	attrs := []slog.Attr{slog.Any("err", errors.New("boom"))}
	got, err := structFromAttrs(attrs)
	if err != nil {
		t.Fatalf("structFromAttrs: %v", err)
	}
	if got.GetFields()["err"].GetStringValue() != "boom" {
		t.Errorf("err = %v, want boom", got.GetFields()["err"])
	}
}

func TestStructFromAttrs_logValuer(t *testing.T) {
	t.Parallel()

	attrs := []slog.Attr{slog.Any("v", fakeLogValuer{})}
	got, err := structFromAttrs(attrs)
	if err != nil {
		t.Fatalf("structFromAttrs: %v", err)
	}
	if got.GetFields()["v"].GetStringValue() != "resolved" {
		t.Errorf("v = %v, want resolved (LogValuer must be resolved)", got.GetFields()["v"])
	}
}

func TestStructFromAttrs_skipsZeroAttr(t *testing.T) {
	t.Parallel()

	got, err := structFromAttrs([]slog.Attr{{}, slog.String("real", "x")})
	if err != nil {
		t.Fatalf("structFromAttrs: %v", err)
	}
	if len(got.GetFields()) != 1 {
		t.Fatalf("got %d fields, want 1 (the zero Attr must be skipped)", len(got.GetFields()))
	}
}

// fakeLogValuer is a hand-written slog.LogValuer fake (go-testing.md).
type fakeLogValuer struct{}

func (fakeLogValuer) LogValue() slog.Value { return slog.StringValue("resolved") }
