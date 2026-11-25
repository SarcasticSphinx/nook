package main

import (
	"time"
	"unicode/utf8"
)

// Game rooms. The hub knows nothing about the games themselves: the person
// who creates a room runs the game on their own laptop, and the hub only
// passes small packets between the people in a room, found by its 6-digit
// code. In Bluetooth mode the packets also travel over the mesh, so a room
// can span laptops (turn-based games only: Bluetooth is too slow for the
// fast ones).

const (
	maxGamePkt   = 6000 // bytes in one game packet
	maxGamesEach = 4    // game rooms one person can be in at once
	maxGameRooms = 500
)

func validCode(s string) bool {
	if len(s) != 6 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// gallow is the game packets' token bucket: a fast game sends a picture of
// the board several times a second.
func (u *user) gallow(now time.Time) bool {
	const rate, burst = 40.0, 80.0
	if u.gfilled.IsZero() {
		u.gtokens = burst
	} else if u.gtokens += now.Sub(u.gfilled).Seconds() * rate; u.gtokens > burst {
		u.gtokens = burst
	}
	u.gfilled = now
	if u.gtokens < 1 {
		return false
	}
	u.gtokens--
	return true
}

// gameLocked handles the three game requests:
//
//	gsub   Arg = code        start getting a room's packets
//	gunsub Arg = code        stop
//	game   To = code, Arg = person's id ("" = everyone else in the room),
//	       Text = the packet (opaque to the hub)
func (h *Hub) gameLocked(u *user, req Req) Resp {
	switch req.Op {
	case "gsub":
		if !validCode(req.Arg) {
			return Resp{Err: "game codes are 6 digits"}
		}
		if u.games == nil {
			u.games = map[string]bool{}
		}
		if u.games[req.Arg] {
			return Resp{OK: true}
		}
		if len(u.games) >= maxGamesEach {
			return Resp{Err: "you're in too many games"}
		}
		set := h.games[req.Arg]
		if set == nil {
			if len(h.games) >= maxGameRooms {
				return Resp{Err: "too many games going on"}
			}
			set = map[string]bool{}
			h.games[req.Arg] = set
		}
		set[u.id] = true
		u.games[req.Arg] = true
		return Resp{OK: true}
	case "gunsub":
		h.ungameLocked(u, req.Arg)
		return Resp{OK: true}
	}
	code := req.To
	if !u.games[code] {
		return Resp{Err: "you're not in that game"}
	}
	if len(req.Text) == 0 || len(req.Text) > maxGamePkt || !utf8.ValidString(req.Text) {
		return Resp{Err: "bad game packet"}
	}
	if !u.gallow(time.Now()) {
		return Resp{Err: errSlow}
	}
	h.deliverGameLocked(u, code, req.Arg, req.Text)
	if !u.remote {
		h.emitLocked(LocalEvent{Kind: "game", ID: u.id, Nick: u.nick, Msg: Msg{Room: code, To: req.Arg, Text: req.Text}})
	}
	return Resp{OK: true}
}

func (h *Hub) deliverGameLocked(from *user, code, to, text string) {
	set := h.games[code]
	if len(set) == 0 {
		return
	}
	b := sse(Ev{T: "game", Code: code, User: from.info(), Text: text})
	if to != "" {
		if set[to] && to != from.id {
			if x := h.users[to]; x != nil {
				h.toUserLocked(x, b)
			}
		}
		return
	}
	for id := range set {
		if x := h.users[id]; x != nil && x != from {
			h.toUserLocked(x, b)
		}
	}
}

func (h *Hub) ungameLocked(u *user, code string) {
	delete(u.games, code)
	if set := h.games[code]; set != nil {
		delete(set, u.id)
		if len(set) == 0 {
			delete(h.games, code)
		}
	}
}

func (h *Hub) dropGamesLocked(u *user) {
	for code := range u.games {
		h.ungameLocked(u, code)
	}
}

// RemoteGame delivers a game packet that came over the mesh from someone on
// another laptop (added with RemoteUser first).
func (h *Hub) RemoteGame(from, code, to, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[from]
	if h.closed || u == nil || !u.remote || !validCode(code) {
		return
	}
	if len(text) == 0 || len(text) > maxGamePkt || !utf8.ValidString(text) {
		return
	}
	if !u.gallow(time.Now()) {
		return
	}
	h.deliverGameLocked(u, code, to, text)
}
