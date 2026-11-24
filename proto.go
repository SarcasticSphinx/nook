package main

import (
	"net"
	"strconv"
)

// Wire protocol.
//
// The hub speaks plain HTTP so the terminal app and a web browser use exactly
// the same thing:
//
//	GET  /events  Server-Sent Events stream, one JSON Ev per "data:" line
//	POST /api     JSON Req in, JSON Resp out
//	GET  /ping    JSON HubInfo (used by `nook scan` and by browsers hunting
//	              for a new host)
//
// Discovery is a UDP broadcast of qMagic to the discovery port; every hub
// answers with rMagic followed by its HubInfo as JSON.

const (
	qMagic = "NOOK?1"
	rMagic = "NOOK!1"
)

// Ev is one event pushed from the hub to a client.
type Ev struct {
	T     string     `json:"t"` // hello msg hist user room moved bye err sys ping
	SID   string     `json:"sid,omitempty"`
	Hub   string     `json:"hub,omitempty"`
	Now   int64      `json:"now,omitempty"`
	Me    *UserInfo  `json:"me,omitempty"`
	Users []UserInfo `json:"users,omitempty"`
	Rooms []RoomInfo `json:"rooms,omitempty"`
	Hist  []Msg      `json:"hist,omitempty"`
	Addr  []string   `json:"addr,omitempty"`  // the hub's LAN addresses
	Peers []string   `json:"peers,omitempty"` // where a new host may appear
	Msg   *Msg       `json:"msg,omitempty"`
	User  *UserInfo  `json:"user,omitempty"`
	Room  *RoomInfo  `json:"room,omitempty"`
	URL   string     `json:"url,omitempty"`
	Next  string     `json:"next,omitempty"`
	Text  string     `json:"text,omitempty"`
}

type UserInfo struct {
	ID   string `json:"id"`
	Nick string `json:"nick"`
	On   bool   `json:"on"`
}

type RoomInfo struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
	Gone    bool     `json:"gone,omitempty"`
}

// Msg is a chat message. Room messages have Room set; direct messages have
// To set instead.
type Msg struct {
	ID     int64     `json:"id"`
	TS     int64     `json:"ts"`
	Room   string    `json:"room,omitempty"`
	From   string    `json:"from"`
	Nick   string    `json:"nick"`
	To     string    `json:"to,omitempty"`
	ToNick string    `json:"tonick,omitempty"`
	Text   string    `json:"text"`
	File   *FileInfo `json:"file,omitempty"`
}

// FileInfo describes a file someone shared. In Wi-Fi mode the host keeps
// it; in Bluetooth mode it stays on the sender's laptop until fetched.
type FileInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type Req struct {
	SID  string `json:"sid"`
	Op   string `json:"op"` // say join leave nick typing older
	To   string `json:"to,omitempty"`
	Text string `json:"text,omitempty"`
	Arg  string `json:"arg,omitempty"`
}

type Resp struct {
	OK   bool   `json:"ok"`
	Err  string `json:"err,omitempty"`
	Room string `json:"room,omitempty"`
	Nick string `json:"nick,omitempty"`
	Hist []Msg  `json:"hist,omitempty"` // op "older"
	More bool   `json:"more,omitempty"` // op "older": there is more before these
}

// HubInfo describes a running hub. It is what discovery and /ping return.
type HubInfo struct {
	V       int    `json:"v"`
	ID      string `json:"id"`
	Port    int    `json:"port"`
	Users   int    `json:"users"`
	Remote  int    `json:"remote"` // online people who reached it over the network
	Started int64  `json:"started"`
	Prio    int    `json:"prio"` // 1 = started on purpose with `nook host`
	Lock    string `json:"lock,omitempty"`

	IP  string `json:"-"` // best address to dial from this computer
	LAN string `json:"-"` // a non-loopback address for it, if seen
}

func (h HubInfo) URL() string {
	return "http://" + net.JoinHostPort(h.IP, strconv.Itoa(h.Port))
}

// better reports whether hub a should win over hub b when two hubs find each
// other on the same network. A hub someone started on purpose beats an
// automatic one; a hub that people have actually reached over the network
// beats one nobody could reach (a firewall may be blocking it); after that
// the older hub wins.
func better(a, b HubInfo) bool {
	if a.Prio != b.Prio {
		return a.Prio > b.Prio
	}
	if (a.Remote > 0) != (b.Remote > 0) {
		return a.Remote > 0
	}
	if a.Started != b.Started {
		return a.Started < b.Started
	}
	return a.ID < b.ID
}
