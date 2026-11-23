package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// decoy is what Esc shows: an ordinary, idle shell prompt with your real
// user name, computer name and folder. You can type in it, and `ls`, `dir`,
// `cd`, `pwd`, `clear` and `exit` behave like the real thing.
type decoy struct {
	lines                 []string
	in                    []rune
	cwd, home, user, host string
	style                 string // zsh bash ps cmd
}

func newDecoy() *decoy {
	d := &decoy{}
	d.cwd, _ = os.Getwd()
	d.home, _ = os.UserHomeDir()
	if d.user = osUser(); d.user == "" {
		d.user = "user"
	}
	d.host, _ = os.Hostname()
	if i := strings.IndexByte(d.host, '.'); i > 0 {
		d.host = d.host[:i]
	}
	switch {
	case runtime.GOOS == "windows" && os.Getenv("PROMPT") != "":
		d.style = "cmd" // cmd.exe sets PROMPT for its children; PowerShell doesn't
	case runtime.GOOS == "windows":
		d.style = "ps"
	case strings.HasSuffix(os.Getenv("SHELL"), "bash"):
		d.style = "bash"
	default:
		d.style = "zsh"
	}
	return d
}

func (d *decoy) prompt() string {
	switch d.style {
	case "ps":
		return "PS " + d.cwd + "> "
	case "cmd":
		return d.cwd + ">"
	case "bash":
		if runtime.GOOS == "darwin" {
			return d.host + ":" + d.short() + " " + d.user + "$ "
		}
		dir := d.cwd
		if d.home != "" && strings.HasPrefix(dir, d.home) {
			dir = "~" + dir[len(d.home):]
		}
		return d.user + "@" + d.host + ":" + dir + "$ "
	}
	return d.user + "@" + d.host + " " + d.short() + " % "
}

func (d *decoy) short() string {
	if d.cwd == d.home {
		return "~"
	}
	return filepath.Base(d.cwd)
}

// key handles one key press; it returns true when the user typed `exit`.
func (d *decoy) key(ev *tcell.EventKey, width int) bool {
	switch ev.Key() {
	case tcell.KeyEnter:
		return d.enter(width)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if n := len(d.in); n > 0 {
			d.in = d.in[:n-1]
		}
	case tcell.KeyCtrlU:
		d.in = d.in[:0]
	case tcell.KeyCtrlL:
		d.lines = nil
	case tcell.KeyRune:
		if len(d.in) < 500 {
			d.in = append(d.in, ev.Rune())
		}
	}
	return false
}

func (d *decoy) out(lines ...string) { d.lines = append(d.lines, lines...) }

func (d *decoy) enter(width int) bool {
	line := string(d.in)
	d.out(d.prompt() + line)
	d.in = d.in[:0]
	f := strings.Fields(line)
	if len(f) > 0 {
		switch strings.ToLower(f[0]) {
		case "exit", "logout":
			return true
		case "clear", "cls":
			d.lines = nil
		case "ls", "dir", "ll", "gci":
			d.out(d.listing(width)...)
		case "pwd":
			if d.style == "ps" {
				d.out("", "Path", "----", d.cwd, "")
			} else {
				d.out(d.cwd)
			}
		case "cd", "chdir", "sl", "set-location":
			d.cd(strings.TrimSpace(strings.TrimPrefix(line, f[0])))
		case "whoami":
			d.out(d.user)
		case "echo":
			d.out(strings.TrimSpace(strings.TrimPrefix(line, f[0])))
		}
	}
	if len(d.lines) > 400 {
		d.lines = d.lines[len(d.lines)-400:]
	}
	return false
}

func (d *decoy) cd(arg string) {
	t := arg
	if t == "" {
		if d.style == "cmd" {
			d.out(d.cwd)
			return
		}
		t = d.home
	}
	if strings.HasPrefix(t, "~") {
		t = d.home + t[1:]
	}
	if !filepath.IsAbs(t) {
		t = filepath.Join(d.cwd, t)
	}
	if st, err := os.Stat(t); err == nil && st.IsDir() {
		d.cwd = filepath.Clean(t)
		return
	}
	switch d.style {
	case "ps":
		d.out("Set-Location: Cannot find path '" + t + "' because it does not exist.")
	case "cmd":
		d.out("The system cannot find the path specified.")
	case "bash":
		d.out("bash: cd: " + arg + ": No such file or directory")
	default:
		d.out("cd: no such file or directory: " + arg)
	}
}

func (d *decoy) listing(width int) []string {
	ents, err := os.ReadDir(d.cwd)
	if err != nil {
		return nil
	}
	if d.style == "ps" || d.style == "cmd" {
		out := []string{"", "    Directory: " + d.cwd, "", "Mode                 LastWriteTime         Length Name", "----                 -------------         ------ ----"}
		for _, e := range ents {
			info, err := e.Info()
			if err != nil {
				continue
			}
			mode, size := "-a---", fmt.Sprint(info.Size())
			if e.IsDir() {
				mode, size = "d----", ""
			}
			t := info.ModTime()
			stamp := fmt.Sprintf("%10s %8s", t.Format("1/2/2006"), t.Format("3:04 PM"))
			out = append(out, fmt.Sprintf("%-5s %24s %14s %s", mode, stamp, size, e.Name()))
		}
		return append(out, "")
	}
	var names []string
	maxw := 1
	for _, e := range ents {
		n := e.Name()
		if strings.HasPrefix(n, ".") {
			continue
		}
		names = append(names, n)
		if w := uniseg.StringWidth(n); w > maxw {
			maxw = w
		}
	}
	if len(names) == 0 {
		return nil
	}
	colw := maxw + 2
	cols := imax(1, width/colw)
	nrows := (len(names) + cols - 1) / cols
	out := make([]string, 0, nrows)
	for r := 0; r < nrows; r++ {
		var b strings.Builder
		for c := 0; c < cols; c++ {
			i := c*nrows + r
			if i >= len(names) {
				break
			}
			b.WriteString(names[i])
			if (c+1)*nrows+r < len(names) {
				b.WriteString(strings.Repeat(" ", colw-uniseg.StringWidth(names[i])))
			}
		}
		out = append(out, b.String())
	}
	return out
}

func (d *decoy) draw(s tcell.Screen, w, h int) {
	var rows [][]cell
	for _, l := range d.lines {
		rows = append(rows, hardWrap(l, w, tcell.StyleDefault)...)
	}
	rows = append(rows, hardWrap(d.prompt()+string(d.in), w, tcell.StyleDefault)...)
	start := imax(0, len(rows)-h)
	for y, r := range rows[start:] {
		x := 0
		for _, c := range r {
			s.SetContent(x, y, c.r[0], c.r[1:], c.st)
			x += c.w
		}
	}
	last := rows[len(rows)-1]
	cx := 0
	for _, c := range last {
		cx += c.w
	}
	if cx >= w {
		cx = w - 1
	}
	s.ShowCursor(cx, len(rows)-1-start)
}
