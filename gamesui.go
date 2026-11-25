package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// The games screen inside the chat app: /play opens it, Q goes back.

const (
	gsMenu = iota
	gsCode // typing a room code
	gsPlay
)

type gameAd struct {
	code, game, host string
	n, max, phase    int
	at               time.Time
}

type gamesUI struct {
	u       *tui
	scr     int
	sel     int
	code    []rune
	ads     map[string]*gameAd
	s       *gsess
	msg     string
	quitAt  time.Time
	stop    chan struct{}
	lobbyOn bool
	whoAt   time.Time
	adAt    time.Time
}

// canvas is a rectangle of the screen a game draws into.
type canvas struct {
	u       *tui
	x0, y0  int
	w, h    int
	noColor bool
}

func (c *canvas) text(x, y int, s string, st tcell.Style) int {
	if y < 0 || y >= c.h || x >= c.w {
		return x
	}
	if c.noColor {
		st = monoStyle(st)
	}
	return c.u.put(c.x0+x, c.y0+y, c.x0+c.w, s, st) - c.x0
}

// center draws text in the middle of row y.
func (c *canvas) center(y int, s string, st tcell.Style) {
	c.text(imax(0, (c.w-textWidth(s))/2), y, s, st)
}

func monoStyle(st tcell.Style) tcell.Style {
	_, bg, attr := st.Decompose()
	out := tcell.StyleDefault.Attributes(attr)
	if bg != tcell.ColorDefault && bg != tcell.ColorReset {
		out = out.Reverse(true)
	}
	return out
}

func textWidth(s string) int { return uniseg.StringWidth(s) }

var (
	gsText = tcell.StyleDefault
	gsDim  = tcell.StyleDefault.Foreground(tcell.PaletteColor(244))
	gsHead = tcell.StyleDefault.Foreground(tcell.PaletteColor(180)).Bold(true)
	gsKey  = tcell.StyleDefault.Foreground(tcell.PaletteColor(109)).Bold(true)
	gsSel  = tcell.StyleDefault.Foreground(tcell.PaletteColor(222)).Bold(true)
	gsGood = tcell.StyleDefault.Foreground(tcell.PaletteColor(114)).Bold(true)
	gsWarn = tcell.StyleDefault.Foreground(tcell.PaletteColor(179))
	gsBad  = tcell.StyleDefault.Foreground(tcell.PaletteColor(167)).Bold(true)
	gsNum  = tcell.StyleDefault.Foreground(tcell.PaletteColor(222)).Bold(true)
)

// seatColors tells players apart in every game.
var seatColors = []tcell.Color{
	tcell.PaletteColor(114), tcell.PaletteColor(81), tcell.PaletteColor(213), tcell.PaletteColor(221),
	tcell.PaletteColor(209), tcell.PaletteColor(147),
}

func seatColor(i int) tcell.Color {
	if i < 0 {
		return tcell.PaletteColor(250)
	}
	return seatColors[i%len(seatColors)]
}

// openGames is /play [code | game].
func (u *tui) openGames(arg string) {
	if u.gm == nil {
		g := &gamesUI{u: u, ads: map[string]*gameAd{}, stop: make(chan struct{})}
		u.gm = g
		go func(stop chan struct{}) {
			t := time.NewTicker(30 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					u.s.PostEvent(tcell.NewEventInterrupt(func() {
						if u.gm != nil && u.gm.stop == stop {
							u.gm.pump(time.Now())
						}
					}))
				}
			}
		}(g.stop)
	}
	g := u.gm
	arg = strings.TrimSpace(strings.ToLower(arg))
	switch {
	case arg == "":
		if g.s == nil {
			g.toMenu("")
		}
	case validCode(strings.ReplaceAll(arg, " ", "")):
		g.join(strings.ReplaceAll(arg, " ", ""))
	default:
		for _, d := range gameDefs {
			if strings.HasPrefix(d.id, arg) || strings.HasPrefix(strings.ToLower(d.name), arg) {
				g.solo(d)
				return
			}
		}
		g.toMenu("no game called " + arg)
	}
}

