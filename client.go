package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	modeAuto = iota // find a room, or host one if there is none
	modeHost        // host here
	modeJoin        // join one fixed address
)

const (
	stSearch = iota
	stOnline
	stOffline
)

const (
	endDrop = iota
	endMoved
	endBye
	endFatal
)

const maxLines = 3000

// Line is one row of the conversation as this client shows it.
type Line struct {
	Seq  int64 // local order of arrival
	ID   int64
	TS   int64 // unix ms, already corrected for the hub's clock
	Sys  bool
	Key  string // "#room" or "@userid"
	From string
	Nick string
	Text string
	Mine bool
}

type ClientOpts struct {
	Mode  int
	URL   string // for modeJoin
	Port  int    // discovery / hosting port; 0 disables discovery (tests)
	Key   string
	Nick  string
	Token string

	// Bluetooth mode: what /info says, and a few words for the prompt
	// when something needs attention ("no one nearby yet").
	Status func() string
	Note   func() string
}

type endResult struct {
	kind  int
	url   string
	next  string
	hello bool
}

// Client keeps one connection to the hub, follows it when the host changes,
// and holds what the UI shows.
type Client struct {
	mu   sync.Mutex
	o    ClientOpts
	lock string

	url, sid, hubID string
	addrs           []string
	me              UserInfo
	nick            string
	users           map[string]*UserInfo
	rooms           map[string]*RoomInfo
	lines           []Line
	seq             int64
	seen            map[int64]bool
	skew            int64
	target          string
	dms             []string
	lastDM          string
	state           int
	fatal           string
	hostNow         bool
	hub             *Hub
	quit            bool
	cancel          context.CancelFunc
	lockedNote      bool
	verbose         bool
	onChange        func()

	hc, sc *http.Client
	wake   chan struct{}
	disc   func(int, time.Duration) []HubInfo
}

func NewClient(o ClientOpts) *Client {
	tr := &http.Transport{
		Proxy:                 nil, // never send LAN traffic through a proxy
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 20 * time.Second}).DialContext,
		ResponseHeaderTimeout: 6 * time.Second,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       time.Minute,
	}
	return &Client{
		o:      o,
		lock:   lockHash(o.Key),
		nick:   o.Nick,
		users:  map[string]*UserInfo{},
		rooms:  map[string]*RoomInfo{},
		seen:   map[int64]bool{},
		target: "#" + general,
		sc:     &http.Client{Transport: tr},
		hc:     &http.Client{Transport: tr, Timeout: 6 * time.Second},
		wake:   make(chan struct{}, 1),
		disc:   discoverFn,
	}
}

func (c *Client) SetOnChange(f func()) { c.mu.Lock(); c.onChange = f; c.mu.Unlock() }
func (c *Client) SetVerbose(v bool)    { c.mu.Lock(); c.verbose = v; c.mu.Unlock() }

func (c *Client) changed() {
	c.mu.Lock()
	f := c.onChange
	c.mu.Unlock()
	if f != nil {
		f()
	}
}

func (c *Client) Start() { go c.run() }

// Close leaves. If we were hosting, everyone is told so they can move on.
func (c *Client) Close() {
	c.mu.Lock()
	if c.quit {
		c.mu.Unlock()
		return
	}
	c.quit = true
	h, cancel := c.hub, c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	c.poke()
	if h != nil {
		h.Close("")
	}
}

func (c *Client) poke() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Client) wait(d time.Duration) (quit bool) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-c.wake:
	}
	return c.quitting()
}

func (c *Client) quitting() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.quit }
func (c *Client) State() int     { c.mu.Lock(); defer c.mu.Unlock(); return c.state }
func (c *Client) Hosting() bool  { c.mu.Lock(); defer c.mu.Unlock(); return c.hub != nil }
func (c *Client) URL() string    { c.mu.Lock(); defer c.mu.Unlock(); return c.url }
func (c *Client) MeID() string   { c.mu.Lock(); defer c.mu.Unlock(); return c.me.ID }
func (c *Client) LastSeq() int64 { c.mu.Lock(); defer c.mu.Unlock(); return c.seq }

