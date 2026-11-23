package main

import (
	"crypto/ecdh"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// Bluetooth mode has no host. Every laptop runs its own private hub (only
// this computer can reach it), and the mesh keeps it in step with everyone
// else: it holds Bluetooth links to a few laptops nearby, and every message
// is passed on from link to link until every laptop has it. Each message has
// a unique id, so a laptop handles it once, and a hop limit, so it dies out.
//
// Everything happens on one goroutine (loop); the radio and the hub only
// hand it events.

type meshCfg struct {
	TTL      int           // hops a message may travel
	Announce time.Duration // how often a laptop says "I'm here"
	Offline  time.Duration // silence after which someone counts as gone
	Recent   int           // public messages kept to catch newcomers up
	Tick     time.Duration
}

var defaultMeshCfg = meshCfg{TTL: 7, Announce: 30 * time.Second, Offline: 95 * time.Second, Recent: 40, Tick: time.Second}

type Mesh struct {
	hub    *Hub
	rad    radio
	cfg    meshCfg
	notify func(string) // shows a notice in the chat
	change func()       // asks the screen to redraw

	ev        chan meshEv
	quit      chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	// owned by loop
	me, nick, pub string
	rooms         []string
	key           *ecdh.PrivateKey
	peers         map[string]*peer
	links         map[int64]*mlink
	seen          map[string]time.Time
	recent        []stored
	asm           assembler
	annMID        string
	lastAnn       time.Time
	annEvery      time.Duration
	lastPrune     time.Time
	syncedAt      map[int64]time.Time
	state         string
	noAdvert      bool

	stream uint16
	seq    uint32 // atomic: packet counter for framing

	mu   sync.Mutex
	snap meshSnap
}

type meshSnap struct {
	state         string
	noAdvert      bool
	direct, total int // our own links, and all of them
	people        int // others currently around
}

type peer struct {
	id, nick string
	rooms    []string
	pub      string
	annMID   string
	lastSeen time.Time
	on       bool
}

type mlink struct {
	id     int64
	mtu    int
	shared bool
	hi, lo chan []byte // chat first, catch-up and announcements after
	stop   chan struct{}
}

type stored struct {
	p  pkt
	at time.Time
}

const (
	evState = iota
	evUp
	evDown
	evRecv
)

type meshEv struct {
	kind   int
	link   int64
	mtu    int
	shared bool
	data   []byte
	state  string
}

func NewMesh(h *Hub, r radio, cfg meshCfg) (*Mesh, error) {
	key, pub, err := newDMKey()
	if err != nil {
		return nil, err
	}
	return &Mesh{
		hub: h, rad: r, cfg: cfg,
		notify: func(string) {}, change: func() {},
		ev: make(chan meshEv, 8192), quit: make(chan struct{}), done: make(chan struct{}),
		key: key, pub: pub,
		peers: map[string]*peer{}, links: map[int64]*mlink{},
		seen: map[string]time.Time{}, syncedAt: map[int64]time.Time{},
		annEvery: cfg.Announce + time.Duration(rand.Int63n(int64(cfg.Announce)/4+1)),
		stream:   uint16(rand.Intn(1 << 16)),
		seq:      uint32(rand.Intn(1 << 16)),
		state:    "starting",
	}, nil
}

// Start brings up Bluetooth and the mesh.
func (m *Mesh) Start() error {
	go m.loop()
	return m.rad.Start(m)
}

// Close says goodbye to everyone and turns the radio off.
func (m *Mesh) Close() {
	m.closeOnce.Do(func() {
		close(m.quit)
		<-m.done
		time.Sleep(300 * time.Millisecond) // let the goodbye go out
		for _, l := range m.links {
			close(l.stop)
		}
		m.rad.Close()
	})
}

// ---- radioEvents (called from the radio's threads) ----

func (m *Mesh) RadioState(s string) { m.postWait(meshEv{kind: evState, state: s}) }
func (m *Mesh) LinkUp(link int64, mtu int, shared bool) {
	m.postWait(meshEv{kind: evUp, link: link, mtu: mtu, shared: shared})
}
func (m *Mesh) LinkDown(link int64) { m.postWait(meshEv{kind: evDown, link: link}) }
func (m *Mesh) Recv(link int64, f []byte) {
	select { // under overload, drop data rather than stall Bluetooth
	case m.ev <- meshEv{kind: evRecv, link: link, data: f}:
	default:
	}
}

func (m *Mesh) postWait(e meshEv) {
	select {
	case m.ev <- e:
	case <-m.quit:
	}
}

// ---- the loop ----

func (m *Mesh) loop() {
	defer close(m.done)
	t := time.NewTicker(m.cfg.Tick)
	defer t.Stop()
	local := m.hub.LocalEvents()
	for {
		select {
		case <-m.quit:
			m.sayBye()
			return
		case e := <-m.ev:
			m.onEvent(e)
		case le := <-local:
			m.onLocal(le)
		case now := <-t.C:
			m.tick(now)
		}
	}
}

func (m *Mesh) onEvent(e meshEv) {
	switch e.kind {
	case evState:
		switch e.state {
		case "noadvert":
			m.noAdvert = true
		case "advertising":
			m.noAdvert = false
		default:
			m.state = e.state
		}
	case evUp:
		if old := m.links[e.link]; old != nil {
			close(old.stop)
		}
		if e.mtu < frameHdr+8 {
			e.mtu = 20
		}
		l := &mlink{id: e.link, mtu: e.mtu, shared: e.shared, hi: make(chan []byte, 256), lo: make(chan []byte, 512), stop: make(chan struct{})}
		m.links[e.link] = l
		go m.writer(l)
		if m.me != "" && !l.shared {
			m.enqueue(l, encodePkt(m.myAnn(true)), false)
			m.enqueue(l, encodePkt(&pkt{T: "sync", From: m.me}), false) // "catch me up"
		}
	case evDown:
		if l := m.links[e.link]; l != nil {
			close(l.stop)
			delete(m.links, e.link)
			delete(m.syncedAt, e.link)
			m.asm.dropLink(e.link)
		}
	case evRecv:
		if b := m.asm.add(e.link, e.data, time.Now()); b != nil {
			if p, err := decodePkt(b); err == nil {
				m.handle(e.link, p)
			}
		}
	}
	m.publish()
}

// writer sends one link's packets, frame by frame.
func (m *Mesh) writer(l *mlink) {
	for {
		var b []byte
		select {
		case b = <-l.hi:
		default:
			select {
			case b = <-l.hi:
			case b = <-l.lo:
			case <-l.stop:
				return
			}
		}
		seq := uint16(atomic.AddUint32(&m.seq, 1))
		frames, err := splitFrames(m.stream, seq, b, l.mtu)
		if err != nil {
			continue
		}
		for _, f := range frames {
			if m.rad.Send(l.id, f) != nil {
				break
			}
		}
	}
}

func (m *Mesh) enqueue(l *mlink, b []byte, chat bool) {
	q := l.lo
	if chat {
		q = l.hi
	}
	select {
	case q <- b:
	default: // the link is badly behind; this packet still reaches people via other links
	}
}

func (m *Mesh) broadcast(p *pkt, chat bool) {
	b := encodePkt(p)
	for _, l := range m.links {
		m.enqueue(l, b, chat)
	}
}

// forward passes a packet on: to every link but the one it came from (a
// shared link gets it back, since others share it), one hop less to go.
func (m *Mesh) forward(from int64, p *pkt) {
	if p.Hist || p.TTL <= 1 {
		return
	}
	q := *p
	q.TTL--
	b := encodePkt(&q)
	chat := p.T == "msg" || p.T == "dm"
	for id, l := range m.links {
		if id != from || l.shared {
			m.enqueue(l, b, chat)
		}
	}
}

// dup records a message id and reports whether it was seen before.
func (m *Mesh) dup(mid string, now time.Time) bool {
	if mid == "" || len(mid) > 64 {
		return true
	}
	if _, ok := m.seen[mid]; ok {
		return true
	}
	m.seen[mid] = now
	return false
}

func validNode(id string) bool {
	if len(id) != 12 {
		return false
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func cleanRooms(list []string) []string {
	var out []string
	for _, r := range list {
		if n := cleanRoom(r); n != "" && !has(out, n) && len(out) < 20 {
			out = append(out, n)
		}
	}
	return out
}

func (m *Mesh) peerFor(id, nick string) *peer {
	pr := m.peers[id]
	if pr == nil {
		pr = &peer{id: id}
		m.peers[id] = pr
	}
	if n := cleanNick(nick); n != "" {
		pr.nick = n
	}
	if pr.nick == "" {
		pr.nick = "anon"
	}
	return pr
}

func (m *Mesh) handle(from int64, p *pkt) {
	now := time.Now()
	age := time.Duration(p.Age) * time.Millisecond
	if age < 0 || age > 24*time.Hour {
		age = 0
	}
	at := now.Add(-age)
	if p.T == "sync" {
		if l := m.links[from]; l != nil && now.Sub(m.syncedAt[from]) > 5*time.Second {
			m.syncedAt[from] = now
			m.catchUp(l)
		}
		return
	}
	if !validNode(p.From) || p.From == m.me || m.dup(p.MID, now) {
		return
	}
	switch p.T {
	case "ann":
		pr := m.peerFor(p.From, p.Nick)
		if at.After(pr.lastSeen) || pr.annMID == "" {
			pr.lastSeen, pr.annMID, pr.pub = at, p.MID, p.Pub
			pr.rooms = cleanRooms(p.Rooms)
		}
		pr.on = now.Sub(pr.lastSeen) < m.cfg.Offline
		m.hub.RemoteUser(pr.id, pr.nick, pr.on, pr.rooms)
	case "msg":
		pr := m.peerFor(p.From, p.Nick)
		if !p.Hist && at.After(pr.lastSeen) {
			pr.lastSeen, pr.on = at, true
		}
		m.hub.RemoteUser(pr.id, pr.nick, pr.on, nil)
		m.hub.RemoteMsg(Msg{TS: at.UnixMilli(), Room: p.Room, From: p.From, Text: p.Text})
		m.remember(p, at)
	case "dm":
		if p.To == m.me {
			text, err := openDM(m.key, p.Pub, p.From, p.To, p.Box)
			if err != nil {
				return
			}
			pr := m.peerFor(p.From, p.Nick)
			if pr.pub == "" {
				pr.pub = p.Pub
			}
			m.hub.RemoteUser(pr.id, pr.nick, pr.on || !p.Hist, nil)
			m.hub.RemoteMsg(Msg{TS: at.UnixMilli(), From: p.From, To: m.me, Text: text})
			return // it was for us: no need to pass it on
		}
	case "bye":
		if pr := m.peers[p.From]; pr != nil && pr.on {
			pr.on, pr.lastSeen = false, time.Time{}
			m.hub.RemoteUser(pr.id, pr.nick, false, nil)
		}
	default:
		return
	}
	m.forward(from, p)
}

func (m *Mesh) remember(p *pkt, at time.Time) {
	if p.Room == "" || m.cfg.Recent <= 0 {
		return
	}
	cp := *p
	cp.Hist, cp.Age = false, 0
	m.recent = append(m.recent, stored{cp, at})
	if len(m.recent) > m.cfg.Recent {
		m.recent = append([]stored(nil), m.recent[len(m.recent)-m.cfg.Recent:]...)
	}
}

// catchUp tells a newly linked laptop who is around and what was said
// lately. These copies go one hop only.
func (m *Mesh) catchUp(l *mlink) {
	now := time.Now()
	if m.me != "" {
		m.enqueue(l, encodePkt(m.myAnn(true)), false)
	}
	for _, pr := range m.peers {
		if pr.on && pr.annMID != "" {
			p := pkt{T: "ann", MID: pr.annMID, From: pr.id, Nick: pr.nick, Rooms: pr.rooms, Pub: pr.pub,
				Hist: true, Age: now.Sub(pr.lastSeen).Milliseconds()}
			m.enqueue(l, encodePkt(&p), false)
		}
	}
	for _, s := range m.recent {
		p := s.p
		p.Hist, p.Age = true, now.Sub(s.at).Milliseconds()
		m.enqueue(l, encodePkt(&p), false)
	}
}

func (m *Mesh) myAnn(hist bool) *pkt {
	return &pkt{T: "ann", MID: m.annMID, TTL: m.cfg.TTL, From: m.me, Nick: m.nick, Rooms: m.rooms, Pub: m.pub, Hist: hist}
}

func (m *Mesh) announce() {
	if m.me == "" {
		return
	}
	m.annMID = randHex(8)
	m.dup(m.annMID, time.Now())
	m.broadcast(m.myAnn(false), false)
	m.lastAnn = time.Now()
}

func (m *Mesh) sayBye() {
	if m.me != "" {
		m.broadcast(&pkt{T: "bye", MID: randHex(8), TTL: m.cfg.TTL, From: m.me}, true)
	}
}

// onLocal acts on what the person at this computer did.
func (m *Mesh) onLocal(e LocalEvent) {
	switch e.Kind {
	case "me":
		first := m.me == ""
		m.me, m.nick, m.rooms = e.ID, e.Nick, e.Rooms
		m.announce()
		if first { // links that came up before we knew who we are
			for _, l := range m.links {
				m.enqueue(l, encodePkt(&pkt{T: "sync", From: m.me}), false)
			}
		}
	case "say":
		mg := e.Msg
		now := time.Now()
		if m.me == "" {
			return
		}
		if mg.Room != "" {
			p := &pkt{T: "msg", MID: randHex(8), TTL: m.cfg.TTL, From: m.me, Nick: m.nick, Room: mg.Room, Text: mg.Text}
			m.dup(p.MID, now)
			m.remember(p, now)
			m.broadcast(p, true)
			return
		}
		pr := m.peers[mg.To]
		if pr == nil || pr.pub == "" {
			m.say("can't send that to " + mg.ToNick + ": they haven't been seen over Bluetooth yet")
			return
		}
		box, err := sealDM(m.key, pr.pub, m.me, mg.To, mg.Text)
		if err != nil {
			m.say("couldn't encrypt that message")
			return
		}
		p := &pkt{T: "dm", MID: randHex(8), TTL: m.cfg.TTL, From: m.me, Nick: m.nick, To: mg.To, Box: box, Pub: m.pub}
		m.dup(p.MID, now)
		m.broadcast(p, true)
		if !pr.on {
			m.say(pr.nick + " isn't nearby right now, so they may not get it")
		}
	}
}

func (m *Mesh) say(text string) { go m.notify(text) }

func (m *Mesh) tick(now time.Time) {
	if m.me != "" && now.Sub(m.lastAnn) > m.annEvery {
		m.announce()
	}
	for _, pr := range m.peers {
		if pr.on && now.Sub(pr.lastSeen) > m.cfg.Offline {
			pr.on = false
			m.hub.RemoteUser(pr.id, pr.nick, false, nil)
		}
	}
	if now.Sub(m.lastPrune) > 30*time.Second {
		for mid, t := range m.seen {
			if now.Sub(t) > 15*time.Minute {
				delete(m.seen, mid)
			}
		}
		m.asm.prune(now)
		m.lastPrune = now
	}
	m.publish()
}

// publish updates what Status and Note report, and redraws if it changed.
func (m *Mesh) publish() {
	s := meshSnap{state: m.state, noAdvert: m.noAdvert, total: len(m.links)}
	for _, l := range m.links {
		if !l.shared {
			s.direct++
		}
	}
	for _, pr := range m.peers {
		if pr.on {
			s.people++
		}
	}
	m.mu.Lock()
	changed := s != m.snap
	m.snap = s
	m.mu.Unlock()
	if changed {
		go m.change()
	}
}

func (m *Mesh) snapshot() meshSnap {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snap
}

// Note is a few words for the prompt when something needs attention.
func (m *Mesh) Note() string {
	s := m.snapshot()
	switch s.state {
	case "off":
		return "Bluetooth off"
	case "unauthorized":
		return "Bluetooth not allowed"
	case "unsupported":
		return "no Bluetooth"
	case "on":
		if s.people == 0 && s.direct == 0 {
			return "no one nearby yet"
		}
		return ""
	}
	switch m.permission() {
	case "ask":
		return "click Allow on the Bluetooth popup"
	case "denied":
		return "Bluetooth not allowed"
	}
	return "starting Bluetooth"
}

func (m *Mesh) permission() string {
	if p, ok := m.rad.(permissioner); ok {
		return p.Permission()
	}
	return "allowed"
}

// Status is the longer explanation /info shows.
func (m *Mesh) Status() string {
	s := m.snapshot()
	switch s.state {
	case "off":
		return "Bluetooth is off. Turn it on, or use Wi-Fi mode instead: nook wifi"
	case "unauthorized":
		return "this terminal isn't allowed to use Bluetooth. Allow it in System Settings > Privacy & Security > Bluetooth, or use: nook wifi"
	case "unsupported":
		return "this computer has no Bluetooth Low Energy. Use Wi-Fi mode instead: nook wifi"
	case "on":
	default:
		switch m.permission() {
		case "ask":
			return "macOS is asking whether nook may use Bluetooth: click Allow on the popup"
		case "denied":
			return "nook isn't allowed to use Bluetooth. Allow it in System Settings > Privacy & Security > Bluetooth, or use: nook wifi"
		}
		return "Bluetooth mode · starting up"
	}
	msg := fmt.Sprintf("Bluetooth mode · %d people around · linked to %d nearby", s.people, s.direct)
	if s.noAdvert {
		msg += " · this laptop's Bluetooth can't be found by others, but it can find them"
	}
	return msg
}
