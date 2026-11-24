package main

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// The full-screen view: a thin bar with your chats, the conversation
// bottom-aligned like a shell, and one input line. Esc swaps the whole
// screen for a fake shell prompt; Ctrl+C quits and leaves the terminal
// exactly as it was.

type uiOpts struct {
	Skin    string
	Ghost   bool
	All     bool // every chat mixed together (/all); normally one chat at a time
	Wipe    bool
	NoBar   bool // hide the chat bar
	People  bool // show the people list
	NoColor bool
}

var (
	stText = tcell.StyleDefault
	stMeta = tcell.StyleDefault.Foreground(tcell.ColorGray)
	// muted colours for names, so a busy room is easy to follow
	namePalette = []int{73, 137, 108, 139, 110, 173, 67, 133, 143, 168}
	// the chat skin's names: soft, faded colours, one per person; yours is
	// never one of theirs
	brightPalette = []int{174, 108, 139, 180, 109, 145, 151, 181, 146, 144}
	meColor       = 110
)

func brightColor(who string) tcell.Color {
	h := fnv.New32a()
	h.Write([]byte(who))
	return tcell.PaletteColor(brightPalette[h.Sum32()%uint32(len(brightPalette))])
}

type cell struct {
	r  []rune
	w  int
	st tcell.Style
}

type row struct {
	x     int // column of the first cell
	cells []cell
}

type tui struct {
	c        *Client
	s        tcell.Screen
	o        uiOpts
	fadeSec  int64 // atomic: hide messages older than this; 0 = never
	in       []rune
	cur, off int
	hist     []string
	hpos     int
	draft    string
	scroll   int
	clearSeq int64
	hidden   bool
	decoy    *decoy
	paste    bool
	quit     bool
	poked    int32

	view    *viewer   // a file opened with /see
	noMouse bool      // /mouse: let the terminal select text instead
	comp    *compList // Tab completion in progress
	tabs    []tabHit  // where each chat is on the bar, for clicks
	peekKey string    // the chat the "new in ..." row points to
	peekY   int
}

type tabHit struct {
	x0, x1 int
	key    string
}

func runTUI(c *Client, o uiOpts, s tcell.Screen) error {
	if s == nil {
		var err error
		if s, err = tcell.NewScreen(); err != nil {
			return err
		}
	}
	if err := s.Init(); err != nil {
		return err
	}
	if !validSkin(o.Skin) {
		o.Skin = "plain"
	}
	u := &tui{c: c, s: s, o: o, decoy: newDecoy()}
	s.SetStyle(tcell.StyleDefault)
	s.EnablePaste()
	u.setMouse()
	s.Clear()
	c.SetOnChange(u.poke)
	c.SetViewer(func(fi FileInfo, no int, data []byte) {
		s.PostEvent(tcell.NewEventInterrupt(func() { u.openViewer(fi, no, data) }))
	})
	c.Start()

	done := make(chan struct{})
	go func() { // redraw once a second while /fade is on
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if atomic.LoadInt64(&u.fadeSec) > 0 {
					u.poke()
				}
			}
		}
	}()

	for !u.quit {
		u.draw()
		switch ev := s.PollEvent().(type) {
		case nil:
			u.quit = true
		case *tcell.EventResize:
			s.Sync()
		case *tcell.EventPaste:
			u.paste = ev.Start()
		case *tcell.EventKey:
			u.key(ev)
		case *tcell.EventMouse:
			u.mouse(ev)
		case *tcell.EventInterrupt:
			if f, ok := ev.Data().(func()); ok {
				f() // run on the UI goroutine (used by tests)
			} else {
				atomic.StoreInt32(&u.poked, 0)
			}
		}
	}
	close(done)
	c.SetOnChange(nil)
	c.SetViewer(nil)
	s.Fini()
	if o.Wipe {
		wipeTerminal()
	}
	c.Close()
	return nil
}

// poke asks for a redraw from any goroutine; bursts collapse into one.
func (u *tui) poke() {
	if atomic.CompareAndSwapInt32(&u.poked, 0, 1) {
		if u.s.PostEvent(tcell.NewEventInterrupt(nil)) != nil {
			atomic.StoreInt32(&u.poked, 0)
		}
	}
}

