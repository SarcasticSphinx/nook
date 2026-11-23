package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- a pretend Bluetooth network ----

type simNet struct {
	mu   sync.Mutex
	next int64
	mtu  int
	loss float64 // share of frames lost in the air
	rng  *rand.Rand
}

func (n *simNet) lost() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.loss == 0 {
		return false
	}
	if n.rng == nil {
		n.rng = rand.New(rand.NewSource(42))
	}
	return n.rng.Float64() < n.loss
}

func (n *simNet) id() int64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.next++
	return n.next
}

type simEnd struct {
	r    *simRadio
	link int64
}

type simRadio struct {
	net  *simNet
	mu   sync.Mutex
	ev   radioEvents
	ends map[int64][]simEnd // a shared link has several
}

func (n *simNet) radio() *simRadio { return &simRadio{net: n, ends: map[int64][]simEnd{}} }

func (r *simRadio) Start(ev radioEvents) error {
	r.mu.Lock()
	r.ev = ev
	r.mu.Unlock()
	ev.RadioState("on")
	return nil
}

func (r *simRadio) events() radioEvents {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ev
}

func (r *simRadio) Send(link int64, f []byte) error {
	if len(f) > r.net.mtu {
		return errors.New("frame bigger than the link allows")
	}
	r.mu.Lock()
	ends := append([]simEnd(nil), r.ends[link]...)
	r.mu.Unlock()
	if len(ends) == 0 {
		return errors.New("link gone")
	}
	if r.net.lost() {
		return nil // gone into thin air; the sender doesn't know
	}
	for _, e := range ends {
		if ev := e.r.events(); ev != nil {
			ev.Recv(e.link, append([]byte(nil), f...))
		}
	}
	return nil
}

func (r *simRadio) Close() {
	r.mu.Lock()
	ends := r.ends
	r.ends = map[int64][]simEnd{}
	r.mu.Unlock()
	for _, list := range ends {
		for _, e := range list {
			e.r.detach(e.link, r)
		}
	}
}

// detach removes `from` from one of our links; the link goes down when
// nobody is left on it.
func (r *simRadio) detach(link int64, from *simRadio) {
	r.mu.Lock()
	list := r.ends[link]
	var keep []simEnd
	for _, e := range list {
		if e.r != from {
			keep = append(keep, e)
		}
	}
	gone := len(list) > 0 && len(keep) == 0
	if gone {
		delete(r.ends, link)
	} else if len(keep) > 0 {
		r.ends[link] = keep
	}
	ev := r.ev
	r.mu.Unlock()
	if gone && ev != nil {
		ev.LinkDown(link)
	}
}

func (n *simNet) connect(a, b *simRadio) {
	la, lb := n.id(), n.id()
	a.mu.Lock()
	a.ends[la] = []simEnd{{b, lb}}
	a.mu.Unlock()
	b.mu.Lock()
	b.ends[lb] = []simEnd{{a, la}}
	b.mu.Unlock()
	a.events().LinkUp(la, n.mtu, false)
	b.events().LinkUp(lb, n.mtu, false)
}

// share connects centrals to p the way Windows does it: p has one shared
// link that reaches all of them and can't tell them apart.
func (n *simNet) share(p *simRadio, cs ...*simRadio) {
	lp := n.id()
	for _, c := range cs {
		lc := n.id()
		p.mu.Lock()
		p.ends[lp] = append(p.ends[lp], simEnd{c, lc})
		p.mu.Unlock()
		c.mu.Lock()
		c.ends[lc] = []simEnd{{p, lp}}
		c.mu.Unlock()
		c.events().LinkUp(lc, n.mtu, false)
	}
	p.events().LinkUp(lp, n.mtu, true)
}

// ---- simulated laptops ----

var testMeshCfg = meshCfg{TTL: 7, Announce: time.Second, Offline: 6 * time.Second, Recent: 40, Tick: 50 * time.Millisecond, Retry: 300 * time.Millisecond}

type simNode struct {
	hub  *Hub
	mesh *Mesh
	rad  *simRadio
	c    *Client
}

