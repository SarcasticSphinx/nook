//go:build !(darwin && cgo && !nobluetooth)

package main

// ownBluetooth only matters on macOS (see ble_darwin.go).
func ownBluetooth() (code int, ok bool) { return 0, false }