func (g *gamesUI) close() {
	if g.s != nil {
		g.s.leave()
		g.s = nil
	}
	if g.lobbyOn {
		g.u.c.GameSub(lobbyCode, false)
	}
	close(g.stop)
	g.u.gm = nil
	g.u.setMouse()
}

func (g *gamesUI) toMenu(msg string) {
	if g.s != nil {
		g.s.leave()
		g.s = nil
	}
	g.scr, g.msg = gsMenu, msg
	g.who(time.Now())
}

func (g *gamesUI) who(now time.Time) {
	g.whoAt = now
	g.syncLobby()
	g.u.c.GameSend(lobbyCode, "", gmsg{K: "who"})
}

// syncLobby listens for open rooms while the menu is up, and while we host
// one (to answer "who's playing?").
func (g *gamesUI) syncLobby() {
	want := g.scr != gsPlay || (g.s != nil && g.s.host && g.s.open)
	if want != g.lobbyOn {
		g.lobbyOn = want
		g.u.c.GameSub(lobbyCode, want)
	}
}

func (g *gamesUI) screen() func() (int, int) { return g.u.s.Size }

func (g *gamesUI) solo(d *gameDef) {
	if g.s != nil {
		g.s.leave()
	}
	g.s = startSolo(g.u.c, d, g.screen(), time.Now())
	g.scr, g.msg = gsPlay, ""
	g.syncLobby()
}

func (g *gamesUI) create(d *gameDef, open bool) {
	if !d.room {
		g.msg = d.name + " is for one player"
		return
	}
	if !g.u.c.Online() {
		g.msg = "not connected yet · try again in a moment"
		return
	}
	if d.fast && g.u.c.Bluetooth() {
		g.msg = d.name + " with others needs Wi-Fi mode (nook wifi): Bluetooth is too slow for it"
		return
	}
	if g.s != nil {
		g.s.leave()
	}
	g.s = hostRoom(g.u.c, d, open, g.screen(), time.Now())
	g.scr, g.msg = gsPlay, ""
	g.syncLobby()
	if open {
		g.u.c.say("#"+general, fmt.Sprintf("opened a %s room for everyone · type /play %s to join", d.name, g.s.code))
	}
}

func (g *gamesUI) join(code string) {
	if !g.u.c.Online() {
		g.scr, g.msg = gsMenu, "not connected yet · try again in a moment"
		return
	}
	if g.s != nil {
		if !g.s.solo && g.s.code == code {
			g.scr = gsPlay
			return
		}
		g.s.leave()
	}
	g.s = joinRoom(g.u.c, code, time.Now())
	g.scr, g.msg = gsPlay, ""
	g.syncLobby()
}

// recv is a game packet from the hub (on the UI goroutine).
func (g *gamesUI) recv(code, from, nick, text string) {
	var m gmsg
	if json.Unmarshal([]byte(text), &m) != nil {
		return
	}
	now := time.Now()
	if code == lobbyCode {
		g.lobbyPkt(from, nick, &m, now)
		return
	}
	if s := g.s; s != nil && !s.solo && s.code == code {
		s.recv(from, nick, &m, now)
	}
}

func (g *gamesUI) lobbyPkt(from, nick string, m *gmsg, now time.Time) {
	me, _ := g.u.c.Me()
	if from == me {
		return
	}
	switch m.K {
	case "who":
		if s := g.s; s != nil && s.host && s.open && now.Sub(g.adAt) > time.Second {
			g.adAt = now
			s.ad(now)
		}
	case "ad":
		d := gameByID(m.G)
		if d == nil || !validCode(m.C) || m.C == lobbyCode || len(g.ads) > 50 && g.ads[m.C] == nil {
			return
		}
		host := cleanNick(m.HN)
		if host == "" {
			host = cleanNick(nick)
		}
		g.ads[m.C] = &gameAd{code: m.C, game: d.id, host: host, n: imin(len(m.P), d.max), max: d.max, phase: m.S, at: now}
	case "gone":
		delete(g.ads, m.C)
	}
}

