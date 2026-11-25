package main

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ---- the rules of each game ----

func testOpts(solo bool) engOpts {
	return engOpts{rnd: rand.New(rand.NewSource(1)), solo: solo, w: 80, h: 24}
}

func TestSnakeEatsGrowsAndCrashes(t *testing.T) {
	e := newSnake([]gp{{ID: "a"}}, testOpts(true)).(*snakeEng)
	now := time.Now()
	e.step(now) // starts the countdown
	now = now.Add(2 * time.Second)
	head := e.sn[0].body[0]
	e.food = []pt{head.add(pt{1, 0})}
	e.step(now)
	if e.sn[0].score != 1 || len(e.sn[0].body) != 4 {
		t.Fatalf("after eating: score %d, length %d", e.sn[0].score, len(e.sn[0].body))
	}
	if len(e.food) != 1 {
		t.Fatalf("food not replaced: %v", e.food)
	}
	e.input(0, "l") // straight back: ignored
	if len(e.sn[0].q) != 0 {
		t.Fatal("a U-turn was accepted")
	}
	for i := 0; i < 100 && !e.done; i++ {
		e.food = nil
		e.step(now)
	}
	if !e.done || !strings.Contains(e.msg, "game over") {
		t.Fatalf("never hit the wall: done=%v msg=%q", e.done, e.msg)
	}
}

// multiSnake is a round with others, past the countdown, with no food about.
func multiSnake(t *testing.T, n int) (*snakeEng, time.Time) {
	seats := []gp{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}[:n]
	e := newSnake(seats, testOpts(false)).(*snakeEng)
	now := time.Now()
	e.step(now)
	now = now.Add(4 * time.Second)
	e.food, e.nfood = nil, 0
	return e, now
}

func line(x, y, n, dx int) []pt {
	var out []pt
	for k := 0; k < n; k++ {
		out = append(out, pt{x - dx*k, y})
	}
	return out
}

func TestSnakeBiteAndEatWhole(t *testing.T) {
	e, now := multiSnake(t, 2)
	// a (heading down) runs into the middle of b's body, lying left to right
	e.sn[0].body, e.sn[0].dir = []pt{{10, 4}, {10, 3}, {10, 2}}, pt{0, 1}
	e.sn[1].body, e.sn[1].dir = line(14, 5, 8, 1), pt{1, 0} // x 14..7, head at 14
	e.step(now)
	a, b := e.sn[0], e.sn[1]
	if !a.alive || !b.alive {
		t.Fatalf("alive a=%v b=%v", a.alive, b.alive)
	}
	// b moved right, so its piece at x=10 is now its 6th: it keeps 5 pieces
	if len(b.body) != 5 || a.grow != 3 || a.score != 3 {
		t.Fatalf("b %d long, a grows %d scores %d", len(b.body), a.grow, a.score)
	}
	if e.feed != "@0 bit @1 (+3)" {
		t.Fatalf("feed %q", e.feed)
	}

	// right behind the head: eaten whole, and the body becomes food
	e, now = multiSnake(t, 2)
	e.sn[0].body, e.sn[0].dir = []pt{{13, 4}, {13, 3}, {13, 2}}, pt{0, 1}
	e.sn[1].body, e.sn[1].dir = line(14, 5, 6, 1), pt{1, 0}
	e.step(now)
	if e.sn[1].alive || e.sn[0].kills != 1 || len(e.food) == 0 || e.sn[1].backAt.IsZero() {
		t.Fatalf("b alive=%v kills=%d food=%d", e.sn[1].alive, e.sn[0].kills, len(e.food))
	}
	// and it comes back, safe for a moment
	now = now.Add(snakeRespawn + 100*time.Millisecond)
	e.step(now)
	if !e.sn[1].alive || !e.safe(1) || len(e.sn[1].body) != 3 {
		t.Fatalf("not back: alive=%v safe=%v", e.sn[1].alive, e.safe(1))
	}
}

func TestSnakeHeadOnLongerWins(t *testing.T) {
	e, now := multiSnake(t, 3)
	e.sn[0].body, e.sn[0].dir = line(5, 5, 6, 1), pt{1, 0}
	e.sn[1].body, e.sn[1].dir = line(7, 5, 3, -1), pt{-1, 0}
	e.sn[2].body, e.sn[2].dir = line(5, 12, 3, 1), pt{1, 0}
	e.step(now)
	if !e.sn[0].alive || e.sn[1].alive || e.sn[0].kills != 1 {
		t.Fatalf("head on: a=%v b=%v kills=%d", e.sn[0].alive, e.sn[1].alive, e.sn[0].kills)
	}
	// equal length: both go
	e, now = multiSnake(t, 2)
	e.sn[0].body, e.sn[0].dir = line(5, 5, 3, 1), pt{1, 0}
	e.sn[1].body, e.sn[1].dir = line(7, 5, 3, -1), pt{-1, 0}
	e.step(now)
	if e.sn[0].alive || e.sn[1].alive {
		t.Fatal("equal snakes head on should both die")
	}
}