func (u *tui) key(ev *tcell.EventKey) {
	k := ev.Key()
	if k == tcell.KeyCtrlC {
		u.quit = true
		return
	}
	if u.hidden {
		if k == tcell.KeyEscape {
			u.hidden = false
			u.setMouse()
			return
		}
		w, _ := u.s.Size()
		if u.decoy.key(ev, w) {
			u.quit = true
		}
		return
	}
	if k == tcell.KeyEscape { // the boss key works everywhere, even in a file
		u.hide()
		return
	}
	if u.view != nil {
		u.viewerKey(ev)
		return
	}
	if k != tcell.KeyTab {
		u.comp = nil
	}
	if u.paste { // pasted text: keep it on one line, never send half of it
		switch k {
		case tcell.KeyEnter, tcell.KeyTab, tcell.KeyLF:
			u.insert(' ')
			return
		case tcell.KeyRune:
			u.insert(ev.Rune())
			return
		}
	}
	switch k {
	case tcell.KeyEnter:
		u.submit()
	case tcell.KeyTab:
		if ev.Modifiers()&tcell.ModCtrl != 0 { // Ctrl+Tab, where the terminal passes it on
			u.cycle(1)
		} else {
			u.complete()
		}
	case tcell.KeyCtrlN:
		u.cycle(1)
	case tcell.KeyCtrlP, tcell.KeyBacktab:
		u.cycle(-1)
	case tcell.KeyCtrlG:
		u.c.Goto("#" + general)
		u.scroll = 0
	case tcell.KeyPgUp:
		u.scroll += u.page()
	case tcell.KeyPgDn:
		if u.scroll -= u.page(); u.scroll < 0 {
			u.scroll = 0
		}
	case tcell.KeyUp:
		u.histMove(-1)
	case tcell.KeyDown:
		u.histMove(1)
	case tcell.KeyLeft:
		if u.cur > 0 {
			u.cur--
		}
	case tcell.KeyRight:
		if u.cur < len(u.in) {
			u.cur++
		}
	case tcell.KeyHome, tcell.KeyCtrlA:
		u.cur = 0
	case tcell.KeyEnd, tcell.KeyCtrlE:
		u.cur = len(u.in)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if u.cur > 0 {
			u.in = append(u.in[:u.cur-1], u.in[u.cur:]...)
			u.cur--
		}
	case tcell.KeyDelete:
		if u.cur < len(u.in) {
			u.in = append(u.in[:u.cur], u.in[u.cur+1:]...)
		}
	case tcell.KeyCtrlU:
		u.in, u.cur = u.in[:0], 0
	case tcell.KeyCtrlK:
		u.in = u.in[:u.cur]
	case tcell.KeyCtrlW:
		i := u.cur
		for i > 0 && u.in[i-1] == ' ' {
			i--
		}
		for i > 0 && u.in[i-1] != ' ' {
			i--
		}
		u.in = append(u.in[:i], u.in[u.cur:]...)
		u.cur = i
	case tcell.KeyCtrlL:
		u.clearScreen()
	case tcell.KeyRune:
		if ev.Modifiers()&tcell.ModAlt == 0 {
			u.insert(ev.Rune())
		}
	}
}

func (u *tui) cycle(dir int) {
	u.c.Cycle(dir)
	u.scroll = 0
}

func (u *tui) hide() {
	u.hidden = true
	u.s.DisableMouse() // the fake shell behaves like a real one: select, scroll
}

func (u *tui) setMouse() {
	if u.noMouse || u.hidden {
		u.s.DisableMouse()
	} else {
		u.s.EnableMouse(tcell.MouseButtonEvents)
	}
}

// mouse: the wheel scrolls the chat (to older messages at the top), a
// click on the bar opens that chat.
func (u *tui) mouse(ev *tcell.EventMouse) {
	if u.hidden {
		return
	}
	b := ev.Buttons()
	x, y := ev.Position()
	if u.view != nil {
		switch {
		case b&tcell.WheelUp != 0:
			u.view.off = imax(0, u.view.off-3)
		case b&tcell.WheelDown != 0:
			u.view.off = imin(u.view.off+3, imax(0, len(u.view.pv.text)-1))
		}
		return
	}
	switch {
	case b&tcell.WheelUp != 0:
		u.scroll += 3
	case b&tcell.WheelDown != 0:
		if u.scroll -= 3; u.scroll < 0 {
			u.scroll = 0
		}
	case b&tcell.Button1 != 0:
		if y == 0 && !u.o.NoBar {
			for _, t := range u.tabs {
				if x >= t.x0 && x < t.x1 {
					u.c.Goto(t.key)
					u.scroll = 0
				}
			}
		} else if y == u.peekY && u.peekKey != "" {
			u.c.Goto(u.peekKey)
			u.scroll = 0
		}
	}
}

