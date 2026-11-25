package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Games. /play opens a menu of small games to play alone or with others.
//
// A game with others lives in a room found by a 6-digit code. Whoever makes
// the room runs the game on their own laptop (the "host" of that game, not
// to be confused with the Wi-Fi host): everyone else sends it their key
// presses and gets back a picture of the game. The hub only passes packets
// along (see hubgames.go). An open room is also announced in #general and
// listed in everyone's games menu, so anyone can jump in.
//
// What keeps a room healthy:
//   - joining: "hi" is repeated until the host answers; a code nobody
//     answers for is reported as not found.
//   - joining late: you watch the round being played and get a seat in the
//     next one. A full room lets you watch.
//   - pictures carry a sequence number, so a late or doubled packet never
//     shows an older state; the host also repeats the picture now and then,
//     so a lost packet heals by itself.
//   - everyone pings the host and the host pings everyone. Someone who goes
//     quiet is marked away: a bot plays their cards, their snake stops.
//     When they come back (same person, even after a restart of nook) they
//     get their seat back.
//   - when the host's connection drops, the others see "waiting for the
//     host" and carry on when it's back; after 25 seconds the room closes.
//   - Bluetooth is slow, so fast games (Snake) need Wi-Fi mode with others.

const (
	lobbyCode = "000000" // where open rooms are listed

	phLobby = 0
	phPlay  = 1

	maxSpecs = 24
)

// How long things may stay quiet (variables so tests can be quick).
var (
	pingEvery    = 2 * time.Second
	awayAfter    = 7 * time.Second  // a player that quiet is away
	dropAfter    = 12 * time.Second // someone that quiet in the lobby is gone
	hostWaitNote = 5 * time.Second  // "waiting for the host"
	hostGone     = 25 * time.Second // the room is over
	joinTimeout  = 7 * time.Second
	adEvery      = 6 * time.Second
	adExpire     = 20 * time.Second
)

// gp is someone in a game: a seat at the table or a watcher.
type gp struct {
	ID   string `json:"i"`
	Nick string `json:"n"`
	Bot  bool   `json:"b,omitempty"`
	Away bool   `json:"a,omitempty"`
}

// gmsg is one game packet.
//
//	to the host:  hi ping in bye
//	from it:      lobby st ping end
//	lobby code:   who (anyone) ad gone (hosts of open rooms)
type gmsg struct {
	K  string          `json:"k"`
	G  string          `json:"g,omitempty"`  // which game
	C  string          `json:"c,omitempty"`  // room code (ads)
	N  int64           `json:"n,omitempty"`  // picture number
	Y  int             `json:"y,omitempty"`  // picture for seat Y-1; -1 everyone; 0 watchers
	HN string          `json:"hn,omitempty"` // host's name (ads)
	P  []gp            `json:"p,omitempty"`  // seats
	X  []gp            `json:"x,omitempty"`  // watchers
	S  int             `json:"s,omitempty"`  // phase
	M  int             `json:"m,omitempty"`  // most seats (ads)
	D  string          `json:"d,omitempty"`  // a key press
	V  json.RawMessage `json:"v,omitempty"`  // the picture
	T  string          `json:"t,omitempty"`  // why the room ended
	O  bool            `json:"o,omitempty"`  // open to everyone
}

type engOpts struct {
	rnd  *rand.Rand
	solo bool
	w, h int // the screen, for games that fit themselves to it when alone
}

// engine is a game's rules. Only the host has one.
type engine interface {
	every() time.Duration    // how often step runs
	step(now time.Time) bool // moves time on; true if anything changed
	input(seat int, d string) bool
	view(seat int) interface{}   // what seat sees (-1: watchers)
	shared() bool                // everyone sees the same thing
	setAway(seat int, away bool) // someone dropped off, or came back
	over() bool
}

// gameView draws a game from the pictures the host sends.
type gameView interface {
	load(b []byte) error
	draw(cv *canvas, x *gctx)
	key(ev *tcell.EventKey, x *gctx) string // what to send the host ("" nothing)
	size() (w, h int)                       // the smallest screen it fits
	over() bool                             // the round is finished
}

type gctx struct {
	me    int // your seat, -1 watching
	seats []gp
	solo  bool
	host  bool
	now   time.Time
}

