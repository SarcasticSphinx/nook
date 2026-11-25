package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Maze Muncher: eat every dot in the maze while four ghosts hunt you. A big
// dot turns the tables for a few seconds. For one player.

func init() {
	gameDefs = append(gameDefs, &gameDef{
		id: "maze", name: "Maze Muncher", blurb: "eat every dot, dodge the ghosts",
		keys: "arrows or WASD move · big dots let you eat the ghosts",
		min:  1, max: 1, solo: true, soloSeats: 1,
		newEng:  newMaze,
		newView: func() gameView { return &mazeView{} },
	})
}

// # wall, = ghost house wall, - ghost door, . dot, o big dot, space: empty
var mazeMap = []string{
	"#####################",
	"#o...##.......##...o#",
	"#.##.##.##.##.##.##.#",
	"#...................#",
	"#.#.###.#.#.#.###.#.#",
	"#.#...#.#...#.#...#.#",
	"#.###.#.#####.#.###.#",
	"#.....#.......#.....#",
	"###.#.##=---=##.#.###",
	"###.#.#       #.#.###",
	"....#.#       #.#....",
	"###.#.#########.#.###",
	"#...................#",
	"#.###.###.#.###.###.#",
	"#o..#...#...#...#..o#",
	"###.#.#.#.#.#.#.#.###",
	"#.....#.......#.....#",
	"#.#######.#.#######.#",
	"#...................#",
	"#####################",
}

var (
	mazeStart   = pt{10, 14}
	mazeDoorOut = pt{10, 7} // just outside the ghost door
	mazeHome    = pt{10, 9} // inside the ghost house
	ghostHomes  = []pt{{10, 7}, {9, 9}, {10, 10}, {11, 9}}
	ghostCorner = []pt{{19, 0}, {1, 0}, {19, 19}, {1, 19}}
	ghostWait   = []time.Duration{0, 2 * time.Second, 5 * time.Second, 8 * time.Second}
)

const (
	ghHome = iota
	ghOut  // leaving the house
	ghRoam
	ghEyes // eaten: going home
)

type ghost struct {
	at, dir pt
	state   int
	acc     float64
	freeAt  time.Time
	scared  bool
}

type mazeEng struct {
	o        engOpts
	grid     [][]byte
	w, h     int
	dots     int
	me, dir  pt
	want     pt
	acc      float64
	gh       []*ghost
	score    int
	lives    int
	level    int
	scaredTo time.Time
	combo    int
	pauseTo  time.Time
	msg      string
	done     bool
	t0       time.Time
	now      time.Time
}

func newMaze(seats []gp, o engOpts) engine {
	e := &mazeEng{o: o, lives: 3, level: 1}
	e.newLevel()
	return e
}

func (e *mazeEng) newLevel() {
	e.grid = make([][]byte, len(mazeMap))
	e.dots = 0
	for y, row := range mazeMap {
		e.grid[y] = []byte(row)
		e.dots += strings.Count(row, ".") + strings.Count(row, "o")
	}
	e.h, e.w = len(e.grid), len(e.grid[0])
	e.reset()
	e.msg = fmt.Sprintf("level %d · ready!", e.level)
}

// reset puts everyone back in place (a new level, or after being caught).
func (e *mazeEng) reset() {
	e.me, e.dir, e.want, e.acc = mazeStart, pt{-1, 0}, pt{-1, 0}, 0
	e.gh = nil
	for i, p := range ghostHomes {
		g := &ghost{at: p, dir: pt{0, -1}, state: ghHome}
		if i == 0 {
			g.state, g.dir = ghRoam, pt{-1, 0}
		}
		e.gh = append(e.gh, g)
	}
	e.scaredTo = time.Time{}
	e.pauseTo = time.Time{} // set on the next step
	e.t0 = time.Time{}
}

