package metadata

import (
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	metadatav1 "github.com/pluggableharness/agent/pkg/metadata/proto/v1"
)

// BlockOption configures optional fields on a MetadataBlock builder.
type BlockOption func(*metadatav1.MetadataBlock)

// WithPriority sets the block's ordering/eviction hint. Higher wins.
func WithPriority(priority int32) BlockOption {
	return func(b *metadatav1.MetadataBlock) { b.Priority = priority }
}

// WithTone sets the presentation intent token. Defaults to ToneNeutral
// when no option is provided.
func WithTone(tone Tone) BlockOption {
	return func(b *metadatav1.MetadataBlock) { b.Tone = tone }
}

// WithSessionID sets the session the block belongs to. The kernel also
// stamps this from PublishMetadataRequest.session_id; setting it here is
// useful when constructing a block for a bus payload outside a publish call.
func WithSessionID(sessionID string) BlockOption {
	return func(b *metadatav1.MetadataBlock) { b.SessionId = sessionID }
}

func applyOpts(b *metadatav1.MetadataBlock, opts []BlockOption) *metadatav1.MetadataBlock {
	if b.Tone == ToneUnspecified {
		b.Tone = ToneNeutral
	}
	b.Liveness = metadatav1.Liveness_LIVENESS_LIVE
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// KeyValue builds a MetadataBlock with a short labeled value body.
func KeyValue(id, key, value string, opts ...BlockOption) *metadatav1.MetadataBlock {
	return applyOpts(&metadatav1.MetadataBlock{
		Id: id,
		Body: &metadatav1.MetadataBlock_KeyValue{
			KeyValue: &metadatav1.KeyValue{Key: key, Value: value},
		},
	}, opts)
}

// Progress builds a MetadataBlock with a progress body. Pass total <= 0
// for indeterminate progress.
func Progress(id, label string, completed, total int64, opts ...BlockOption) *metadatav1.MetadataBlock {
	body := &metadatav1.Progress{Label: label, Completed: completed}
	if total > 0 {
		body.Total = &total
	}
	return applyOpts(&metadatav1.MetadataBlock{
		Id: id,
		Body: &metadatav1.MetadataBlock_Progress{
			Progress: body,
		},
	}, opts)
}

// Status builds a MetadataBlock with a status body. detail may be empty.
func Status(id, text, detail string, opts ...BlockOption) *metadatav1.MetadataBlock {
	body := &metadatav1.Status{Text: text}
	if detail != "" {
		body.Detail = &detail
	}
	return applyOpts(&metadatav1.MetadataBlock{
		Id: id,
		Body: &metadatav1.MetadataBlock_Status{
			Status: body,
		},
	}, opts)
}

// ItemList builds a MetadataBlock with an ordered list body. title may be empty.
func ItemList(id, title string, items []string, opts ...BlockOption) *metadatav1.MetadataBlock {
	body := &metadatav1.ItemList{Items: items}
	if title != "" {
		body.Title = &title
	}
	return applyOpts(&metadatav1.MetadataBlock{
		Id: id,
		Body: &metadatav1.MetadataBlock_ItemList{
			ItemList: body,
		},
	}, opts)
}

// Timer builds a MetadataBlock with a timer body starting at startedAt.
// deadline and duration are optional (zero values omitted).
func Timer(id, label string, startedAt time.Time, deadline time.Time, duration time.Duration, opts ...BlockOption) *metadatav1.MetadataBlock {
	body := &metadatav1.Timer{StartedAt: timestamppb.New(startedAt)}
	if label != "" {
		body.Label = &label
	}
	if !deadline.IsZero() {
		body.Deadline = timestamppb.New(deadline)
	}
	if duration > 0 {
		body.Duration = durationpb.New(duration)
	}
	return applyOpts(&metadatav1.MetadataBlock{
		Id: id,
		Body: &metadatav1.MetadataBlock_Timer{
			Timer: body,
		},
	}, opts)
}
