package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Last Card: a shedding card game. Match the top card's colour or symbol;
// first to empty their hand wins. Skip, Reverse, +2, Wild and Wild +4 cards
// shake things up. Bots fill empty seats, and play for anyone who drops off.

func init() {
	gameDefs = append(gameDefs, &gameDef{
		id: "cards", name: "Last Card", blurb: "match colour or symbol, empty your hand first",
		keys: "← → pick a card · Enter plays · D draws · P passes after drawing",
		min:  2, max: 6, solo: true, room: true, bots: true, soloSeats: 4,
		newEng:  newCards,
		newView: func() gameView { return &cardsView{} },
	})
}

// A card is two letters: colour (r g b y, w for wild) and symbol
// (0-9, s skip, v reverse, + draw two, w wild, 4 wild draw four).
const cardColors = "rgby"

const (
	cardsTurnLimit = 30 * time.Second
	cardsBotDelay  = 900 * time.Millisecond
)

type cardsEng struct {
	o       engOpts
	n       int
	bot     []bool
	away    []bool
	deck    []string
	pile    []string
	hands   [][]string
	color   byte
	turn    int
	dir     int
	drew    string // the card drawn this turn, if any (it may be played)
	turnNo  int
	turnAt  time.Time
	winner  int
	log     []string
	scores  []int
	lastAct time.Time
}

func newDeck() []string {
	var d []string
	for _, c := range cardColors {
		d = append(d, string(c)+"0")
		for _, s := range "123456789sv+" {
			d = append(d, string(c)+string(s), string(c)+string(s))
		}
	}
	for i := 0; i < 4; i++ {
		d = append(d, "ww", "w4")
	}
	return d
}

func newCards(seats []gp, o engOpts) engine {
	n := len(seats)
	e := &cardsEng{o: o, n: n, bot: make([]bool, n), away: make([]bool, n), hands: make([][]string, n),
		dir: 1, winner: -1, scores: make([]int, n)}
	for i, s := range seats {
		e.bot[i] = s.Bot
	}
	e.deck = newDeck()
	o.rnd.Shuffle(len(e.deck), func(i, j int) { e.deck[i], e.deck[j] = e.deck[j], e.deck[i] })
	for k := 0; k < 7; k++ {
		for i := 0; i < n; i++ {
			e.hands[i] = append(e.hands[i], e.draw1())
		}
	}
	for _, h := range e.hands {
		sortHand(h)
	}
	// the first card is a number card, so nobody starts with a penalty
	for {
		c := e.draw1()
		if c[0] != 'w' && c[1] >= '0' && c[1] <= '9' {
			e.pile = append(e.pile, c)
			e.color = c[0]
			break
		}
		e.deck = append([]string{c}, e.deck...)
	}
	if !o.solo {
		e.turn = o.rnd.Intn(n)
	}
	e.say("@%d starts", e.turn)
	return e
}

func (e *cardsEng) draw1() string {
	if len(e.deck) == 0 && len(e.pile) > 1 { // shuffle the pile back in, but the top card
		top := e.pile[len(e.pile)-1]
		e.deck = append(e.deck, e.pile[:len(e.pile)-1]...)
		e.pile = []string{top}
		e.o.rnd.Shuffle(len(e.deck), func(i, j int) { e.deck[i], e.deck[j] = e.deck[j], e.deck[i] })
	}
	if len(e.deck) == 0 {
		return ""
	}
	c := e.deck[len(e.deck)-1]
	e.deck = e.deck[:len(e.deck)-1]
	return c
}

func (e *cardsEng) give(seat, k int) int {
	got := 0
	for i := 0; i < k; i++ {
		if c := e.draw1(); c != "" {
			e.hands[seat] = append(e.hands[seat], c)
			got++
		}
	}
	return got
}

func (e *cardsEng) say(f string, a ...interface{}) {
	e.log = append(e.log, fmt.Sprintf(f, a...))
	if len(e.log) > 4 {
		e.log = e.log[len(e.log)-4:]
	}
}

func (e *cardsEng) top() string { return e.pile[len(e.pile)-1] }

func (e *cardsEng) playable(c string) bool {
	if c[0] == 'w' {
		return true
	}
	return c[0] == e.color || cardSym(c) == cardSym(e.top())
}

