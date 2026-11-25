package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Two board games for two: Tic-tac-toe and Four in a Row (drop pieces into
// columns; four in a line wins). Alone, you play the computer.

func init() {
	gameDefs = append(gameDefs, &gameDef{
		id: "ttt", name: "Tic-tac-toe", blurb: "three in a row",
		keys: "arrows move · Enter places · or press 1-9",
		min:  2, max: 2, solo: true, room: true, soloSeats: 2,
		newEng:  func(s []gp, o engOpts) engine { return newBoard("ttt", s, o) },
		newView: func() gameView { return &boardView{cur: 4} },
	}, &gameDef{
		id: "four", name: "Four in a Row", blurb: "drop pieces, line up four",
		keys: "← → pick a column · Enter drops · or press 1-7",
		min:  2, max: 2, solo: true, room: true, soloSeats: 2,
		newEng:  func(s []gp, o engOpts) engine { return newBoard("four", s, o) },
		newView: func() gameView { return &boardView{cur: 3} },
	})
}

const (
	boardTurnLimit = 60 * time.Second // then the computer moves for you
	boardAwayLimit = 30 * time.Second // away that long: you lose
)

type boardEng struct {
	kind             string
	cols, rows, need int
	b                []int8 // 0 empty, 1 seat 0, 2 seat 1
	turn, moves      int
	win              int // -1 playing, 0 or 1 that seat won, 2 draw
	line             []int
	last             int
	msg              string
	bot              [2]bool
	away             [2]time.Time // when they went away (zero: here)
	turnAt           time.Time
	solo             bool
	o                engOpts
}

func newBoard(kind string, seats []gp, o engOpts) *boardEng {
	e := &boardEng{kind: kind, cols: 3, rows: 3, need: 3, win: -1, last: -1, solo: o.solo, o: o}
	if kind == "four" {
		e.cols, e.rows, e.need = 7, 6, 4
	}
	e.b = make([]int8, e.cols*e.rows)
	for i := 0; i < 2 && i < len(seats); i++ {
		e.bot[i] = seats[i].Bot
	}
	if !o.solo {
		e.turn = o.rnd.Intn(2) // who starts is fair with others
	}
	return e
}

func (e *boardEng) every() time.Duration { return 150 * time.Millisecond }
func (e *boardEng) shared() bool         { return true }
func (e *boardEng) over() bool           { return e.win >= 0 }

func (e *boardEng) setAway(seat int, away bool) {
	if seat < 0 || seat > 1 {
		return
	}
	if !away {
		e.away[seat] = time.Time{}
	} else if e.away[seat].IsZero() {
		e.away[seat] = time.Now()
	}
}

func (e *boardEng) input(seat int, d string) bool {
	// "m<move number>:<cell or column>": a move made for an old position is dropped
	if e.win >= 0 || seat != e.turn || e.bot[seat] || !strings.HasPrefix(d, "m") {
		return false
	}
	n, c, ok := strings.Cut(d[1:], ":")
	mn, err1 := strconv.Atoi(n)
	at, err2 := strconv.Atoi(c)
	if !ok || err1 != nil || err2 != nil || mn != e.moves {
		return false
	}
	return e.play(at)
}

// play puts the piece of whoever's turn it is at a cell (Tic-tac-toe) or
// in a column (Four in a Row).
func (e *boardEng) play(at int) bool {
	cell := -1
	if e.kind == "ttt" {
		if at >= 0 && at < 9 && e.b[at] == 0 {
			cell = at
		}
	} else if at >= 0 && at < e.cols {
		for r := e.rows - 1; r >= 0; r-- {
			if e.b[r*e.cols+at] == 0 {
				cell = r*e.cols + at
				break
			}
		}
	}
	if cell < 0 {
		return false
	}
	e.b[cell] = int8(e.turn + 1)
	e.moves++
	e.last = cell
	if line := e.lineAt(e.b, cell); line != nil {
		e.win, e.line = e.turn, line
		e.msg = fmt.Sprintf("@%d wins", e.turn)
	} else if e.moves == len(e.b) {
		e.win, e.msg = 2, "a draw"
	} else {
		e.turn = 1 - e.turn
	}
	e.turnAt = time.Time{}
	return true
}

func (e *boardEng) lineAt(b []int8, cell int) []int {
	p := b[cell]
	r0, c0 := cell/e.cols, cell%e.cols
	for _, d := range [][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}} {
		line := []int{cell}
		for _, sgn := range []int{1, -1} {
			r, c := r0+d[0]*sgn, c0+d[1]*sgn
			for r >= 0 && r < e.rows && c >= 0 && c < e.cols && b[r*e.cols+c] == p {
				line = append(line, r*e.cols+c)
				r, c = r+d[0]*sgn, c+d[1]*sgn
			}
		}
		if len(line) >= e.need {
			return line
		}
	}
	return nil
}