func (c *Client) setURL(u string)  { c.mu.Lock(); c.url = u; c.mu.Unlock() }
func (c *Client) getFatal() string { c.mu.Lock(); defer c.mu.Unlock(); return c.fatal }

func (c *Client) setState(s int) {
	c.mu.Lock()
	changed := c.state != s
	c.state = s
	c.mu.Unlock()
	if changed {
		c.changed()
	}
}

func backoff(n int) time.Duration {
	d := time.Duration(n) * 700 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

func (c *Client) run() {
	fails := 0
	for !c.quitting() {
		if c.getFatal() != "" { // e.g. wrong key: wait for /key
			if c.wait(time.Hour) {
				return
			}
			continue
		}
		base := c.URL()
		if base == "" {
			if base = c.locate(); base == "" {
				if c.wait(2 * time.Second) {
					return
				}
				continue
			}
			c.setURL(base)
		}
		res := c.stream(base)
		if c.quitting() {
			return
		}
		if res.hello {
			fails = 0
		}
		switch res.kind {
		case endFatal:
			c.setState(stOffline)
			continue
		case endMoved:
			c.setState(stSearch) // not online again until the new host says hello
			c.setURL(res.url)
			continue
		case endBye:
			c.setState(stSearch)
			if c.o.Mode == modeJoin {
				if c.wait(1500 * time.Millisecond) {
					return
				}
				continue
			}
			c.setURL("")
			if c.o.Mode == modeAuto && res.next != "" && res.next == c.MeID() {
				c.mu.Lock()
				c.hostNow = true // the old host picked us to take over
				c.mu.Unlock()
			} else if c.wait(time.Duration(800+rand.Intn(1200)) * time.Millisecond) {
				return
			}
			continue
		}
		fails++
		c.setState(stOffline)
		if c.o.Mode == modeHost && !c.Hosting() {
			c.setURL("") // our own hub died: start it again
		}
		if c.o.Mode == modeAuto && fails >= 3 {
			fails = 0
			c.setURL("") // the host is gone: look around again
			continue
		}
		if c.wait(backoff(fails)) {
			return
		}
	}
}

// locate decides where to connect: an existing room, or a new one hosted here.
func (c *Client) locate() string {
	switch c.o.Mode {
	case modeJoin:
		return c.o.URL
	case modeHost:
		return c.host(1)
	}
	c.setState(stSearch)
	c.mu.Lock()
	hostNow := c.hostNow
	c.hostNow = false
	c.mu.Unlock()
	if !hostNow && c.o.Port > 0 {
		for i := 0; i < 2; i++ {
			if hubs := c.find(1200 * time.Millisecond); len(hubs) > 0 {
				return hubs[0].URL()
			}
			// Nobody yet. Wait a random moment so that when many people start
			// together, one becomes the host and the rest find it. Windows
			// waits longer: hosting there pops up a firewall window, so it's
			// better if a Mac ends up hosting.
			pause := time.Duration(rand.Intn(1500)) * time.Millisecond
			if runtime.GOOS == "windows" {
				pause += 2 * time.Second
			}
			if i == 0 && c.wait(pause) {
				return ""
			}
		}
	}
	return c.host(0)
}

func (c *Client) find(wait time.Duration) []HubInfo {
	c.mu.Lock()
	lock, port := c.lock, c.o.Port
	c.mu.Unlock()
	var ok []HubInfo
	locked := false
	for _, h := range c.disc(port, wait) {
		if h.Lock == lock {
			ok = append(ok, h)
		} else if lock == "" {
			locked = true
		}
	}
	if len(ok) == 0 && locked {
		c.mu.Lock()
		note := !c.lockedNote
		c.lockedNote = true
		c.mu.Unlock()
		if note {
			c.Sys("found a room with a password: /key PASSWORD to join it")
		}
	}
	return ok
}

func (c *Client) host(prio int) string {
	c.mu.Lock()
	if h := c.hub; h != nil {
		select {
		case <-h.Done(): // it handed over or stopped; start a fresh one
			c.hub = nil
		default:
			c.mu.Unlock()
			return localURL(h.Port())
		}
	}
	c.mu.Unlock()
	h, err := StartHub(HubOpts{Port: c.o.Port, UDPPort: c.o.Port, Prio: prio, Key: c.o.Key, Elect: prio == 0})
	if err != nil {
		c.Sys("can't host on this computer: " + err.Error())
		return ""
	}
	c.mu.Lock()
	c.hub = h
	c.mu.Unlock()
	go func() {
		<-h.Done()
		c.mu.Lock()
		if c.hub == h {
			c.hub = nil
		}
		c.mu.Unlock()
	}()
	return localURL(h.Port())
}

// stream holds the event stream open until it ends, and says why it ended.
func (c *Client) stream(base string) (res endResult) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.mu.Lock()
	if c.quit {
		c.mu.Unlock()
		return
	}
	c.cancel = cancel
	q := url.Values{}
	q.Set("tok", c.o.Token)
	q.Set("nick", c.nick)
	q.Set("c", "term")
	q.Set("os", runtime.GOOS)
	if c.o.Key != "" {
		q.Set("key", c.o.Key)
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/events?"+q.Encode(), nil)
	if err != nil {
		return
	}
	resp, err := c.sc.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	idle := time.AfterFunc(40*time.Second, cancel) // the hub pings every 15s
	defer idle.Stop()
	br := bufio.NewReader(resp.Body)
	var data []byte
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return
		}
		idle.Reset(40 * time.Second)
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			if len(data) > 0 {
				var ev Ev
				if json.Unmarshal(data, &ev) == nil {
					if ev.T == "hello" {
						res.hello = true
					}
					if r := c.handle(&ev); r.kind != endDrop {
						r.hello = res.hello
						return r
					}
				}
				data = data[:0]
			}
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			d := bytes.TrimPrefix(line[5:], []byte(" "))
			if len(data) > 0 {
				data = append(data, '\n')
			}
			if data = append(data, d...); len(data) > 8<<20 {
				return
			}
		}
	}
}

