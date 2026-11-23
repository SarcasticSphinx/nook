package main

import (
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// The full-screen view: messages bottom-aligned like a shell, one input line,
// no borders, no title, no colours beyond grey. Esc swaps the whole screen
// for a fake shell prompt; Ctrl+C quits and the terminal is left exactly as
// it was before.

type uiOpts struct {
	Skin  string
	Ghost bool
	Focus bool
	Wipe  bool
}

var (
	stText = tcell.StyleDefault
	stMeta = tcell.StyleDefault.Foreground(tcell.ColorGray)
)

type cell struct {
	r  []rune
	w  int
	st tcell.Style
}

type row struct {
	indent int
	cells  []cell
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
	u := &tui{c: c, s: s, o: o, decoy: newDecoy()}
	s.SetStyle(tcell.StyleDefault)
	s.EnablePaste()
	s.Clear()
	c.SetOnChange(u.poke)
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
			return
		}
		w, _ := u.s.Size()
		if u.decoy.key(ev, w) {
			u.quit = true
		}
		return
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
	case tcell.KeyEscape:
		u.hidden = true
	case tcell.KeyEnter:
		u.submit()
	case tcell.KeyTab:
		u.c.Cycle(1)
		u.scroll = 0
	case tcell.KeyBacktab:
		u.c.Cycle(-1)
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

func (u *tui) insert(r rune) {
	if len(u.in) >= maxText {
		return
	}
	u.in = append(u.in, 0)
	copy(u.in[u.cur+1:], u.in[u.cur:])
	u.in[u.cur] = r
	u.cur++
}

func (u *tui) page() int {
	_, h := u.s.Size()
	if h > 3 {
		return h - 2
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
			u.hidden = true
			return
		case "dim", "ghost":
			u.o.Ghost = !u.o.Ghost
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
		case "skin":
			if arg == "log" || arg == "plain" {
				u.o.Skin = arg
			} else {
				u.c.Sys("skins: plain, log")
			}
			return
		case "focus":
			if u.o.Focus = !u.o.Focus; u.o.Focus {
				u.c.Sys("showing only the chat you're in (Tab to switch) · /focus again for all")
			} else {
				u.c.Sys("showing every chat")
			}
			return
		}
	}
	u.c.Input(line)
}

func (u *tui) style(kind int) tcell.Style {
	if u.o.Ghost {
		return stMeta.Dim(true)
	}
	if kind == pText {
		return stText
	}
	return stMeta
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
	v := u.c.View(1500)
	area := h - 1
	rows := u.layout(&v, w, area+u.scroll)
	if len(rows) < area+u.scroll {
		u.scroll = imax(0, len(rows)-area)
	}
	end := len(rows) - u.scroll
	start := imax(0, end-area)
	y := area - (end - start)
	for _, r := range rows[start:end] {
		x := r.indent
		for _, c := range r.cells {
			if x+c.w > w {
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

// layout wraps the newest lines until it has at least `need` rows.
func (u *tui) layout(v *View, width, need int) []row {
	now := time.Now().UnixMilli()
	fade := atomic.LoadInt64(&u.fadeSec) * 1000
	var blocks [][]row
	n := 0
	for i := len(v.Lines) - 1; i >= 0 && n < need; i-- {
		l := &v.Lines[i]
		if u.o.Focus && !l.Sys && l.Key != v.Target {
			continue
		}
		if u.scroll == 0 && (l.Seq <= u.clearSeq || (fade > 0 && now-l.TS > fade)) {
			continue
		}
		b := u.wrap(fmtLine(l, v, u.o.Skin), width)
		blocks = append(blocks, b)
		n += len(b)
	}
	rows := make([]row, 0, n)
	for i := len(blocks) - 1; i >= 0; i-- {
		rows = append(rows, blocks[i]...)
	}
	return rows
}

// wrap breaks parts into rows at word boundaries; continuation rows are
// indented to line up under the message text.
func (u *tui) wrap(parts []part, width int) []row {
	if width < 1 {
		return nil
	}
	type tok struct {
		cells []cell
		w     int
		space bool
	}
	var toks []tok
	indent := 0
	for pi, p := range parts {
		st := u.style(p.kind)
		g := uniseg.NewGraphemes(p.text)
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
			if pi < len(parts)-1 {
				indent += cw
			}
		}
	}
	if indent > width/2 {
		indent = 2
	}
	if width < 8 {
		indent = 0
	}
	var rows []row
	cur := row{}
	curW, avail := 0, width
	newline := func() {
		rows = append(rows, cur)
		cur = row{indent: indent}
		curW, avail = 0, width-indent
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
		case t.w <= width-indent && curW > 0:
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
	return append(rows, cur)
}

func (u *tui) prompt(v *View) string {
	p := v.Label
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
	st := u.style(pMeta)
	x := 0
	for _, r := range p { // prompts are ASCII
		u.s.SetContent(x, y, r, nil, st)
		x++
	}
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
	tst := u.style(pText)
	cx := x
	g := uniseg.NewGraphemes(string(u.in[u.off:]))
	for g.Next() {
		cw := g.Width()
		if cw <= 0 {
			continue
		}
		if cx+cw > w {
			break
		}
		rs := g.Runes()
		u.s.SetContent(cx, y, rs[0], rs[1:], tst)
		cx += cw
	}
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