func (e *cardsEng) next(from, steps int) int {
	return ((from+e.dir*steps)%e.n + e.n) % e.n
}

func (e *cardsEng) every() time.Duration { return 150 * time.Millisecond }
func (e *cardsEng) shared() bool         { return false }
func (e *cardsEng) over() bool           { return e.winner >= 0 }

func (e *cardsEng) setAway(seat int, away bool) {
	if seat >= 0 && seat < e.n && e.away[seat] != away {
		e.away[seat] = away
		if away {
			e.say("@%d dropped off · a bot plays for them", seat)
		} else {
			e.say("@%d is back", seat)
		}
	}
}

// input: "<turn number>:p<index>[colour]" plays a card, ":d" draws, ":k" passes
func (e *cardsEng) input(seat int, d string) bool {
	if e.winner >= 0 || seat != e.turn || e.bot[seat] {
		return false
	}
	n, act, ok := strings.Cut(d, ":")
	if tn, err := strconv.Atoi(n); !ok || err != nil || tn != e.turnNo || act == "" {
		return false
	}
	switch act[0] {
	case 'd':
		return e.doDraw(seat)
	case 'k':
		if e.drew == "" {
			return false
		}
		e.say("@%d passes", seat)
		e.advance(1)
		return true
	case 'p':
		body := act[1:]
		var col byte
		if len(body) > 0 && strings.IndexByte(cardColors, body[len(body)-1]) >= 0 {
			col, body = body[len(body)-1], body[:len(body)-1]
		}
		i, err := strconv.Atoi(body)
		if err != nil {
			return false
		}
		return e.doPlay(seat, i, col)
	}
	return false
}

func (e *cardsEng) doDraw(seat int) bool {
	if e.drew != "" {
		return false
	}
	if e.give(seat, 1) == 0 {
		e.say("the deck is empty · @%d passes", seat)
		e.advance(1)
		return true
	}
	c := e.hands[seat][len(e.hands[seat])-1]
	e.say("@%d draws a card", seat)
	if !e.playable(c) {
		e.advance(1) // nothing to do with it
		return true
	}
	e.drew = c
	e.turnAt = time.Time{} // a fresh moment to decide
	return true
}

func (e *cardsEng) doPlay(seat, i int, col byte) bool {
	h := e.hands[seat]
	if i < 0 || i >= len(h) {
		return false
	}
	c := h[i]
	if !e.playable(c) || (e.drew != "" && i != len(h)-1) { // after drawing, only the drawn card
		return false
	}
	if c[0] == 'w' {
		if col == 0 {
			return false
		}
		e.color = col
	} else {
		e.color = c[0]
	}
	e.hands[seat] = append(h[:i:i], h[i+1:]...)
	e.pile = append(e.pile, c)
	e.say("@%d plays %s", seat, cardName(c, e.color))
	if len(e.hands[seat]) == 0 {
		e.winner = seat
		pts := 0
		for _, hand := range e.hands {
			for _, x := range hand {
				pts += cardPoints(x)
			}
		}
		e.scores[seat] = pts
		e.say("@%d wins!", seat)
		return true
	}
	if len(e.hands[seat]) == 1 {
		e.say("@%d: last card!", seat)
	}
	steps := 1
	switch cardSym(c) {
	case 's':
		e.say("@%d is skipped", e.next(seat, 1))
		steps = 2
	case 'v':
		e.dir = -e.dir
		if e.n == 2 {
			steps = 2 // with two, reverse is a skip
		}
	case '+':
		v := e.next(seat, 1)
		e.say("@%d draws %d and is skipped", v, e.give(v, 2))
		steps = 2
	case 'F':
		v := e.next(seat, 1)
		e.say("@%d draws %d and is skipped", v, e.give(v, 4))
		steps = 2
	}
	e.advance(steps)
	return true
}

func (e *cardsEng) advance(steps int) {
	for _, h := range e.hands {
		sortHand(h)
	}
	e.turn = e.next(e.turn, steps)
	e.drew = ""
	e.turnNo++
	e.turnAt = time.Time{}
}

// sortHand puts a hand in order: by colour, then symbol, wilds last.
func sortHand(h []string) {
	rank := func(c string) int {
		ci := strings.IndexByte(cardColors+"w", c[0])
		return ci*100 + strings.IndexByte("0123456789sv+w4", c[1])
	}
	sort.SliceStable(h, func(i, j int) bool { return rank(h[i]) < rank(h[j]) })
}

