package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Snake: eat, grow, don't hit the walls. Alone, don't hit yourself either.
//
// With others it's a free-for-all for two minutes: run your head into
// another snake and you bite off everything from there to its tail (and
// grow by that much); bite right behind its head and you eat it whole.
// Head to head, the longer snake eats the shorter. A snake that dies spills
// into food and comes back three seconds later, small and safe for two
// seconds. Most points when the time runs out wins.

func init() {
	gameDefs = append(gameDefs, &gameDef{
		id: "snake", name: "Snake", blurb: "eat, grow · with others: bite the other snakes",
		keys: "arrows or WASD steer · eat dots · bite other snakes from the side",
		min:  2, max: 4, solo: true, room: true, fast: true, soloSeats: 1,
		newEng:  newSnake,
		newView: func() gameView { return &snakeView{} },
	})
}

type pt struct{ X, Y int }

func (a pt) add(b pt) pt { return pt{a.X + b.X, a.Y + b.Y} }

var dirs = map[string]pt{"u": {0, -1}, "d": {0, 1}, "l": {-1, 0}, "r": {1, 0}}

func dirName(d pt) byte {
	switch d {
	case pt{0, -1}:
		return 'u'
	case pt{0, 1}:
		return 'd'
	case pt{-1, 0}:
		return 'l'
	}
	return 'r'
}

type snk struct {
	body  []pt // head first
	dir   pt
	q     []pt // turns pressed but not made yet
	alive bool
	score int

	// with others
	grow      int       // pieces still to grow
	kills     int       // snakes eaten whole
	away      bool      // the player dropped off: no coming back until they return
	backAt    time.Time // when a dead snake comes back
	safeUntil time.Time // just back: can't bite or be bitten
}

const (
	snakeRound   = 2 * time.Minute
	snakeRespawn = 3 * time.Second
	snakeSafe    = 2 * time.Second
	snakeMaxFood = 120
)

