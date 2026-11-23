package main

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"
)

// runBluetooth chats over the Bluetooth mesh. It reports false when this
// build has no Bluetooth at all, so the caller can use Wi-Fi instead.
func runBluetooth(a args, name string, ui uiOpts) bool {
	rad := newRadio()
	if rad == nil {
		return false
	}
	h, err := StartHub(HubOpts{Mesh: true})
	if err != nil {
		die("can't start: %v", err)
	}
	m, err := NewMesh(h, rad, defaultMeshCfg)
	if err != nil {
		die("can't start: %v", err)
	}
	c := NewClient(ClientOpts{
		Mode: modeJoin, URL: localURL(h.Port()), Nick: name, Token: deviceToken(a.id),
		Status: m.Status, Note: m.Note, Files: m,
	})
	m.notify, m.change = c.Sys, c.Poke
	if a.key != "" {
		c.Sys("--key only works in Wi-Fi mode: nook wifi --key " + a.key)
	}
	if err := m.Start(); err != nil {
		c.Sys("Bluetooth didn't start: " + err.Error() + " · try: nook wifi")
	}
	runUI(c, ui, a.plain)
	m.Close()
	h.Close("")
	return true
}

// btCheck tries Bluetooth for a while and says plainly what works.
func btCheck() {
	rad := newRadio()
	if rad == nil {
		fmt.Println("this copy of nook was built without Bluetooth. Use: nook wifi")
		os.Exit(1)
	}
	fmt.Println("checking Bluetooth for 20 seconds (Ctrl+C to stop early)...")
	ev := &checkEvents{start: time.Now(), links: map[int64]bool{}}
	if err := rad.Start(ev); err != nil {
		fmt.Println("Bluetooth didn't start:", err)
		os.Exit(1)
	}
	if p, ok := rad.(permissioner); ok {
		ev.perm = p
		if p.Permission() == "ask" {
			fmt.Println("  macOS is asking whether nook may use Bluetooth: click Allow on the popup")
		}
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	select {
	case <-sig:
	case <-time.After(20 * time.Second):
	}
	rad.Close()
	fmt.Println()
	fmt.Println(ev.summary())
}

type checkEvents struct {
	mu    sync.Mutex
	start time.Time
	state string
	adv   int // 1: others can find us, -1: they can't
	links map[int64]bool
	ever  int
	bytes int
	perm  permissioner
}

func (e *checkEvents) say(format string, args ...interface{}) {
	fmt.Printf("  %4.1fs  %s\n", time.Since(e.start).Seconds(), fmt.Sprintf(format, args...))
}

func (e *checkEvents) RadioState(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch s {
	case "advertising":
		if e.adv != 1 {
			e.adv = 1
			e.say("other laptops can find this one")
		}
	case "noadvert":
		if e.adv != -1 {
			e.adv = -1
			e.say("this laptop's Bluetooth can't be found by others (it can still find them)")
		}
	default:
		if s != e.state {
			e.state = s
			e.say("Bluetooth: %s", map[string]string{
				"on": "on", "off": "switched off", "starting": "starting",
				"unauthorized": "not allowed for this terminal app",
				"unsupported":  "not available here (no Bluetooth Low Energy)",
			}[s])
		}
	}
}

func (e *checkEvents) LinkUp(l int64, mtu int, shared bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if shared {
		e.say("ready for other laptops to connect")
		return
	}
	e.links[l] = true
	e.ever++
	e.say("linked to a nook nearby (%d-byte packets)", mtu)
}

func (e *checkEvents) LinkDown(l int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.links[l] {
		delete(e.links, l)
		e.say("a link closed")
	}
}

func (e *checkEvents) Recv(_ int64, f []byte) {
	e.mu.Lock()
	e.bytes += len(f)
	e.mu.Unlock()
}

func (e *checkEvents) summary() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case e.state == "unauthorized":
		return "Bluetooth isn't allowed for this terminal app. Allow it in System Settings > Privacy & Security > Bluetooth, then try again."
	case e.state == "off":
		return "Bluetooth is switched off. Turn it on and try again."
	case e.state == "unsupported":
		return "This computer has no Bluetooth Low Energy. Use: nook wifi"
	case e.state != "on" && e.perm != nil && e.perm.Permission() == "ask":
		return "macOS is still waiting for you to allow Bluetooth for nook: click Allow on the popup, then run nook check again."
	case e.state != "on" && e.perm != nil && e.perm.Permission() == "denied":
		return "nook isn't allowed to use Bluetooth. Allow it in System Settings > Privacy & Security > Bluetooth, then try again."
	case e.state != "on":
		return "Bluetooth didn't come up in time. Try again, or use: nook wifi"
	case e.ever > 0:
		return fmt.Sprintf("Bluetooth works: linked to %d other nook(s), received %d bytes.", e.ever, e.bytes)
	}
	return "Bluetooth works on this laptop, but no other nook was found. Run nook (or nook check) on another laptop a few metres away and try again."
}