func cardPoints(c string) int {
	switch {
	case c[0] == 'w':
		return 50
	case c[1] >= '0' && c[1] <= '9':
		return int(c[1] - '0')
	}
	return 20
}

func colorName(c byte) string {
	switch c {
	case 'r':
		return "red"
	case 'g':
		return "green"
	case 'b':
		return "blue"
	case 'y':
		return "yellow"
	}
	return "wild"
}

// cardSym is a card's symbol, with the wilds told apart from number
// cards: W wild, F wild draw four (a red 4 is a 4, not a +4).
func cardSym(c string) byte {
	if c[0] == 'w' {
		if c[1] == '4' {
			return 'F'
		}
		return 'W'
	}
	return c[1]
}

func cardLabel(c string) string {
	switch cardSym(c) {
	case 's':
		return "SKIP"
	case 'v':
		return "REV"
	case '+':
		return "+2"
	case 'W':
		return "WILD"
	case 'F':
		return "W+4"
	}
	return string(c[1])
}

// cardInfo says what a card does, in a few words.
func cardInfo(c string) string {
	col := colorName(c[0])
	switch cardSym(c) {
	case 's':
		return col + " SKIP: the next player misses their turn"
	case 'v':
		return col + " REVERSE: flips the order (with 2 players it acts as a skip)"
	case '+':
		return col + " +2: the next player draws 2 and misses their turn"
	case 'W':
		return "WILD: goes on anything; you choose the next colour"
	case 'F':
		return "WILD +4: goes on anything; pick a colour, next player draws 4, misses a turn"
	}
	return col + " " + string(c[1]) + ": a number card"
}

// symbolName is how the matching rule names a card's symbol.
func symbolName(c string) string {
	switch cardSym(c) {
	case 's':
		return "a SKIP"
	case 'v':
		return "a REVERSE"
	case '+':
		return "a +2"
	case 'W', 'F':
		return ""
	}
	return "a " + string(c[1])
}

func cardName(c string, col byte) string {
	if c[0] == 'w' {
		return cardLabel(c) + " (" + colorName(col) + ")"
	}
	n := map[byte]string{'s': "skip", 'v': "reverse", '+': "+2"}[c[1]]
	if n == "" {
		n = string(c[1])
	}
	return colorName(c[0]) + " " + n
}

func (e *cardsEng) step(now time.Time) bool {
	if e.winner >= 0 {
		return false
	}
	if e.turnAt.IsZero() {
		e.turnAt = now
		return true
	}
	t := e.turn
	waited := now.Sub(e.turnAt)
	auto := (e.bot[t] || e.away[t]) && waited > cardsBotDelay
	if !auto && !e.o.solo && waited > cardsTurnLimit {
		auto = true
		e.say("@%d took too long", t)
	}
	if auto {
		return e.botTurn(t)
	}
	return !e.o.solo && waited%time.Second < e.every() // the timer on screen
}

// botTurn plays like a sensible person: hold the wilds back, dump action
// cards on someone close to winning, pick the colour you hold most of.
func (e *cardsEng) botTurn(seat int) bool {
	h := e.hands[seat]
	best, bestScore := -1, -1000
	nextLeft := len(e.hands[e.next(seat, 1)])
	for i, c := range h {
		if e.drew != "" && i != len(h)-1 {
			continue
		}
		if !e.playable(c) {
			continue
		}
		s := 10
		switch {
		case c[0] == 'w':
			s = 1
			if c[1] == '4' && nextLeft <= 2 {
				s = 40
			}
		case c[1] == 's' || c[1] == 'v' || c[1] == '+':
			s = 15
			if nextLeft <= 2 {
				s = 35
			}
		}
		if c[0] == e.color {
			s += 2
		}
		s += e.o.rnd.Intn(3)
		if s > bestScore {
			best, bestScore = i, s
		}
	}
	if best < 0 {
		if e.drew != "" {
			e.say("@%d passes", seat)
			e.advance(1)
			return true
		}
		return e.doDraw(seat)
	}
	var col byte
	if h[best][0] == 'w' {
		count := map[byte]int{}
		for _, c := range h {
			if c[0] != 'w' {
				count[c[0]]++
			}
		}
		col = cardColors[e.o.rnd.Intn(4)]
		for _, c := range []byte(cardColors) {
			if count[c] > count[col] {
				col = c
			}
		}
	}
	return e.doPlay(seat, best, col)
}