func (g *gamesUI) adList(now time.Time) []*gameAd {
	var out []*gameAd
	for k, a := range g.ads {
		if now.Sub(a.at) > adExpire {
			delete(g.ads, k)
			continue
		}
		if g.s != nil && g.s.code == a.code {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].code < out[j].code })
	return out
}

func (g *gamesUI) pump(now time.Time) {
	s := g.s
	if s == nil {
		if g.scr == gsMenu && now.Sub(g.whoAt) > 8*time.Second {
			g.who(now)
		}
		return
	}
	if g.u.hidden && s.solo && s.def.fast && s.phase == phPlay {
		s.paused = true
	}
	s.pump(now)
	if s.ended != "" {
		why := s.ended
		g.s = nil
		g.toMenu(why)
	}
}

// ---- keys ----

func (g *gamesUI) key(ev *tcell.EventKey) {
	now := time.Now()
	k, r := ev.Key(), ev.Rune()
	if k == tcell.KeyRune {
		r = unicode.ToLower(r)
	}
	switch g.scr {
	case gsMenu:
		g.menuKey(k, r)
	case gsCode:
		switch {
		case k == tcell.KeyEnter:
			if c := string(g.code); validCode(c) {
				g.join(c)
			} else {
				g.msg = "a room code is 6 digits"
			}
		case k == tcell.KeyBackspace || k == tcell.KeyBackspace2:
			if len(g.code) > 0 {
				g.code = g.code[:len(g.code)-1]
			} else {
				g.scr = gsMenu
			}
		case k == tcell.KeyRune && r >= '0' && r <= '9' && len(g.code) < 6:
			g.code = append(g.code, r)
			if len(g.code) == 6 {
				g.join(string(g.code))
			}
		case k == tcell.KeyRune && r == 'q':
			g.scr = gsMenu
		}
	case gsPlay:
		g.playKey(ev, k, r, now)
	}
}

func (g *gamesUI) menuItems(now time.Time) (int, []*gameAd) {
	ads := g.adList(now)
	return len(gameDefs) + len(ads), ads
}

func (g *gamesUI) menuKey(k tcell.Key, r rune) {
	now := time.Now()
	n, ads := g.menuItems(now)
	if g.sel >= n {
		g.sel = n - 1
	}
	var def *gameDef
	var ad *gameAd
	if g.sel < len(gameDefs) {
		def = gameDefs[g.sel]
	} else if i := g.sel - len(gameDefs); i < len(ads) {
		ad = ads[i]
	}
	switch {
	case k == tcell.KeyUp || r == 'k' || r == 'w':
		g.sel = (g.sel + n - 1) % n
	case k == tcell.KeyDown || r == 'j' || r == 's':
		g.sel = (g.sel + 1) % n
	case k == tcell.KeyEnter:
		switch {
		case ad != nil:
			g.join(ad.code)
		case def != nil && def.solo:
			g.solo(def)
		case def != nil:
			g.create(def, true)
		}
	case r == 'c' && def != nil:
		g.create(def, false)
	case r == 'o' && def != nil:
		g.create(def, true)
	case r == 'c' || r == 'o':
		g.msg = "pick a game first (up/down)"
	case r == 'j':
		g.scr, g.code, g.msg = gsCode, g.code[:0], ""
	case r >= '0' && r <= '9':
		g.scr, g.code, g.msg = gsCode, []rune{r}, ""
	case r == 'r':
		g.who(now)
		g.msg = "looking for open rooms…"
	case r == 'q' || k == tcell.KeyBackspace || k == tcell.KeyBackspace2:
		g.close()
	}
}

