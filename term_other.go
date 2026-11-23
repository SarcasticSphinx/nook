//go:build !windows

package main

import "os"

// wipeTerminal clears the screen and scrollback after quitting (--wipe).
func wipeTerminal() { os.Stdout.WriteString("\x1b[H\x1b[2J\x1b[3J") }