func (x *gctx) name(i int) string {
	if i < 0 || i >= len(x.seats) {
		return "?"
	}
	if i == x.me {
		return "you"
	}
	return x.seats[i].Nick
}

type gameDef struct {
	id, name, blurb, keys string
	min, max              int
	solo, room            bool
	fast                  bool // too fast for Bluetooth
	bots                  bool // a room can fill seats with bots
	soloSeats             int  // seats when alone: you and bots
	newEng                func(seats []gp, o engOpts) engine
	newView               func() gameView
}

var gameDefs []*gameDef // filled in by each game's file, put in order below

func init() {
	order := []string{"snake", "maze", "cards", "ttt", "four"}
	rank := func(id string) int {
		for i, o := range order {
			if o == id {
				return i
			}
		}
		return len(order)
	}
	sort.SliceStable(gameDefs, func(i, j int) bool { return rank(gameDefs[i].id) < rank(gameDefs[j].id) })
}

func gameByID(id string) *gameDef {
	for _, d := range gameDefs {
		if d.id == id {
			return d
		}
	}
	return nil
}

// gameNet is what a game needs from the connection; a Client in the app.
type gameNet interface {
	GameSub(code string, on bool)
	GameSend(code, to string, v interface{})
	Me() (id, nick string)
	Bluetooth() bool
}

// gsess is one game being played: alone, as its host, or as a guest.
type gsess struct {
	net  gameNet
	def  *gameDef
	code string
	solo bool
	host bool
	open bool
	me   string

	hostID, hostNick string
	phase            int
	seats, specs     []gp
	eng              engine
	view             gameView
	haveView         bool
	rnd              *rand.Rand
	scr              func() (w, h int)

	nextStep, pubAt, lobbyAt, pingAt, adAt, whoAt time.Time
	seq, lastSeq                                  int64
	seen                                          map[string]time.Time
	hostSeen, joinAt, hiAt                        time.Time
	joined                                        bool
	paused, dirty                                 bool
	note                                          string
	ended                                         string // set when the room is over: why
}

func newCode(r *rand.Rand) string {
	for {
		c := fmt.Sprintf("%06d", r.Intn(1000000))
		if c != lobbyCode {
			return c
		}
	}
}

func (s *gsess) mySeat() int {
	for i, p := range s.seats {
		if p.ID == s.me {
			return i
		}
	}
	return -1
}

func (s *gsess) ctx(now time.Time) *gctx {
	return &gctx{me: s.mySeat(), seats: s.seats, solo: s.solo, host: s.host, now: now}
}

func botSeats(n int) []gp {
	names := []string{"pip", "juno", "orbit", "maple", "rook"}
	var out []gp
	for i := 0; i < n; i++ {
		out = append(out, gp{ID: "bot" + strconv.Itoa(i+1), Nick: names[i%len(names)] + "-bot", Bot: true})
	}
	return out
}

// startSolo starts a game against the computer.
func startSolo(n gameNet, d *gameDef, scr func() (int, int), now time.Time) *gsess {
	me, nick := n.Me()
	if me == "" {
		me = "me"
	}
	s := &gsess{net: n, def: d, solo: true, host: true, me: me, scr: scr,
		rnd: rand.New(rand.NewSource(now.UnixNano())), view: d.newView()}
	s.seats = append([]gp{{ID: me, Nick: nick}}, botSeats(imax(0, d.soloSeats-1))...)
	s.begin(now)
	return s
}

// hostRoom makes a room for others to join.
func hostRoom(n gameNet, d *gameDef, open bool, scr func() (int, int), now time.Time) *gsess {
	me, nick := n.Me()
	r := rand.New(rand.NewSource(now.UnixNano()))
	s := &gsess{net: n, def: d, host: true, open: open, me: me, code: newCode(r), rnd: r, scr: scr,
		hostID: me, hostNick: nick, view: d.newView(), seen: map[string]time.Time{}}
	s.seats = []gp{{ID: me, Nick: nick}}
	n.GameSub(s.code, true)
	s.sendLobby(now)
	return s
}

// joinRoom asks the host of a room to let us in.
func joinRoom(n gameNet, code string, now time.Time) *gsess {
	me, _ := n.Me()
	s := &gsess{net: n, code: code, me: me, joinAt: now, hostSeen: now}
	n.GameSub(code, true)
	s.hi(now)
	return s
}