func (c *Client) handle(ev *Ev) (res endResult) {
	c.mu.Lock()
	switch ev.T {
	case "hello":
		newHub := ev.Hub != c.hubID
		if newHub {
			c.seen = map[int64]bool{} // message ids restart with every hub
			c.hubID = ev.Hub
		}
		c.sid = ev.SID
		c.skew = 0
		if ev.Now > 0 {
			c.skew = time.Now().UnixMilli() - ev.Now
		}
		if ev.Me != nil {
			c.me = *ev.Me
			c.nick = ev.Me.Nick
		}
		c.users = map[string]*UserInfo{}
		for i := range ev.Users {
			u := ev.Users[i]
			c.users[u.ID] = &u
		}
		c.rooms = map[string]*RoomInfo{}
		for i := range ev.Rooms {
			r := ev.Rooms[i]
			c.rooms[r.Name] = &r
		}
		c.addrs = ev.Addr
		for _, m := range ev.Hist {
			c.addMsgLocked(m)
		}
		c.fixTargetLocked()
		c.state = stOnline
		if newHub {
			if c.o.Status != nil {
				c.sysLocked(c.o.Status() + " · /help")
			} else if c.hub != nil && c.url == localURL(c.hub.Port()) {
				where := "no network yet — connect to the Wi-Fi"
				if len(c.addrs) > 0 {
					where = strings.Join(c.addrs, "  ")
				}
				c.sysLocked("you're hosting · others join: " + where)
			} else {
				n := 0
				for _, u := range c.users {
					if u.On {
						n++
					}
				}
				c.sysLocked(fmt.Sprintf("connected · %d online · /help", n))
			}
		}
	case "msg":
		if ev.Msg != nil {
			c.addMsgLocked(*ev.Msg)
		}
	case "hist":
		for _, m := range ev.Hist {
			c.addMsgLocked(m)
		}
	case "user":
		if u := ev.User; u != nil {
			if old := c.users[u.ID]; old != nil && old.Nick != u.Nick && u.ID != c.me.ID {
				c.sysLocked(old.Nick + " is now " + u.Nick)
			}
			cp := *u
			c.users[u.ID] = &cp
			if u.ID == c.me.ID {
				c.me = cp
				c.nick = cp.Nick
			}
		}
	case "room":
		if r := ev.Room; r != nil {
			old := c.rooms[r.Name]
			wasIn := old != nil && has(old.Members, c.me.ID)
			if r.Gone {
				delete(c.rooms, r.Name)
			} else {
				cp := *r
				c.rooms[r.Name] = &cp
			}
			nowIn := !r.Gone && has(r.Members, c.me.ID)
			if r.Name != general && wasIn && nowIn {
				for _, id := range r.Members {
					if id != c.me.ID && !has(old.Members, id) {
						c.sysLocked(c.nickLocked(id) + " joined #" + r.Name)
					}
				}
				for _, id := range old.Members {
					if id != c.me.ID && !has(r.Members, id) {
						c.sysLocked(c.nickLocked(id) + " left #" + r.Name)
					}
				}
			}
			if wasIn != nowIn {
				c.fixTargetLocked()
			}
		}
	case "moved":
		c.sysLocked("the room moved to another host · following")
		res = endResult{kind: endMoved, url: ev.URL}
	case "bye":
		c.sysLocked("the host left · finding a new one")
		res = endResult{kind: endBye, next: ev.Next}
	case "err":
		c.fatal = ev.Text
		c.sysLocked(ev.Text + " · /key PASSWORD to try again")
		res = endResult{kind: endFatal}
	case "sys":
		c.sysLocked(ev.Text)
	}
	c.mu.Unlock()
	c.changed()
	return res
}