type snakeEng struct {
	w, h    int
	solo    bool
	o       engOpts
	sn      []*snk
	food    []pt
	startAt time.Time
	cd      int
	done    bool
	msg     string
	speed   time.Duration
	nfood   int
	endAt   time.Time // with others: when the round ends
	now     time.Time
	feed    string // with others: the latest bite, "@0 bit @2 (+4)"
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func newSnake(seats []gp, o engOpts) engine {
	e := &snakeEng{solo: o.solo, o: o, w: 30, h: 17, speed: 110 * time.Millisecond}
	if o.solo {
		e.w, e.h = clamp((o.w-4)/2, 14, 32), clamp(o.h-6, 10, 20)
		e.speed = 130 * time.Millisecond
	}
	starts := []struct {
		p pt
		d pt
	}{
		{pt{4, e.h / 4}, pt{1, 0}}, {pt{e.w - 5, e.h - 1 - e.h/4}, pt{-1, 0}},
		{pt{e.w - 5, e.h / 4}, pt{-1, 0}}, {pt{4, e.h - 1 - e.h/4}, pt{1, 0}},
	}
	if o.solo {
		starts[0].p = pt{e.w / 2, e.h / 2}
	}
	for i := range seats {
		st := starts[i%len(starts)]
		s := &snk{dir: st.d, alive: true}
		for k := 0; k < 3; k++ {
			s.body = append(s.body, pt{st.p.X - st.d.X*k, st.p.Y - st.d.Y*k})
		}
		e.sn = append(e.sn, s)
	}
	e.nfood = 1
	if !o.solo {
		e.nfood = len(seats) + 1
	}
	for len(e.food) < e.nfood {
		e.placeFood()
	}
	return e
}

func (e *snakeEng) taken(p pt) bool {
	for _, s := range e.sn {
		if !s.alive {
			continue
		}
		for _, b := range s.body {
			if b == p {
				return true
			}
		}
	}
	for _, f := range e.food {
		if f == p {
			return true
		}
	}
	return false
}

func (e *snakeEng) placeFood() {
	for try := 0; try < 500; try++ {
		p := pt{e.o.rnd.Intn(e.w), e.o.rnd.Intn(e.h)}
		if !e.taken(p) {
			e.food = append(e.food, p)
			return
		}
	}
}

func (e *snakeEng) every() time.Duration { return e.speed }
func (e *snakeEng) shared() bool         { return true }
func (e *snakeEng) over() bool           { return e.done }

func (e *snakeEng) setAway(seat int, away bool) {
	if seat < 0 || seat >= len(e.sn) || e.done {
		return
	}
	s := e.sn[seat]
	if e.solo {
		if away && s.alive {
			s.alive = false
			e.checkEnd()
		}
		return
	}
	s.away = away
	if away && s.alive {
		e.die(seat, -1) // a snake nobody steers turns into food
	} else if !away && !s.alive {
		s.backAt = e.now.Add(time.Second)
	}
}

func (e *snakeEng) input(seat int, d string) bool {
	if seat < 0 || seat >= len(e.sn) {
		return false
	}
	dir, ok := dirs[d]
	s := e.sn[seat]
	if !ok || !s.alive || len(s.q) >= 3 {
		return false
	}
	last := s.dir
	if len(s.q) > 0 {
		last = s.q[len(s.q)-1]
	}
	if dir == last || dir == (pt{-last.X, -last.Y}) {
		return false
	}
	s.q = append(s.q, dir)
	return false // nothing to show until the next step
}

func (e *snakeEng) step(now time.Time) bool {
	if e.done {
		return false
	}
	if e.startAt.IsZero() {
		wait := 3 * time.Second
		if e.solo {
			wait = 1500 * time.Millisecond
		}
		e.startAt = now.Add(wait)
	}
	if now.Before(e.startAt) {
		cd := int(e.startAt.Sub(now)/time.Second) + 1
		if cd != e.cd {
			e.cd = cd
			return true
		}
		return false
	}
	e.cd = 0
	e.now = now
	if !e.solo {
		return e.stepMulti(now)
	}
	n := len(e.sn)
	heads := make([]pt, n)
	grow := make([]bool, n)
	for i, s := range e.sn {
		if !s.alive {
			continue
		}
		if len(s.q) > 0 {
			s.dir, s.q = s.q[0], s.q[1:]
		}
		heads[i] = s.body[0].add(s.dir)
		for _, f := range e.food {
			if f == heads[i] {
				grow[i] = true
			}
		}
	}
	occ := map[pt]bool{}
	for j, s := range e.sn {
		if !s.alive {
			continue
		}
		for k, b := range s.body {
			if k == len(s.body)-1 && !grow[j] {
				continue // the tail moves out of the way
			}
			occ[b] = true
		}
	}
	dead := make([]bool, n)
	for i, s := range e.sn {
		if !s.alive {
			continue
		}
		h := heads[i]
		if h.X < 0 || h.Y < 0 || h.X >= e.w || h.Y >= e.h || occ[h] {
			dead[i] = true
		}
		for j := range e.sn {
			if j != i && e.sn[j].alive && heads[j] == h {
				dead[i] = true // head to head: both lose
			}
		}
	}
	for i, s := range e.sn {
		if !s.alive {
			continue
		}
		if dead[i] {
			s.alive = false
			continue
		}
		s.body = append([]pt{heads[i]}, s.body...)
		if grow[i] {
			s.score++
			for k, f := range e.food {
				if f == heads[i] {
					e.food = append(e.food[:k], e.food[k+1:]...)
					break
				}
			}
			if e.solo && e.speed > 60*time.Millisecond {
				e.speed -= 3 * time.Millisecond
			}
		} else {
			s.body = s.body[:len(s.body)-1]
		}
	}
	for len(e.food) < e.nfood && len(e.food) < e.w*e.h/4 {
		before := len(e.food)
		e.placeFood()
		if len(e.food) == before {
			break
		}
	}
	e.checkEnd()
	return true
}

// ---- with others ----

// owner says which snake has a piece at each cell, and which piece.
type owner struct{ snake, piece int }

func (e *snakeEng) safe(i int) bool { return e.now.Before(e.sn[i].safeUntil) }

func (e *snakeEng) stepMulti(now time.Time) bool {
	if e.endAt.IsZero() {
		e.endAt = now.Add(snakeRound)
	}
	n := len(e.sn)
	heads := make([]pt, n)
	moving := make([]bool, n)
	for i, s := range e.sn {
		if !s.alive {
			continue
		}
		if len(s.q) > 0 {
			s.dir, s.q = s.q[0], s.q[1:]
		}
		heads[i], moving[i] = s.body[0].add(s.dir), true
	}
	dead := make([]bool, n)
	killer := make([]int, n)
	for i := range killer {
		killer[i] = -1
	}
	// walls
	for i := range e.sn {
		if h := heads[i]; moving[i] && (h.X < 0 || h.Y < 0 || h.X >= e.w || h.Y >= e.h) {
			dead[i] = true
		}
	}
	// head to head (same cell, or passing through each other): the longer eats the shorter
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if !moving[i] || !moving[j] || e.safe(i) || e.safe(j) {
				continue
			}
			a, b := e.sn[i], e.sn[j]
			if heads[i] != heads[j] && !(heads[i] == b.body[0] && heads[j] == a.body[0]) {
				continue
			}
			switch {
			case len(a.body) > len(b.body):
				dead[j], killer[j] = true, i
			case len(b.body) > len(a.body):
				dead[i], killer[i] = true, j
			default:
				dead[i], dead[j] = true, true
			}
		}
	}
	// bodies where they are now; a tail that is about to move doesn't count
	occ := map[pt]owner{}
	for j, s := range e.sn {
		if !s.alive || e.safe(j) {
			continue
		}
		for k, b := range s.body {
			if k == 0 || (k == len(s.body)-1 && s.grow == 0) {
				continue
			}
			occ[b] = owner{j, k}
		}
	}
	type bite struct{ by, at int }
	bites := map[int]bite{} // victim -> the bite closest to its head
	for i := range e.sn {
		if !moving[i] || dead[i] {
			continue
		}
		o, hit := occ[heads[i]]
		switch {
		case !hit:
		case o.snake == i:
			if !e.safe(i) {
				dead[i] = true // ran into yourself
			}
		case e.safe(i):
			// just back: you slide over the others
		default:
			if b, ok := bites[o.snake]; !ok || o.piece < b.at {
				bites[o.snake] = bite{i, o.piece}
			}
		}
	}
	// food
	for i, s := range e.sn {
		if !moving[i] || dead[i] {
			continue
		}
		for k, f := range e.food {
			if f == heads[i] {
				e.food = append(e.food[:k], e.food[k+1:]...)
				s.grow++
				s.score++
				break
			}
		}
	}
	// move
	for i, s := range e.sn {
		if !moving[i] || dead[i] {
			continue
		}
		s.body = append([]pt{heads[i]}, s.body...)
		if s.grow > 0 {
			s.grow--
		} else {
			s.body = s.body[:len(s.body)-1]
		}
	}
	// bites: after the move, piece k of the victim is at k+1 (if it moved)
	for v, b := range bites {
		vs := e.sn[v]
		if dead[v] || dead[b.by] || !vs.alive {
			continue
		}
		at := b.at
		if moving[v] {
			at++
		}
		if at >= len(vs.body) {
			continue
		}
		if at < 3 { // bitten right behind the head (only a stub would be left): eaten whole
			dead[v], killer[v] = true, b.by
			continue
		}
		cut := len(vs.body) - at
		vs.body = vs.body[:at]
		vs.grow = 0
		by := e.sn[b.by]
		by.grow += cut
		by.score += cut
		e.feed = fmt.Sprintf("@%d bit @%d (+%d)", b.by, v, cut)
	}
	for i := range e.sn {
		if dead[i] && e.sn[i].alive {
			e.die(i, killer[i])
		}
	}
	// coming back
	for i, s := range e.sn {
		if !s.alive && !s.away && !s.backAt.IsZero() && !now.Before(s.backAt) {
			e.respawn(i)
		}
	}
	for len(e.food) < e.nfood {
		before := len(e.food)
		e.placeFood()
		if len(e.food) == before {
			break
		}
	}
	if !now.Before(e.endAt) {
		e.finish()
	}
	return true
}