type cardsPic struct {
	H   []string `json:"h"`            // your hand (nothing when watching)
	C   []int    `json:"c"`            // cards each seat holds
	Top string   `json:"top"`          // the top of the pile
	Col string   `json:"col"`          // the colour to match
	T   int      `json:"t"`            // whose turn
	TN  int      `json:"tn"`           // turn number
	Dir int      `json:"dir"`          // 1 or -1
	Dr  bool     `json:"dr,omitempty"` // you drew this turn: play it (last card) or pass
	Dk  int      `json:"dk"`           // cards left in the deck
	L   []string `json:"l"`
	W   int      `json:"w"`
	Pts int      `json:"pts,omitempty"`
	TL  int      `json:"tl,omitempty"`
	A   []bool   `json:"a,omitempty"` // away
}

func (e *cardsEng) view(seat int) interface{} {
	p := cardsPic{Top: e.top(), Col: string(e.color), T: e.turn, TN: e.turnNo, Dir: e.dir, Dk: len(e.deck),
		L: e.log, W: e.winner, A: e.away}
	for _, h := range e.hands {
		p.C = append(p.C, len(h))
	}
	if seat >= 0 && seat < e.n {
		p.H = e.hands[seat]
		p.Dr = seat == e.turn && e.drew != ""
	}
	if e.winner >= 0 {
		p.Pts = e.scores[e.winner]
	}
	if !e.o.solo && e.winner < 0 && !e.turnAt.IsZero() && !e.bot[e.turn] && !e.away[e.turn] {
		p.TL = int((cardsTurnLimit - time.Since(e.turnAt)) / time.Second)
	}
	return p
}

// ---- drawing ----

type cardsView struct {
	p      cardsPic
	cur    int
	pick   bool // choosing a colour for a wild card
	pickAt int
	turnNo int
}

func validCard(c string) bool {
	if len(c) != 2 {
		return false
	}
	if c[0] == 'w' {
		return c[1] == 'w' || c[1] == '4'
	}
	return strings.IndexByte(cardColors, c[0]) >= 0 && strings.IndexByte("0123456789sv+", c[1]) >= 0
}

func (v *cardsView) load(b []byte) error {
	var p cardsPic
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	if !validCard(p.Top) || len(p.Col) != 1 || len(p.C) > 8 || len(p.H) > 120 || len(p.L) > 8 {
		return errors.New("bad picture")
	}
	for _, c := range p.H {
		if !validCard(c) {
			return errors.New("bad picture")
		}
	}
	newTurn := p.TN != v.turnNo || v.p.Top == ""
	if newTurn {
		v.pick = false // a new turn: forget a half-made choice
		v.turnNo = p.TN
	}
	v.p = p
	if newTurn && len(p.H) > 0 && (v.cur >= len(p.H) || !v.playable(p.H[v.cur])) {
		for i, c := range p.H { // start on a card you can play
			if v.playable(c) {
				v.cur = i
				break
			}
		}
	}
	if v.p.Dr && len(p.H) > 0 {
		v.cur = len(p.H) - 1
	}
	if v.cur >= len(p.H) {
		v.cur = imax(0, len(p.H)-1)
	}
	return nil
}

func (v *cardsView) size() (int, int) { return 60, 20 }
func (v *cardsView) over() bool       { return v.p.W >= 0 && v.p.Top != "" }

func (v *cardsView) playable(c string) bool {
	return c[0] == 'w' || c[0] == v.p.Col[0] || cardSym(c) == cardSym(v.p.Top)
}