func (s *gsess) hi(now time.Time) {
	s.hiAt = now
	s.net.GameSend(s.code, s.hostID, gmsg{K: "hi"})
}

// leave closes the session; a host closes the room for everyone.
func (s *gsess) leave() {
	if s.solo {
		return
	}
	if s.host {
		s.net.GameSend(s.code, "", gmsg{K: "end", T: s.hostNick + " closed the room"})
		if s.open {
			s.net.GameSend(lobbyCode, "", gmsg{K: "gone", C: s.code})
		}
	} else if s.hostID != "" {
		s.net.GameSend(s.code, s.hostID, gmsg{K: "bye"})
	}
	s.net.GameSub(s.code, false)
}

// begin starts a round with the people in the seats.
func (s *gsess) begin(now time.Time) {
	w, h := 80, 24
	if s.scr != nil {
		w, h = s.scr()
	}
	for i := range s.seats {
		s.seats[i].Away = false
	}
	s.eng = s.def.newEng(s.seats, engOpts{rnd: s.rnd, solo: s.solo, w: w, h: h})
	s.phase = phPlay
	s.nextStep = now.Add(s.eng.every())
	s.paused = false
	if !s.solo {
		for _, p := range s.seats {
			s.seen[p.ID] = now
		}
		s.sendLobby(now)
	}
	s.publish(now)
}

// backToLobby is "play again": everyone who's still here, watchers too,
// gets a seat while there are seats.
func (s *gsess) backToLobby(now time.Time) {
	if s.solo {
		s.begin(now)
		return
	}
	var seats []gp
	for _, p := range s.seats {
		if !p.Away || p.Bot {
			p.Away = false
			seats = append(seats, p)
		}
	}
	var specs []gp
	for _, p := range s.specs {
		if len(seats) < s.def.max {
			seats = append(seats, p)
		} else {
			specs = append(specs, p)
		}
	}
	s.seats, s.specs = seats, specs
	s.eng, s.phase, s.haveView = nil, phLobby, false
	s.sendLobby(now)
}

func (s *gsess) lobbyMsg() gmsg {
	return gmsg{K: "lobby", G: s.def.id, P: s.seats, X: s.specs, S: s.phase, O: s.open}
}

func (s *gsess) sendLobby(now time.Time) {
	s.lobbyAt = now
	if s.solo {
		return
	}
	s.net.GameSend(s.code, "", s.lobbyMsg())
	s.ad(now)
}

func (s *gsess) ad(now time.Time) {
	if !s.open || !s.host {
		return
	}
	s.adAt = now
	s.net.GameSend(lobbyCode, "", gmsg{K: "ad", G: s.def.id, C: s.code, HN: s.hostNick, P: s.seats, S: s.phase, M: s.def.max})
}

// publish shows everyone the game as it is now.
func (s *gsess) publish(now time.Time) {
	if s.eng == nil {
		return
	}
	s.seq++
	s.pubAt = now
	s.dirty = false
	mine := s.mySeat()
	if s.eng.shared() {
		b, err := json.Marshal(s.eng.view(-1))
		if err != nil {
			return
		}
		if s.view.load(b) == nil {
			s.haveView = true
		}
		if !s.solo {
			s.net.GameSend(s.code, "", gmsg{K: "st", N: s.seq, Y: -1, V: b})
		}
		return
	}
	if b, err := json.Marshal(s.eng.view(mine)); err == nil && s.view.load(b) == nil {
		s.haveView = true
	}
	if s.solo {
		return
	}
	for i, p := range s.seats {
		if p.Bot || p.ID == s.me || p.Away {
			continue
		}
		b, _ := json.Marshal(s.eng.view(i))
		s.net.GameSend(s.code, p.ID, gmsg{K: "st", N: s.seq, Y: i + 1, V: b})
	}
	if len(s.specs) > 0 {
		b, _ := json.Marshal(s.eng.view(-1))
		for _, p := range s.specs {
			s.net.GameSend(s.code, p.ID, gmsg{K: "st", N: s.seq, V: b})
		}
	}
}

