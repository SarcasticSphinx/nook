package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The hub is the one computer everyone's messages go through. Any copy of
// nook can be the hub; it keeps everything in memory only, so when the hub
// stops, the history is gone.

const (
	roomHist    = 200
	dmHist      = 100
	maxSessions = 300
	maxPerIP    = 24
	maxRooms    = 200
	maxUsers    = 1000
	general     = "general"

	errNotConnected = "not connected"
)

var (
	errBusy      = errors.New("busy")
	errBadClient = errors.New("this copy of nook is too old for this room")

	// discoverFn is swapped out in tests.
	discoverFn = discover
)

type HubOpts struct {
	Port    int // first TCP port to try; 0 = any free port
	UDPPort int // discovery port to answer on; 0 = don't answer
	Prio    int // 1 = `nook host`, 0 = started automatically
	Key     string
	Elect   bool // hand over to a better hub if one shows up

	// Mesh makes a private hub for Bluetooth mode: it listens on this
	// computer only, holds people who live on other laptops (RemoteUser,
	// RemoteMsg) and reports what the person here does on LocalEvents.
	Mesh bool
}

// LocalEvent is something the person at this computer did that the mesh has
// to tell everyone else about.
type LocalEvent struct {
	Kind  string // "me" (online, renamed or changed rooms) or "say"
	ID    string
	Nick  string
	Rooms []string
	Msg   Msg
}

type Hub struct {
	mu       sync.Mutex
	id       string
	started  time.Time
	opts     HubOpts
	lock     string
	port     int
	users    map[string]*user
	byTok    map[string]*user
	byNick   map[string]*user
	rooms    map[string]*room
	dms      map[string][]Msg
	sessions map[string]*session
	perIP    map[string]int
	nextID   int64
	closed   bool
	srv      *http.Server
	udp      *net.UDPConn
	done     chan struct{}
	once     sync.Once
	disc     func(int, time.Duration) []HubInfo
	local    chan LocalEvent
}

type user struct {
	id, nick, tok string
	want          string // the name a remote person asked for
	remote        bool   // lives on another laptop (Bluetooth mode)
	on            bool
	rooms         map[string]bool
	sess          map[*session]bool
	offTimer      *time.Timer
	tokens        float64
	filled        time.Time
}

type room struct {
	name    string
	members map[string]bool
	hist    []Msg
}

type session struct {
	id     string
	u      *user
	ip     string
	out    chan []byte
	kill   chan struct{}
	once   sync.Once
	remote bool
	term   bool
	osName string
	since  time.Time
}

func (s *session) stop() { s.once.Do(func() { close(s.kill) }) }

func (u *user) info() *UserInfo { return &UserInfo{ID: u.id, Nick: u.nick, On: u.on} }

// allow is a small token bucket: bursts of 8 messages, 4 per second after.
func (u *user) allow(now time.Time) bool {
	const rate, burst = 4.0, 8.0
	if u.filled.IsZero() {
		u.tokens = burst
	} else if u.tokens += now.Sub(u.filled).Seconds() * rate; u.tokens > burst {
		u.tokens = burst
	}
	u.filled = now
	if u.tokens < 1 {
		return false
	}
	u.tokens--
	return true
}

func (r *room) info() *RoomInfo {
	m := make([]string, 0, len(r.members))
	for id := range r.members {
		m = append(m, id)
	}
	sort.Strings(m)
	return &RoomInfo{Name: r.name, Members: m}
}

func StartHub(o HubOpts) (*Hub, error) {
	h := &Hub{
		id:       randHex(6),
		started:  time.Now(),
		opts:     o,
		lock:     lockHash(o.Key),
		users:    map[string]*user{},
		byTok:    map[string]*user{},
		byNick:   map[string]*user{},
		rooms:    map[string]*room{general: {name: general, members: map[string]bool{}}},
		dms:      map[string][]Msg{},
		sessions: map[string]*session{},
		perIP:    map[string]int{},
		done:     make(chan struct{}),
		disc:     discoverFn,
	}
	var ln net.Listener
	var err error
	if o.Mesh {
		h.local = make(chan LocalEvent, 1024)
		ln, err = net.Listen("tcp", "127.0.0.1:0") // nobody else can reach it
	} else {
		ln, err = listenTCP(o.Port)
	}
	if err != nil {
		return nil, err
	}
	h.port = ln.Addr().(*net.TCPAddr).Port
	if o.UDPPort > 0 {
		if u, err := net.ListenUDP("udp4", &net.UDPAddr{Port: o.UDPPort}); err == nil {
			h.udp = u
			go h.answer()
		}
	}
	h.srv = &http.Server{
		Handler:           h.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          quietLog,
	}
	go h.srv.Serve(ln)
	if o.Elect && o.UDPPort > 0 {
		go h.elect()
	}
	return h, nil
}