func (e *boardEng) step(now time.Time) bool {
	if e.win >= 0 {
		return false
	}
	if e.turnAt.IsZero() {
		e.turnAt = now
		return true
	}
	t := e.turn
	if a := e.away[t]; !a.IsZero() && now.Sub(a) > boardAwayLimit {
		e.win = 1 - t
		e.msg = fmt.Sprintf("@%d left · @%d wins", t, 1-t)
		return true
	}
	waited := now.Sub(e.turnAt)
	switch {
	case e.bot[t] && waited > 700*time.Millisecond:
		return e.play(e.botMove())
	case !e.bot[t] && !e.solo && waited > boardTurnLimit:
		return e.play(e.botMove()) // too long: the computer moves for them
	}
	// the turn timer ticks once a second on everyone's screen
	return !e.solo && waited%time.Second < e.every()
}

type boardPic struct {
	B  string `json:"b"`
	T  int    `json:"t"`
	N  int    `json:"n"`
	W  int    `json:"w"`
	L  []int  `json:"l,omitempty"`
	LA int    `json:"la"`
	M  string `json:"m,omitempty"`
	TL int    `json:"tl,omitempty"` // seconds left to move
	K  string `json:"k"`
}

func (e *boardEng) view(seat int) interface{} {
	var sb strings.Builder
	for _, v := range e.b {
		sb.WriteByte('0' + byte(v))
	}
	p := boardPic{B: sb.String(), T: e.turn, N: e.moves, W: e.win, L: e.line, LA: e.last, M: e.msg, K: e.kind}
	if !e.solo && e.win < 0 && !e.turnAt.IsZero() && !e.bot[e.turn] {
		p.TL = int((boardTurnLimit - time.Since(e.turnAt)) / time.Second)
	}
	return p
}

// ---- the computer's moves ----

func (e *boardEng) botMove() int {
	moves := e.legal(e.b)
	if len(moves) == 0 {
		return -1
	}
	if e.kind == "ttt" {
		if e.o.rnd.Intn(100) < 12 { // now and then a human-looking slip
			return moves[e.o.rnd.Intn(len(moves))]
		}
		best, bestScore := moves[0], -1000
		for _, m := range e.order(moves) {
			b := append([]int8(nil), e.b...)
			cell := e.drop(b, m, int8(e.turn+1))
			s := -e.negamax(b, cell, int8(2-e.turn), 9, -1000, 1000)
			if s > bestScore {
				best, bestScore = m, s
			}
		}
		return best
	}
	best, bestScore := moves[0], -1000000
	for _, m := range e.order(moves) {
		b := append([]int8(nil), e.b...)
		cell := e.drop(b, m, int8(e.turn+1))
		s := -e.negamax(b, cell, int8(2-e.turn), 5, -1000000, 1000000)
		if s > bestScore {
			best, bestScore = m, s
		}
	}
	return best
}

func (e *boardEng) legal(b []int8) []int {
	var out []int
	if e.kind == "ttt" {
		for i, v := range b {
			if v == 0 {
				out = append(out, i)
			}
		}
		return out
	}
	for c := 0; c < e.cols; c++ {
		if b[c] == 0 {
			out = append(out, c)
		}
	}
	return out
}

// order tries the middle first, and shuffles equal moves a little.
func (e *boardEng) order(m []int) []int {
	out := append([]int(nil), m...)
	e.o.rnd.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	mid := e.cols / 2
	if e.kind == "ttt" {
		mid = 4
	}
	for i := range out {
		if out[i] == mid {
			out[0], out[i] = out[i], out[0]
		}
	}
	return out
}

func (e *boardEng) drop(b []int8, m int, p int8) int {
	if e.kind == "ttt" {
		b[m] = p
		return m
	}
	for r := e.rows - 1; r >= 0; r-- {
		if b[r*e.cols+m] == 0 {
			b[r*e.cols+m] = p
			return r*e.cols + m
		}
	}
	return -1
}

