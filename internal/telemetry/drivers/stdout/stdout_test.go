package stdout_test

import (
	"context"
	"io"
	"os"
	"testing"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/pluggableharness/agent/internal/telemetry/drivers/stdout"
)

func TestBackend(t *testing.T) {
	t.Parallel()

	b := stdout.New()
	if got := b.Name(); got != "stdout" {
		t.Errorf("Name() = %q, want stdout", got)
	}

	ctx := context.Background()

	exp, err := b.TraceExporter(ctx)
	if err != nil {
		t.Fatalf("TraceExporter: %v", err)
	}
	if err := exp.ExportSpans(ctx, nil); err != nil {
		t.Errorf("ExportSpans: %v", err)
	}
	if err := exp.Shutdown(ctx); err != nil {
		t.Errorf("trace exporter Shutdown: %v", err)
	}

	reader, err := b.MetricReader(ctx)
	if err != nil {
		t.Fatalf("MetricReader: %v", err)
	}
	if err := reader.Shutdown(ctx); err != nil {
		t.Errorf("metric reader Shutdown: %v", err)
	}

	logExp, err := b.LogExporter(ctx)
	if err != nil {
		t.Fatalf("LogExporter: %v", err)
	}
	if err := logExp.Export(ctx, nil); err != nil {
		t.Errorf("Export: %v", err)
	}
	if err := logExp.Shutdown(ctx); err != nil {
		t.Errorf("log exporter Shutdown: %v", err)
	}

	uploader, err := b.TraceUploader(ctx)
	if err != nil {
		t.Fatalf("TraceUploader: %v", err)
	}
	if err := uploader.Start(ctx); err != nil {
		t.Errorf("uploader Start: %v", err)
	}
	if err := uploader.Stop(ctx); err != nil {
		t.Errorf("uploader Stop: %v", err)
	}
}

// TestBackend_traceUploaderWritesToStdout confirms UploadTraces actually
// prints something rather than silently discarding — the one behavior
// that distinguishes this driver's TraceUploader from noop's.
func TestBackend_traceUploaderWritesToStdout(t *testing.T) {
	b := stdout.New()
	uploader, err := b.TraceUploader(context.Background())
	if err != nil {
		t.Fatalf("TraceUploader: %v", err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = orig })

	spans := []*tracepb.ResourceSpans{{}}
	if err := uploader.UploadTraces(context.Background(), spans); err != nil {
		t.Fatalf("UploadTraces: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close write end: %v", err)
	}

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("UploadTraces wrote nothing to stdout")
	}
}