func TestSnakeSafeAfterComingBack(t *testing.T) {
	e, now := multiSnake(t, 2)
	e.sn[0].body, e.sn[0].dir = []pt{{10, 4}, {10, 3}, {10, 2}}, pt{0, 1}
	e.sn[1].body, e.sn[1].dir = line(14, 5, 8, 1), pt{1, 0}
	e.sn[1].safeUntil = now.Add(time.Second)
	e.step(now)
	if len(e.sn[1].body) != 8 || !e.sn[0].alive {
		t.Fatalf("a safe snake was bitten: %d long", len(e.sn[1].body))
	}
}

func TestSnakeRoundEndsOnTime(t *testing.T) {
	e, now := multiSnake(t, 2)
	e.step(now)
	e.sn[0].score, e.sn[1].score = 3, 9
	e.sn[0].body, e.sn[0].dir = line(5, 2, 3, 1), pt{0, 1}
	e.sn[1].body, e.sn[1].dir = line(5, 8, 3, 1), pt{0, 1}
	e.step(now.Add(snakeRound + time.Second))
	if !e.done || e.msg != "time! @1 wins with 9 points" {
		t.Fatalf("done=%v msg=%q", e.done, e.msg)
	}
}

func TestSnakePlayerWhoDropsOffSpills(t *testing.T) {
	e, now := multiSnake(t, 2)
	e.now = now
	e.setAway(1, true)
	if e.sn[1].alive || len(e.food) == 0 || e.done {
		t.Fatalf("alive=%v food=%d done=%v", e.sn[1].alive, len(e.food), e.done)
	}
	e.step(now.Add(10 * time.Second))
	if e.sn[1].alive {
		t.Fatal("an away snake came back")
	}
	e.setAway(1, false)
	e.step(now.Add(12 * time.Second))
	if !e.sn[1].alive {
		t.Fatal("didn't come back with its player")
	}
}