func (u *tui) insert(r rune) {
	if len(u.in) >= maxText+200 { // room for a long /send path
		return
	}
	u.in = append(u.in, 0)
	copy(u.in[u.cur+1:], u.in[u.cur:])
	u.in[u.cur] = r
	u.cur++
}

func (u *tui) page() int {
	_, h := u.s.Size()
	if h > 4 {
		return h - 3
	}
	return 1
}

func (u *tui) clearScreen() {
	u.clearSeq = u.c.LastSeq()
	u.scroll = 0
}

func (u *tui) histMove(d int) {
	if len(u.hist) == 0 {
		return
	}
	if u.hpos == len(u.hist) && d < 0 {
		u.draft = string(u.in)
	}
	n := u.hpos + d
	if n < 0 || n > len(u.hist) {
		return
	}
	u.hpos = n
	if n == len(u.hist) {
		u.in = []rune(u.draft)
	} else {
		u.in = []rune(u.hist[n])
	}
	u.cur = len(u.in)
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (u *tui) submit() {
	line := strings.TrimSpace(string(u.in))
	u.in, u.cur, u.off, u.scroll = u.in[:0], 0, 0, 0
	if line == "" {
		return
	}
	if len(u.hist) == 0 || u.hist[len(u.hist)-1] != line {
		if u.hist = append(u.hist, line); len(u.hist) > 100 {
			u.hist = u.hist[1:]
		}
	}
	u.hpos = len(u.hist)
	if strings.HasPrefix(line, "/") && !strings.HasPrefix(line, "//") {
		cmd, arg := splitCmd(line)
		switch cmd {
		case "quit", "q", "exit":
			u.quit = true
			return
		case "clear", "cls":
			u.clearScreen()
			return
		case "hide":
			u.hide()
			return
		case "mouse":
			u.noMouse = !u.noMouse
			u.setMouse()
			if u.noMouse {
				u.c.Sys("mouse off: select text with it · /mouse to scroll with the wheel again")
			} else {
				u.c.Sys("mouse on: the wheel scrolls the chat · /mouse to select text instead")
			}
			return
		case "dim", "ghost":
			u.o.Ghost = !u.o.Ghost
			return
		case "people", "panel":
			u.o.People = !u.o.People
			return
		case "bar", "tabs":
			u.o.NoBar = !u.o.NoBar
			return
		case "colors", "colours", "color":
			u.o.NoColor = !u.o.NoColor
			u.c.Sys("colours " + onOff(!u.o.NoColor))
			return
		case "fade":
			n, err := strconv.Atoi(arg)
			if arg == "" {
				n, err = 30, nil
			}
			if arg == "off" || err != nil || n < 0 {
				n = 0
			}
			atomic.StoreInt64(&u.fadeSec, int64(n))
			if n > 0 {
				u.c.Sys("messages now fade after " + strconv.Itoa(n) + "s · /fade off")
			} else {
				u.c.Sys("fade off")
			}
			return
		case "skin", "theme", "style":
			switch {
			case validSkin(arg):
				u.o.Skin = arg
			case arg == "" || arg == "next":
				for i, s := range skins {
					if s == u.o.Skin {
						u.o.Skin = skins[(i+1)%len(skins)]
						break
					}
				}
				u.c.Sys("skin: " + u.o.Skin + " · others: " + strings.Join(skins, " "))
			default:
				u.c.Sys("skins: " + strings.Join(skins, " "))
			}
			return
		case "all", "focus", "mix":
			if u.o.All = !u.o.All; u.o.All {
				u.c.Sys("showing every chat together · /all again for one at a time")
			} else {
				u.c.Sys("one chat at a time · Ctrl+N next chat · /all to mix them")
			}
			return
		}
	}
	u.c.Input(line)
}

func nameColor(who string) tcell.Color {
	h := fnv.New32a()
	h.Write([]byte(who))
	return tcell.PaletteColor(namePalette[h.Sum32()%uint32(len(namePalette))])
}

func (u *tui) style(p part) tcell.Style {
	if u.o.Ghost {
		return stMeta.Dim(true)
	}
	if u.o.NoColor {
		switch p.kind {
		case pText, pName:
			return stText
		case pMe, pDM, pNotice, pFile, pCmd, pHead:
			return stText.Bold(true)
		}
		return stMeta
	}
	switch p.kind {
	case pText:
		return stText
	case pMe:
		return stText.Foreground(tcell.PaletteColor(meColor)).Bold(true)
	case pName:
		if u.o.Skin == "chat" { // a chat app look: each name its own soft colour
			return stText.Foreground(brightColor(p.who)).Bold(true)
		}
		return stText.Foreground(nameColor(p.who))
	case pCmd:
		return stText.Foreground(tcell.PaletteColor(109))
	case pHead:
		return stText.Foreground(tcell.PaletteColor(180)).Bold(true)
	case pRoom:
		return stText.Foreground(tcell.PaletteColor(110))
	case pDM:
		return stText.Foreground(tcell.PaletteColor(175)).Bold(true)
	case pNotice:
		return stText.Foreground(tcell.PaletteColor(179)).Italic(true)
	case pFile:
		return stText.Foreground(tcell.PaletteColor(108)).Bold(true)
	}
	return stMeta
}

// put draws text from column x and returns the column after it.
func (u *tui) put(x, y, maxX int, text string, st tcell.Style) int {
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		cw := g.Width()
		if cw <= 0 {
			continue
		}
		if x+cw > maxX {
			return x
		}
		rs := g.Runes()
		u.s.SetContent(x, y, rs[0], rs[1:], st)
		x += cw
	}
	return x
}

