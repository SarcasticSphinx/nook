//go:build (windows || linux) && !nobluetooth

package main

import (
	"errors"
	"math/rand"
	"runtime"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

// tinyRadio drives Bluetooth on Windows (WinRT) and Linux (BlueZ) through
// tinygo.org/x/bluetooth. The laptop runs the nook service for others to
// connect to; the library can't tell those laptops apart, so they all share
// one link (whatever this laptop sends there reaches all of them). It also
// connects out to a few other nooks itself.
type tinyRadio struct {
	ad            *bluetooth.Adapter
	ev            radioEvents
	svc, rxU, txU bluetooth.UUID
	service       *bluetooth.Service
	rx, tx        bluetooth.Characteristic

	mu     sync.Mutex
	links  map[int64]*tinyLink
	byAddr map[string]*tinyLink
	failed map[string]time.Time
	next   int64
	quit   chan struct{}
	once   sync.Once
}

type tinyLink struct {
	id   int64
	addr string
	dev  bluetooth.Device
	rx   bluetooth.DeviceCharacteristic
}

const sharedLinkID = 1

var errLinkGone = errors.New("bluetooth: link gone")

func newRadio() radio {
	return &tinyRadio{
		links: map[int64]*tinyLink{}, byAddr: map[string]*tinyLink{}, failed: map[string]time.Time{},
		next: sharedLinkID, quit: make(chan struct{}),
	}
}

func (r *tinyRadio) Start(ev radioEvents) error {
	r.ev = ev
	var err error
	if r.svc, err = bluetooth.ParseUUID(bleService); err != nil {
		return err
	}
	r.rxU, _ = bluetooth.ParseUUID(bleRX)
	r.txU, _ = bluetooth.ParseUUID(bleTX)
	r.ad = bluetooth.DefaultAdapter
	if err := r.ad.Enable(); err != nil {
		ev.RadioState("unsupported")
		return nil
	}
	r.ad.SetConnectHandler(func(d bluetooth.Device, connected bool) {
		if !connected {
			r.lost(d.Address.String())
		}
	})
	r.serve()
	go r.scanLoop()
	return nil
}

// serve publishes the nook service so other laptops can find and join us.
func (r *tinyRadio) serve() {
	r.service = &bluetooth.Service{
		UUID: r.svc,
		Characteristics: []bluetooth.CharacteristicConfig{
			{
				Handle: &r.rx, UUID: r.rxU,
				Flags: bluetooth.CharacteristicWriteWithoutResponsePermission | bluetooth.CharacteristicWritePermission,
				WriteEvent: func(_ bluetooth.Connection, _ int, value []byte) {
					if len(value) > 0 {
						r.ev.Recv(sharedLinkID, append([]byte(nil), value...))
					}
				},
			},
			{Handle: &r.tx, UUID: r.txU, Flags: bluetooth.CharacteristicNotifyPermission | bluetooth.CharacteristicReadPermission},
		},
	}
	err := r.ad.AddService(r.service) // on Windows this also starts advertising
	if err == nil && runtime.GOOS == "linux" {
		adv := r.ad.DefaultAdvertisement()
		if err = adv.Configure(bluetooth.AdvertisementOptions{ServiceUUIDs: []bluetooth.UUID{r.svc}}); err == nil {
			err = adv.Start()
		}
	}
	if err != nil { // e.g. a Bluetooth chip that can't be a server: we can still connect out
		r.service = nil
		r.ev.RadioState("noadvert")
		return
	}
	r.ev.RadioState("advertising")
	// Notifications go to every connected laptop at once, so use the size
	// every Bluetooth link accepts.
	r.ev.LinkUp(sharedLinkID, 20, true)
}

func (r *tinyRadio) Send(link int64, f []byte) error {
	if link == sharedLinkID {
		_, err := r.tx.Write(f)
		return err
	}
	r.mu.Lock()
	l := r.links[link]
	r.mu.Unlock()
	if l == nil {
		return errLinkGone
	}
	if _, err := l.rx.WriteWithoutResponse(f); err != nil {
		r.lost(l.addr)
		return err
	}
	return nil
}

func (r *tinyRadio) stopped() bool {
	select {
	case <-r.quit:
		return true
	default:
		return false
	}
}

func (r *tinyRadio) sleep(d time.Duration) (quit bool) {
	select {
	case <-r.quit:
		return true
	case <-time.After(d):
		return false
	}
}

// scanLoop listens for other nooks for a few seconds, then connects to a few
// of them (chosen at random so thirty laptops spread out), and repeats.
func (r *tinyRadio) scanLoop() {
	for !r.stopped() {
		var mu sync.Mutex
		found := map[string]bluetooth.Address{}
		stop := time.AfterFunc(3*time.Second, func() { r.ad.StopScan() })
		err := r.ad.Scan(func(_ *bluetooth.Adapter, res bluetooth.ScanResult) {
			if res.HasServiceUUID(r.svc) {
				mu.Lock()
				found[res.Address.String()] = res.Address
				mu.Unlock()
			}
		})
		stop.Stop()
		if err != nil { // usually: Bluetooth is switched off
			r.ev.RadioState("off")
			if r.sleep(5 * time.Second) {
				return
			}
			continue
		}
		r.ev.RadioState("on")
		mu.Lock()
		addrs := make([]bluetooth.Address, 0, len(found))
		for _, a := range found {
			addrs = append(addrs, a)
		}
		mu.Unlock()
		rand.Shuffle(len(addrs), func(i, j int) { addrs[i], addrs[j] = addrs[j], addrs[i] })
		for _, a := range addrs {
			if r.stopped() || r.outCount() >= maxOutLinks {
				break
			}
			if !r.skip(a.String()) {
				r.dial(a)
			}
		}
		if r.sleep(4 * time.Second) {
			return
		}
	}
}

func (r *tinyRadio) outCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.links)
}