func newSimNode(t *testing.T, n *simNet, name string, cfg meshCfg) *simNode {
	t.Helper()
	h, err := StartHub(HubOpts{Mesh: true})
	if err != nil {
		t.Fatal(err)
	}
	rad := n.radio()
	m, err := NewMesh(h, rad, cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(ClientOpts{Mode: modeJoin, URL: localURL(h.Port()), Nick: name, Token: deviceToken("sim-" + name),
		Status: m.Status, Note: m.Note, Files: m})
	m.notify, m.change = c.Sys, c.Poke
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	c.Start()
	s := &simNode{h, m, rad, c}
	t.Cleanup(func() { s.stop() })
	waitFor(t, name+" online", func() bool { return c.State() == stOnline })
	return s
}

func (s *simNode) stop() {
	s.c.Close()
	s.mesh.Close()
	s.hub.Close("")
}

func waitLong(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func roomHas(h *Hub, room, id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.rooms[room]
	return r != nil && r.members[id]
}

func hasDM(h *Hub, text string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, list := range h.dms {
		for _, m := range list {
			if m.Text == text {
				return true
			}
		}
	}
	return false
}

// Thirty laptops, each linked to only a few others: everything reaches
// everyone, rooms stay in their rooms, and DMs stay private.
func TestMeshThirtyLaptops(t *testing.T) {
	n := &simNet{mtu: 64}
	var nodes []*simNode
	for i := 0; i < 30; i++ {
		nodes = append(nodes, newSimNode(t, n, fmt.Sprintf("p%02d", i), testMeshCfg))
	}
	r := rand.New(rand.NewSource(7))
	for i := 1; i < len(nodes); i++ { // a chain, so everyone is reachable...
		n.connect(nodes[i-1].rad, nodes[i].rad)
	}
	for i := 0; i < 15; i++ { // ...plus random shortcuts
		if a, b := r.Intn(30), r.Intn(30); a != b {
			n.connect(nodes[a].rad, nodes[b].rad)
		}
	}
	waitLong(t, "everyone sees everyone", 30*time.Second, func() bool {
		for _, s := range nodes {
			if s.hub.Online() != 30 {
				return false
			}
		}
		return true
	})

	nodes[0].c.Input("hello mesh")
	for _, s := range nodes {
		s := s
		waitFor(t, "message reaches everyone", func() bool { return sees(s.c, "#general", "hello mesh") })
	}

	nodes[7].c.Input("/join #lab")
	nodes[12].c.Input("/join #lab")
	waitFor(t, "p07 in #lab", func() bool { return target(nodes[7].c) == "#lab" })
	waitFor(t, "p12 in #lab", func() bool { return target(nodes[12].c) == "#lab" })
	waitLong(t, "p12 knows p07 is in #lab", 10*time.Second, func() bool { return roomHas(nodes[12].hub, "lab", nodes[7].c.MeID()) })
	nodes[7].c.Input("lab only")
	waitFor(t, "room message", func() bool { return sees(nodes[12].c, "#lab", "lab only") })
	time.Sleep(300 * time.Millisecond)
	if sees(nodes[3].c, "", "lab only") {
		t.Fatal("someone outside #lab saw a #lab message")
	}

	nodes[4].c.Input("/dm p20 psst")
	waitFor(t, "DM arrives", func() bool { return sees(nodes[20].c, "@"+nodes[4].c.MeID(), "psst") })
	time.Sleep(300 * time.Millisecond)
	for i, s := range nodes {
		if i != 4 && i != 20 && (hasDM(s.hub, "psst") || sees(s.c, "", "psst")) {
			t.Fatalf("p%02d could read a DM that wasn't theirs", i)
		}
	}
}

// Windows can't tell the laptops connected to it apart, so it hands
// everything to all of them on one shared link. Links as small as 20 bytes
// still carry long messages in any script.
func TestMeshSharedLinkTinyFrames(t *testing.T) {
	n := &simNet{mtu: 20}
	p := newSimNode(t, n, "win", testMeshCfg)
	a := newSimNode(t, n, "ana", testMeshCfg)
	b := newSimNode(t, n, "bo", testMeshCfg)
	c := newSimNode(t, n, "cy", testMeshCfg)
	n.share(p.rad, a.rad, b.rad, c.rad)
	waitLong(t, "all four online", 20*time.Second, func() bool {
		return p.hub.Online() == 4 && a.hub.Online() == 4 && b.hub.Online() == 4 && c.hub.Online() == 4
	})
	long := strings.TrimSpace(strings.Repeat("আমি পরে আসব, ", 40))
	a.c.Input(long)
	for _, s := range []*simNode{p, b, c} {
		s := s
		waitFor(t, "long message across the shared link", func() bool { return sees(s.c, "#general", long) })
	}
	b.c.Input("/dm cy only for you")
	waitFor(t, "DM over the shared link", func() bool { return sees(c.c, "", "only for you") })
	time.Sleep(300 * time.Millisecond)
	if sees(a.c, "", "only for you") || hasDM(a.hub, "only for you") || hasDM(p.hub, "only for you") {
		t.Fatal("a DM was readable by others on the shared link")
	}
}

// Someone who arrives later is caught up on recent messages.
func TestMeshCatchUp(t *testing.T) {
	n := &simNet{mtu: 64}
	a := newSimNode(t, n, "a", testMeshCfg)
	b := newSimNode(t, n, "b", testMeshCfg)
	c := newSimNode(t, n, "c", testMeshCfg)
	if c.mesh.Note() != "no one nearby yet" {
		t.Fatalf("note while alone: %q", c.mesh.Note())
	}
	n.connect(a.rad, b.rad)
	waitFor(t, "a and b meet", func() bool { return b.hub.Online() == 2 })
	a.c.Input("early bird")
	waitFor(t, "b gets it", func() bool { return sees(b.c, "#general", "early bird") })
	n.connect(b.rad, c.rad)
	waitFor(t, "late arrival is caught up", func() bool { return sees(c.c, "#general", "early bird") })
	waitFor(t, "late arrival sees everyone", func() bool { return c.hub.Online() == 3 })
	if c.mesh.Note() != "" {
		t.Fatalf("note with people around: %q", c.mesh.Note())
	}
}

// Quitting says goodbye, so others see you leave at once.
func TestMeshGoodbye(t *testing.T) {
	n := &simNet{mtu: 64}
	cfg := testMeshCfg
	cfg.Offline = time.Minute // so only the goodbye can explain a quick exit
	a := newSimNode(t, n, "a", cfg)
	b := newSimNode(t, n, "b", cfg)
	n.connect(a.rad, b.rad)
	waitFor(t, "both online", func() bool { return a.hub.Online() == 2 })
	b.stop()
	waitFor(t, "b shows as gone", func() bool { return a.hub.Online() == 1 })
}

func TestFrames(t *testing.T) {
	data := bytes.Repeat([]byte("abcdefghij"), 100)
	fs, err := splitFrames(7, 9, data, 20)
	if err != nil || len(fs) != 72 {
		t.Fatalf("frames=%d err=%v", len(fs), err)
	}
	other, _ := splitFrames(8, 9, []byte("someone else's packet, mixed in"), 20)
	var a assembler
	now := time.Now()
	var got [][]byte
	for i, f := range fs { // interleave two senders on one link
		if b := a.add(1, f, now); b != nil {
			got = append(got, b)
		}
		if i < len(other) {
			if b := a.add(1, other[i], now); b != nil {
				got = append(got, b)
			}
		}
	}
	if len(got) != 2 || !bytes.Equal(got[1], data) || string(got[0]) != "someone else's packet, mixed in" {
		t.Fatalf("reassembly failed: %d packets", len(got))
	}
	if _, err := splitFrames(1, 1, make([]byte, 255*14+1), 20); err != errTooBig {
		t.Fatal("oversized packet was not refused")
	}
	a.add(2, fs[0], now) // an incomplete packet is forgotten after a while
	a.prune(now.Add(time.Minute))
	if len(a.parts) != 0 {
		t.Fatal("stale fragments kept")
	}
	p := &pkt{T: "msg", MID: "x", Text: strings.Repeat("compress me ", 50)}
	b := encodePkt(p)
	q, err := decodePkt(b)
	if err != nil || q.Text != p.Text || b[0] != 'Z' {
		t.Fatalf("packet round trip: %v", err)
	}
}

func TestDMCrypto(t *testing.T) {
	ka, pa, _ := newDMKey()
	kb, pb, _ := newDMKey()
	kc, _, _ := newDMKey()
	box, err := sealDM(ka, pb, "aaaaaaaaaaaa", "bbbbbbbbbbbb", "meet at 3")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := openDM(kb, pa, "aaaaaaaaaaaa", "bbbbbbbbbbbb", box); err != nil || got != "meet at 3" {
		t.Fatalf("recipient can't read it: %v", err)
	}
	if _, err := openDM(kc, pa, "aaaaaaaaaaaa", "bbbbbbbbbbbb", box); err == nil {
		t.Fatal("a third laptop could read it")
	}
	if _, err := openDM(kb, pa, "cccccccccccc", "bbbbbbbbbbbb", box); err == nil {
		t.Fatal("the sender could be swapped")
	}
}