func (u *tui) draw() {
	s := u.s
	s.Clear()
	w, h := s.Size()
	if w <= 0 || h <= 0 {
		s.Show()
		return
	}
	if u.hidden {
		u.decoy.draw(s, w, h)
		s.Show()
		return
	}
	if u.view != nil {
		u.drawViewer(w, h)
		s.Show()
		return
	}
	want := h + u.scroll + 100
	v := u.c.View(want, !u.o.All)
	top, bottom := 0, h-1 // bottom is the input row
	u.tabs = u.tabs[:0]
	if !u.o.NoBar && h >= 5 {
		u.drawBar(&v, w, 0)
		top = 1
	}
	if c := u.comp; c != nil && len(c.list) > 1 && h >= 6 {
		bottom--
		x := 0
		for i, it := range c.list {
			st := u.style(part{kind: pMeta})
			if i == c.i {
				st = u.style(part{kind: pCmd}).Reverse(true)
			}
			x = u.put(x, bottom, w, " "+it+" ", st)
		}
	}
	if len(v.Busy) > 0 && h >= 6 {
		bottom--
		u.put(0, bottom, w, strings.Join(v.Busy, "    "), u.style(part{kind: pFile}))
	}
	u.peekKey = ""
	if p := v.Peek; p != nil && h >= 7 {
		bottom--
		u.peekKey, u.peekY = p.Key, bottom
		where, who := "new in "+p.Key, " · "+v.Nicks[p.From]+": "
		if strings.HasPrefix(p.Key, "@") {
			where, who = "DM from "+v.Nicks[p.From], ": "
		}
		text := p.Text
		if p.File != nil {
			text = "[file] " + p.File.Name
		}
		x := u.put(0, bottom, w, "↳ "+where, u.style(part{kind: pDM}))
		x = u.put(x, bottom, w, who, u.style(part{kind: pMeta}))
		hint := "  ^N"
		x = u.put(x, bottom, w-len(hint), text, u.style(part{kind: pFaint}))
		u.put(x, bottom, w, hint, u.style(part{kind: pMeta}))
	}
	mw := w
	if u.o.People && w >= 60 {
		pw := 22
		mw = w - pw - 1
		u.drawPeople(&v, mw, top, w, bottom)
	}
	area := bottom - top
	rows, atTop := u.layout(&v, mw, area+u.scroll)
	if atTop && v.Only && v.Older == olderMore && len(v.Lines) < want {
		u.c.Older(v.Target) // scrolled to the top: fetch earlier messages
	}
	if len(rows) < area+u.scroll {
		u.scroll = imax(0, len(rows)-area)
	}
	end := len(rows) - u.scroll
	start := imax(0, end-area)
	y := top + area - (end - start)
	for _, r := range rows[start:end] {
		x := r.x
		for _, c := range r.cells {
			if x+c.w > mw {
				break
			}
			s.SetContent(x, y, c.r[0], c.r[1:], c.st)
			x += c.w
		}
		y++
	}
	u.drawInput(&v, w, h-1)
	s.Show()
}