// negamax scores the position for p, who is about to move; the last move
// (at cell) was the other player's.
func (e *boardEng) negamax(b []int8, cell int, p int8, depth, alpha, beta int) int {
	if e.lineAt(b, cell) != nil {
		return -(1000 + depth) // the other player just won; sooner is worse
	}
	moves := e.legal(b)
	if len(moves) == 0 {
		return 0
	}
	if depth == 0 {
		return e.heur(b, p)
	}
	best := -1000000
	for _, m := range e.order(moves) {
		c := e.drop(b, m, p)
		s := -e.negamax(b, c, 3-p, depth-1, -beta, -alpha)
		b[c] = 0
		if s > best {
			best = s
		}
		if s > alpha {
			alpha = s
		}
		if alpha >= beta {
			break
		}
	}
	return best
}

// heur counts open lines (Four in a Row): pieces in a window of four with
// none of the other player's.
func (e *boardEng) heur(b []int8, p int8) int {
	score := 0
	for r := 0; r < e.rows; r++ {
		if b[r*e.cols+e.cols/2] == p {
			score += 3
		} else if b[r*e.cols+e.cols/2] != 0 {
			score -= 3
		}
	}
	for r := 0; r < e.rows; r++ {
		for c := 0; c < e.cols; c++ {
			for _, d := range [][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}} {
				er, ec := r+d[0]*3, c+d[1]*3
				if er < 0 || er >= e.rows || ec < 0 || ec >= e.cols {
					continue
				}
				mine, theirs := 0, 0
				for k := 0; k < 4; k++ {
					switch b[(r+d[0]*k)*e.cols+c+d[1]*k] {
					case p:
						mine++
					case 0:
					default:
						theirs++
					}
				}
				switch {
				case theirs == 0 && mine == 3:
					score += 50
				case theirs == 0 && mine == 2:
					score += 5
				case mine == 0 && theirs == 3:
					score -= 60
				case mine == 0 && theirs == 2:
					score -= 5
				}
			}
		}
	}
	return score
}

// ---- drawing ----

type boardView struct {
	p   boardPic
	cur int
}

func (v *boardView) load(b []byte) error {
	var p boardPic
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	if (p.K == "ttt" && len(p.B) != 9) || (p.K == "four" && len(p.B) != 42) || (p.K != "ttt" && p.K != "four") {
		return errors.New("bad picture")
	}
	v.p = p
	return nil
}

func (v *boardView) dims() (cols, rows int) {
	if v.p.K == "four" {
		return 7, 6
	}
	return 3, 3
}

func (v *boardView) size() (int, int) {
	if v.p.K == "four" {
		return 40, 18
	}
	return 34, 16
}

func (v *boardView) over() bool { return v.p.W >= 0 && v.p.B != "" }

func (v *boardView) key(ev *tcell.EventKey, x *gctx) string {
	cols, _ := v.dims()
	if v.p.W >= 0 || x.me != v.p.T {
		return ""
	}
	n := cols
	if v.p.K == "ttt" {
		n = 9
	}
	mv := func(at int) string { return "m" + strconv.Itoa(v.p.N) + ":" + strconv.Itoa(at) }
	switch ev.Key() {
	case tcell.KeyLeft:
		if v.cur%cols > 0 {
			v.cur--
		}
	case tcell.KeyRight:
		if v.cur%cols < cols-1 {
			v.cur++
		}
	case tcell.KeyUp:
		if v.p.K == "ttt" && v.cur >= 3 {
			v.cur -= 3
		}
	case tcell.KeyDown:
		if v.p.K == "ttt" && v.cur < 6 {
			v.cur += 3
		} else if v.p.K == "four" {
			return mv(v.cur)
		}
	case tcell.KeyEnter:
		return mv(v.cur)
	case tcell.KeyRune:
		r := ev.Rune()
		switch {
		case r == ' ':
			return mv(v.cur)
		case r >= '1' && r <= '9' && int(r-'1') < n:
			v.cur = int(r - '1')
			return mv(v.cur)
		case r == 'a' || r == 'h':
			if v.cur%cols > 0 {
				v.cur--
			}
		case r == 'd' || r == 'l':
			if v.cur%cols < cols-1 {
				v.cur++
			}
		case r == 'w' || r == 'k':
			if v.p.K == "ttt" && v.cur >= 3 {
				v.cur -= 3
			}
		case r == 's' || r == 'j':
			if v.p.K == "ttt" && v.cur < 6 {
				v.cur += 3
			}
		}
	}
	return ""
}

