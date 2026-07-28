package pluginhost

// Unit tier: the structural guard that callbackSlot forwards every RPC
// the kernel-callback service declares.
//
// This is deliberately separate from TestCallbackSlot_forwardsEveryRPC,
// which proves forwarding *works* for a hand-picked set. That test cannot
// catch a newly added RPC nobody remembered to list in it — and did not:
// the frontend state-surface RPCs (CreateSession and fifteen others) went
// in unforwarded, so a frontend calling CreateSession from its Configure
// handler got the generated "method CreateSession not implemented" stub
// instead of the kernel. Enumerating from the generated descriptor is
// what makes the check exhaustive by construction.

import (
	"os"
	"regexp"
	"testing"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// declaredForwards returns every RPC name slot.go declares a callbackSlot
// method for.
//
// It reads the source rather than reflecting over the type because
// reflection cannot tell a declared method from one promoted out of the
// embedded UnimplementedKernelCallbackServiceServer: Go names the
// promoted wrapper after the outer type, so both appear as
// (*callbackSlot).Name. The distinction is exactly what this test exists
// to make, and only the source has it.
func declaredForwards(t *testing.T) map[string]bool {
	t.Helper()

	src, err := os.ReadFile("slot.go")
	if err != nil {
		t.Fatalf("read slot.go: %v", err)
	}
	re := regexp.MustCompile(`func \(s \*callbackSlot\) (\w+)\(`)
	out := make(map[string]bool)
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		out[m[1]] = true
	}
	return out
}

// TestCallbackSlot_declaresEveryServiceMethod fails with the exact list
// of RPCs missing a forward, so the fix is mechanical.
func TestCallbackSlot_declaresEveryServiceMethod(t *testing.T) {
	t.Parallel()

	declared := declaredForwards(t)
	sd := kernelv1.KernelCallbackService_ServiceDesc

	var missing []string
	for _, m := range sd.Methods {
		if !declared[m.MethodName] {
			missing = append(missing, m.MethodName)
		}
	}
	for _, s := range sd.Streams {
		if !declared[s.StreamName] {
			missing = append(missing, s.StreamName)
		}
	}

	if len(missing) > 0 {
		t.Errorf("slot.go declares no forward for %d RPC(s): %v\n"+
			"Each would silently answer the generated \"method X not implemented\" "+
			"stub instead of reaching internal/kernelcallback. Add a forwarding "+
			"method to slot.go for each.", len(missing), missing)
	}

	if want := len(sd.Methods) + len(sd.Streams); len(declared) < want {
		t.Errorf("slot.go declares %d callbackSlot methods, want at least %d (one per RPC)", len(declared), want)
	}
}
