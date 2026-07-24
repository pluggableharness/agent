// Package log implements the kernel side of the plugin-to-kernel Log
// callback described in specifications/kernel-callbacks.md §5: it turns a
// wire-level LogEntry (pluggableharness.log.v1) arriving over the KernelCallbackService
// (pluggableharness.kernel.v1) into real log/slog output, so a plugin's own log lines
// reach the kernel's centralized logging instead of vanishing into an
// unread subprocess stderr.
//
// This package is deliberately standalone: it implements only the Log RPC,
// not the full KernelCallbackServiceServer interface (RunSession,
// CountTokens, and Emit belong to other, not-yet-built packages). A future
// composed server delegates its Log method straight to Server.Log here.
package log
