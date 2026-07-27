// Package shell is the reference TUI's frame: layout, focus, keymap, and the
// composition of every region into one painted view.
//
// The shell fills the four gaps the frontend protocol deliberately leaves to
// the implementation — focus, keybindings, terminal resize, and scrollback —
// none of which appear anywhere in docs/specifications/frontend/. Those
// decisions are documented for operators in
// docs/first-party/frontends/tui.md, and the constants and tables here are the
// authority that document describes; changing one means changing both.
//
// The design is message-driven and I/O-free. Model performs no reads or writes
// and never touches a terminal: an EventSource translates whatever it is
// attached to into the message vocabulary in event.go, and operator actions
// leave through an emitter callback. That is what allows the whole shell —
// including key routing, focus cycling, and responsive region dropping — to be
// exercised by calling Update directly, with no TTY and no kernel.
//
// EventSource is declared in this package rather than beside an implementation
// because this is where it is consumed, and the shell needs exactly one method
// of it.
package shell
