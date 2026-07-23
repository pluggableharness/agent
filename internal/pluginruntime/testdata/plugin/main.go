//go:build integration

// Command plugin is the minimal fixture internal/pluginruntime's
// integration tier (launch_integration_test.go) builds and launches as a
// real subprocess, to exercise a genuine hashicorp/go-plugin round-trip:
// one canned ToolService.GetSchema RPC, plus one callback into
// KernelCallbackService.Log over the fixed callback broker ID
// (pkg/common.CallbackBrokerID), proving the reverse channel.
//
// This is the one place in internal/pluginruntime that implements the
// plugin *side* of the go-plugin adapter — purely for this fixture, never
// a template for a real plugin SDK (see ../../CLAUDE.md). Build-tagged
// integration so it never enters the default `go build ./...` (which
// already skips testdata/ regardless).
package main

import (
	"context"
	"errors"
	"time"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/pluggableharness/agent/pkg/common"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	logv1 "github.com/pluggableharness/agent/pkg/log/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// fixtureToolName is the single tool GetSchema reports — checked by
// launch_integration_test.go to confirm the RPC actually round-tripped
// through the real subprocess rather than returning a zero value some
// other way.
const fixtureToolName = "fixture_echo"

// toolServer is the canned ToolServiceServer this fixture serves.
type toolServer struct {
	toolv1.UnimplementedToolServiceServer
}

// GetSchema returns a single, fixed ToolSchema — the "one canned RPC"
// this fixture exists to round-trip.
func (toolServer) GetSchema(context.Context, *toolv1.GetSchemaRequest) (*toolv1.GetSchemaResponse, error) {
	return &toolv1.GetSchemaResponse{
		Tools: []*toolv1.ToolSchema{
			{
				Name:        fixtureToolName,
				Kind:        toolv1.ToolKind_TOOL_KIND_RESOURCE,
				Risk:        toolv1.RiskClass_RISK_CLASS_LOW,
				Description: "internal/pluginruntime integration fixture",
			},
		},
	}, nil
}

// toolPlugin is the plugin-side half of the go-plugin adapter for
// ToolService.
type toolPlugin struct {
	plugin.Plugin
}

var _ plugin.GRPCPlugin = (*toolPlugin)(nil)

// GRPCServer registers toolServer, then — in the background, since the
// kernel doesn't start serving the callback broker until it dispenses
// this plugin's client, which happens after GRPCServer must have already
// returned — dials the fixed callback broker ID and calls Log once.
func (toolPlugin) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	s.RegisterService(&toolv1.ToolService_ServiceDesc, toolServer{})

	go func() {
		conn, err := broker.Dial(common.CallbackBrokerID)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		client := kernelv1.NewKernelCallbackServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = client.Log(ctx, &kernelv1.LogRequest{
			Entry: &logv1.LogEntry{
				Level:   logv1.LogLevel_LOG_LEVEL_INFO,
				Logger:  "pluginruntime.testdata.plugin",
				Message: "fixture plugin started",
				Time:    timestamppb.Now(),
			},
		})
	}()

	return nil
}

// GRPCClient is never called plugin-side; this fixture only serves.
func (toolPlugin) GRPCClient(context.Context, *plugin.GRPCBroker, *grpc.ClientConn) (any, error) {
	return nil, errors.New("testdata/plugin: GRPCClient is not used plugin-side")
}

func main() {
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: common.Handshake,
		Plugins: plugin.PluginSet{
			common.PluginKey(commonv1.Category_CATEGORY_TOOL): &toolPlugin{},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}
