package metadata_test

import (
	"testing"
	"time"

	"github.com/pluggableharness/agent/pkg/metadata"
	metadatav1 "github.com/pluggableharness/agent/pkg/metadata/proto/v1"
)

func TestToneByName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want metadata.Tone
		ok   bool
	}{
		{"neutral", "neutral", metadata.ToneNeutral, true},
		{"warning", "warning", metadata.ToneWarning, true},
		{"unknown falls back", "chartreuse", metadata.ToneNeutral, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := metadata.ToneByName(tt.in)
			if ok != tt.ok || got != tt.want {
				t.Errorf("ToneByName(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestKeyValue(t *testing.T) {
	t.Parallel()

	b := metadata.KeyValue("branch", "branch", "main",
		metadata.WithPriority(10),
		metadata.WithTone(metadata.ToneInfo),
		metadata.WithSessionID("sess-1"),
	)
	if b.GetId() != "branch" {
		t.Errorf("id = %q, want branch", b.GetId())
	}
	if b.GetPriority() != 10 || b.GetTone() != metadata.ToneInfo {
		t.Errorf("priority/tone = %d/%v", b.GetPriority(), b.GetTone())
	}
	if b.GetSessionId() != "sess-1" {
		t.Errorf("session_id = %q", b.GetSessionId())
	}
	if b.GetLiveness() != metadatav1.Liveness_LIVENESS_LIVE {
		t.Errorf("liveness = %v, want LIVE", b.GetLiveness())
	}
	kv := b.GetKeyValue()
	if kv == nil || kv.GetKey() != "branch" || kv.GetValue() != "main" {
		t.Errorf("body = %+v", kv)
	}
}

func TestProgressIndeterminate(t *testing.T) {
	t.Parallel()

	b := metadata.Progress("p1", "loading", 3, 0)
	p := b.GetProgress()
	if p == nil || p.GetCompleted() != 3 || p.Total != nil {
		t.Errorf("progress = %+v, want completed=3 no total", p)
	}
}

func TestProgressDeterminate(t *testing.T) {
	t.Parallel()

	b := metadata.Progress("p1", "loading", 3, 10)
	p := b.GetProgress()
	if p == nil || p.GetTotal() != 10 {
		t.Errorf("progress total = %v, want 10", p.GetTotal())
	}
}

func TestStatusAndItemList(t *testing.T) {
	t.Parallel()

	st := metadata.Status("s1", "ok", "detail")
	if st.GetStatus().GetText() != "ok" || st.GetStatus().GetDetail() != "detail" {
		t.Errorf("status = %+v", st.GetStatus())
	}
	il := metadata.ItemList("l1", "todos", []string{"a", "b"})
	if il.GetItemList().GetTitle() != "todos" || len(il.GetItemList().GetItems()) != 2 {
		t.Errorf("item_list = %+v", il.GetItemList())
	}
}

func TestTimer(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	deadline := start.Add(time.Minute)
	b := metadata.Timer("t1", "turn", start, deadline, 30*time.Second)
	tm := b.GetTimer()
	if tm == nil || tm.GetLabel() != "turn" {
		t.Fatalf("timer = %+v", tm)
	}
	if !tm.GetStartedAt().AsTime().Equal(start) {
		t.Errorf("started_at = %v", tm.GetStartedAt().AsTime())
	}
	if tm.GetDuration().AsDuration() != 30*time.Second {
		t.Errorf("duration = %v", tm.GetDuration().AsDuration())
	}
}

func TestDefaultToneIsNeutral(t *testing.T) {
	t.Parallel()

	b := metadata.KeyValue("k", "a", "b")
	if b.GetTone() != metadata.ToneNeutral {
		t.Errorf("default tone = %v, want neutral", b.GetTone())
	}
}
