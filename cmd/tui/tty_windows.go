//go:build windows

package main

import "os"

// openTTY opens the Windows console device for direct read/write.
//
// This is the Windows half of the same constraint the unix build documents:
// stdout carries the go-plugin handshake and is piped into the host's logger,
// so the shell paints to the console device instead. CONIN$/CONOUT$ are the
// console equivalents of /dev/tty, but they are two separate handles rather
// than one bidirectional file, so the caller receives the output handle and
// Bubble Tea opens console input itself.
func openTTY() (*os.File, error) {
	return os.OpenFile("CONOUT$", os.O_RDWR, 0)
}