func (c *Client) addMsgLocked(m Msg) {
	if c.seen[m.ID] {
		return
	}
	c.seen[m.ID] = true
	l := Line{ID: m.ID, TS: m.TS + c.skew, From: m.From, Nick: m.Nick, Text: m.Text, Mine: m.From == c.me.ID}
	if m.Room != "" {
		l.Key = "#" + m.Room
	} else {
		peer := m.To
		if peer == c.me.ID {
			peer = m.From
		}
		l.Key = "@" + peer
		c.touchDMLocked(peer)
		if m.From != c.me.ID {
			c.lastDM = m.From
		}
	}
	c.insertLocked(l)
}

// insertLocked keeps lines in time order (history from a room you just
// joined slots in where it belongs).
func (c *Client) insertLocked(l Line) {
	c.seq++
	l.Seq = c.seq
	i := len(c.lines)
	for i > 0 && c.lines[i-1].TS > l.TS {
		i--
	}
	if i == len(c.lines) {
		c.lines = append(c.lines, l)
	} else {
		c.lines = append(c.lines, Line{})
		copy(c.lines[i+1:], c.lines[i:])
		c.lines[i] = l
	}
	if len(c.lines) > maxLines {
		c.lines = append([]Line(nil), c.lines[len(c.lines)-maxLines+500:]...)
	}
}

func (c *Client) sysLocked(text string) {
	c.insertLocked(Line{Sys: true, TS: nowMs(), Text: text})
}

// Sys shows a local notice.
func (c *Client) Sys(text string) {
	c.mu.Lock()
	c.sysLocked(text)
	c.mu.Unlock()
	c.changed()
}

func (c *Client) nickLocked(id string) string {
	if u := c.users[id]; u != nil {
		return u.Nick
	}
	return "?"
}

func (c *Client) touchDMLocked(peer string) {
	if !has(c.dms, peer) {
		c.dms = append(c.dms, peer)
	}
}

func (c *Client) myRoomsLocked() []string {
	var names []string
	for name, r := range c.rooms {
		if has(r.Members, c.me.ID) {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == general) != (names[j] == general) {
			return names[i] == general
		}
		return names[i] < names[j]
	})
	for i := range names {
		names[i] = "#" + names[i]
	}
	return names
}

