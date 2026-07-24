package statebackend

import (
	"bytes"
	"context"
	"sort"
	"testing"
	"time"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// fuzzValidEventKinds is every EventKind that encodeEventKind accepts,
// built once (outside FuzzEventRoundTrip's Fuzz closure) in a stable,
// sorted order so a given kindByte input maps to the same kind across
// runs — reproducibility matters for a saved fuzz failure to replay
// identically.
var fuzzValidEventKinds = func() []kernelv1.EventKind {
	kinds := make([]kernelv1.EventKind, 0, len(eventKindText))
	for k := range eventKindText {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}()

// FuzzEventRoundTrip appends an arbitrary, always-valid-shaped Event to a
// fresh session and reads it back via Events(), asserting field equality
// and byte-identical payload. kindByte is mapped modulo into
// fuzzValidEventKinds so every fuzzed input produces a kind AppendEvent
// accepts — this fuzzes the append/scan/decode round trip itself, not
// EventKind validation (event_test.go's TestEncodeEventKind_unspecifiedRejected
// already covers the rejection path). Must never panic, per go-testing.md's
// unit-tier speed budget this creates a fresh t.TempDir() Store per
// iteration to keep each session's event IDs collision-free without
// tracking state across iterations.
func FuzzEventRoundTrip(f *testing.F) {
	f.Add("evt-1", byte(0), []byte("hello"), "v1")
	f.Add("", byte(1), []byte{}, "")
	f.Add("evt-unicode-🎉", byte(255), []byte{0x00, 0xFF, 0x7F}, "schema-v2")
	f.Add("evt-nul\x00id", byte(37), bytes.Repeat([]byte{0xAA}, 4096), "s")

	f.Fuzz(func(t *testing.T, id string, kindByte byte, payload []byte, schemaVersion string) {
		if payload == nil {
			// A nil []byte binds as SQL NULL, which the payload BLOB NOT
			// NULL column rejects — that's a real, correct constraint
			// enforcement, not a round-trip bug, so it's out of scope for
			// this fuzz target. Normalize instead of asserting either way.
			payload = []byte{}
		}
		kind := fuzzValidEventKinds[int(kindByte)%len(fuzzValidEventKinds)]

		st := newTestStore(t)
		sess := createSession(t, st, testSessionMeta())

		wantTimestamp := time.Now()
		ev := Event{
			ID:            id,
			Timestamp:     wantTimestamp,
			Kind:          kind,
			Producer:      testProducer(),
			SchemaVersion: schemaVersion,
			Payload:       payload,
		}

		seq, err := sess.AppendEvent(context.Background(), ev)
		if err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}

		var got []Event
		for e, err := range sess.Events(context.Background()) {
			if err != nil {
				t.Fatalf("Events: %v", err)
			}
			got = append(got, e)
		}
		if len(got) != 1 {
			t.Fatalf("Events returned %d events, want 1", len(got))
		}
		readBack := got[0]

		if readBack.Sequence != seq {
			t.Fatalf("Sequence = %d, want %d", readBack.Sequence, seq)
		}
		if readBack.ID != id {
			t.Fatalf("ID = %q, want %q", readBack.ID, id)
		}
		if readBack.Kind != kind {
			t.Fatalf("Kind = %v, want %v", readBack.Kind, kind)
		}
		if readBack.SchemaVersion != schemaVersion {
			t.Fatalf("SchemaVersion = %q, want %q", readBack.SchemaVersion, schemaVersion)
		}
		if !bytes.Equal(readBack.Payload, payload) {
			t.Fatalf("Payload mismatch: got %d bytes, want %d bytes", len(readBack.Payload), len(payload))
		}
		wantTimestampTrunc := wantTimestamp.UTC().Truncate(time.Millisecond)
		if !readBack.Timestamp.Equal(wantTimestampTrunc) {
			t.Fatalf("Timestamp = %v, want %v (millisecond-truncated)", readBack.Timestamp, wantTimestampTrunc)
		}
		if readBack.Producer == nil ||
			readBack.Producer.GetCategory() != ev.Producer.GetCategory() ||
			readBack.Producer.GetName() != ev.Producer.GetName() ||
			readBack.Producer.GetVersion() != ev.Producer.GetVersion() {
			t.Fatalf("Producer = %+v, want %+v", readBack.Producer, ev.Producer)
		}
	})
}
