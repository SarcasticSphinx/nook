//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// wipeTerminal clears the screen and scrollback after quitting (--wipe).
// Old consoles that can't do it are simply left alone.
func wipeTerminal() {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return
	}
	if windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) != nil {
		return
	}
	os.Stdout.WriteString("\x1b[H\x1b[2J\x1b[3J")
	windows.SetConsoleMode(h, mode)
}