func (c *Client) fixTargetLocked() {
	t := c.target
	switch {
	case strings.HasPrefix(t, "#"):
		if r := c.rooms[t[1:]]; r != nil && has(r.Members, c.me.ID) {
			return
		}
	case strings.HasPrefix(t, "@"):
		if _, ok := c.users[t[1:]]; ok {
			return
		}
	}
	c.target = ""
	if rs := c.myRoomsLocked(); len(rs) > 0 {
		c.target = rs[0]
	}
}

func (c *Client) labelLocked(key string) string {
	if strings.HasPrefix(key, "@") {
		return "@" + c.nickLocked(key[1:])
	}
	return key
}

// Cycle moves the "talking to" target through your rooms and DMs.
func (c *Client) Cycle(dir int) {
	c.mu.Lock()
	ts := append(c.myRoomsLocked(), prefixAll("@", c.dms)...)
	if len(ts) > 0 {
		i := 0
		for j, t := range ts {
			if t == c.target {
				i = j
			}
		}
		c.target = ts[(i+dir+len(ts))%len(ts)]
	}
	c.mu.Unlock()
	c.changed()
}

func prefixAll(p string, list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = p + s
	}
	return out
}

// View is a snapshot of what to draw.
type View struct {
	Lines  []Line
	Target string
	Label  string
	State  int
	Nicks  map[string]string
	Note   string
}

func (v *View) label(key string) string {
	if strings.HasPrefix(key, "@") {
		if n, ok := v.Nicks[key[1:]]; ok {
			return "@" + n
		}
		return "@?"
	}
	return key
}

func (c *Client) View(n int) View {
	c.mu.Lock()
	defer c.mu.Unlock()
	start := 0
	if len(c.lines) > n {
		start = len(c.lines) - n
	}
	v := View{
		Lines:  append([]Line(nil), c.lines[start:]...),
		Target: c.target,
		Label:  c.labelLocked(c.target),
		State:  c.state,
		Nicks:  make(map[string]string, len(c.users)),
	}
	for id, u := range c.users {
		v.Nicks[id] = u.Nick
	}
	if c.o.Note != nil {
		v.Note = c.o.Note()
	}
	return v
}

// Poke asks the screen to redraw (e.g. Bluetooth status changed).
func (c *Client) Poke() { c.changed() }

// ---- commands ----

func splitCmd(line string) (cmd, arg string) {
	cmd, arg, _ = strings.Cut(strings.TrimPrefix(line, "/"), " ")
	return strings.ToLower(cmd), strings.TrimSpace(arg)
}

// Input handles one line typed by the user: a message or a /command.
func (c *Client) Input(line string) {
	line = strings.TrimSpace(line)
	switch {
	case line == "":
		return
	case strings.HasPrefix(line, "//"):
		c.say("", line[1:])
		return
	case !strings.HasPrefix(line, "/"):
		c.say("", line)
		return
	}
	cmd, arg := splitCmd(line)
	switch cmd {
	case "join", "j":
		c.join(arg)
	case "leave", "part":
		c.leave(arg)
	case "dm", "msg", "m", "w", "query":
		c.dm(arg)
	case "r", "reply":
		c.reply(arg)
	case "nick", "name":
		c.rename(arg)
	case "who", "users", "online":
		c.who(arg)
	case "rooms", "list":
		c.listRooms()
	case "info", "addr", "ip":
		c.info()
	case "key":
		c.setKey(arg)
	case "help", "h", "?":
		c.help()
	default:
		c.Sys("unknown command /" + cmd + " · /help")
	}
}

func (c *Client) conn() (base, sid string, on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.url, c.sid, c.state == stOnline
}

func (c *Client) say(to, text string) {
	c.mu.Lock()
	if to == "" {
		to = c.target
	}
	c.mu.Unlock()
	base, sid, on := c.conn()
	switch {
	case to == "":
		c.Sys("you're not in a room · /join #name")
	case !on:
		c.Sys("offline · not sent")
	default:
		go c.post(base, Req{SID: sid, Op: "say", To: to, Text: text}, nil)
	}
}