func (e *mazeEng) every() time.Duration { return 50 * time.Millisecond }
func (e *mazeEng) shared() bool         { return true }
func (e *mazeEng) over() bool           { return e.done }
func (e *mazeEng) setAway(int, bool)    {}

func (e *mazeEng) input(seat int, d string) bool {
	if dir, ok := dirs[d]; ok && seat == 0 {
		e.want = dir
	}
	return false
}

func (e *mazeEng) cell(p pt) byte {
	if p.Y < 0 || p.Y >= e.h {
		return '#'
	}
	return e.grid[p.Y][((p.X%e.w)+e.w)%e.w]
}

func (e *mazeEng) wrap(p pt) pt { p.X = ((p.X % e.w) + e.w) % e.w; return p }

func (e *mazeEng) open(p pt) bool {
	c := e.cell(p)
	return c != '#' && c != '=' && c != '-' && !e.inHouse(p)
}

func (e *mazeEng) inHouse(p pt) bool { return p.Y >= 9 && p.Y <= 10 && p.X >= 7 && p.X <= 13 }

func (e *mazeEng) ghostOpen(g *ghost, p pt) bool {
	c := e.cell(p)
	if c == '#' || c == '=' {
		return false
	}
	if c == '-' || e.inHouse(p) {
		return g.state == ghOut || g.state == ghEyes || g.state == ghHome
	}
	return true
}

func dist2(a, b pt) int { return (a.X-b.X)*(a.X-b.X) + (a.Y-b.Y)*(a.Y-b.Y) }

func (e *mazeEng) step(now time.Time) bool {
	e.now = now
	if e.done {
		return false
	}
	if e.t0.IsZero() {
		e.t0 = now
		e.pauseTo = now.Add(2 * time.Second)
		for i, g := range e.gh {
			g.freeAt = now.Add(2*time.Second + ghostWait[i])
		}
		return true
	}
	if now.Before(e.pauseTo) {
		return false
	}
	if e.msg != "" {
		e.msg = ""
	}
	// you
	mePrev := e.me
	speed := 0.42
	if e.acc += speed; e.acc >= 1 {
		e.acc--
		if e.open(e.wrap(e.me.add(e.want))) {
			e.dir = e.want
		}
		if next := e.wrap(e.me.add(e.dir)); e.open(next) {
			e.me = next
			switch e.grid[next.Y][next.X] {
			case '.':
				e.grid[next.Y][next.X] = ' '
				e.score += 10
				e.dots--
			case 'o':
				e.grid[next.Y][next.X] = ' '
				e.score += 50
				e.dots--
				secs := imax(2, 8-e.level)
				e.scaredTo = now.Add(time.Duration(secs) * time.Second)
				e.combo = 0
				for _, g := range e.gh {
					if g.state == ghRoam {
						g.scared = true
						g.dir = pt{-g.dir.X, -g.dir.Y}
					}
				}
			}
		}
		if e.hit() {
			return true
		}
	}
	if e.dots == 0 {
		e.level++
		e.score += 500
		e.newLevel()
		return true
	}
	scared := now.Before(e.scaredTo)
	if !scared {
		for _, g := range e.gh {
			g.scared = false
		}
	}
	// the ghosts
	elapsed := now.Sub(e.t0)
	chase := elapsed%(27*time.Second) >= 7*time.Second // 7s to their corners, then 20s after you
	for i, g := range e.gh {
		if g.state == ghHome {
			if now.After(g.freeAt) {
				g.state = ghOut
			} else {
				continue
			}
		}
		sp := 0.36 + 0.02*float64(e.level-1)
		if sp > 0.44 {
			sp = 0.44
		}
		switch {
		case g.state == ghEyes:
			sp = 0.9
		case g.scared:
			sp = 0.22
		case g.at.Y == 10 && (g.at.X < 4 || g.at.X > 16):
			sp = 0.2 // the tunnel is slow
		}
		if g.acc += sp; g.acc < 1 {
			continue
		}
		g.acc--
		var target pt
		switch g.state {
		case ghOut:
			target = mazeDoorOut
		case ghEyes:
			target = mazeHome
		default:
			target = e.target(i, chase)
		}
		g.dir = e.choose(g, target)
		prev := g.at
		g.at = e.wrap(g.at.add(g.dir))
		if prev == e.me && g.at == mePrev {
			g.at = e.me // you passed through each other: that counts as meeting
		}
		switch {
		case g.state == ghOut && !e.inHouse(g.at) && e.cell(g.at) != '-':
			g.state = ghRoam
		case g.state == ghEyes && e.inHouse(g.at):
			g.state, g.scared, g.freeAt = ghHome, false, now.Add(time.Second)
		}
	}
	e.hit()
	return true
}

