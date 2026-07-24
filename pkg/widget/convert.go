package widget

import (
	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"
)

// toProtoCapabilities converts a domain Capabilities to its wire
// representation, for Service.GetCapabilities' response.
func toProtoCapabilities(caps Capabilities) *widgetv1.WidgetCapabilities {
	return &widgetv1.WidgetCapabilities{
		Regions:             caps.Regions,
		ConfigSchema:        caps.ConfigSchema,
		SupportedHookPoints: caps.SupportedHookPoints,
	}
}

// fromProtoAttachRequest converts a wire AttachRequest to its domain
// representation, for handing to Provider.Attach.
func fromProtoAttachRequest(req *widgetv1.AttachRequest) AttachRequest {
	return AttachRequest{SessionID: req.GetSessionId()}
}

// toProtoUpdate converts a domain Update to its wire representation,
// translating Mode to the wire's bare Replace bool (UpdateReplace -> true,
// UpdateAppend -> false) for UpdateSender.Send.
func toProtoUpdate(u Update) *widgetv1.WidgetUpdate {
	return &widgetv1.WidgetUpdate{
		Region:  u.Region,
		Content: u.Content,
		Replace: u.Mode == UpdateReplace,
	}
}