// die turns a snake into food where it lay.
func (e *snakeEng) die(i, by int) {
	s := e.sn[i]
	s.alive, s.grow, s.q = false, 0, nil
	s.backAt = e.now.Add(snakeRespawn)
	for k, b := range s.body {
		if k%2 == 0 && len(e.food) < snakeMaxFood && b.X >= 0 && b.Y >= 0 && b.X < e.w && b.Y < e.h {
			dup := false
			for _, f := range e.food {
				if f == b {
					dup = true
					break
				}
			}
			if !dup {
				e.food = append(e.food, b)
			}
		}
	}
	if by >= 0 && by != i {
		k := e.sn[by]
		k.kills++
		k.score += 5 + len(s.body)/2
		k.grow += len(s.body) / 2
		e.feed = fmt.Sprintf("@%d ate @%d!", by, i)
	} else if !s.away {
		e.feed = fmt.Sprintf("@%d crashed", i)
	}
}

// respawn brings a snake back somewhere with room around it.
func (e *snakeEng) respawn(i int) {
	s := e.sn[i]
	ds := []pt{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	for try := 0; try < 300; try++ {
		d := ds[e.o.rnd.Intn(4)]
		h := pt{3 + e.o.rnd.Intn(imax(1, e.w-6)), 3 + e.o.rnd.Intn(imax(1, e.h-6))}
		body := []pt{h, {h.X - d.X, h.Y - d.Y}, {h.X - 2*d.X, h.Y - 2*d.Y}}
		ok := true
		for k := -2; k <= 5 && ok; k++ { // the cells around it and ahead are free
			for _, c := range []pt{{h.X + d.X*k, h.Y + d.Y*k}, {h.X + d.X*k + d.Y, h.Y + d.Y*k + d.X}, {h.X + d.X*k - d.Y, h.Y + d.Y*k - d.X}} {
				if e.takenBySnake(c) {
					ok = false
					break
				}
			}
		}
		if !ok {
			continue
		}
		s.body, s.dir, s.q, s.grow = body, d, nil, 0
		s.alive, s.backAt, s.safeUntil = true, time.Time{}, e.now.Add(snakeSafe)
		return
	}
	s.backAt = e.now.Add(500 * time.Millisecond) // crowded: try again soon
}

func (e *snakeEng) takenBySnake(p pt) bool {
	for _, s := range e.sn {
		if !s.alive {
			continue
		}
		for _, b := range s.body {
			if b == p {
				return true
			}
		}
	}
	return false
}

// finish ends a round with others: most points wins.
func (e *snakeEng) finish() {
	e.done = true
	best, who, tie := -1, -1, false
	for i, s := range e.sn {
		switch {
		case s.score > best:
			best, who, tie = s.score, i, false
		case s.score == best:
			tie = true
		}
	}
	if tie {
		e.msg = fmt.Sprintf("time! a draw at %d points", best)
	} else {
		e.msg = fmt.Sprintf("time! @%d wins with %d points", who, best)
	}
}

func (e *snakeEng) checkEnd() {
	alive, last := 0, -1
	for i, s := range e.sn {
		if s.alive {
			alive++
			last = i
		}
	}
	if e.solo {
		if alive == 0 {
			e.done = true
			e.msg = fmt.Sprintf("game over · you ate %d", e.sn[0].score)
		}
		return
	}
	if alive > 1 {
		return
	}
	e.done = true
	if alive == 1 {
		e.msg = fmt.Sprintf("@%d wins", last)
		return
	}
	best, who, tie := -1, -1, false
	for i, s := range e.sn {
		switch {
		case s.score > best:
			best, who, tie = s.score, i, false
		case s.score == best:
			tie = true
		}
	}
	if tie {
		e.msg = "everyone crashed · a draw"
	} else {
		e.msg = fmt.Sprintf("everyone crashed · @%d ate the most", who)
	}
}

type snakeV struct {
	X  int    `json:"x"`
	Y  int    `json:"y"`
	B  string `json:"b"` // from the head back: the way to the next piece
	A  bool   `json:"a,omitempty"`
	Sc int    `json:"sc,omitempty"`
	K  int    `json:"k,omitempty"`  // snakes eaten
	P  bool   `json:"p,omitempty"`  // just back: safe
	R  int    `json:"r,omitempty"`  // seconds until it comes back
	Aw bool   `json:"aw,omitempty"` // the player dropped off
}

type snakePic struct {
	W  int      `json:"w"`
	H  int      `json:"h"`
	S  []snakeV `json:"s"`
	F  []int    `json:"f"`
	CD int      `json:"cd,omitempty"`
	O  bool     `json:"o,omitempty"`
	M  string   `json:"m,omitempty"`
	TL int      `json:"tl,omitempty"` // with others: seconds left in the round
	FD string   `json:"fd,omitempty"` // the latest bite
}

func (e *snakeEng) view(seat int) interface{} {
	p := snakePic{W: e.w, H: e.h, CD: e.cd, O: e.done, M: e.msg, FD: e.feed}
	if !e.solo && !e.endAt.IsZero() && !e.done {
		p.TL = int((e.endAt.Sub(e.now) + time.Second - 1) / time.Second)
	}
	for i, s := range e.sn {
		v := snakeV{X: s.body[0].X, Y: s.body[0].Y, A: s.alive, Sc: s.score, K: s.kills, P: s.alive && e.safe(i), Aw: s.away}
		if !s.alive && !s.away && !s.backAt.IsZero() && !e.done {
			v.R = imax(1, int((s.backAt.Sub(e.now)+time.Second-1)/time.Second))
		}
		b := make([]byte, 0, len(s.body))
		for k := 1; k < len(s.body); k++ {
			b = append(b, dirName(pt{s.body[k].X - s.body[k-1].X, s.body[k].Y - s.body[k-1].Y}))
		}
		v.B = string(b)
		p.S = append(p.S, v)
	}
	for _, f := range e.food {
		p.F = append(p.F, f.X, f.Y)
	}
	return p
}

// ---- drawing ----

type snakeView struct {
	p      snakePic
	bodies [][]pt
}

func (v *snakeView) load(b []byte) error {
	var p snakePic
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	if p.W < 4 || p.H < 4 || p.W > 80 || p.H > 50 || len(p.S) > 8 {
		return errors.New("bad picture")
	}
	bodies := make([][]pt, len(p.S))
	for i, s := range p.S {
		c := pt{s.X, s.Y}
		bodies[i] = append(bodies[i], c)
		for k := 0; k < len(s.B) && k < p.W*p.H; k++ {
			d, ok := dirs[string(s.B[k])]
			if !ok {
				return errors.New("bad picture")
			}
			c = c.add(d)
			bodies[i] = append(bodies[i], c)
		}
	}
	v.p, v.bodies = p, bodies
	return nil
}

func (v *snakeView) size() (int, int) {
	if v.p.W == 0 {
		return 40, 14
	}
	return v.p.W*2 + 4, v.p.H + 5
}

func (v *snakeView) over() bool { return v.p.O }

func (v *snakeView) key(ev *tcell.EventKey, x *gctx) string {
	switch ev.Key() {
	case tcell.KeyUp:
		return "u"
	case tcell.KeyDown:
		return "d"
	case tcell.KeyLeft:
		return "l"
	case tcell.KeyRight:
		return "r"
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'w', 'W', 'k':
			return "u"
		case 's', 'S', 'j':
			return "d"
		case 'a', 'A', 'h':
			return "l"
		case 'd', 'D', 'l':
			return "r"
		}
	}
	return ""
}