func (e *mazeEng) target(i int, chase bool) pt {
	if !chase {
		return ghostCorner[i]
	}
	switch i {
	case 1: // a few steps ahead of you
		return e.me.add(pt{e.dir.X * 4, e.dir.Y * 4})
	case 2: // somewhere between you and the first ghost, mirrored
		a := e.me.add(pt{e.dir.X * 2, e.dir.Y * 2})
		r := e.gh[0].at
		return pt{2*a.X - r.X, 2*a.Y - r.Y}
	case 3: // shy: after you from far away, its corner when close
		if dist2(e.gh[3].at, e.me) < 64 {
			return ghostCorner[3]
		}
	}
	return e.me
}

// choose picks a ghost's next step: never straight back unless stuck; the
// way closest to its target, or any way at all when scared.
func (e *mazeEng) choose(g *ghost, target pt) pt {
	back := pt{-g.dir.X, -g.dir.Y}
	var opts []pt
	for _, d := range []pt{{0, -1}, {-1, 0}, {0, 1}, {1, 0}} {
		if d != back && e.ghostOpen(g, e.wrap(g.at.add(d))) {
			opts = append(opts, d)
		}
	}
	if len(opts) == 0 {
		return back
	}
	if g.scared && g.state == ghRoam {
		return opts[e.o.rnd.Intn(len(opts))]
	}
	best, bd := opts[0], 1<<30
	for _, d := range opts {
		if dd := dist2(g.at.add(d), target); dd < bd {
			best, bd = d, dd
		}
	}
	return best
}

// hit checks whether you and a ghost are in one place.
func (e *mazeEng) hit() bool {
	for _, g := range e.gh {
		if g.at != e.me || g.state != ghRoam {
			continue
		}
		if g.scared {
			e.combo++
			pts := 200 << (e.combo - 1)
			e.score += pts
			g.state, g.scared = ghEyes, false
			e.msg = fmt.Sprintf("+%d", pts)
			e.pauseTo = e.now.Add(400 * time.Millisecond)
			continue
		}
		e.lives--
		if e.lives <= 0 {
			e.done = true
			e.msg = fmt.Sprintf("game over · score %d · level %d", e.score, e.level)
			return true
		}
		e.reset()
		e.msg = "caught! ready…"
		return true
	}
	return false
}

type mazePic struct {
	G  string  `json:"g"` // the maze, rows joined
	W  int     `json:"w"`
	P  [3]int  `json:"p"`  // you: x, y, direction (0 up 1 left 2 down 3 right)
	Gh [][]int `json:"gh"` // ghosts: x, y, state (0 normal 1 scared 2 eyes 3 scared, nearly over)
	S  int     `json:"s"`
	L  int     `json:"l"`
	Lv int     `json:"lv"`
	M  string  `json:"m,omitempty"`
	O  bool    `json:"o,omitempty"`
}