func (g *gamesUI) playKey(ev *tcell.EventKey, k tcell.Key, r rune, now time.Time) {
	s := g.s
	if s == nil {
		g.toMenu("")
		return
	}
	if r == 'q' {
		if s.solo || !s.joined && !s.host || now.Sub(g.quitAt) < 2*time.Second {
			g.toMenu("")
			return
		}
		g.quitAt = now
		if s.host {
			s.note = "press Q again to close the room for everyone"
		} else {
			s.note = "press Q again to leave"
		}
		return
	}
	if s.phase == phLobby {
		g.lobbyKey(k, r, now)
		return
	}
	over := s.haveView && s.view.over()
	if over && k == tcell.KeyEnter {
		if s.host {
			s.backToLobby(now)
		}
		return
	}
	if s.solo && s.def.fast && (r == 'p' || r == ' ') {
		s.paused = !s.paused
		return
	}
	if s.paused {
		return
	}
	if s.haveView && !over {
		s.input(s.view.key(ev, s.ctx(now)), now)
	}
}

func (g *gamesUI) lobbyKey(k tcell.Key, r rune, now time.Time) {
	s := g.s
	if !s.host {
		return
	}
	switch {
	case k == tcell.KeyEnter:
		if len(s.seats) < s.def.min {
			s.note = fmt.Sprintf("%s needs %d players", s.def.name, s.def.min)
			if s.def.bots {
				s.note += " · B adds a bot"
			}
			return
		}
		s.note = ""
		s.begin(now)
	case r == 'b' && s.def.bots:
		if len(s.seats) >= s.def.max {
			s.note = "the table is full"
			return
		}
		used := map[string]bool{}
		for _, p := range s.seats {
			used[p.ID] = true
		}
		for _, b := range botSeats(6) {
			if !used[b.ID] {
				s.seats = append(s.seats, b)
				break
			}
		}
		s.sendLobby(now)
	case r == 'x' && s.def.bots:
		for i := len(s.seats) - 1; i >= 0; i-- {
			if s.seats[i].Bot {
				s.seats = append(s.seats[:i], s.seats[i+1:]...)
				s.sendLobby(now)
				return
			}
		}
	case r == 'i':
		to := g.u.c.Target()
		if to == "" {
			to = "#" + general
		}
		g.u.c.say(to, fmt.Sprintf("join my %s game · type /play %s", s.def.name, s.code))
		s.note = "invite sent to " + to
	}
}

// ---- drawing ----

func (g *gamesUI) draw(w, h int) {
	now := time.Now()
	cv := &canvas{u: g.u, w: w, h: h, noColor: g.u.o.NoColor}
	switch g.scr {
	case gsMenu, gsCode:
		g.drawMenu(cv, now)
	case gsPlay:
		g.drawPlay(cv, now)
	}
}

