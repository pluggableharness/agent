//go:build !windows

package main

import "os"

// openTTY opens the controlling terminal for direct read/write.
//
// The shell must never render to stdout or read stdin: when it runs as a
// go-plugin subprocess the handshake line is written to stdout and the host
// pipes stdout and stderr into its own logger, so painting there would corrupt
// the handshake and reading there would compete with the plugin transport.
// Opening the controlling terminal sidesteps both.
func openTTY() (*os.File, error) {
	return os.OpenFile("/dev/tty", os.O_RDWR, 0)
}
