package metadata

import (
	"fmt"
	"sort"
	"sync"

	"google.golang.org/protobuf/proto"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	metadatav1 "github.com/pluggableharness/agent/pkg/metadata/proto/v1"
)

// Topic is the event-bus topic the kernel publishes metadata changes on.
// session_id lives in the payload, not the topic, to keep topic cardinality
// bounded (event-bus.md / plan: topics without session_id segments).
const Topic = "kernel.metadata"

// Store is an in-memory, per-process MetadataBlock collection keyed by
// (session_id, block_id). Safe for concurrent use.
type Store struct {
	mu sync.RWMutex
	// blocks[sessionID][blockID] = block
	blocks map[string]map[string]*metadatav1.MetadataBlock
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{blocks: make(map[string]map[string]*metadatav1.MetadataBlock)}
}

// Publish upserts block under sessionID, stamping producer and
// liveness=LIVE. Returns a clone of the stored block.
func (s *Store) Publish(sessionID string, producer *commonv1.ProducerRef, block *metadatav1.MetadataBlock) (*metadatav1.MetadataBlock, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("metadata: publish: session_id is required")
	}
	if block == nil || block.GetId() == "" {
		return nil, fmt.Errorf("metadata: publish: block id is required")
	}
	if block.GetBody() == nil {
		return nil, fmt.Errorf("metadata: publish: block body is required")
	}

	stored := proto.Clone(block).(*metadatav1.MetadataBlock)
	stored.SessionId = sessionID
	stored.Producer = producer
	stored.Liveness = metadatav1.Liveness_LIVENESS_LIVE
	if stored.Tone == metadatav1.Tone_TONE_UNSPECIFIED {
		stored.Tone = metadatav1.Tone_TONE_NEUTRAL
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blocks[sessionID] == nil {
		s.blocks[sessionID] = make(map[string]*metadatav1.MetadataBlock)
	}
	s.blocks[sessionID][stored.GetId()] = stored
	return proto.Clone(stored).(*metadatav1.MetadataBlock), nil
}

// Retract flips the named block to DISCONNECTED. Returns the updated
// block, or an error if it was never published.
func (s *Store) Retract(sessionID, blockID string) (*metadatav1.MetadataBlock, error) {
	if sessionID == "" || blockID == "" {
		return nil, fmt.Errorf("metadata: retract: session_id and block_id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	byID := s.blocks[sessionID]
	if byID == nil || byID[blockID] == nil {
		return nil, fmt.Errorf("metadata: retract: block %q not found in session %q", blockID, sessionID)
	}
	stored := proto.Clone(byID[blockID]).(*metadatav1.MetadataBlock)
	stored.Liveness = metadatav1.Liveness_LIVENESS_DISCONNECTED
	byID[blockID] = stored
	return proto.Clone(stored).(*metadatav1.MetadataBlock), nil
}

// DisconnectProducer flips every LIVE block owned by producer in
// sessionID to DISCONNECTED. Returns the updated blocks (may be empty).
func (s *Store) DisconnectProducer(sessionID string, producer *commonv1.ProducerRef) []*metadatav1.MetadataBlock {
	if sessionID == "" || producer == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	byID := s.blocks[sessionID]
	if byID == nil {
		return nil
	}
	out := make([]*metadatav1.MetadataBlock, 0)
	for id, b := range byID {
		if b.GetLiveness() != metadatav1.Liveness_LIVENESS_LIVE {
			continue
		}
		if !sameProducer(b.GetProducer(), producer) {
			continue
		}
		stored := proto.Clone(b).(*metadatav1.MetadataBlock)
		stored.Liveness = metadatav1.Liveness_LIVENESS_DISCONNECTED
		byID[id] = stored
		out = append(out, proto.Clone(stored).(*metadatav1.MetadataBlock))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetId() < out[j].GetId() })
	return out
}

// List returns every block for sessionID in stable id order.
func (s *Store) List(sessionID string) []*metadatav1.MetadataBlock {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byID := s.blocks[sessionID]
	if len(byID) == 0 {
		return nil
	}
	out := make([]*metadatav1.MetadataBlock, 0, len(byID))
	for _, b := range byID {
		out = append(out, proto.Clone(b).(*metadatav1.MetadataBlock))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetId() < out[j].GetId() })
	return out
}

func sameProducer(a, b *commonv1.ProducerRef) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.GetCategory() == b.GetCategory() &&
		a.GetName() == b.GetName() &&
		a.GetVersion() == b.GetVersion()
}