func listenTCP(port int) (net.Listener, error) {
	if port == 0 {
		return net.Listen("tcp", ":0")
	}
	var err error
	for p := port; p < port+10 && p <= 65535; p++ {
		var ln net.Listener
		if ln, err = net.Listen("tcp", ":"+strconv.Itoa(p)); err == nil {
			return ln, nil
		}
	}
	return nil, err
}

func (h *Hub) Port() int             { return h.port }
func (h *Hub) Done() <-chan struct{} { return h.done }

// URLs are the addresses other people on the network can use to reach us.
func (h *Hub) URLs() []string {
	if h.opts.Mesh {
		return nil
	}
	var out []string
	for _, ip := range lanIPs() {
		out = append(out, "http://"+net.JoinHostPort(ip.String(), strconv.Itoa(h.port)))
	}
	return out
}

func (h *Hub) Online() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, u := range h.users {
		if u.on {
			n++
		}
	}
	return n
}

func (h *Hub) Info() HubInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	inf := HubInfo{V: 1, ID: h.id, Port: h.port, Started: h.started.UnixMilli(), Prio: h.opts.Prio, Lock: h.lock}
	for _, u := range h.users {
		if !u.on {
			continue
		}
		inf.Users++
		for s := range u.sess {
			if s.remote {
				inf.Remote++
				break
			}
		}
	}
	return inf
}

// answer replies to discovery broadcasts.
func (h *Hub) answer() {
	buf := make([]byte, 64)
	for {
		n, from, err := h.udp.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			select {
			case <-h.done:
				return
			case <-time.After(50 * time.Millisecond): // e.g. a stray ICMP error on Windows
			}
			continue
		}
		if n >= len(qMagic) && string(buf[:len(qMagic)]) == qMagic {
			b, _ := json.Marshal(h.Info())
			_, _ = h.udp.WriteToUDP(append([]byte(rMagic), b...), from)
		}
	}
}

// elect keeps looking for other hubs on the network. If two people started a
// room at the same moment, the worse hub sends its people to the better one
// so the class ends up in a single room.
func (h *Hub) elect() {
	wait := 2*time.Second + time.Duration(rand.Intn(1000))*time.Millisecond
	for {
		select {
		case <-h.done:
			return
		case <-time.After(wait):
		}
		wait = 4*time.Second + time.Duration(rand.Intn(1000))*time.Millisecond
		me := h.Info()
		var best *HubInfo
		for _, o := range h.disc(h.opts.UDPPort, 900*time.Millisecond) {
			o := o
			if o.ID == me.ID || o.Lock != me.Lock || !better(o, me) {
				continue
			}
			if best == nil || better(o, *best) {
				best = &o
			}
		}
		if best != nil {
			h.Close(h.reachURL(*best))
			return
		}
	}
}

// reachURL is the address our people should use to reach hub o.
func (h *Hub) reachURL(o HubInfo) string {
	ip := o.LAN
	if ip == "" { // only heard over loopback, so it runs on this computer
		ip = "127.0.0.1"
		if ips := lanIPs(); len(ips) > 0 {
			ip = ips[0].String()
		}
	}
	return "http://" + net.JoinHostPort(ip, strconv.Itoa(o.Port))
}

// Close stops the hub. With moved set, everyone is sent there; otherwise
// they are told the host left (and who is best placed to take over).
func (h *Hub) Close(moved string) {
	h.once.Do(func() {
		h.mu.Lock()
		h.closed = true
		ev := Ev{T: "moved", URL: moved}
		if moved == "" {
			ev = Ev{T: "bye"}
			ev.Next, ev.URL = h.successorLocked()
		}
		b := sse(ev)
		n := len(h.sessions)
		for _, s := range h.sessions {
			h.sendLocked(s, b)
			s.stop()
		}
		for _, u := range h.users {
			if u.offTimer != nil {
				u.offTimer.Stop()
			}
		}
		h.mu.Unlock()
		if h.udp != nil {
			h.udp.Close()
		}
		if n > 0 {
			time.Sleep(150 * time.Millisecond) // let the last event reach people
		}
		h.srv.Close()
		close(h.done)
	})
}