func (v *boardView) status(x *gctx) (string, tcell.Style) {
	p := v.p
	switch {
	case p.W == 2:
		return "a draw", gsSel
	case p.W >= 0:
		return winnerText(p.M, x), gsSel
	case p.T == x.me:
		s := "your move"
		if p.TL > 0 && p.TL <= 20 {
			s += fmt.Sprintf(" · %ds", p.TL)
		}
		return s, gsGood
	}
	s := x.name(p.T) + " is thinking…"
	if p.TL > 0 && p.TL <= 20 {
		s += fmt.Sprintf(" %ds", p.TL)
	}
	return s, gsDim
}

func (v *boardView) piece(i int, x *gctx) (string, tcell.Style) {
	sym := []string{"X", "O"}
	if v.p.K == "four" {
		sym = []string{"●", "●"}
	}
	return sym[i], tcell.StyleDefault.Foreground(seatColor(i)).Bold(true)
}

func (v *boardView) draw(cv *canvas, x *gctx) {
	p := v.p
	inLine := map[int]bool{}
	for _, c := range p.L {
		inLine[c] = true
	}
	myTurn := p.W < 0 && p.T == x.me
	// who is who
	y := 1
	px := 2
	for i := 0; i < 2 && i < len(x.seats); i++ {
		s, st := v.piece(i, x)
		px = cv.text(px, y, s+" ", st)
		px = cv.text(px, y, x.name(i)+"    ", gsText)
	}
	msg, mst := v.status(x)
	cv.text(2, y+1, msg, mst)
	if p.K == "ttt" {
		ox, oy := imax(0, (cv.w-23)/2), 4
		for r := 0; r < 3; r++ {
			for c := 0; c < 3; c++ {
				i := r*3 + c
				cx, cy := ox+c*8, oy+r*4
				bg := tcell.StyleDefault
				if myTurn && i == v.cur {
					bg = bg.Background(tcell.PaletteColor(237))
				}
				for k := 0; k < 3; k++ {
					cv.text(cx, cy+k, "       ", bg)
				}
				if s := p.B[i]; s != '0' {
					sym, st := v.piece(int(s-'1'), x)
					if inLine[i] {
						st = st.Reverse(true)
					}
					cv.text(cx+3, cy+1, sym, st.Background(bgOf(bg)))
				} else if myTurn {
					cv.text(cx+3, cy+1, strconv.Itoa(i+1), gsDim.Background(bgOf(bg)))
				}
				if c < 2 {
					for k := 0; k < 3; k++ {
						cv.text(cx+7, cy+k, "│", gsDim)
					}
				}
			}
			if r < 2 {
				cv.text(ox, oy+r*4+3, "───────┼───────┼───────", gsDim)
			}
		}
		return
	}
	ox, oy := imax(0, (cv.w-29)/2), 4
	for c := 0; c < 7; c++ {
		st := gsDim
		if myTurn && c == v.cur {
			st = gsSel
		}
		cv.text(ox+2+c*4, oy, strconv.Itoa(c+1), st)
		if myTurn && c == v.cur {
			sym, pst := v.piece(x.me, x)
			cv.text(ox+2+c*4, oy+1, sym, pst)
		}
	}
	for r := 0; r < 6; r++ {
		row := oy + 2 + r
		cv.text(ox, row, "│", gsDim)
		for c := 0; c < 7; c++ {
			i := r*7 + c
			if s := p.B[i]; s != '0' {
				sym, st := v.piece(int(s-'1'), x)
				if inLine[i] {
					st = st.Reverse(true)
				}
				if i == p.LA && p.W < 0 {
					st = st.Underline(true)
				}
				cv.text(ox+2+c*4, row, sym, st)
			} else {
				cv.text(ox+2+c*4, row, "·", gsDim)
			}
			cv.text(ox+4+c*4, row, "│", gsDim)
		}
	}
	cv.text(ox, oy+8, "└"+repeat("───┴", 6)+"───┘", gsDim)
}

func bgOf(st tcell.Style) tcell.Color {
	_, bg, _ := st.Decompose()
	return bg
}
