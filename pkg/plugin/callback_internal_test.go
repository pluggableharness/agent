package plugin

import (
	"errors"
	"testing"

	"github.com/hashicorp/go-plugin"

	"github.com/pluggableharness/agent/pkg/kernel"
)

// This file is a white-box (package plugin, not plugin_test) test
// deliberately: Callback's dial-then-wrap behavior cannot be fully unit
// tested without a real *plugin.GRPCBroker (its only constructor is
// unexported and requires an unexported streamer type this package cannot
// supply from outside hashicorp/go-plugin — the identical, already-
// confirmed limitation pkg/kernel/client.go's Dial doc comment documents
// for its own case). What is tested here — via the unexported dial func
// field — is the sync.Once guard and error-wrapping behavior around
// whatever kernel.Dial would have done, which is exactly the seam
// callback.go's dial field exists to expose for testing.

func TestCallback_Client_beforeBrokerSet(t *testing.T) {
	t.Parallel()

	c := NewCallback()

	_, err := c.Client(t.Context())
	if !errors.Is(err, errCallbackBrokerUnset) {
		t.Fatalf("Client() error = %v, want wrapping errCallbackBrokerUnset", err)
	}
}

func TestCallback_Client_dialsOnce(t *testing.T) {
	t.Parallel()

	calls := 0
	want := kernel.NewClient(nil)
	c := &Callback{
		dial: func(*plugin.GRPCBroker) (*kernel.Client, error) {
			calls++
			return want, nil
		},
	}
	c.setBroker(&plugin.GRPCBroker{})

	for i := range 3 {
		got, err := c.Client(t.Context())
		if err != nil {
			t.Fatalf("Client() call %d: error = %v, want nil", i, err)
		}
		if got != want {
			t.Errorf("Client() call %d = %v, want %v", i, got, want)
		}
	}
	if calls != 1 {
		t.Errorf("dial called %d times, want 1 (sync.Once)", calls)
	}
}

func TestCallback_Client_dialError(t *testing.T) {
	t.Parallel()

	dialErr := errors.New("dial boom")
	calls := 0
	c := &Callback{
		dial: func(*plugin.GRPCBroker) (*kernel.Client, error) {
			calls++
			return nil, dialErr
		},
	}
	c.setBroker(&plugin.GRPCBroker{})

	for i := range 2 {
		_, err := c.Client(t.Context())
		if !errors.Is(err, dialErr) {
			t.Fatalf("Client() call %d: error = %v, want wrapping %v", i, err, dialErr)
		}
	}
	if calls != 1 {
		t.Errorf("dial called %d times, want 1 (sync.Once — a failed dial is not retried)", calls)
	}
}
