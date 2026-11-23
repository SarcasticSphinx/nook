package main

// A radio is the Bluetooth hardware as the mesh sees it: links to nearby
// laptops that carry small frames. Each platform has its own (ble_*.go);
// the tests use a simulated one.
type radio interface {
	Start(ev radioEvents) error
	// Send writes one frame of at most the link's mtu bytes. It may block
	// briefly while Bluetooth is busy.
	Send(link int64, frame []byte) error
	Close()
}

// permissioner is a radio that needs the user's OK first (macOS).
// Permission is "ask" (a popup is waiting), "denied" or "allowed".
type permissioner interface {
	Permission() string
}

// radioEvents is how a radio reports back. Calls must return quickly.
type radioEvents interface {
	// RadioState is one of: on, off, unauthorized, unsupported; or
	// advertising / noadvert: whether other laptops can find this one.
	RadioState(state string)
	// LinkUp reports a working link. A shared link reaches several laptops
	// at once and can't tell them apart (a Windows laptop's own server).
	LinkUp(link int64, mtu int, shared bool)
	LinkDown(link int64)
	Recv(link int64, frame []byte)
}

// The Bluetooth service every nook advertises ("nook" in hex starts it).
// Laptops that connect write to bleRX; the laptop they connected to answers
// with notifications on bleTX.
const (
	bleService = "6e6f6f6b-4c41-4e00-9a3f-2b5c7d1e8f01"
	bleRX      = "6e6f6f6b-4c41-4e00-9a3f-2b5c7d1e8f02"
	bleTX      = "6e6f6f6b-4c41-4e00-9a3f-2b5c7d1e8f03"

	maxOutLinks = 4 // links we open ourselves; others connect to us too
)