func (r *tinyRadio) skip(addr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byAddr[addr] != nil || time.Since(r.failed[addr]) < 8*time.Second
}

func (r *tinyRadio) fail(addr string) {
	r.mu.Lock()
	r.failed[addr] = time.Now()
	r.mu.Unlock()
}

func (r *tinyRadio) dial(a bluetooth.Address) {
	addr := a.String()
	dev, err := r.ad.Connect(a, bluetooth.ConnectionParams{})
	if err != nil {
		r.fail(addr)
		return
	}
	ok := false
	defer func() {
		if !ok {
			dev.Disconnect()
			r.fail(addr)
		}
	}()
	svcs, err := dev.DiscoverServices([]bluetooth.UUID{r.svc})
	if err != nil || len(svcs) == 0 {
		return
	}
	chars, err := svcs[0].DiscoverCharacteristics([]bluetooth.UUID{r.rxU, r.txU})
	if err != nil {
		return
	}
	var rx, tx *bluetooth.DeviceCharacteristic
	for i := range chars {
		switch chars[i].UUID() {
		case r.rxU:
			rx = &chars[i]
		case r.txU:
			tx = &chars[i]
		}
	}
	if rx == nil || tx == nil {
		return
	}
	mtu := 20
	if m, err := rx.GetMTU(); err == nil && int(m) > 23 {
		mtu = int(m) - 3
	}
	r.mu.Lock()
	r.next++
	l := &tinyLink{id: r.next, addr: addr, dev: dev, rx: *rx}
	r.links[l.id] = l
	r.byAddr[addr] = l
	r.mu.Unlock()
	id := l.id
	if err := tx.EnableNotifications(func(buf []byte) {
		if len(buf) > 0 {
			r.ev.Recv(id, append([]byte(nil), buf...))
		}
	}); err != nil {
		r.mu.Lock()
		delete(r.links, id)
		delete(r.byAddr, addr)
		r.mu.Unlock()
		return
	}
	ok = true
	r.ev.LinkUp(id, mtu, false)
}

func (r *tinyRadio) lost(addr string) {
	r.mu.Lock()
	l := r.byAddr[addr]
	if l != nil {
		delete(r.byAddr, addr)
		delete(r.links, l.id)
	}
	r.mu.Unlock()
	if l != nil {
		r.ev.LinkDown(l.id)
		r.fail(addr)
	}
}

func (r *tinyRadio) Close() {
	r.once.Do(func() {
		close(r.quit)
		if r.ad == nil {
			return
		}
		r.ad.StopScan()
		r.mu.Lock()
		links := r.links
		r.links, r.byAddr = map[int64]*tinyLink{}, map[string]*tinyLink{}
		r.mu.Unlock()
		for _, l := range links {
			l.dev.Disconnect()
		}
		if r.service != nil {
			if runtime.GOOS == "linux" {
				r.ad.DefaultAdvertisement().Stop()
			}
			r.ad.RemoveService(r.service)
		}
	})
}