// winnerText puts names in place of seat numbers ("@2 wins"), and "you"
// where it's you ("you win").
func winnerText(m string, x *gctx) string {
	for i := len(x.seats) - 1; i >= 0; i-- {
		m = strings.ReplaceAll(m, fmt.Sprintf("@%d", i), x.name(i))
	}
	for _, v := range [][2]string{{"you wins", "you win"}, {"you starts", "you start"}, {"you plays", "you play"},
		{"you draws", "you draw"}, {"you passes", "you pass"}, {"you is", "you are"}, {"you ate the most", "you ate the most"}} {
		m = strings.ReplaceAll(m, v[0], v[1])
	}
	return m
}

func (v *snakeView) draw(cv *canvas, x *gctx) {
	p := v.p
	bw, bh := p.W*2+2, p.H+2
	ox, oy := imax(0, (cv.w-bw)/2), imax(0, (cv.h-bh-1)/2)
	border := gsDim
	cv.text(ox, oy, "┌"+repeat("─", p.W*2)+"┐", border)
	for y := 0; y < p.H; y++ {
		cv.text(ox, oy+1+y, "│", border)
		cv.text(ox+1+p.W*2, oy+1+y, "│", border)
	}
	cv.text(ox, oy+1+p.H, "└"+repeat("─", p.W*2)+"┘", border)
	cell := func(c pt, s string, st tcell.Style) {
		if c.X >= 0 && c.Y >= 0 && c.X < p.W && c.Y < p.H {
			cv.text(ox+1+c.X*2, oy+1+c.Y, s, st)
		}
	}
	for i := 0; i+1 < len(p.F); i += 2 {
		cell(pt{p.F[i], p.F[i+1]}, "● ", tcell.StyleDefault.Foreground(tcell.PaletteColor(203)).Bold(true))
	}
	for i, body := range v.bodies {
		if !p.S[i].A {
			continue
		}
		col := seatColor(i)
		if x.solo {
			col = seatColor(0)
		}
		if p.S[i].P && time.Now().UnixMilli()%400 < 200 {
			continue // just back: blinking, and safe
		}
		for k := len(body) - 1; k >= 0; k-- {
			st := tcell.StyleDefault.Background(col)
			s := "  "
			if k == 0 {
				st = st.Foreground(tcell.ColorBlack).Bold(true)
				s = "••"
			}
			cell(body[k], s, st)
		}
	}
	// scores under the board
	sx := ox
	for i, s := range p.S {
		if sx >= cv.w-4 {
			break
		}
		name := x.name(i)
		st := tcell.StyleDefault.Foreground(seatColor(i))
		mark := "■ "
		if !s.A {
			mark, st = "✗ ", gsDim
		}
		label := fmt.Sprintf("%s %d", name, s.Sc)
		switch {
		case x.solo:
		case s.Aw:
			label += " (away)"
		case s.R > 0:
			label += fmt.Sprintf(" back in %ds", s.R)
		case s.A:
			label += fmt.Sprintf(" · %d long", len(v.bodies[i]))
		}
		sx = cv.text(sx, oy+bh, mark, st)
		sx = cv.text(sx, oy+bh, label+"   ", st)
	}
	if !x.solo {
		line := ""
		if p.TL > 0 {
			line = fmt.Sprintf("%d:%02d left", p.TL/60, p.TL%60)
		}
		if p.FD != "" {
			if line != "" {
				line += "   "
			}
			line += winnerText(p.FD, x)
		}
		cv.text(ox, oy+bh+1, line, gsDim)
	}
	if p.CD > 0 {
		cv.center(oy+bh/2, fmt.Sprintf("  %d  ", p.CD), gsSel.Reverse(true))
	}
	if p.O {
		cv.center(oy+bh/2, "  "+winnerText(p.M, x)+"  ", gsSel.Reverse(true))
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*imax(0, n))
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
