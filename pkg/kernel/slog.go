package kernel

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	logv1 "github.com/pluggableharness/agent/pkg/log/proto/v1"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// defaultFlushInterval and defaultMaxBatchSize are NewSlogHandler's
// fallback batching parameters when not overridden via
// WithFlushInterval/WithMaxBatchSize — chosen so a plugin logging at a
// normal rate sees its entries reach the kernel within a second, while a
// plugin logging in a tight loop (e.g. at TRACE) doesn't pay one RPC per
// line.
const (
	defaultFlushInterval = time.Second
	defaultMaxBatchSize  = 100
)

// slogSink is the shared, mutex-guarded state a SlogHandler and every
// handler WithAttrs/WithGroup derives from it all flush through — split
// out from SlogHandler itself because WithAttrs/WithGroup return a new
// SlogHandler value (per slog's own documented handler-derivation
// pattern), and a sync.Mutex must never be copied
// (go vet's copylocks check) — see SlogHandler's own doc comment.
type slogSink struct {
	client        *Client
	sessionID     *string
	flushInterval time.Duration
	maxBatch      int

	mu      sync.Mutex
	pending []*logv1.LogEntry

	closeOnce sync.Once
	done      chan struct{}
	wg        sync.WaitGroup
}

// SlogHandlerOption configures NewSlogHandler.
type SlogHandlerOption func(*slogSink)

// WithSessionID attaches session_id to every batch this handler flushes
// (kernel-callbacks.md#log: MAY be omitted, set when log output is
// attributable to a specific session). Omit this option for
// startup/shutdown/Configure-time logging that predates or outlives any
// session.
func WithSessionID(sessionID string) SlogHandlerOption {
	return func(s *slogSink) { s.sessionID = &sessionID }
}

// WithFlushInterval overrides how often a SlogHandler flushes its pending
// batch on a timer, regardless of size. Omitting this option leaves
// defaultFlushInterval.
func WithFlushInterval(d time.Duration) SlogHandlerOption {
	return func(s *slogSink) {
		if d > 0 {
			s.flushInterval = d
		}
	}
}

// WithMaxBatchSize overrides how many pending entries trigger an
// immediate flush from Handle, ahead of the next timer tick. Omitting
// this option leaves defaultMaxBatchSize.
func WithMaxBatchSize(n int) SlogHandlerOption {
	return func(s *slogSink) {
		if n > 0 {
			s.maxBatch = n
		}
	}
}

// SlogHandler is a log/slog.Handler that batches records and flushes them
// via Log (kernel-callbacks.md#log) — the logging half of this package's
// plugin-author-facing surface (see the package doc comment). Construct
// one with (*Client).NewSlogHandler; the zero value is not usable.
//
// SlogHandler carries no lock of its own — sink is a pointer shared by
// every handler value WithAttrs/WithGroup derives, so deriving a new
// handler (slog's own With/WithGroup mechanism) is a cheap value copy,
// never a lock copy.
type SlogHandler struct {
	sink        *slogSink
	level       *slog.Level // nil: fall back to sink.client.LogLevel() via wireToLevel
	groupPrefix string
	attrs       []slog.Attr
}

// NewSlogHandler returns a SlogHandler flushing batches through c. A
// background goroutine flushes on flushInterval (defaultFlushInterval
// unless overridden); Handle also flushes immediately once maxBatch
// entries are pending (defaultMaxBatchSize unless overridden). Call Close
// before the plugin process exits so any still-pending entries aren't
// lost.
func (c *Client) NewSlogHandler(opts ...SlogHandlerOption) *SlogHandler {
	sink := &slogSink{
		client:        c,
		flushInterval: defaultFlushInterval,
		maxBatch:      defaultMaxBatchSize,
		done:          make(chan struct{}),
	}
	for _, opt := range opts {
		opt(sink)
	}
	sink.wg.Add(1)
	go sink.flushLoop()
	return &SlogHandler{sink: sink}
}