func (g *gamesUI) drawMenu(cv *canvas, now time.Time) {
	w, h := cv.w, cv.h
	cv.text(1, 0, "games", gsHead)
	cv.text(w-17, 0, "Q back to chat", gsDim)
	y := 2
	n, ads := g.menuItems(now)
	if g.sel >= n {
		g.sel = imax(0, n-1)
	}
	cv.text(3, y, fmt.Sprintf("%-15s %-6s %-6s %s", "", "alone", "others", ""), gsDim)
	y++
	for i, d := range gameDefs {
		st, mark := gsText, "  "
		if i == g.sel {
			st, mark = gsSel, "› "
		}
		alone, others := "  -", "  -"
		if d.solo {
			alone = "  ✓"
		}
		if d.room {
			if others = fmt.Sprintf("  %d", d.max); d.min < d.max {
				others = fmt.Sprintf(" %d-%d", imax(2, d.min), d.max)
			}
		}
		x := cv.text(1, y, mark, st)
		x = cv.text(x, y, fmt.Sprintf("%-15s", d.name), st)
		x = cv.text(x+1, y, fmt.Sprintf("%-6s", alone), gsGood)
		x = cv.text(x+1, y, fmt.Sprintf("%-6s", others), gsGood)
		cv.text(x+2, y, d.blurb, gsDim)
		y++
	}
	y++
	if g.sel < len(gameDefs) {
		d := gameDefs[g.sel]
		x := 3
		if d.solo {
			x = cv.text(x, y, "Enter", gsKey)
			x = cv.text(x, y, " play alone   ", gsDim)
		}
		if d.room {
			x = cv.text(x, y, "O", gsKey)
			x = cv.text(x, y, " open room (anyone in #general can join)   ", gsDim)
			x = cv.text(x, y, "C", gsKey)
			cv.text(x, y, " private room (6-digit code)", gsDim)
		}
	} else {
		x := cv.text(3, y, "Enter", gsKey)
		cv.text(x, y, " join this room", gsDim)
	}
	y++
	x := cv.text(3, y, "J", gsKey)
	x = cv.text(x, y, " or type a code to join a room   ", gsDim)
	x = cv.text(x, y, "R", gsKey)
	cv.text(x, y, " refresh", gsDim)
	y += 2
	cv.text(1, y, "open rooms", gsHead)
	y++
	if len(ads) == 0 {
		cv.text(3, y, "none right now · O opens one for everyone", gsDim)
		y++
	}
	for i, a := range ads {
		d := gameByID(a.game)
		st, mark := gsText, "  "
		if len(gameDefs)+i == g.sel {
			st, mark = gsSel, "› "
		}
		state := "waiting for players"
		if a.phase == phPlay {
			state = "playing (you can watch, then join the next round)"
		}
		if a.n >= a.max && a.phase == phLobby {
			state = "full (you can watch)"
		}
		x := cv.text(1, y, mark, st)
		x = cv.text(x, y, fmt.Sprintf("%-15s", d.name), st)
		x = cv.text(x+1, y, a.code, gsNum)
		x = cv.text(x+2, y, fmt.Sprintf("%s · %d/%d", a.host, a.n, a.max), st)
		cv.text(x+2, y, state, gsDim)
		y++
		if y >= h-3 {
			break
		}
	}
	if g.scr == gsCode {
		c := string(g.code) + strings.Repeat("_", 6-len(g.code))
		x := cv.text(1, h-2, "room code: ", gsHead)
		x = cv.text(x, h-2, spaced(c), gsNum)
		cv.text(x+3, h-2, "Enter join · Backspace back", gsDim)
	} else if g.msg != "" {
		cv.text(1, h-2, g.msg, gsWarn)
	}
	if g.u.c.Bluetooth() {
		cv.text(1, h-1, "Bluetooth mode: turn games work with others; Snake with others needs nook wifi", gsDim)
	}
}

