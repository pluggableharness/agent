package kernel

import (
	"context"
	"fmt"
	"sync"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/pluggableharness/agent/pkg/common"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	logv1 "github.com/pluggableharness/agent/pkg/log/proto/v1"
)

// Client wraps the dialed connection to KernelCallbackService — see the
// package doc comment. The zero value is not usable; construct one with
// Dial or NewClient.
type Client struct {
	conn *grpc.ClientConn
	raw  kernelv1.KernelCallbackServiceClient

	mu        sync.RWMutex
	telemetry *kernelv1.GetTelemetryConfigResult // nil until LoadTelemetryConfig succeeds
}

// Dial connects to the kernel-callback service over broker's fixed,
// well-known broker ID (pkg/common.CallbackBrokerID) — the one channel
// every plugin subprocess is handed at handshake, regardless of category
// (kernel-callbacks.md#the-callback-channel). Call this once, typically
// from within a plugin category's own GRPCServer(broker, s) method, and
// keep the returned Client for the process's lifetime.
//
// Dial has no direct unit test: *plugin.GRPCBroker's only constructor is
// unexported and requires an unexported streamer type this package cannot
// supply from outside hashicorp/go-plugin — the identical, already-
// confirmed limitation internal/pluginruntime/CLAUDE.md documents for its
// own categoryPlugin.GRPCClient. NewClient (below), which Dial delegates
// to once it has a real *grpc.ClientConn, carries the actual test
// coverage for this package's dial-then-wrap behavior.
func Dial(broker *plugin.GRPCBroker) (*Client, error) {
	conn, err := broker.Dial(common.CallbackBrokerID)
	if err != nil {
		return nil, fmt.Errorf("kernel: dial: %w", err)
	}
	return NewClient(conn), nil
}

// NewClient wraps an already-dialed connection to KernelCallbackService.
// Most plugin authors want Dial instead; NewClient exists for a caller
// that already has a *grpc.ClientConn in hand (a test, or a caller
// composing its own broker-dial logic).
func NewClient(conn *grpc.ClientConn) *Client {
	return &Client{conn: conn, raw: kernelv1.NewKernelCallbackServiceClient(conn)}
}

// Raw returns the underlying generated client, for calling an RPC this
// package doesn't (yet) wrap ergonomically.
func (c *Client) Raw() kernelv1.KernelCallbackServiceClient {
	return c.raw
}

// Close closes the underlying connection. Rarely needed in practice — the
// broker connection is torn down when the plugin subprocess itself exits
// — but provided for a caller (or a test) managing a Client's lifetime
// explicitly.
func (c *Client) Close() error {
	return c.conn.Close()
}

// LoadTelemetryConfig calls GetTelemetryConfig once and caches the result
// — TracingEnabled/MetricsEnabled/LogsEnabled/LogLevel/SamplingRatio then
// become cached field reads, never a per-call RPC round-trip
// (observability.md#gettelemetryconfig-caching). A plugin SHOULD call
// this once at startup, typically from the same bootstrap call that
// wires its OTel SDK pipeline. Safe to call again later (e.g. to pick up
// a changed operator configuration), though the operator config this
// answers from is not expected to change mid-process in practice.
func (c *Client) LoadTelemetryConfig(ctx context.Context) error {
	result, err := c.raw.GetTelemetryConfig(ctx, &kernelv1.GetTelemetryConfigRequest{})
	if err != nil {
		return fmt.Errorf("kernel: load telemetry config: %w", err)
	}
	c.mu.Lock()
	c.telemetry = result
	c.mu.Unlock()
	return nil
}

// TracingEnabled reports whether trace export is on, per the last
// LoadTelemetryConfig call. Reports false if LoadTelemetryConfig has
// never been called — a plugin that skips loading configuration gets the
// conservative "don't export" default rather than an unconfigured true.
func (c *Client) TracingEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.telemetry.GetTracesEnabled()
}

// MetricsEnabled reports whether metrics export is on, per the last
// LoadTelemetryConfig call. Same not-yet-loaded default as TracingEnabled.
func (c *Client) MetricsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.telemetry.GetMetricsEnabled()
}

// LogsEnabled reports whether log export is on, per the last
// LoadTelemetryConfig call. Same not-yet-loaded default as TracingEnabled.
// Note this governs whether the *kernel* forwards logs to its configured
// backend, not whether Log itself is callable — a plugin MAY still call
// Log (via NewSlogHandler or directly) regardless of this value.
func (c *Client) LogsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.telemetry.GetLogsEnabled()
}

// LogLevel returns the operator's configured log-level floor, per the
// last LoadTelemetryConfig call. Defaults to LOG_LEVEL_INFO if
// LoadTelemetryConfig has never been called, matching
// configuration/blocks-reference.md's own documented settings.log_level
// default.
func (c *Client) LogLevel() logv1.LogLevel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.telemetry == nil {
		return logv1.LogLevel_LOG_LEVEL_INFO
	}
	return c.telemetry.GetLogLevel()
}

// SamplingRatio returns the operator's configured trace sampling ratio,
// per the last LoadTelemetryConfig call. Defaults to 1.0 (sample
// everything) if LoadTelemetryConfig has never been called — the same
// full-sampling default telemetry.DefaultConfig uses kernel-side.
func (c *Client) SamplingRatio() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.telemetry == nil {
		return 1.0
	}
	return c.telemetry.GetSamplingRatio()
}