// drawBar shows your chats (the one you're in highlighted, unread counts
// next to the others) and how many people are here.
func (u *tui) drawBar(v *View, w, y int) {
	here := 0
	for _, p := range v.People {
		if p.On {
			here++
		}
	}
	right := fmt.Sprintf(" %d here · ^N next chat ", here)
	maxX := w - len(right)
	x := 0
	for _, ch := range v.Chats {
		st := u.style(part{kind: pMeta})
		if ch.Key == v.Target {
			st = stText.Reverse(true)
			if u.o.Ghost {
				st = stMeta.Dim(true).Reverse(true)
			}
		}
		x0 := x
		x = u.put(x, y, maxX, " "+ch.Label+" ", st)
		if ch.Unread > 0 {
			x = u.put(x, y, maxX, strconv.Itoa(ch.Unread), u.style(part{kind: pDM}))
		}
		u.tabs = append(u.tabs, tabHit{x0, x, ch.Key})
		x = u.put(x, y, maxX, " ", stMeta)
	}
	if maxX > x {
		u.put(maxX, y, w, right, u.style(part{kind: pMeta}))
	}
}

// drawPeople lists everyone on the right: who is here, then who has gone.
func (u *tui) drawPeople(v *View, x0, top, w, bottom int) {
	meta := u.style(part{kind: pMeta})
	for y := top; y < bottom; y++ {
		u.s.SetContent(x0, y, '|', nil, meta)
	}
	y := top
	line := func(text string, st tcell.Style) {
		if y < bottom {
			u.put(x0+2, y, w, text, st)
			y++
		}
	}
	here := 0
	for _, p := range v.People {
		if p.On {
			here++
		}
	}
	line(fmt.Sprintf("people · %d here", here), u.style(part{kind: pText}))
	for _, p := range v.People {
		name := p.Nick
		if p.ID == v.MeID {
			name += " (you)"
		}
		if p.On {
			line("* "+name, u.style(part{kind: pName, who: p.ID}))
		} else {
			line("  "+name, meta)
		}
	}
}

// layout wraps the newest lines until it has at least `need` rows. atTop
// reports that every line of the chat fit, so earlier ones could be
// fetched; the top then says where the chat starts.
func (u *tui) layout(v *View, width, need int) (rows []row, atTop bool) {
	now := time.Now().UnixMilli()
	fade := atomic.LoadInt64(&u.fadeSec) * 1000
	var blocks [][]row
	n := 0
	hid := false
	i := len(v.Lines) - 1
	for ; i >= 0 && n < need; i-- {
		l := &v.Lines[i]
		if u.scroll == 0 && (l.Seq <= u.clearSeq || (fade > 0 && now-l.TS > fade)) {
			hid = true
			continue
		}
		var prev *Line
		if i > 0 {
			prev = &v.Lines[i-1]
		}
		var b []row
		for _, p := range fmtLine(l, prev, v, u.o.Skin) {
			b = append(b, u.wrap(p, width)...)
		}
		blocks = append(blocks, b)
		n += len(b)
	}
	atTop = i < 0 && !hid
	if atTop && v.Only && v.Target != "" {
		text := "· · ·"
		switch v.Older {
		case olderBusy:
			text = "loading earlier messages"
		case olderDone:
			if strings.HasPrefix(v.Target, "@") {
				text = "the start of your chat with " + strings.TrimPrefix(v.Label, "@")
			} else {
				text = "the start of " + v.Target
			}
		}
		blocks = append(blocks, u.wrap(para{parts: []part{{text, pMeta, ""}}, align: alignCenter}, width), []row{{}})
		n += 2
	}
	rows = make([]row, 0, n)
	for i := len(blocks) - 1; i >= 0; i-- {
		rows = append(rows, blocks[i]...)
	}
	return rows, atTop
}