// successorLocked picks who should host next: someone on the terminal app
// (browsers can't host), preferring Macs and Linux because Windows firewalls
// often block incoming connections, then whoever has been here longest.
func (h *Hub) successorLocked() (uid, url string) {
	var best *session
	rank := func(s *session) int {
		if s.osName == "windows" {
			return 1
		}
		return 0
	}
	for _, s := range h.sessions {
		if !s.term || !s.remote {
			continue
		}
		if best == nil || rank(s) < rank(best) || (rank(s) == rank(best) && s.since.Before(best.since)) {
			best = s
		}
	}
	if best == nil {
		return "", ""
	}
	return best.u.id, "http://" + net.JoinHostPort(best.ip, strconv.Itoa(h.basePort()))
}

func (h *Hub) basePort() int {
	if h.opts.Port > 0 {
		return h.opts.Port
	}
	return h.port
}

func (h *Hub) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.index)
	mux.HandleFunc("/events", h.events)
	mux.HandleFunc("/api", h.api)
	mux.HandleFunc("/ping", h.ping)
	mux.HandleFunc("/get", h.getPage)
	mux.HandleFunc("/dl/", h.download)
	mux.HandleFunc("/install.sh", h.installer)
	mux.HandleFunc("/install.ps1", h.installer)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func (h *Hub) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; connect-src *")
	io.WriteString(w, indexHTML)
}

func (h *Hub) ping(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(h.Info())
}

func sse(ev Ev) []byte {
	b, _ := json.Marshal(ev)
	out := make([]byte, 0, len(b)+8)
	out = append(out, "data: "...)
	out = append(out, b...)
	return append(out, '\n', '\n')
}

var pingEv = sse(Ev{T: "ping"})