func (e *mazeEng) view(int) interface{} {
	var b strings.Builder
	for _, r := range e.grid {
		b.Write(r)
	}
	dn := map[pt]int{{0, -1}: 0, {-1, 0}: 1, {0, 1}: 2, {1, 0}: 3}
	p := mazePic{G: b.String(), W: e.w, P: [3]int{e.me.X, e.me.Y, dn[e.dir]}, S: e.score, L: e.lives, Lv: e.level, M: e.msg, O: e.done}
	left := e.scaredTo.Sub(e.now)
	for _, g := range e.gh {
		st := 0
		switch {
		case g.state == ghEyes:
			st = 2
		case g.scared && left < 2*time.Second && left%(400*time.Millisecond) < 200*time.Millisecond:
			st = 3
		case g.scared:
			st = 1
		}
		p.Gh = append(p.Gh, []int{g.at.X, g.at.Y, st})
	}
	return p
}

// ---- drawing ----

type mazeView struct{ p mazePic }

func (v *mazeView) load(b []byte) error {
	var p mazePic
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	if p.W <= 0 || len(p.G)%p.W != 0 || len(p.Gh) > 8 {
		return errors.New("bad picture")
	}
	v.p = p
	return nil
}

func (v *mazeView) size() (int, int) { return 44, 23 }
func (v *mazeView) over() bool       { return v.p.O }

func (v *mazeView) key(ev *tcell.EventKey, x *gctx) string {
	return (&snakeView{}).key(ev, x) // the same keys steer
}

var ghostColors = []tcell.Color{tcell.PaletteColor(203), tcell.PaletteColor(218), tcell.PaletteColor(87), tcell.PaletteColor(215)}

func (v *mazeView) draw(cv *canvas, x *gctx) {
	p := v.p
	w, h := p.W, len(p.G)/p.W
	ox, oy := imax(0, (cv.w-w*2)/2), 1
	wall := tcell.StyleDefault.Background(tcell.PaletteColor(25))
	blink := time.Now().UnixMilli()%500 < 300
	for y := 0; y < h; y++ {
		for xx := 0; xx < w; xx++ {
			c := p.G[y*w+xx]
			px := ox + xx*2
			switch c {
			case '#', '=':
				cv.text(px, oy+y, "  ", wall)
			case '-':
				cv.text(px, oy+y, "──", tcell.StyleDefault.Foreground(tcell.PaletteColor(218)))
			case '.':
				cv.text(px, oy+y, "· ", tcell.StyleDefault.Foreground(tcell.PaletteColor(223)))
			case 'o':
				if blink {
					cv.text(px, oy+y, "● ", tcell.StyleDefault.Foreground(tcell.PaletteColor(223)).Bold(true))
				}
			}
		}
	}
	for i, g := range p.Gh {
		if len(g) < 3 {
			continue
		}
		st := tcell.StyleDefault.Background(ghostColors[i%len(ghostColors)]).Foreground(tcell.ColorBlack).Bold(true)
		s := "oo"
		switch g[2] {
		case 1:
			st, s = tcell.StyleDefault.Background(tcell.PaletteColor(63)).Foreground(tcell.PaletteColor(231)), "~~"
		case 2:
			st = tcell.StyleDefault.Foreground(tcell.PaletteColor(231)).Bold(true)
		case 3:
			st, s = tcell.StyleDefault.Background(tcell.PaletteColor(231)).Foreground(tcell.PaletteColor(63)), "~~"
		}
		if g[0] >= 0 && g[0] < w && g[1] >= 0 && g[1] < h {
			cv.text(ox+g[0]*2, oy+g[1], s, st)
		}
	}
	face := []string{"\\/", "<=", "/\\", "=>"}[p.P[2]&3]
	cv.text(ox+p.P[0]*2, oy+p.P[1], face, tcell.StyleDefault.Background(tcell.PaletteColor(221)).Foreground(tcell.ColorBlack).Bold(true))
	hud := fmt.Sprintf("score %d   level %d   lives %s", p.S, p.Lv, repeat("● ", p.L))
	cv.text(ox, oy+h, hud, gsHead)
	if p.M != "" {
		cv.center(oy+12, "  "+p.M+"  ", gsSel.Reverse(true))
	}
}
