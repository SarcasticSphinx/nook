package main

import (
	"bytes"
	"encoding/json"
	"io"
)

// The client's side of game rooms: packets go out through their own queue
// (so a busy game never holds up chat, nor chat a game), and the rooms
// you're in are asked for again whenever the connection comes back.

func (c *Client) SetOnGame(f func(code, from, nick, text string)) {
	c.mu.Lock()
	c.onGame = f
	c.mu.Unlock()
}

func (c *Client) gameIn(ev *Ev) {
	if ev.User == nil || !validCode(ev.Code) {
		return
	}
	c.mu.Lock()
	f, in := c.onGame, c.gameSubs[ev.Code]
	c.mu.Unlock()
	if f != nil && in {
		f(ev.Code, ev.User.ID, ev.User.Nick, ev.Text)
	}
}

// Bluetooth reports whether this is Bluetooth mode, where fast games can't
// be played with others.
func (c *Client) Bluetooth() bool { return c.o.Status != nil }

// Online reports whether the connection is up right now.
func (c *Client) Online() bool { _, _, on := c.conn(); return on }

// Target is the chat you're in.
func (c *Client) Target() string { c.mu.Lock(); defer c.mu.Unlock(); return c.target }

// Me is your id and name.
func (c *Client) Me() (id, nick string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.me.ID, c.nick
}

// GameSub starts or stops getting a game room's packets.
func (c *Client) GameSub(code string, on bool) {
	c.mu.Lock()
	if c.gameSubs[code] == on {
		c.mu.Unlock()
		return
	}
	if on {
		c.gameSubs[code] = true
	} else {
		delete(c.gameSubs, code)
	}
	c.mu.Unlock()
	op := "gunsub"
	if on {
		op = "gsub"
	}
	c.gameReq(Req{Op: op, Arg: code})
}

// GameSend sends a packet to one person in a game room (to = their id) or
// to everyone else in it (to = "").
func (c *Client) GameSend(code, to string, v interface{}) {
	b, err := json.Marshal(v)
	if err != nil || len(b) > maxGamePkt {
		return
	}
	c.gameReq(Req{Op: "game", To: code, Arg: to, Text: string(b)})
}

func (c *Client) gameReq(r Req) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.quit {
		return
	}
	select {
	case c.gameq <- r:
	default: // far behind: the next picture of the game replaces this one
	}
}

func (c *Client) resubLocked() {
	for code := range c.gameSubs {
		select {
		case c.gameq <- Req{Op: "gsub", Arg: code}:
		default:
		}
	}
}

func (c *Client) gameSender() {
	for r := range c.gameq {
		base, sid, on := c.conn()
		if !on {
			continue // the room asks again for missing pictures; subs are redone on reconnect
		}
		r.SID = sid
		body, _ := json.Marshal(r)
		resp, err := c.hc.Post(base+"/api", "application/json", bytes.NewReader(body))
		if err != nil {
			continue
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
	}
}
