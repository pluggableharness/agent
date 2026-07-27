package modeltest

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"

	"github.com/hashicorp/go-hclog"
)

// errServeUnsupported is returned by the kernel-side plugin adapter's
// GRPCServer, which is never reached: this package only ever runs on the
// launching side of the connection.
var errServeUnsupported = errors.New("modeltest: this adapter only runs kernel-side and never serves")

// commandContext builds the subprocess command for a plugin binary.
//
// The environment is a deliberate allowlist rather than os.Environ(),
// mirroring what the real launcher does: a plugin that only works because
// it inherited a credential from the test runner's environment would pass
// here and fail under the kernel, which is the opposite of what a
// conformance run is for. A provider needing configuration receives it
// through Configure, via WithConfig.
func commandContext(ctx context.Context, binaryPath string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binaryPath) // #nosec G204 -- the caller names the binary under test; that is this function's entire purpose
	cmd.Env = allowedEnv()
	return cmd
}

// allowedEnv returns the minimal environment a launched plugin gets.
func allowedEnv() []string {
	env := make([]string, 0, 3)
	for _, key := range []string{"PATH", "HOME", "TMPDIR"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	return env
}

// discardLogger silences go-plugin's own subprocess-management chatter.
//
// It is handshake and process bookkeeping, not the plugin's application
// output, and surfacing it would bury a conformance failure in noise. A
// plugin's own logs cross the kernel-callback channel, which a
// conformance run does not serve.
func discardLogger() hclog.Logger {
	return hclog.New(&hclog.LoggerOptions{Output: io.Discard, Level: hclog.Off})
}