func TestBoardGames(t *testing.T) {
	e := newBoard("ttt", []gp{{ID: "a"}, {ID: "b"}}, testOpts(false))
	e.turn = 0
	mv := func(seat, at int) bool { return e.input(seat, "m"+itoa(e.moves)+":"+itoa(at)) }
	if mv(1, 0) {
		t.Fatal("played out of turn")
	}
	for _, m := range []struct{ s, at int }{{0, 0}, {1, 3}, {0, 1}, {1, 4}} {
		if !mv(m.s, m.at) {
			t.Fatalf("move %v refused", m)
		}
	}
	if mv(0, 4) {
		t.Fatal("played on a taken cell")
	}
	if e.input(0, "m1:2") {
		t.Fatal("a move for an old position was accepted")
	}
	mv(0, 2)
	if e.win != 0 || len(e.line) != 3 {
		t.Fatalf("win=%d line=%v", e.win, e.line)
	}

	f := newBoard("four", []gp{{ID: "a"}, {ID: "b", Bot: true}}, testOpts(true))
	// the bot (seat 1) has three in column 6: it must take the win
	for _, c := range []int{0, 6, 1, 6, 0, 6} {
		f.play(c)
	}
	f.play(3) // seat 0, elsewhere
	if f.turn != 1 {
		t.Fatalf("turn %d", f.turn)
	}
	f.play(f.botMove())
	if f.win != 1 {
		t.Fatalf("the bot missed a winning move: win=%d board=%v", f.win, f.b)
	}
	// and it blocks yours
	g := newBoard("four", []gp{{ID: "a"}, {ID: "b", Bot: true}}, testOpts(true))
	for _, c := range []int{2, 5, 3, 6, 4} { // yours: 2 3 4, with 5 already taken
		g.play(c)
	}
	if m := g.botMove(); m != 1 {
		t.Fatalf("the bot didn't block three in a row: played %d", m)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestBoardOpponentLeavingLoses(t *testing.T) {
	e := newBoard("ttt", []gp{{ID: "a"}, {ID: "b"}}, testOpts(false))
	e.turn = 1
	now := time.Now()
	e.step(now)
	e.setAway(1, true)
	e.away[1] = now.Add(-boardAwayLimit - time.Second)
	e.step(now)
	if e.win != 0 {
		t.Fatalf("win=%d msg=%q", e.win, e.msg)
	}
}

func cardsWith(n int, hands [][]string, top string) *cardsEng {
	seats := make([]gp, n)
	e := newCards(seats, testOpts(false)).(*cardsEng)
	e.hands = hands
	e.pile = []string{top}
	e.color = top[0]
	e.turn, e.dir, e.turnNo, e.drew = 0, 1, 0, ""
	return e
}

func TestCardsRules(t *testing.T) {
	e := cardsWith(3, [][]string{{"r5", "b7", "ww", "g+"}, {"y1", "y2"}, {"g3", "g4"}}, "r9")
	if e.input(0, "0:p1") {
		t.Fatal("blue 7 on red 9 was accepted")
	}
	if e.input(0, "0:p2") {
		t.Fatal("a wild with no colour was accepted")
	}
	if e.input(1, "0:p0") {
		t.Fatal("played out of turn")
	}
	if !e.input(0, "0:p0") || e.turn != 1 || e.top() != "r5" {
		t.Fatalf("red 5: turn %d top %s", e.turn, e.top())
	}
	if e.input(1, "0:d") {
		t.Fatal("an old turn number was accepted")
	}
	// draw a card that can't be played: the turn passes by itself
	e.deck = append(e.deck, "b1")
	if !e.input(1, "1:d") || e.turn != 2 || len(e.hands[1]) != 3 {
		t.Fatalf("draw: turn %d hand %v", e.turn, e.hands[1])
	}
	// draw one that can: play it or pass
	e.deck = append(e.deck, "r2")
	if !e.input(2, "2:d") || e.turn != 2 || e.drew != "r2" {
		t.Fatalf("drawn playable: turn %d drew %q", e.turn, e.drew)
	}
	if e.input(2, "2:p0") {
		t.Fatal("after drawing, a card other than the drawn one was played")
	}
	if !e.input(2, "2:k") || e.turn != 0 {
		t.Fatalf("pass: turn %d", e.turn)
	}
	// and a drawn card can be played at once, same turn
	d := cardsWith(2, [][]string{{"b1", "g2"}, {"g5"}}, "r9")
	d.deck = append(d.deck, "r4")
	if !d.input(0, "0:d") || d.turn != 0 || d.drew != "r4" {
		t.Fatalf("draw: turn %d drew %q", d.turn, d.drew)
	}
	if !d.input(0, "0:p2") || d.top() != "r4" || d.turn != 1 || len(d.hands[0]) != 2 {
		t.Fatalf("playing the drawn card: top %s turn %d hand %v", d.top(), d.turn, d.hands[0])
	}
	// +2: the next player draws two and is skipped
	e.color = 'g'
	before := len(e.hands[1])
	n := e.turnNo
	idx := -1
	for i, c := range e.hands[0] {
		if c == "g+" {
			idx = i
		}
	}
	if !e.input(0, itoa(n)+":p"+itoa(idx)) {
		t.Fatalf("+2 refused (hand %v)", e.hands[0])
	}
	if len(e.hands[1]) != before+2 || e.turn != 2 {
		t.Fatalf("+2: hand %d → %d, turn %d", before, len(e.hands[1]), e.turn)
	}

	// with two players, reverse is a skip
	r := cardsWith(2, [][]string{{"bv", "b1"}, {"g1"}}, "b3")
	r.input(0, "0:p0")
	if r.turn != 0 {
		t.Fatalf("reverse with two: turn %d", r.turn)
	}
	// a plain 4 is a number card, not a +4, and doesn't match a wild +4 by symbol
	f := cardsWith(2, [][]string{{"r4", "b2"}, {"g1"}}, "r9")
	f.input(0, "0:p0")
	if f.turn != 1 || len(f.hands[1]) != 1 || cardLabel("r4") != "4" || cardLabel("w4") != "W+4" {
		t.Fatalf("red 4: turn %d, next hand %v", f.turn, f.hands[1])
	}
	f.pile, f.color = append(f.pile, "w4"), 'g'
	if f.playable("b4") || !f.playable("g4") {
		t.Fatal("a 4 matched a wild +4 by its symbol")
	}
	// emptying your hand wins
	w := cardsWith(2, [][]string{{"ww"}, {"g1", "w4"}}, "b3")
	if !w.input(0, "0:p0y") || w.winner != 0 || w.scores[0] != 51 || w.color != 'y' {
		t.Fatalf("winner %d score %d colour %c", w.winner, w.scores[0], w.color)
	}
}

func TestCardsBotsPlayAWholeGame(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		o := testOpts(false)
		o.rnd = rand.New(rand.NewSource(seed))
		e := newCards(botSeats(5), o).(*cardsEng)
		now := time.Now()
		for i := 0; i < 20000 && !e.over(); i++ {
			now = now.Add(time.Second)
			e.step(now)
			total := len(e.deck) + len(e.pile)
			for _, h := range e.hands {
				total += len(h)
			}
			if total != 108 {
				t.Fatalf("seed %d: %d cards in the game", seed, total)
			}
		}
		if !e.over() {
			t.Fatalf("seed %d: bots never finished", seed)
		}
	}
}

func TestMazeIsWholeAndGhostsComeOut(t *testing.T) {
	e := newMaze([]gp{{ID: "a"}}, testOpts(true)).(*mazeEng)
	// every dot can be reached from the start
	seen := map[pt]bool{mazeStart: true}
	q := []pt{mazeStart}
	for len(q) > 0 {
		p := q[0]
		q = q[1:]
		for _, d := range dirs {
			n := e.wrap(p.add(d))
			if !seen[n] && e.open(n) {
				seen[n] = true
				q = append(q, n)
			}
		}
	}
	for y, row := range mazeMap {
		for x, c := range row {
			if (c == '.' || c == 'o') && !seen[pt{x, y}] {
				t.Fatalf("dot at %d,%d can't be reached", x, y)
			}
		}
	}
	now := time.Now()
	e.lives = 1000
	for i := 0; i < 400; i++ {
		now = now.Add(50 * time.Millisecond)
		e.step(now)
	}
	out := 0
	for _, g := range e.gh {
		if g.state == ghRoam || g.state == ghEyes {
			out++
		}
	}
	if out < 3 {
		t.Fatalf("only %d ghosts left the house after 20s", out)
	}
}

// Every game's picture survives the trip and draws on small and large
// screens without trouble.
func TestGameViewsDraw(t *testing.T) {
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	defer sim.Fini()
	u := &tui{s: sim}
	for _, d := range gameDefs {
		seats := []gp{{ID: "me", Nick: "me"}, {ID: "b", Nick: "bea"}, {ID: "c", Nick: "cy", Bot: true}}
		if d.max < len(seats) {
			seats = seats[:d.max]
		}
		e := d.newEng(seats, testOpts(d.max == 1))
		now := time.Now()
		for i := 0; i < 60; i++ {
			now = now.Add(200 * time.Millisecond)
			e.step(now)
		}
		for seat := -1; seat < len(seats); seat++ {
			b, err := json.Marshal(e.view(seat))
			if err != nil {
				t.Fatal(err)
			}
			if len(b) > maxGamePkt {
				t.Fatalf("%s: picture is %d bytes", d.id, len(b))
			}
			v := d.newView()
			if err := v.load(b); err != nil {
				t.Fatalf("%s: %v (%s)", d.id, err, b)
			}
			for _, sz := range [][2]int{{80, 24}, {120, 40}, {30, 10}} {
				sim.SetSize(sz[0], sz[1])
				v.draw(&canvas{u: u, w: sz[0], h: sz[1]}, &gctx{me: seat, seats: seats})
			}
		}
		if err := d.newView().load([]byte(`{"w":-5}`)); err == nil {
			t.Fatalf("%s: accepted a broken picture", d.id)
		}
	}
}

// ---- rooms, over a real hub ----

type gnode struct {
	c      *Client
	s      *gsess
	in     chan [4]string
	lobby  []gmsg
	frozen bool
}

func newGNode(t *testing.T, h *Hub, nick string) *gnode {
	n := &gnode{c: testClient(t, h, nick, ""), in: make(chan [4]string, 4096)}
	n.c.SetOnGame(func(code, from, nick, text string) {
		select {
		case n.in <- [4]string{code, from, nick, text}:
		default:
		}
	})
	return n
}

func (n *gnode) tick(now time.Time) {
	if n.frozen {
		return
	}
	for {
		select {
		case p := <-n.in:
			var m gmsg
			if json.Unmarshal([]byte(p[3]), &m) != nil {
				continue
			}
			if p[0] == lobbyCode {
				n.lobby = append(n.lobby, m)
				continue
			}
			if n.s != nil && n.s.code == p[0] {
				n.s.recv(p[1], p[2], &m, now)
			}
		default:
			if n.s != nil {
				n.s.pump(now)
			}
			return
		}
	}
}

func gameRun(t *testing.T, what string, nodes []*gnode, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		now := time.Now()
		for _, n := range nodes {
			n.tick(now)
		}
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func quickGameTimers(t *testing.T) {
	old := []time.Duration{pingEvery, awayAfter, dropAfter, hostWaitNote, hostGone, joinTimeout}
	pingEvery, awayAfter, dropAfter = 100*time.Millisecond, 700*time.Millisecond, 1200*time.Millisecond
	hostWaitNote, hostGone, joinTimeout = 400*time.Millisecond, 1500*time.Millisecond, 900*time.Millisecond
	t.Cleanup(func() {
		pingEvery, awayAfter, dropAfter, hostWaitNote, hostGone, joinTimeout = old[0], old[1], old[2], old[3], old[4], old[5]
	})
}

func TestGameRoomLifecycle(t *testing.T) {
	quickGameTimers(t)
	h := testHub(t, "")
	a, b, c := newGNode(t, h, "ana"), newGNode(t, h, "ben"), newGNode(t, h, "cat")
	online(t, a.c, b.c, c.c)
	all := []*gnode{a, b, c}
	now := time.Now()
	a.s = hostRoom(a.c, gameByID("cards"), false, nil, now)
	if !validCode(a.s.code) || a.s.code == lobbyCode {
		t.Fatalf("code %q", a.s.code)
	}

	// joining by code
	b.s = joinRoom(b.c, a.s.code, now)
	gameRun(t, "ben is seated", all, func() bool { return b.s.joined && b.s.mySeat() == 1 && len(a.s.seats) == 2 })
	if b.s.def == nil || b.s.def.id != "cards" || b.s.hostNick != "ana" {
		t.Fatalf("ben sees def=%v host=%q", b.s.def, b.s.hostNick)
	}

	// a bot, then start: ben gets his own hand
	a.s.seats = append(a.s.seats, botSeats(1)...)
	a.s.begin(time.Now())
	hand := func(n *gnode) []string { return n.s.view.(*cardsView).p.H }
	gameRun(t, "ben sees his cards", all, func() bool { return b.s.haveView && len(hand(b)) == 7 })
	eng := a.s.eng.(*cardsEng)
	if strings.Join(hand(b), " ") != strings.Join(eng.hands[1], " ") {
		t.Fatalf("ben's hand %v, the host has %v", hand(b), eng.hands[1])
	}
	if strings.Join(hand(a), " ") == strings.Join(hand(b), " ") {
		t.Fatal("ben sees ana's hand")
	}

	// ben's key presses reach the game
	eng.turn, eng.turnAt = 1, time.Now()
	a.s.publish(time.Now())
	gameRun(t, "ben's turn shows", all, func() bool { return b.s.view.(*cardsView).p.T == 1 })
	tn := b.s.view.(*cardsView).p.TN
	b.s.input(itoa(tn)+":d", time.Now())
	gameRun(t, "ben drew", all, func() bool { return len(eng.hands[1]) == 8 })

	// someone arriving mid-game watches, with no hand
	c.s = joinRoom(c.c, a.s.code, time.Now())
	gameRun(t, "cat watches", all, func() bool {
		return c.s.joined && c.s.mySeat() < 0 && c.s.haveView && len(a.s.specs) == 1
	})
	if len(hand(c)) != 0 {
		t.Fatal("a watcher was shown a hand")
	}

	// ben drops off: a bot plays for him; he comes back to his seat
	b.frozen = true
	gameRun(t, "ben marked away", all, func() bool { return a.s.seats[1].Away && eng.away[1] })
	b.frozen = false
	gameRun(t, "ben is back", all, func() bool { return !a.s.seats[1].Away && !eng.away[1] })

	// play again: the watcher gets a seat
	a.s.backToLobby(time.Now())
	gameRun(t, "cat is seated", all, func() bool { return c.s.mySeat() >= 0 && c.s.phase == phLobby })

	// ben leaves the lobby, then the host closes the room
	b.s.leave()
	b.s = nil
	gameRun(t, "ben gone", all, func() bool { return a.s.find(a.s.seats, b.c.MeID()) < 0 })
	a.s.leave()
	gameRun(t, "cat told", all, func() bool { return c.s.ended != "" })
	if !strings.Contains(c.s.ended, "ana closed the room") {
		t.Fatalf("cat was told %q", c.s.ended)
	}
}

func TestGameRoomWrongCodeAndLostHost(t *testing.T) {
	quickGameTimers(t)
	h := testHub(t, "")
	a, b := newGNode(t, h, "ana"), newGNode(t, h, "ben")
	online(t, a.c, b.c)
	all := []*gnode{a, b}
	b.s = joinRoom(b.c, "123456", time.Now())
	gameRun(t, "not found", all, func() bool { return b.s.ended != "" })
	if !strings.Contains(b.s.ended, "no game with the code 123456") {
		t.Fatalf("got %q", b.s.ended)
	}

	// the host vanishes without a word (its laptop sleeps): the others are told
	a.s = hostRoom(a.c, gameByID("ttt"), false, nil, time.Now())
	b.s = joinRoom(b.c, a.s.code, time.Now())
	gameRun(t, "joined", all, func() bool { return b.s.joined })
	a.s.begin(time.Now())
	gameRun(t, "playing", all, func() bool { return b.s.haveView })
	a.frozen = true
	gameRun(t, "waiting note", all, func() bool { return strings.Contains(b.s.note, "waiting for ana") })
	gameRun(t, "room over", all, func() bool { return b.s.ended != "" })
}

func TestGameRoomFullLetsYouWatch(t *testing.T) {
	quickGameTimers(t)
	h := testHub(t, "")
	a, b, c := newGNode(t, h, "ana"), newGNode(t, h, "ben"), newGNode(t, h, "cat")
	online(t, a.c, b.c, c.c)
	all := []*gnode{a, b, c}
	a.s = hostRoom(a.c, gameByID("four"), false, nil, time.Now())
	b.s = joinRoom(b.c, a.s.code, time.Now())
	gameRun(t, "ben seated", all, func() bool { return b.s.mySeat() == 1 })
	c.s = joinRoom(c.c, a.s.code, time.Now())
	gameRun(t, "cat watching", all, func() bool { return c.s.joined && len(a.s.specs) == 1 })
	if c.s.mySeat() >= 0 || len(a.s.seats) != 2 {
		t.Fatalf("seats %v specs %v", a.s.seats, a.s.specs)
	}
}

func TestSnakeRoomOverWifi(t *testing.T) {
	quickGameTimers(t)
	h := testHub(t, "")
	a, b := newGNode(t, h, "ana"), newGNode(t, h, "ben")
	online(t, a.c, b.c)
	all := []*gnode{a, b}
	a.s = hostRoom(a.c, gameByID("snake"), true, nil, time.Now())
	b.s = joinRoom(b.c, a.s.code, time.Now())
	gameRun(t, "joined", all, func() bool { return b.s.mySeat() == 1 })
	a.s.begin(time.Now())
	eng := a.s.eng.(*snakeEng)
	gameRun(t, "running", all, func() bool { return eng.cd == 0 && b.s.haveView && b.s.lastSeq > 30 })
	b.s.input("u", time.Now())
	gameRun(t, "ben turned", all, func() bool { return eng.sn[1].dir == (pt{0, -1}) || !eng.sn[1].alive })
	seq := b.s.lastSeq
	gameRun(t, "pictures keep coming", all, func() bool { return b.s.lastSeq > seq+5 || eng.done })
}

func TestOpenRoomIsListed(t *testing.T) {
	quickGameTimers(t)
	h := testHub(t, "")
	a, b := newGNode(t, h, "ana"), newGNode(t, h, "ben")
	online(t, a.c, b.c)
	all := []*gnode{a, b}
	b.c.GameSub(lobbyCode, true)
	a.c.GameSub(lobbyCode, true)
	a.s = hostRoom(a.c, gameByID("cards"), true, nil, time.Now())
	gameRun(t, "ad seen", all, func() bool {
		for _, m := range b.lobby {
			if m.K == "ad" && m.C == a.s.code && m.HN == "ana" && m.G == "cards" {
				return true
			}
		}
		return false
	})
	code := a.s.code
	a.s.leave()
	gameRun(t, "gone seen", all, func() bool {
		for _, m := range b.lobby {
			if m.K == "gone" && m.C == code {
				return true
			}
		}
		return false
	})
}

// fakeNet is a gameNet with nobody else on it.
type fakeNet struct{ bt bool }

func (f *fakeNet) GameSub(string, bool)                 {}
func (f *fakeNet) GameSend(string, string, interface{}) {}
func (f *fakeNet) Me() (string, string)                 { return "me", "me" }
func (f *fakeNet) Bluetooth() bool                      { return f.bt }

func TestFastGameNeedsWifiToJoin(t *testing.T) {
	s := joinRoom(&fakeNet{bt: true}, "222222", time.Now())
	s.recv("host", "ana", &gmsg{K: "lobby", G: "snake", P: []gp{{ID: "host", Nick: "ana"}}}, time.Now())
	if !strings.Contains(s.ended, "needs Wi-Fi mode") {
		t.Fatalf("got %q", s.ended)
	}
	s = joinRoom(&fakeNet{bt: true}, "222223", time.Now())
	s.recv("host", "ana", &gmsg{K: "lobby", G: "cards", P: []gp{{ID: "host", Nick: "ana"}}}, time.Now())
	if s.ended != "" || !s.joined {
		t.Fatalf("cards over Bluetooth: ended %q", s.ended)
	}
}

func TestHubGameRelayRules(t *testing.T) {
	h := testHub(t, "")
	a, b := testClient(t, h, "ana", ""), testClient(t, h, "ben", "")
	online(t, a, b)
	got := make(chan string, 10)
	b.SetOnGame(func(code, from, nick, text string) { got <- code + " " + text })
	// not in the room: nothing gets through
	a.GameSend("111111", "", gmsg{K: "x"})
	b.GameSub("111111", true)
	time.Sleep(150 * time.Millisecond)
	a.GameSend("111111", "", gmsg{K: "y"})
	select {
	case g := <-got:
		t.Fatalf("a packet from outside the room arrived: %s", g)
	case <-time.After(300 * time.Millisecond):
	}
	a.GameSub("111111", true)
	time.Sleep(150 * time.Millisecond)
	a.GameSend("111111", "", gmsg{K: "z"})
	select {
	case g := <-got:
		if g != `111111 {"k":"z"}` {
			t.Fatalf("got %s", g)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the packet never arrived")
	}
	if r := h.do(Req{SID: "nope", Op: "gsub", Arg: "1"}); r.OK {
		t.Fatal("bad session accepted")
	}
}

// A turn game across three laptops over (simulated) Bluetooth: the middle
// one passes the packets on.
func TestCardsRoomOverBluetooth(t *testing.T) {
	quickGameTimers(t)
	awayAfter, hostWaitNote, hostGone = 3*time.Second, 2*time.Second, 8*time.Second
	n := &simNet{mtu: 64}
	sa := newSimNode(t, n, "ali", testMeshCfg)
	sb := newSimNode(t, n, "bina", testMeshCfg)
	sc := newSimNode(t, n, "chen", testMeshCfg)
	n.connect(sa.rad, sb.rad)
	n.connect(sb.rad, sc.rad) // ali and chen only meet through bina
	waitFor(t, "everyone sees everyone", func() bool { return sa.hub.Online() == 3 && sc.hub.Online() == 3 })
	wrap := func(c *Client) *gnode {
		g := &gnode{c: c, in: make(chan [4]string, 4096)}
		c.SetOnGame(func(code, from, nick, text string) {
			select {
			case g.in <- [4]string{code, from, nick, text}:
			default:
			}
		})
		return g
	}
	a, c := wrap(sa.c), wrap(sc.c)
	all := []*gnode{a, c}
	a.s = hostRoom(a.c, gameByID("cards"), false, nil, time.Now())
	c.s = joinRoom(c.c, a.s.code, time.Now())
	gameRun(t, "chen seated over two hops", all, func() bool { return c.s.mySeat() == 1 })
	a.s.begin(time.Now())
	gameRun(t, "chen sees his hand", all, func() bool { return c.s.haveView && len(c.s.view.(*cardsView).p.H) == 7 })
	eng := a.s.eng.(*cardsEng)
	eng.turn, eng.turnAt = 1, time.Now()
	a.s.publish(time.Now())
	gameRun(t, "chen's turn", all, func() bool { return c.s.view.(*cardsView).p.T == 1 })
	c.s.input(itoa(c.s.view.(*cardsView).p.TN)+":d", time.Now())
	gameRun(t, "chen drew", all, func() bool { return len(eng.hands[1]) == 8 || eng.turn != 1 })
}

// The whole thing as people use it: two chat screens, one opens a room
// for everyone, the other sees it announced and joins from the games menu.
func TestGamesScreensEndToEnd(t *testing.T) {
	h := testHub(t, "")
	start := func(nick string) (tcell.SimulationScreen, *Client, chan error) {
		s := tcell.NewSimulationScreen("UTF-8")
		s.SetSize(100, 32)
		c := NewClient(ClientOpts{Mode: modeJoin, URL: localURL(h.Port()), Nick: nick, Token: deviceToken("e2e-" + nick)})
		done := make(chan error, 1)
		go func() { done <- runTUI(c, uiOpts{Skin: "plain"}, s) }()
		waitFor(t, nick+" connected", func() bool { return c.State() == stOnline })
		return s, c, done
	}
	sa, _, doneA := start("ana")
	sb, cb, doneB := start("ben")
	typeIn := func(s tcell.SimulationScreen, text string) {
		for _, r := range text {
			s.InjectKey(tcell.KeyRune, r, tcell.ModNone)
		}
	}
	key := func(s tcell.SimulationScreen, k tcell.Key) { s.InjectKey(k, 0, tcell.ModNone) }
	shows := func(s tcell.SimulationScreen, what string) func() bool {
		return func() bool { return strings.Contains(screenText(s), what) }
	}
	dump := func(name string, s tcell.SimulationScreen) {
		if dir := os.Getenv("NOOK_SCREENS"); dir != "" {
			os.WriteFile(filepath.Join(dir, name+".txt"), []byte(screenText(s)), 0o644)
		}
	}

	typeIn(sa, "/play")
	key(sa, tcell.KeyEnter)
	waitFor(t, "menu", shows(sa, "open rooms"))
	dump("1-menu", sa)
	for !strings.Contains(screenText(sa), "› Last Card") {
		key(sa, tcell.KeyDown)
		time.Sleep(20 * time.Millisecond)
	}
	typeIn(sa, "o")
	waitFor(t, "lobby", shows(sa, "room code"))
	dump("2-lobby-host", sa)
	waitFor(t, "announced in #general", shows(sb, "opened a Last Card room for everyone"))
	dump("3-announced", sb)

	typeIn(sb, "/play")
	key(sb, tcell.KeyEnter)
	waitFor(t, "ben sees the open room", shows(sb, "waiting for players"))
	dump("4-menu-with-room", sb)
	for !strings.Contains(screenText(sb), "› Last Card       ") || !strings.Contains(screenText(sb), "ana · 1/6") {
		key(sb, tcell.KeyUp)
		time.Sleep(30 * time.Millisecond)
		if strings.Contains(screenText(sb), "›") && strings.Contains(screenText(sb), "ana · 1/6") {
			lines := strings.Split(screenText(sb), "\n")
			found := false
			for _, l := range lines {
				if strings.Contains(l, "›") && strings.Contains(l, "ana ·") {
					found = true
				}
			}
			if found {
				break
			}
		}
	}
	key(sb, tcell.KeyEnter)
	waitFor(t, "ben in the lobby", shows(sb, "waiting for ana to start"))
	waitFor(t, "ana sees ben", shows(sa, "ben"))
	typeIn(sa, "b")
	key(sa, tcell.KeyEnter)
	waitFor(t, "ana's hand", shows(sa, "your hand (7)"))
	waitFor(t, "ben's hand", shows(sb, "your hand (7)"))
	dump("5-cards-ana", sa)
	dump("6-cards-ben", sb)

	// ben leaves (Q twice), ana sees him go away
	typeIn(sb, "q")
	waitFor(t, "confirm", shows(sb, "press Q again"))
	typeIn(sb, "q")
	waitFor(t, "ben back at the menu", shows(sb, "open rooms"))
	waitFor(t, "a bot plays for ben", shows(sa, "(away)"))
	dump("7-ben-left", sa)

	// solo games draw
	typeIn(sb, "q")
	waitFor(t, "ben back in chat", func() bool { return !strings.Contains(screenText(sb), "open rooms") })
	typeIn(sb, "/play snake")
	key(sb, tcell.KeyEnter)
	waitFor(t, "snake", shows(sb, "Snake"))
	time.Sleep(1800 * time.Millisecond)
	dump("8-snake", sb)
	typeIn(sb, "qq") // the game, then the menu
	typeIn(sb, "/play maze")
	key(sb, tcell.KeyEnter)
	waitFor(t, "maze", shows(sb, "score 0"))
	time.Sleep(2500 * time.Millisecond)
	dump("9-maze", sb)
	typeIn(sb, "qq")
	typeIn(sb, "/play four")
	key(sb, tcell.KeyEnter)
	waitFor(t, "four", shows(sb, "your move"))
	typeIn(sb, "4")
	waitFor(t, "bot answers", func() bool {
		txt := screenText(sb)
		return strings.Count(txt, "●") >= 5 && strings.Contains(txt, "your move")
	})
	dump("10-four", sb)
	_ = cb

	key(sa, tcell.KeyCtrlC)
	key(sb, tcell.KeyCtrlC)
	<-doneA
	<-doneB
}