func (v *cardsView) key(ev *tcell.EventKey, x *gctx) string {
	p := v.p
	if p.W >= 0 || x.me < 0 {
		return ""
	}
	myTurn := p.T == x.me
	pre := strconv.Itoa(p.TN) + ":"
	if v.pick && myTurn {
		var col byte
		switch ev.Key() {
		case tcell.KeyEscape, tcell.KeyBackspace, tcell.KeyBackspace2:
			v.pick = false
		case tcell.KeyRune:
			switch r := ev.Rune(); r {
			case 'r', 'R', '1':
				col = 'r'
			case 'g', 'G', '2':
				col = 'g'
			case 'b', 'B', '3':
				col = 'b'
			case 'y', 'Y', '4':
				col = 'y'
			}
		}
		if col != 0 {
			v.pick = false
			return pre + "p" + strconv.Itoa(v.pickAt) + string(col)
		}
		return ""
	}
	switch ev.Key() {
	case tcell.KeyLeft:
		if v.cur > 0 {
			v.cur--
		}
	case tcell.KeyRight:
		if v.cur < len(p.H)-1 {
			v.cur++
		}
	case tcell.KeyHome:
		v.cur = 0
	case tcell.KeyEnd:
		v.cur = imax(0, len(p.H)-1)
	case tcell.KeyEnter, tcell.KeyUp:
		if myTurn {
			return v.play()
		}
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'a', 'h':
			if v.cur > 0 {
				v.cur--
			}
		case 'l':
			if v.cur < len(p.H)-1 {
				v.cur++
			}
		case ' ':
			if myTurn {
				return v.play()
			}
		case 'd', 'D':
			if myTurn && !p.Dr {
				return pre + "d"
			}
		case 'p', 'P':
			if myTurn && p.Dr {
				return pre + "k"
			}
		}
	}
	return ""
}

func (v *cardsView) play() string {
	p := v.p
	if v.cur < 0 || v.cur >= len(p.H) {
		return ""
	}
	c := p.H[v.cur]
	if !v.playable(c) || (p.Dr && v.cur != len(p.H)-1) {
		return ""
	}
	if c[0] == 'w' {
		v.pick, v.pickAt = true, v.cur
		return ""
	}
	return strconv.Itoa(p.TN) + ":p" + strconv.Itoa(v.cur)
}

func cardStyle(c byte) tcell.Style {
	col := map[byte]tcell.Color{
		'r': tcell.PaletteColor(167), 'g': tcell.PaletteColor(71), 'b': tcell.PaletteColor(68), 'y': tcell.PaletteColor(178),
	}[c]
	if col == 0 {
		return tcell.StyleDefault.Background(tcell.PaletteColor(236)).Foreground(tcell.PaletteColor(255)).Bold(true)
	}
	return tcell.StyleDefault.Background(col).Foreground(tcell.ColorBlack).Bold(true)
}

// drawCard draws a card 5 wide and 3 tall.
func drawCard(cv *canvas, x, y int, c string, dim bool) {
	st := cardStyle(c[0])
	if dim {
		st = st.Dim(true)
	}
	lab := cardLabel(c)
	pad := 5 - textWidth(lab)
	mid := repeat(" ", pad/2) + lab + repeat(" ", pad-pad/2)
	cv.text(x, y, "     ", st)
	cv.text(x, y+1, mid, st)
	cv.text(x, y+2, "     ", st)
}

