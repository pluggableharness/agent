package metadata_test

import (
	"testing"

	"github.com/pluggableharness/agent/internal/metadata"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	metabuilders "github.com/pluggableharness/agent/pkg/metadata"
	metadatav1 "github.com/pluggableharness/agent/pkg/metadata/proto/v1"
)

func producer(name string) *commonv1.ProducerRef {
	return &commonv1.ProducerRef{
		Category: commonv1.Category_CATEGORY_WIDGET,
		Name:     name,
		Version:  "1.0.0",
	}
}

func TestPublishAndList(t *testing.T) {
	t.Parallel()

	s := metadata.NewStore()
	block := metabuilders.KeyValue("branch", "branch", "main")
	got, err := s.Publish("sess-1", producer("git"), block)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got.GetProducer().GetName() != "git" {
		t.Errorf("producer = %v, want git", got.GetProducer())
	}
	if got.GetLiveness() != metadatav1.Liveness_LIVENESS_LIVE {
		t.Errorf("liveness = %v, want LIVE", got.GetLiveness())
	}
	if got.GetSessionId() != "sess-1" {
		t.Errorf("session_id = %q", got.GetSessionId())
	}

	list := s.List("sess-1")
	if len(list) != 1 || list[0].GetId() != "branch" {
		t.Fatalf("List = %+v", list)
	}
}

func TestPublishUpserts(t *testing.T) {
	t.Parallel()

	s := metadata.NewStore()
	p := producer("git")
	if _, err := s.Publish("s", p, metabuilders.KeyValue("branch", "branch", "main")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("s", p, metabuilders.KeyValue("branch", "branch", "develop")); err != nil {
		t.Fatal(err)
	}
	list := s.List("s")
	if len(list) != 1 || list[0].GetKeyValue().GetValue() != "develop" {
		t.Fatalf("upsert = %+v", list)
	}
}

func TestRetract(t *testing.T) {
	t.Parallel()

	s := metadata.NewStore()
	p := producer("git")
	if _, err := s.Publish("s", p, metabuilders.KeyValue("b", "k", "v")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Retract("s", "b")
	if err != nil {
		t.Fatalf("Retract: %v", err)
	}
	if got.GetLiveness() != metadatav1.Liveness_LIVENESS_DISCONNECTED {
		t.Errorf("liveness = %v, want DISCONNECTED", got.GetLiveness())
	}
	// Still listed — never deleted.
	if len(s.List("s")) != 1 {
		t.Fatalf("retract deleted the block")
	}
}

func TestRetractMissing(t *testing.T) {
	t.Parallel()

	s := metadata.NewStore()
	if _, err := s.Retract("s", "nope"); err == nil {
		t.Fatal("expected error for missing block")
	}
}

func TestDisconnectProducer(t *testing.T) {
	t.Parallel()

	s := metadata.NewStore()
	git := producer("git")
	ctx := producer("context")
	if _, err := s.Publish("s", git, metabuilders.KeyValue("g", "k", "v")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("s", ctx, metabuilders.Status("c", "ok", "")); err != nil {
		t.Fatal(err)
	}
	out := s.DisconnectProducer("s", git)
	if len(out) != 1 || out[0].GetId() != "g" {
		t.Fatalf("DisconnectProducer = %+v", out)
	}
	if out[0].GetLiveness() != metadatav1.Liveness_LIVENESS_DISCONNECTED {
		t.Errorf("git block liveness = %v", out[0].GetLiveness())
	}
	// context block still LIVE
	for _, b := range s.List("s") {
		if b.GetId() == "c" && b.GetLiveness() != metadatav1.Liveness_LIVENESS_LIVE {
			t.Errorf("context block should stay LIVE")
		}
	}
}

func TestListStableOrder(t *testing.T) {
	t.Parallel()

	s := metadata.NewStore()
	p := producer("w")
	for _, id := range []string{"c", "a", "b"} {
		if _, err := s.Publish("s", p, metabuilders.KeyValue(id, id, id)); err != nil {
			t.Fatal(err)
		}
	}
	list := s.List("s")
	if len(list) != 3 || list[0].GetId() != "a" || list[1].GetId() != "b" || list[2].GetId() != "c" {
		t.Fatalf("order = %v", []string{list[0].GetId(), list[1].GetId(), list[2].GetId()})
	}
}