func (c *Client) post(base string, r Req, done func(Resp)) {
	body, _ := json.Marshal(r)
	resp, err := c.hc.Post(base+"/api", "application/json", bytes.NewReader(body))
	if err != nil {
		c.Sys("not sent · connection problem")
		return
	}
	defer resp.Body.Close()
	var out Resp
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out) != nil {
		c.Sys("not sent")
		return
	}
	if !out.OK {
		c.Sys(out.Err)
		if out.Err == errNotConnected { // the hub forgot us: reconnect
			c.mu.Lock()
			cancel := c.cancel
			c.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
		return
	}
	if done != nil {
		done(out)
	}
}

func (c *Client) announce() {
	c.mu.Lock()
	v, label := c.verbose, c.labelLocked(c.target)
	c.mu.Unlock()
	if v {
		c.Sys("talking in " + label)
	}
}

func (c *Client) join(arg string) {
	name := cleanRoom(arg)
	if name == "" {
		c.Sys("usage: /join #room")
		return
	}
	c.mu.Lock()
	if r := c.rooms[name]; r != nil && has(r.Members, c.me.ID) {
		c.target = "#" + name
		c.mu.Unlock()
		c.changed()
		c.announce()
		return
	}
	c.mu.Unlock()
	base, sid, on := c.conn()
	if !on {
		c.Sys("offline")
		return
	}
	go c.post(base, Req{SID: sid, Op: "join", Arg: name}, func(r Resp) {
		c.mu.Lock()
		rm := c.rooms[r.Room]
		if rm == nil {
			rm = &RoomInfo{Name: r.Room}
			c.rooms[r.Room] = rm
		}
		if !has(rm.Members, c.me.ID) {
			rm.Members = append(rm.Members, c.me.ID)
		}
		c.target = "#" + r.Room
		c.mu.Unlock()
		c.changed()
		c.announce()
	})
}

func (c *Client) leave(arg string) {
	name := cleanRoom(arg)
	c.mu.Lock()
	if name == "" && strings.HasPrefix(c.target, "#") {
		name = c.target[1:]
	}
	c.mu.Unlock()
	if name == "" {
		c.Sys("usage: /leave #room")
		return
	}
	base, sid, on := c.conn()
	if !on {
		c.Sys("offline")
		return
	}
	go c.post(base, Req{SID: sid, Op: "leave", Arg: name}, func(r Resp) {
		c.mu.Lock()
		if rm := c.rooms[r.Room]; rm != nil {
			rm.Members = without(rm.Members, c.me.ID)
		}
		c.fixTargetLocked()
		c.sysLocked("left #" + r.Room)
		c.mu.Unlock()
		c.changed()
	})
}

func (c *Client) findUserLocked(name string) *UserInfo {
	name = strings.ToLower(name)
	var prefix []*UserInfo
	for _, u := range c.users {
		n := strings.ToLower(u.Nick)
		if n == name {
			return u
		}
		if strings.HasPrefix(n, name) {
			prefix = append(prefix, u)
		}
	}
	if len(prefix) == 1 {
		return prefix[0]
	}
	return nil
}

func (c *Client) dm(arg string) {
	who, text, _ := strings.Cut(strings.TrimSpace(arg), " ")
	who = strings.TrimPrefix(who, "@")
	if who == "" {
		c.Sys("usage: /dm name [message]")
		return
	}
	c.mu.Lock()
	u := c.findUserLocked(who)
	if u == nil {
		c.mu.Unlock()
		c.Sys("no one called " + who + " · /who")
		return
	}
	key := "@" + u.ID
	c.target = key
	c.touchDMLocked(u.ID)
	c.mu.Unlock()
	c.changed()
	c.announce()
	if t := strings.TrimSpace(text); t != "" {
		c.say(key, t)
	}
}

func (c *Client) reply(arg string) {
	c.mu.Lock()
	id := c.lastDM
	if id != "" && arg == "" {
		c.target = "@" + id
	}
	c.mu.Unlock()
	switch {
	case id == "":
		c.Sys("no one has messaged you yet")
	case arg == "":
		c.changed()
		c.announce()
	default:
		c.say("@"+id, arg)
	}
}

