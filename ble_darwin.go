//go:build darwin && cgo && !nobluetooth

package main

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework CoreBluetooth
#cgo LDFLAGS: "-Wl,-sectcreate,__TEXT,__info_plist,${SRCDIR}/ble_darwin_info.plist"
#include <stdlib.h>
#include "ble_darwin.h"
*/
import "C"

import (
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"unsafe"
)

// ownBluetooth runs nook again as a process macOS treats as its own app,
// so the Bluetooth permission is asked for (and remembered) for nook itself
// rather than for whatever terminal started it. ok is false if that isn't
// possible; then nook just carries on as it is.
func ownBluetooth() (code int, ok bool) {
	if os.Getenv("NOOK_OWN") != "" {
		return 0, false // we are the copy
	}
	exe, err := os.Executable()
	if err != nil {
		return 0, false
	}
	os.Setenv("NOOK_OWN", "1")
	argv := make([]*C.char, len(os.Args)+1)
	for i, a := range os.Args {
		argv[i] = C.CString(a)
	}
	path := C.CString(exe)
	defer func() {
		for _, p := range argv {
			C.free(unsafe.Pointer(p))
		}
		C.free(unsafe.Pointer(path))
	}()
	signal.Ignore(syscall.SIGINT, syscall.SIGQUIT) // the copy handles them
	rc := C.nook_respawn_disclaimed(path, &argv[0])
	if rc < 0 {
		signal.Reset(syscall.SIGINT, syscall.SIGQUIT)
		os.Unsetenv("NOOK_OWN")
		return 0, false
	}
	return int(rc), true
}

// macRadio drives ble_darwin.m. CoreBluetooth calls back into the exported
// functions below, from its own queue.
type macRadio struct{}

var (
	macMu     sync.Mutex
	macEv     radioEvents
	macStates = [2]int{-1, -1} // central, peripheral (CBManagerState)
)

func newRadio() radio { return &macRadio{} }

func (r *macRadio) Start(ev radioEvents) error {
	macMu.Lock()
	macEv, macStates = ev, [2]int{-1, -1}
	macMu.Unlock()
	C.nook_ble_start(C.int(maxOutLinks))
	return nil
}

func (r *macRadio) Send(link int64, frame []byte) error {
	if len(frame) == 0 {
		return nil
	}
	if C.nook_ble_send(C.longlong(link), unsafe.Pointer(&frame[0]), C.int(len(frame))) != 0 {
		return errors.New("bluetooth: link busy or gone")
	}
	return nil
}

// Permission says whether macOS lets nook use Bluetooth.
func (r *macRadio) Permission() string {
	switch C.nook_ble_auth() {
	case 0:
		return "ask"
	case 1, 2:
		return "denied"
	}
	return "allowed"
}

func (r *macRadio) Close() {
	C.nook_ble_stop()
	macMu.Lock()
	macEv = nil
	macMu.Unlock()
}

func macEvents() radioEvents {
	macMu.Lock()
	defer macMu.Unlock()
	return macEv
}

// macState turns CoreBluetooth's states (0 unknown, 1 resetting,
// 2 unsupported, 3 unauthorized, 4 off, 5 on) into the radio's words.
func macState(central, periph int) string {
	switch {
	case central == 3 || periph == 3:
		return "unauthorized"
	case central == 2:
		return "unsupported"
	case central == 4 || periph == 4:
		return "off"
	case central == 5:
		return "on"
	}
	return "starting"
}

//export nookBLEState
func nookBLEState(which, state C.int) {
	ev := macEvents()
	if ev == nil {
		return
	}
	if which == 2 { // whether advertising started
		if state == 0 {
			ev.RadioState("noadvert")
		} else {
			ev.RadioState("advertising")
		}
		return
	}
	macMu.Lock()
	macStates[which] = int(state)
	c, p := macStates[0], macStates[1]
	macMu.Unlock()
	ev.RadioState(macState(c, p))
}

//export nookBLELinkUp
func nookBLELinkUp(link C.longlong, mtu, shared C.int) {
	if ev := macEvents(); ev != nil {
		ev.LinkUp(int64(link), int(mtu), shared != 0)
	}
}

//export nookBLELinkDown
func nookBLELinkDown(link C.longlong) {
	if ev := macEvents(); ev != nil {
		ev.LinkDown(int64(link))
	}
}

//export nookBLERecv
func nookBLERecv(link C.longlong, data unsafe.Pointer, n C.int) {
	if ev := macEvents(); ev != nil {
		ev.Recv(int64(link), C.GoBytes(data, n))
	}
}
