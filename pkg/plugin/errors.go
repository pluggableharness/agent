package plugin

import "errors"

// errCallbackBrokerUnset is returned by Callback.Client when called before
// Serve's internal GRPCPlugin adapter has recorded a broker on the
// Callback — i.e. before GRPCServer has run for this plugin process. See
// doc.go's "callback-timing trap" for why Client cannot simply block and
// wait: GRPCServer has to already have returned before the broker it
// hands over is actually servable, so there is no broker to wait for
// until that happens.
var errCallbackBrokerUnset = errors.New("plugin: callback: broker not yet available (Client called before GRPCServer ran)")

// errGRPCClientUnsupported is returned by grpcPlugin.GRPCClient: this
// package only ever runs plugin-side (serving Config.Services back to the
// kernel), never kernel-side (dialing another process's category
// client) — the plugin-side mirror of
// internal/pluginruntime/adapter.go's errGRPCServerUnsupported. Misuse
// fails loudly, at call time, rather than silently no-op-ing.
var errGRPCClientUnsupported = errors.New("plugin: GRPCClient is not supported plugin-side — this package only serves a plugin process, it never dials one")