// input is a key press from the person at this computer.
func (s *gsess) input(d string, now time.Time) {
	if d == "" || s.phase != phPlay {
		return
	}
	if s.host {
		if seat := s.mySeat(); seat >= 0 && s.eng != nil && s.eng.input(seat, d) {
			s.dirty = true
			if s.eng.every() > 80*time.Millisecond && !s.eng.shared() {
				s.publish(now) // turn games: show the move at once
			}
		}
		return
	}
	if s.hostID != "" && s.mySeat() >= 0 {
		s.net.GameSend(s.code, s.hostID, gmsg{K: "in", D: d})
	}
}

// pump runs often (every few dozen ms): game time, pings and timeouts.
func (s *gsess) pump(now time.Time) {
	if s.ended != "" {
		return
	}
	if s.host {
		s.pumpHost(now)
	} else {
		s.pumpGuest(now)
	}
}

func (s *gsess) pumpHost(now time.Time) {
	if s.phase == phPlay && s.eng != nil {
		if !s.paused && !now.Before(s.nextStep) {
			if s.eng.step(now) {
				s.dirty = true
			}
			s.nextStep = s.nextStep.Add(s.eng.every())
			if s.nextStep.Before(now) {
				s.nextStep = now.Add(s.eng.every())
			}
		}
		if s.dirty || (!s.solo && now.Sub(s.pubAt) > 1500*time.Millisecond) {
			s.publish(now)
		}
	}
	if s.solo {
		return
	}
	if now.Sub(s.lobbyAt) > pingEvery {
		s.sendLobby(now) // doubles as the host's ping
	}
	if s.open && now.Sub(s.adAt) > adEvery {
		s.ad(now)
	}
	changed := false
	for i := range s.seats {
		p := &s.seats[i]
		if p.Bot || p.ID == s.me {
			continue
		}
		quiet := now.Sub(s.seen[p.ID])
		if s.phase == phPlay && !p.Away && quiet > awayAfter {
			p.Away, changed = true, true
			if s.eng != nil {
				s.eng.setAway(i, true)
				s.dirty = true
			}
		}
	}
	if s.phase == phLobby {
		keep := s.seats[:0]
		for _, p := range s.seats {
			if p.Bot || p.ID == s.me || now.Sub(s.seen[p.ID]) < dropAfter {
				keep = append(keep, p)
			} else {
				changed = true
			}
		}
		s.seats = keep
	}
	keep := s.specs[:0]
	for _, p := range s.specs {
		if now.Sub(s.seen[p.ID]) < dropAfter {
			keep = append(keep, p)
		} else {
			changed = true
		}
	}
	s.specs = keep
	if changed {
		s.sendLobby(now)
	}
}

func (s *gsess) pumpGuest(now time.Time) {
	if !s.joined {
		if now.Sub(s.joinAt) > joinTimeout {
			s.ended = "no game with the code " + s.code + " (check it, or ask for a new one)"
			s.net.GameSub(s.code, false)
			return
		}
		if now.Sub(s.hiAt) > time.Second {
			s.hi(now)
		}
		return
	}
	if now.Sub(s.pingAt) > pingEvery {
		s.pingAt = now
		s.net.GameSend(s.code, s.hostID, gmsg{K: "ping"})
	}
	quiet := now.Sub(s.hostSeen)
	switch {
	case quiet > hostGone:
		s.ended = "lost the connection to " + s.hostNick + "'s game"
		s.net.GameSub(s.code, false)
	case quiet > hostWaitNote:
		s.note = "waiting for " + s.hostNick + "'s connection…"
		if now.Sub(s.hiAt) > 2*time.Second {
			s.hi(now) // in case the host restarted its connection and forgot us
		}
	default:
		s.note = ""
	}
}

// recv handles a packet for this room.
func (s *gsess) recv(from, nick string, m *gmsg, now time.Time) {
	if s.ended != "" || from == s.me {
		return
	}
	if s.host {
		s.recvHost(from, cleanNick(nick), m, now)
	} else {
		s.recvGuest(from, cleanNick(nick), m, now)
	}
}

func (s *gsess) find(list []gp, id string) int {
	for i, p := range list {
		if p.ID == id {
			return i
		}
	}
	return -1
}