func (v *cardsView) draw(cv *canvas, x *gctx) {
	p := v.p
	names := func(s string) string { return winnerText(s, x) }
	// the table: everyone, how many cards, whose turn
	y, px := 1, 2
	for i, n := range p.C {
		name := x.name(i)
		st := tcell.StyleDefault.Foreground(seatColor(i))
		mark := "  "
		if i == p.T && p.W < 0 {
			mark, st = "▶ ", st.Bold(true)
		}
		label := fmt.Sprintf("%s%s %d", mark, name, n)
		if n == 1 && p.W < 0 {
			label += " LAST CARD"
		}
		if i < len(p.A) && p.A[i] {
			label += " (away)"
		}
		if px+textWidth(label)+3 > cv.w {
			y, px = y+1, 2
		}
		px = cv.text(px, y, label, st) + 3
	}
	arrow := "→ clockwise"
	if p.Dir < 0 {
		arrow = "← anticlockwise"
	}
	y += 2
	// deck and pile
	cx := imax(2, cv.w/2-12)
	cv.text(cx, y, "     ", cardStyle(0).Background(tcell.PaletteColor(238)))
	cv.text(cx, y+1, " ▒▒▒ ", cardStyle(0).Background(tcell.PaletteColor(238)))
	cv.text(cx, y+2, "     ", cardStyle(0).Background(tcell.PaletteColor(238)))
	cv.text(cx, y+3, fmt.Sprintf("deck %d", p.Dk), gsDim)
	drawCard(cv, cx+8, y, p.Top, false)
	cv.text(cx+15, y, "colour: ", gsDim)
	cv.text(cx+23, y, colorName(p.Col[0]), cardStyle(p.Col[0]).Reverse(false))
	cv.text(cx+15, y+1, arrow, gsDim)
	turn := ""
	switch {
	case p.W >= 0:
	case p.T == x.me:
		turn = "your turn"
	default:
		turn = x.name(p.T) + "'s turn"
	}
	if p.TL > 0 && p.TL <= 15 {
		turn += fmt.Sprintf(" · %ds", p.TL)
	}
	tst := gsDim
	if p.T == x.me {
		tst = gsGood
	}
	cv.text(cx+15, y+2, turn, tst)
	if p.W < 0 {
		need := "to play: " + colorName(p.Col[0])
		if sym := symbolName(p.Top); sym != "" {
			need += " or " + sym
		}
		cv.text(cx+15, y+3, need+", or a wild", gsDim)
	}
	y += 5
	for _, l := range p.L {
		cv.text(2, y, "· "+names(l), gsDim)
		y++
	}
	for i := len(p.L); i < 4; i++ {
		y++
	}
	y++
	if p.W >= 0 {
		msg := x.name(p.W) + " won"
		if p.W == x.me {
			msg = "you won"
		}
		cv.center(y, fmt.Sprintf("  %s! · %d points  ", msg, p.Pts), gsSel.Reverse(true))
		y += 2
	}
	if x.me < 0 {
		cv.text(2, y, "you're watching this round", gsDim)
		return
	}
	// your hand, scrolled to keep the chosen card in view
	myTurn := p.T == x.me && p.W < 0
	cv.text(2, y, fmt.Sprintf("your hand (%d)", len(p.H)), gsHead)
	y++
	per := imax(1, (cv.w-4)/6)
	first := 0
	if v.cur >= per {
		first = v.cur - per + 1
	}
	if first > 0 {
		cv.text(0, y+2, "‹", gsDim)
	}
	for i := first; i < len(p.H) && i < first+per; i++ {
		c := p.H[i]
		ok := v.playable(c) && (!p.Dr || i == len(p.H)-1)
		lift := 1
		if i == v.cur {
			lift = 0
		}
		drawCard(cv, 2+(i-first)*6, y+lift, c, myTurn && !ok)
		if i == v.cur {
			cv.text(2+(i-first)*6+2, y+4, "▲", gsSel)
		}
	}
	if first+per < len(p.H) {
		cv.text(cv.w-1, y+2, "›", gsDim)
	}
	if v.cur >= 0 && v.cur < len(p.H) && !v.pick {
		c := p.H[v.cur]
		info, st := cardInfo(c), gsText
		switch {
		case !myTurn:
		case p.Dr && v.cur != len(p.H)-1:
			info, st = "✗ only the card you drew can go now · "+info, gsWarn
		case v.playable(c):
			info, st = "✓ "+info, gsGood
		default:
			info, st = "✗ doesn't match · "+info, gsWarn
		}
		cv.text(2, y+5, info, st)
	}
	y += 6
	switch {
	case v.pick:
		x0 := cv.text(2, y, "pick a colour: ", gsHead)
		for _, c := range []byte(cardColors) {
			x0 = cv.text(x0, y, " "+strings.ToUpper(string(c))+" "+colorName(c)+" ", cardStyle(c))
			x0++
		}
	case myTurn && p.Dr:
		drawn := p.H[len(p.H)-1]
		msg := "you drew " + cardName(drawn, drawn[0])
		if drawn[0] == 'w' {
			msg = "you drew a " + cardLabel(drawn)
		}
		cv.text(2, y, msg+" · Enter plays it now · P keeps it and passes", gsGood)
	case myTurn:
		has := false
		for _, c := range p.H {
			if v.playable(c) {
				has = true
			}
		}
		if has {
			cv.text(2, y, "Enter plays the chosen card · D draws instead", gsDim)
		} else {
			cv.text(2, y, "nothing to play · D draws a card", gsGood)
		}
	}
}
