//go:build nobluetooth || !((darwin && cgo) || windows || linux)

package main

// This build has no Bluetooth (e.g. the Windows 7 builds, or a Mac build
// made without a C compiler). nook falls back to Wi-Fi mode.
func newRadio() radio { return nil }