func (h *Hub) events(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	send := func(b []byte) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(15 * time.Second))
		if _, err := w.Write(b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if h.opts.Key != "" && subtle.ConstantTimeCompare([]byte(q.Get("key")), []byte(h.opts.Key)) != 1 {
		send(sse(Ev{T: "err", Text: "wrong key for this room"}))
		return
	}
	s, hello, err := h.join(q.Get("tok"), q.Get("nick"), hostOf(r.RemoteAddr), q.Get("c") != "web", q.Get("os"))
	if err == errBusy {
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		send(sse(Ev{T: "err", Text: err.Error()}))
		return
	}
	defer h.leave(s)
	if !send(hello) {
		return
	}
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case b := <-s.out:
			if !send(b) {
				return
			}
		case <-ping.C:
			if !send(pingEv) {
				return
			}
		case <-s.kill:
			for { // flush whatever is queued (e.g. "moved"), then hang up
				select {
				case b := <-s.out:
					if !send(b) {
						return
					}
				default:
					return
				}
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (h *Hub) join(tok, nick, ip string, term bool, osName string) (*session, []byte, error) {
	if !validTok(tok) {
		return nil, nil, errBadClient
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || len(h.sessions) >= maxSessions || h.perIP[ip] >= maxPerIP {
		return nil, nil, errBusy
	}
	u := h.byTok[tok]
	if u == nil {
		if len(h.users) >= maxUsers {
			return nil, nil, errBusy
		}
		u = &user{id: hashHex("uid|"+tok, 12), tok: tok, rooms: map[string]bool{general: true}, sess: map[*session]bool{}}
		u.nick = h.freeNickLocked(nick)
		h.users[u.id] = u
		h.byTok[tok] = u
		h.byNick[strings.ToLower(u.nick)] = u
		h.rooms[general].members[u.id] = true
		h.broadcastLocked(Ev{T: "room", Room: h.rooms[general].info()})
	} else if n := cleanNick(nick); n != "" && n != u.nick {
		_ = h.renameLocked(u, n)
	}
	if u.offTimer != nil {
		u.offTimer.Stop()
		u.offTimer = nil
	}
	if !u.on {
		u.on = true
		h.broadcastLocked(Ev{T: "user", User: u.info()})
	}
	s := &session{
		id: randHex(16), u: u, ip: ip,
		out: make(chan []byte, 512), kill: make(chan struct{}),
		remote: !isLoopback(ip), term: term, osName: osName, since: time.Now(),
	}
	h.sessions[s.id] = s
	h.perIP[ip]++
	u.sess[s] = true
	h.emitMeLocked(u)
	return s, h.helloLocked(s), nil
}

func (h *Hub) helloLocked(s *session) []byte {
	u := s.u
	ev := Ev{T: "hello", SID: s.id, Hub: h.id, Now: nowMs(), Me: u.info(), Addr: h.URLs(), Peers: h.peersLocked()}
	for _, x := range h.users {
		ev.Users = append(ev.Users, *x.info())
	}
	sort.Slice(ev.Users, func(i, j int) bool { return strings.ToLower(ev.Users[i].Nick) < strings.ToLower(ev.Users[j].Nick) })
	for _, r := range h.rooms {
		ev.Rooms = append(ev.Rooms, *r.info())
	}
	sort.Slice(ev.Rooms, func(i, j int) bool { return ev.Rooms[i].Name < ev.Rooms[j].Name })
	for name := range u.rooms {
		if r := h.rooms[name]; r != nil {
			ev.Hist = append(ev.Hist, tail(r.hist, 100)...)
		}
	}
	for k, list := range h.dms {
		if pairHas(k, u.id) {
			ev.Hist = append(ev.Hist, tail(list, 50)...)
		}
	}
	sort.Slice(ev.Hist, func(i, j int) bool { return ev.Hist[i].ID < ev.Hist[j].ID })
	return sse(ev)
}

// peersLocked lists places a new host could appear if this one goes away:
// every computer running the terminal app. Browsers use it to follow along.
func (h *Hub) peersLocked() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range h.sessions {
		if !s.term || !s.remote {
			continue
		}
		u := "http://" + net.JoinHostPort(s.ip, strconv.Itoa(h.basePort()))
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	sort.Strings(out)
	return out
}

func (h *Hub) leave(s *session) {
	s.stop()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[s.id] != s {
		return
	}
	delete(h.sessions, s.id)
	h.perIP[s.ip]--
	if h.perIP[s.ip] <= 0 {
		delete(h.perIP, s.ip)
	}
	u := s.u
	delete(u.sess, s)
	if len(u.sess) > 0 || !u.on || h.closed {
		return
	}
	// Wait a moment before saying someone left: Wi-Fi hiccups reconnect fast.
	u.offTimer = time.AfterFunc(4*time.Second, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(u.sess) == 0 && u.on && !h.closed {
			u.on = false
			h.broadcastLocked(Ev{T: "user", User: u.info()})
		}
	})
}

func (h *Hub) sendLocked(s *session, b []byte) {
	select {
	case s.out <- b:
	default:
		s.stop() // too far behind: drop it, it reconnects and gets a fresh copy
	}
}

func (h *Hub) broadcastLocked(ev Ev) {
	b := sse(ev)
	for _, s := range h.sessions {
		h.sendLocked(s, b)
	}
}

func (h *Hub) toUserLocked(u *user, b []byte) {
	for s := range u.sess {
		h.sendLocked(s, b)
	}
}

func (h *Hub) api(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req Req
	resp := Resp{Err: "bad request"}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req) == nil {
		resp = h.do(req)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Hub) do(req Req) Resp {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[req.SID]
	if s == nil || h.closed {
		return Resp{Err: errNotConnected}
	}
	u := s.u
	switch req.Op {
	case "say":
		text := cleanText(req.Text)
		if text == "" {
			return Resp{Err: "empty message"}
		}
		if !u.allow(time.Now()) {
			return Resp{Err: "slow down a little"}
		}
		m := Msg{TS: nowMs(), From: u.id, Nick: u.nick, Text: text}
		switch {
		case strings.HasPrefix(req.To, "#"):
			rm := h.rooms[cleanRoom(req.To)]
			if rm == nil || !rm.members[u.id] {
				return Resp{Err: "you're not in " + req.To + " (/join it first)"}
			}
			h.nextID++
			m.ID, m.Room = h.nextID, rm.name
			rm.hist = appendCap(rm.hist, m, roomHist)
			b := sse(Ev{T: "msg", Msg: &m})
			for id := range rm.members {
				if x := h.users[id]; x != nil {
					h.toUserLocked(x, b)
				}
			}
		case strings.HasPrefix(req.To, "@"):
			peer := h.users[req.To[1:]]
			if peer == nil {
				return Resp{Err: "that person isn't here"}
			}
			h.nextID++
			m.ID, m.To, m.ToNick = h.nextID, peer.id, peer.nick
			k := pairKey(u.id, peer.id)
			h.dms[k] = appendCap(h.dms[k], m, dmHist)
			b := sse(Ev{T: "msg", Msg: &m})
			h.toUserLocked(u, b)
			if peer != u {
				h.toUserLocked(peer, b)
			}
		default:
			return Resp{Err: "pick a room or a person first"}
		}
		h.emitLocked(LocalEvent{Kind: "say", ID: u.id, Nick: u.nick, Msg: m})
		return Resp{OK: true}

	case "join":
		name := cleanRoom(req.Arg)
		if name == "" {
			return Resp{Err: "room names use a-z, 0-9, - and _"}
		}
		rm := h.rooms[name]
		if rm == nil {
			if len(h.rooms) >= maxRooms {
				return Resp{Err: "too many rooms"}
			}
			rm = &room{name: name, members: map[string]bool{}}
			h.rooms[name] = rm
		}
		if !rm.members[u.id] {
			rm.members[u.id] = true
			u.rooms[name] = true
			h.broadcastLocked(Ev{T: "room", Room: rm.info()})
			h.toUserLocked(u, sse(Ev{T: "hist", Hist: tail(rm.hist, 100)}))
			h.emitMeLocked(u)
		}
		return Resp{OK: true, Room: name}

	case "leave":
		name := cleanRoom(req.Arg)
		rm := h.rooms[name]
		if rm == nil || !rm.members[u.id] {
			return Resp{Err: "you're not in #" + name}
		}
		delete(rm.members, u.id)
		delete(u.rooms, name)
		if len(rm.members) == 0 && name != general {
			delete(h.rooms, name)
			h.broadcastLocked(Ev{T: "room", Room: &RoomInfo{Name: name, Gone: true}})
		} else {
			h.broadcastLocked(Ev{T: "room", Room: rm.info()})
		}
		h.emitMeLocked(u)
		return Resp{OK: true, Room: name}

	case "nick":
		n := cleanNick(req.Arg)
		if n == "" {
			return Resp{Err: "names use letters, digits, _ . - (max 16)"}
		}
		if err := h.renameLocked(u, n); err != nil {
			return Resp{Err: err.Error()}
		}
		h.emitMeLocked(u)
		return Resp{OK: true, Nick: u.nick}
	}
	return Resp{Err: "unknown request"}
}

// Names stay reserved for as long as the hub runs, so nobody can take yours
// (and your messages) while you're away.
func (h *Hub) renameLocked(u *user, n string) error {
	if other := h.byNick[strings.ToLower(n)]; other != nil && other != u {
		return fmt.Errorf("%s is taken", n)
	}
	h.setNickLocked(u, n)
	return nil
}

func (h *Hub) setNickLocked(u *user, n string) {
	if n == u.nick {
		return
	}
	if h.byNick[strings.ToLower(u.nick)] == u {
		delete(h.byNick, strings.ToLower(u.nick))
	}
	u.nick = n
	h.byNick[strings.ToLower(n)] = u
	h.broadcastLocked(Ev{T: "user", User: u.info()})
}

func (h *Hub) freeNickLocked(want string) string {
	n := cleanNick(want)
	if n == "" {
		n = "anon"
	}
	if h.byNick[strings.ToLower(n)] == nil {
		return n
	}
	for i := 2; ; i++ {
		suffix := strconv.Itoa(i)
		base := n
		if len(base)+len(suffix) > maxNick {
			base = base[:maxNick-len(suffix)]
		}
		if c := base + suffix; h.byNick[strings.ToLower(c)] == nil {
			return c
		}
	}
}

func appendCap(list []Msg, m Msg, n int) []Msg {
	list = append(list, m)
	if len(list) > n+n/4 { // trim in chunks so we don't copy on every message
		list = append([]Msg(nil), list[len(list)-n:]...)
	}
	return list
}

func tail(list []Msg, n int) []Msg {
	if len(list) > n {
		return list[len(list)-n:]
	}
	return list
}

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func pairHas(k, id string) bool {
	a, b, _ := strings.Cut(k, "|")
	return a == id || b == id
}

// ---- Bluetooth mode: people who live on other laptops ----

// LocalEvents reports what the person at this computer does (Mesh hubs only).
func (h *Hub) LocalEvents() <-chan LocalEvent { return h.local }

func (h *Hub) emitLocked(ev LocalEvent) {
	if h.local == nil {
		return
	}
	select {
	case h.local <- ev:
	default: // the mesh is far behind; it re-announces on its own anyway
	}
}

func (h *Hub) emitMeLocked(u *user) {
	if !u.remote {
		h.emitLocked(LocalEvent{Kind: "me", ID: u.id, Nick: u.nick, Rooms: roomNames(u)})
	}
}

func roomNames(u *user) []string {
	out := make([]string, 0, len(u.rooms))
	for name := range u.rooms {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// RemoteUser adds or updates someone on another laptop. With rooms == nil
// their rooms are left as they are.
func (h *Hub) RemoteUser(id, nick string, on bool, rooms []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || id == "" {
		return
	}
	u := h.users[id]
	if u == nil {
		if len(h.users) >= maxUsers {
			return
		}
		u = &user{id: id, remote: true, want: cleanNick(nick), on: on, rooms: map[string]bool{}, sess: map[*session]bool{}}
		u.nick = h.freeNickLocked(nick)
		h.users[id] = u
		h.byNick[strings.ToLower(u.nick)] = u
		h.broadcastLocked(Ev{T: "user", User: u.info()})
	} else if !u.remote {
		return // never let the mesh touch the person at this computer
	} else {
		if n := cleanNick(nick); n != "" && n != u.want {
			u.want = n
			if other := h.byNick[strings.ToLower(n)]; other == nil || other == u {
				h.setNickLocked(u, n)
			} else {
				h.setNickLocked(u, h.freeNickLocked(n)) // two people picked one name
			}
		}
		if u.on != on {
			u.on = on
			h.broadcastLocked(Ev{T: "user", User: u.info()})
		}
	}
	if rooms == nil {
		return
	}
	want := map[string]bool{}
	for _, r := range rooms {
		if n := cleanRoom(r); n != "" {
			want[n] = true
		}
	}
	for name := range u.rooms {
		if !want[name] {
			h.leaveRoomLocked(u, name)
		}
	}
	for name := range want {
		if !u.rooms[name] {
			h.joinRoomLocked(u, name)
		}
	}
}

func (h *Hub) joinRoomLocked(u *user, name string) *room {
	rm := h.rooms[name]
	if rm == nil {
		if len(h.rooms) >= maxRooms {
			return nil
		}
		rm = &room{name: name, members: map[string]bool{}}
		h.rooms[name] = rm
	}
	if !rm.members[u.id] {
		rm.members[u.id] = true
		u.rooms[name] = true
		h.broadcastLocked(Ev{T: "room", Room: rm.info()})
	}
	return rm
}

func (h *Hub) leaveRoomLocked(u *user, name string) {
	delete(u.rooms, name)
	rm := h.rooms[name]
	if rm == nil || !rm.members[u.id] {
		return
	}
	delete(rm.members, u.id)
	if len(rm.members) == 0 && name != general {
		delete(h.rooms, name)
		h.broadcastLocked(Ev{T: "room", Room: &RoomInfo{Name: name, Gone: true}})
	} else {
		h.broadcastLocked(Ev{T: "room", Room: rm.info()})
	}
}

// RemoteMsg delivers a message that arrived from another laptop. The sender
// must have been added with RemoteUser first.
func (h *Hub) RemoteMsg(m Msg) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[m.From]
	if h.closed || u == nil || !u.remote {
		return
	}
	if m.Text = cleanText(m.Text); m.Text == "" {
		return
	}
	m.Nick = u.nick
	switch {
	case m.Room != "":
		rm := h.rooms[cleanRoom(m.Room)]
		if rm == nil || !rm.members[u.id] { // they're obviously in it
			if rm = h.joinRoomLocked(u, cleanRoom(m.Room)); rm == nil {
				return
			}
		}
		h.nextID++
		m.ID, m.Room, m.To = h.nextID, rm.name, ""
		rm.hist = appendCap(rm.hist, m, roomHist)
		b := sse(Ev{T: "msg", Msg: &m})
		for id := range rm.members {
			if x := h.users[id]; x != nil && !x.remote {
				h.toUserLocked(x, b)
			}
		}
	case m.To != "":
		to := h.users[m.To]
		if to == nil || to.remote {
			return
		}
		h.nextID++
		m.ID, m.ToNick = h.nextID, to.nick
		k := pairKey(u.id, to.id)
		h.dms[k] = appendCap(h.dms[k], m, dmHist)
		h.toUserLocked(to, sse(Ev{T: "msg", Msg: &m}))
	}
}