func spaced(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (g *gamesUI) drawPlay(cv *canvas, now time.Time) {
	s := g.s
	if s == nil {
		return
	}
	w, h := cv.w, cv.h
	title := "joining room " + s.code + "…"
	if s.def != nil {
		title = s.def.name
		if !s.solo {
			title += " · room " + s.code
			if s.open {
				title += " (open)"
			}
		}
	}
	x := cv.text(1, 0, title, gsHead)
	if !s.solo && s.joined || s.host && !s.solo {
		players := 0
		for _, p := range s.seats {
			if !p.Bot {
				players++
			}
		}
		info := fmt.Sprintf(" · %d playing", players)
		if len(s.specs) > 0 {
			info += fmt.Sprintf(" · %d watching", len(s.specs))
		}
		cv.text(x, 0, info, gsDim)
	}
	right := "Q leave"
	if s.solo {
		right = "Q menu"
		if s.def.fast {
			right = "P pause · Q menu"
		}
	}
	cv.text(w-len(right)-1, 0, right, gsDim)

	body := &canvas{u: g.u, x0: 0, y0: 1, w: w, h: h - 2, noColor: cv.noColor}
	switch {
	case s.def == nil:
		body.center(body.h/2, "asking room "+s.code+" to let you in…", gsDim)
	case s.phase == phLobby:
		g.drawLobby(body)
	case !s.haveView:
		body.center(body.h/2, "starting…", gsDim)
	default:
		if mw, mh := s.view.size(); w < mw || h-2 < mh {
			body.center(body.h/2, fmt.Sprintf("make the window bigger: this game needs %d×%d (now %d×%d)", mw, mh+2, w, h), gsWarn)
			break
		}
		s.view.draw(body, s.ctx(now))
		if s.paused {
			body.center(body.h/2, "  paused · P to go on  ", gsSel.Reverse(true))
		}
	}

	// the bottom row: what's going on, or the newest chat message elsewhere
	foot, st := "", gsDim
	switch {
	case s.note != "":
		foot, st = s.note, gsWarn
		if now.Sub(g.quitAt) > 2*time.Second && strings.HasPrefix(foot, "press Q again") {
			s.note = ""
		}
	case s.haveView && s.phase == phPlay && s.view.over():
		switch {
		case s.host:
			foot = "Enter play again · Q leave"
		default:
			foot = "waiting for " + s.hostNick + " to start the next round · Q leave"
		}
	case !s.solo && s.mySeat() < 0 && s.phase == phPlay && s.joined:
		foot = "you're watching · you'll get a seat in the next round"
	}
	if foot == "" {
		if v := g.u.c.View(1, true); v.Peek != nil {
			p := v.Peek
			who := p.Nick
			if who == "" {
				who = v.Nicks[p.From]
			}
			foot = "↳ " + v.label(p.Key) + " " + who + ": " + p.Text
		} else if s.def != nil {
			foot = s.def.keys
		}
	}
	cv.text(1, h-1, foot, st)
}

func (g *gamesUI) drawLobby(cv *canvas) {
	s := g.s
	y := 1
	if !s.solo {
		x := cv.text(3, y, "room code  ", gsDim)
		cv.text(x, y, spaced(s.code), gsNum)
		y++
		how := "others type /play " + s.code + " or pick it in their games menu"
		if !s.open {
			how = "others type /play " + s.code + " (I posts it in your chat)"
		}
		cv.text(3, y, how, gsDim)
		y += 2
	}
	cv.text(3, y, fmt.Sprintf("players %d/%d", len(s.seats), s.def.max), gsHead)
	y++
	for i, p := range s.seats {
		name := p.Nick
		tag := ""
		switch {
		case p.ID == s.hostID:
			tag = " (host)"
		case p.Bot:
			tag = " (bot)"
		}
		if p.ID == s.me {
			name += " · you"
		}
		x := cv.text(5, y, "● ", tcell.StyleDefault.Foreground(seatColor(i)))
		cv.text(x, y, name+tag, gsText)
		y++
	}
	for i := len(s.seats); i < s.def.max && i < 8; i++ {
		cv.text(5, y, "○ free seat", gsDim)
		y++
	}
	if len(s.specs) > 0 {
		y++
		var names []string
		for _, p := range s.specs {
			names = append(names, p.Nick)
		}
		cv.text(3, y, "watching: "+strings.Join(names, ", "), gsDim)
		y++
	}
	y++
	if s.host {
		x := cv.text(3, y, "Enter", gsKey)
		x = cv.text(x, y, " start   ", gsDim)
		if s.def.bots {
			x = cv.text(x, y, "B", gsKey)
			x = cv.text(x, y, " add bot   ", gsDim)
			x = cv.text(x, y, "X", gsKey)
			x = cv.text(x, y, " remove bot   ", gsDim)
		}
		x = cv.text(x, y, "I", gsKey)
		x = cv.text(x, y, " invite in chat   ", gsDim)
		x = cv.text(x, y, "Q", gsKey)
		cv.text(x, y, " close room", gsDim)
	} else {
		cv.text(3, y, "waiting for "+s.hostNick+" to start…", gsDim)
	}
	y += 2
	cv.text(3, y, s.def.keys, gsDim)
}