// wrap breaks a paragraph into rows at word boundaries. Left-aligned
// continuation rows line up under the message text; right-aligned and
// centred paragraphs are placed against the edge or in the middle.
func (u *tui) wrap(p para, width int) []row {
	if width < 1 {
		return nil
	}
	if len(p.parts) == 0 {
		return []row{{}} // an empty spacer row
	}
	limit := width
	if p.max > 0 && p.max < 100 {
		if limit = width * p.max / 100; limit < 24 {
			limit = imin(width, 24)
		}
	}
	type tok struct {
		cells []cell
		w     int
		space bool
	}
	var toks []tok
	indent := 0
	for pi, pt := range p.parts {
		st := u.style(pt)
		g := uniseg.NewGraphemes(pt.text)
		for g.Next() {
			cw := g.Width()
			if cw <= 0 {
				continue
			}
			sp := g.Str() == " "
			if len(toks) == 0 || toks[len(toks)-1].space != sp {
				toks = append(toks, tok{space: sp})
			}
			t := &toks[len(toks)-1]
			t.cells = append(t.cells, cell{r: g.Runes(), w: cw, st: st})
			t.w += cw
			if pi < len(p.parts)-1 {
				indent += cw
			}
		}
	}
	if p.align != alignLeft || indent > limit/2 {
		indent = imin(2, indent)
	}
	if limit < 8 {
		indent = 0
	}
	var rows []row
	cur := row{}
	curW, avail := 0, limit
	newline := func() {
		rows = append(rows, cur)
		cur = row{}
		curW, avail = 0, limit-indent
	}
	for _, t := range toks {
		switch {
		case t.space:
			if curW == 0 && len(rows) > 0 {
				continue
			}
			if curW+t.w <= avail {
				cur.cells = append(cur.cells, t.cells...)
				curW += t.w
			} else {
				newline()
			}
		case curW+t.w <= avail:
			cur.cells = append(cur.cells, t.cells...)
			curW += t.w
		case t.w <= limit-indent && curW > 0:
			newline()
			cur.cells = append(cur.cells, t.cells...)
			curW += t.w
		default: // a word longer than the line: split it
			for _, c := range t.cells {
				if curW+c.w > avail && curW > 0 {
					newline()
				}
				cur.cells = append(cur.cells, c)
				curW += c.w
			}
		}
	}
	rows = append(rows, cur)
	for i := range rows {
		rw := 0
		for _, c := range rows[i].cells {
			rw += c.w
		}
		switch p.align {
		case alignRight:
			rows[i].x = imax(0, width-rw-1)
		case alignCenter:
			rows[i].x = imax(0, (width-rw)/2)
		default:
			if i > 0 {
				rows[i].x = indent
			}
		}
	}
	return rows
}

func (u *tui) prompt(v *View) string {
	p := v.Label
	if strings.HasPrefix(v.Target, "@") {
		p = "DM " + strings.TrimPrefix(v.Label, "@")
	}
	if p == "" {
		p = "(no room)"
	}
	switch v.State {
	case stSearch:
		p = "... " + p
	case stOffline:
		p = "(offline) " + p
	}
	if v.Note != "" {
		p = "(" + v.Note + ") " + p
	}
	if u.scroll > 0 {
		p = "^ " + p
	}
	return p + "> "
}

func (u *tui) drawInput(v *View, w, y int) {
	p := u.prompt(v)
	if len(p) > w/2 {
		p = "> "
	}
	st := u.style(part{kind: pMeta})
	if strings.HasPrefix(v.Target, "@") {
		st = u.style(part{kind: pDM})
	}
	x := u.put(0, y, w, p, st)
	avail := w - x - 1
	if avail < 1 {
		u.s.ShowCursor(imax(0, w-1), y)
		return
	}
	if u.cur < u.off {
		u.off = u.cur
	}
	for u.off < u.cur && uniseg.StringWidth(string(u.in[u.off:u.cur])) > avail {
		u.off++
	}
	u.put(x, y, w, string(u.in[u.off:]), u.style(part{kind: pText}))
	u.s.ShowCursor(x+uniseg.StringWidth(string(u.in[u.off:u.cur])), y)
}

// hardWrap cuts text into rows of at most w columns, like a terminal does.
func hardWrap(text string, w int, st tcell.Style) [][]cell {
	rows := [][]cell{nil}
	cw := 0
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		gw := g.Width()
		if gw <= 0 {
			continue
		}
		if cw+gw > w && cw > 0 {
			rows = append(rows, nil)
			cw = 0
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], cell{r: g.Runes(), w: gw, st: st})
		cw += gw
	}
	return rows
}
