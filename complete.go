package main

import (
	"sort"
	"strings"
)

// Tab completes a /command being typed, then what that command takes (a
// name, a room, a skin), and an @name in a message. Anywhere else Tab
// switches chats.
// One match fills it in; several show above the input and Tab goes through
// them.

var commandNames = []string{
	"all", "bar", "clear", "colors", "dim", "dm", "fade", "files", "g", "get",
	"help", "hide", "info", "join", "key", "leave", "mouse", "nick", "people",
	"quit", "r", "rooms", "see", "send", "skin", "who",
}

type compList struct {
	start int      // where the word being completed starts in the input
	list  []string // what it can become
	i     int      // the one shown now
	after string   // the input after the word, kept as it was
}

func (u *tui) complete() {
	if c := u.comp; c != nil && len(c.list) > 1 { // Tab again: the next one
		c.i = (c.i + 1) % len(c.list)
		u.fill(c, false)
		return
	}
	in := string(u.in[:u.cur])
	start := strings.LastIndex(in, " ") + 1
	word := in[start:]
	cmd := ""
	if strings.HasPrefix(in, "/") {
		cmd, _, _ = strings.Cut(in[1:], " ")
		cmd = strings.ToLower(cmd)
	}
	var pool []string
	v := u.c.View(1, false)
	names := func() []string {
		var out []string
		for _, p := range v.People {
			if p.ID != v.MeID {
				out = append(out, p.Nick)
			}
		}
		return out
	}
	switch {
	case strings.HasPrefix(word, "@") && !strings.HasPrefix(in, "/"):
		for _, n := range names() {
			pool = append(pool, "@"+n)
		}
		if strings.HasPrefix(v.Target, "#") {
			pool = append(pool, "@everyone")
		}
	case start == 0 && strings.HasPrefix(word, "/"):
		for _, c := range commandNames {
			pool = append(pool, "/"+c)
		}
	case cmd == "dm" || cmd == "tm" || cmd == "msg" || cmd == "w" || cmd == "m":
		if start == len("/"+cmd+" ") {
			pool = names()
		}
	case cmd == "join" || cmd == "j" || cmd == "leave":
		pool = v.Rooms
		if !strings.HasPrefix(word, "#") && word != "" {
			word = "#" + word
		}
	case cmd == "skin" || cmd == "theme":
		pool = append(append(pool, skins...), "next")
	case cmd == "get" || cmd == "see" || cmd == "send":
		return // numbers and paths: nothing useful to offer
	default:
		pool = names() // a name inside a message
	}
	var list []string
	lw := strings.ToLower(word)
	for _, p := range pool {
		if strings.HasPrefix(strings.ToLower(p), lw) && p != "" {
			list = append(list, p)
		}
	}
	sort.Strings(list)
	if len(list) == 0 {
		return
	}
	c := &compList{start: start, list: list, after: string(u.in[u.cur:])}
	if len(list) > 1 {
		// fill in what they all share first, like a shell
		common := list[0]
		for _, p := range list[1:] {
			for !strings.HasPrefix(strings.ToLower(p), strings.ToLower(common)) {
				common = common[:len(common)-1]
			}
		}
		if len(common) > len(word) {
			u.setWord(c, common, "")
			return
		}
		c.i = -1
		u.comp = c
		return // the choices show above the input; Tab again picks one
	}
	u.fill(c, true)
}

// fill puts the chosen completion in place of the word.
func (u *tui) fill(c *compList, done bool) {
	end := " "
	if !done {
		end = ""
	}
	u.setWord(c, c.list[imax(0, c.i)], end)
	if done {
		u.comp = nil
	} else {
		u.comp = c
	}
}

func (u *tui) setWord(c *compList, word, end string) {
	before := []rune(string(u.in[:c.start]))
	w := []rune(word + end)
	u.in = append(append(before, w...), []rune(c.after)...)
	u.cur = len(before) + len(w)
}

// atWord: the word before the cursor is an @name being typed.
func (u *tui) atWord() bool {
	in := string(u.in[:u.cur])
	return strings.HasPrefix(in[strings.LastIndex(in, " ")+1:], "@")
}