// Enabled reports whether level is at or above this handler's own level
// (WithLevel), or, absent that, the kernel-reported floor
// (Client.LogLevel, via LoadTelemetryConfig) translated to an slog.Level.
// A Client that never called LoadTelemetryConfig reports LOG_LEVEL_INFO
// (Client.LogLevel's own documented default), so Enabled is never
// unconditionally true before a plugin bootstraps its telemetry config.
func (h *SlogHandler) Enabled(_ context.Context, level slog.Level) bool {
	if h.level != nil {
		return level >= *h.level
	}
	return level >= wireToLevel(h.sink.client.LogLevel())
}

// WithLevel overrides the level Enabled compares against, ahead of the
// kernel-reported floor. Returns a new *SlogHandler; the receiver is
// unchanged.
func (h *SlogHandler) WithLevel(level slog.Level) *SlogHandler {
	h2 := *h
	h2.level = &level
	return &h2
}

// WithAttrs implements slog.Handler: returns a new handler whose Handle
// calls include attrs on every record, in addition to that call's own
// attrs. Keys are prefixed with the handler's current group, matching
// slog's own documented WithGroup/WithAttrs interaction.
func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	merged := make([]slog.Attr, len(h.attrs), len(h.attrs)+len(attrs))
	copy(merged, h.attrs)
	for _, a := range attrs {
		if h.groupPrefix != "" {
			a = slog.Attr{Key: h.groupPrefix + "." + a.Key, Value: a.Value}
		}
		merged = append(merged, a)
	}
	h2 := *h
	h2.attrs = merged
	return &h2
}

// WithGroup implements slog.Handler: returns a new handler whose
// subsequent WithAttrs-accumulated (and per-call Handle) attribute keys
// are prefixed with name, dot-joined with any existing group prefix.
func (h *SlogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	h2 := *h
	if h2.groupPrefix == "" {
		h2.groupPrefix = name
	} else {
		h2.groupPrefix = h2.groupPrefix + "." + name
	}
	return &h2
}

// Handle implements slog.Handler: converts r into a wire LogEntry
// (prefixing r's own attrs with this handler's group, same as WithAttrs
// above) and queues it, flushing immediately if the pending batch has
// reached sink.maxBatch.
func (h *SlogHandler) Handle(ctx context.Context, r slog.Record) error {
	fieldAttrs := make([]slog.Attr, 0, len(h.attrs)+r.NumAttrs())
	fieldAttrs = append(fieldAttrs, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		if h.groupPrefix != "" {
			a = slog.Attr{Key: h.groupPrefix + "." + a.Key, Value: a.Value}
		}
		fieldAttrs = append(fieldAttrs, a)
		return true
	})

	fields, err := structFromAttrs(fieldAttrs)
	if err != nil {
		return fmt.Errorf("kernel: handle: %w", err)
	}

	entry := &logv1.LogEntry{
		Level:   levelToWire(r.Level),
		Message: r.Message,
		Fields:  fields,
		Time:    timestamppb.New(r.Time),
	}

	h.sink.mu.Lock()
	h.sink.pending = append(h.sink.pending, entry)
	full := len(h.sink.pending) >= h.sink.maxBatch
	h.sink.mu.Unlock()

	if full {
		return h.Flush(ctx)
	}
	return nil
}

// Flush sends every currently-pending entry via one Log call, regardless
// of the timer or maxBatch threshold. A no-op returning nil if nothing is
// pending.
func (h *SlogHandler) Flush(ctx context.Context) error {
	return h.sink.flush(ctx)
}

// Close stops the background flush timer and flushes any remaining
// pending entries. Idempotent — safe to call more than once. Call this
// before the plugin process exits.
func (h *SlogHandler) Close() error {
	h.sink.closeOnce.Do(func() {
		close(h.sink.done)
	})
	h.sink.wg.Wait()
	return h.sink.flush(context.Background())
}

func (s *slogSink) flush(ctx context.Context) error {
	s.mu.Lock()
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return nil
	}
	batch := s.pending
	s.pending = nil
	s.mu.Unlock()

	if _, err := s.client.raw.Log(ctx, &kernelv1.LogRequest{SessionId: s.sessionID, Entries: batch}); err != nil {
		return fmt.Errorf("kernel: flush: %w", err)
	}
	return nil
}

func (s *slogSink) flushLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = s.flush(context.Background())
		case <-s.done:
			return
		}
	}
}

var _ slog.Handler = (*SlogHandler)(nil)