func (c *Client) rename(arg string) {
	n := cleanNick(arg)
	if n == "" {
		c.Sys("usage: /nick name (letters, digits, _ . -)")
		return
	}
	base, sid, on := c.conn()
	if !on {
		c.mu.Lock()
		c.nick = n
		c.mu.Unlock()
		c.Sys("you'll be " + n + " when you connect")
		return
	}
	go c.post(base, Req{SID: sid, Op: "nick", Arg: n}, func(r Resp) {
		c.mu.Lock()
		c.nick = r.Nick
		c.mu.Unlock()
		c.Sys("you are now " + r.Nick)
	})
}

func (c *Client) who(arg string) {
	c.mu.Lock()
	var rm *RoomInfo
	if name := cleanRoom(arg); name != "" {
		if rm = c.rooms[name]; rm == nil {
			c.mu.Unlock()
			c.Sys("no room #" + name)
			return
		}
	}
	var names []string
	for _, u := range c.users {
		if !u.On || (rm != nil && !has(rm.Members, u.ID)) {
			continue
		}
		n := u.Nick
		if u.ID == c.me.ID {
			n += " (you)"
		}
		names = append(names, n)
	}
	c.mu.Unlock()
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	where := "online"
	if rm != nil {
		where = "#" + rm.Name
	}
	c.Sys(fmt.Sprintf("%s (%d): %s", where, len(names), strings.Join(names, ", ")))
}

func (c *Client) listRooms() {
	c.mu.Lock()
	var parts []string
	for name, r := range c.rooms {
		on := 0
		for _, id := range r.Members {
			if u := c.users[id]; u != nil && u.On {
				on++
			}
		}
		p := fmt.Sprintf("#%s %d", name, on)
		if has(r.Members, c.me.ID) {
			p += "*"
		}
		parts = append(parts, p)
	}
	c.mu.Unlock()
	sort.Strings(parts)
	c.Sys("rooms (* = you're in it): " + strings.Join(parts, " · ") + " · /join #name")
}

func (c *Client) info() {
	if c.o.Status != nil {
		c.Sys(c.o.Status())
		return
	}
	c.mu.Lock()
	base, addrs, hosting, state := c.url, c.addrs, c.hub != nil, c.state
	c.mu.Unlock()
	if state != stOnline {
		c.Sys("not connected yet")
		return
	}
	link := base
	if len(addrs) > 0 {
		link = strings.Join(addrs, "  ")
	}
	if hosting {
		c.Sys("you're hosting · keep this open · others join: " + link)
	} else {
		c.Sys("connected to " + base)
	}
	if len(addrs) > 0 {
		a := addrs[0]
		c.Sys("browser: " + a)
		c.Sys("install, Mac/Linux: curl -fsSL " + a + "/install.sh | sh")
		c.Sys("install, Windows: irm " + a + "/install.ps1 | iex   (all options: " + a + "/get)")
	}
}

func (c *Client) setKey(key string) {
	c.mu.Lock()
	if c.o.Mode == modeHost {
		c.mu.Unlock()
		c.Sys("to lock a room you host, restart it with --key PASSWORD")
		return
	}
	c.o.Key, c.lock, c.fatal, c.lockedNote = key, lockHash(key), "", false
	h, cancel := c.hub, c.cancel
	if c.o.Mode == modeAuto {
		c.url = "" // look for the room that matches the new key
	}
	c.mu.Unlock()
	if h != nil && h.Online() <= 1 {
		h.Close("") // our own empty room isn't needed any more
	}
	if cancel != nil {
		cancel()
	}
	c.poke()
	if key == "" {
		c.Sys("key cleared")
	} else {
		c.Sys("key set · reconnecting")
	}
}

func (c *Client) help() {
	for _, l := range []string{
		"keys: Esc hide/unhide · Ctrl+C quit · Ctrl+L clear · Tab next chat · PgUp/PgDn scroll",
		"/join #room · /leave · /dm name [text] · /r text · /nick name · /who · /rooms · /info",
		"/dim faint text · /fade 30 · /skin log|plain · /focus · /key word · /quit",
	} {
		c.Sys(l)
	}
}