func (s *gsess) recvHost(from, nick string, m *gmsg, now time.Time) {
	if s.solo {
		return
	}
	if nick == "" {
		nick = "guest"
	}
	seat, spec := s.find(s.seats, from), s.find(s.specs, from)
	if seat >= 0 || spec >= 0 {
		s.seen[from] = now
	}
	switch m.K {
	case "hi":
		s.seen[from] = now
		switch {
		case seat >= 0:
			s.seats[seat].Nick = nick
			if s.seats[seat].Away {
				s.seats[seat].Away = false
				if s.eng != nil {
					s.eng.setAway(seat, false)
				}
			}
		case spec >= 0:
		case s.phase == phLobby && len(s.seats) < s.def.max:
			s.seats = append(s.seats, gp{ID: from, Nick: nick})
		case len(s.specs) < maxSpecs:
			s.specs = append(s.specs, gp{ID: from, Nick: nick})
		default:
			s.net.GameSend(s.code, from, gmsg{K: "end", T: "that room is full"})
			return
		}
		s.sendLobby(now)
		if s.phase == phPlay {
			s.publish(now)
		}
	case "ping":
		if seat >= 0 && s.seats[seat].Away {
			s.seats[seat].Away = false
			if s.eng != nil {
				s.eng.setAway(seat, false)
			}
			s.sendLobby(now)
			s.publish(now)
		} else if seat < 0 && spec < 0 {
			// We forgot them (our connection restarted): ask them to say hi.
			s.net.GameSend(s.code, from, s.lobbyMsg())
		}
	case "in":
		if seat >= 0 && s.phase == phPlay && s.eng != nil && len(m.D) <= 64 {
			if s.seats[seat].Away {
				s.seats[seat].Away = false
				s.eng.setAway(seat, false)
			}
			if s.eng.input(seat, m.D) {
				s.dirty = true
				if !s.eng.shared() {
					s.publish(now)
				}
			}
		}
	case "bye":
		switch {
		case seat >= 0 && s.phase == phLobby:
			s.seats = append(s.seats[:seat], s.seats[seat+1:]...)
		case seat >= 0:
			s.seats[seat].Away = true
			if s.eng != nil {
				s.eng.setAway(seat, true)
				s.dirty = true
			}
		case spec >= 0:
			s.specs = append(s.specs[:spec], s.specs[spec+1:]...)
		default:
			return
		}
		delete(s.seen, from)
		s.sendLobby(now)
	}
}

func (s *gsess) recvGuest(from, nick string, m *gmsg, now time.Time) {
	if s.hostID != "" && from != s.hostID {
		return // only the host speaks for the room
	}
	switch m.K {
	case "lobby":
		d := gameByID(m.G)
		if d == nil {
			s.ended = "that game needs a newer nook (nook update)"
			s.leave()
			return
		}
		if s.def == nil || s.def != d {
			if d.fast && s.net.Bluetooth() {
				s.hostID = from
				s.ended = d.name + " with others needs Wi-Fi mode (nook wifi): Bluetooth is too slow for it"
				s.leave()
				return
			}
			s.def, s.view, s.haveView = d, d.newView(), false
		}
		s.hostID, s.hostNick, s.joined, s.hostSeen = from, nick, true, now
		s.open = m.O
		s.seats, s.specs = cleanGps(m.P, d.max), cleanGps(m.X, maxSpecs)
		if m.S == phLobby {
			s.haveView = false
		}
		s.phase = m.S
		if s.mySeat() < 0 && s.find(s.specs, s.me) < 0 {
			s.hi(now) // we're not on the list (the host forgot us): ask again
		}
	case "st":
		if !s.joined || m.N <= s.lastSeq {
			return
		}
		mine := s.mySeat()
		if m.Y != -1 && m.Y != mine+1 {
			return
		}
		s.hostSeen = now
		if s.view != nil && s.view.load(m.V) == nil {
			s.lastSeq, s.haveView, s.phase = m.N, true, phPlay
		}
	case "end":
		if s.joined || s.hostID == "" {
			s.ended = m.T
			if s.ended == "" {
				s.ended = "the room closed"
			}
			s.net.GameSub(s.code, false)
		}
	}
}

func cleanGps(in []gp, max int) []gp {
	var out []gp
	for _, p := range in {
		if len(out) >= max || len(p.ID) > 40 {
			break
		}
		n := cleanNick(p.Nick)
		if n == "" {
			n = "?"
		}
		out = append(out, gp{ID: p.ID, Nick: n, Bot: p.Bot, Away: p.Away})
	}
	return out
}
