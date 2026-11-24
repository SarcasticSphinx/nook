package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rivo/uniseg"
)

// How a line looks is decided here, once, for the full-screen view and the
// line mode alike. A line becomes one or more paragraphs; each paragraph is
// parts with a kind (which decides colour) and an alignment.

const (
	pText   = iota // the message itself
	pMeta          // time and punctuation: faint
	pFaint         // a message from a chat you're not looking at
	pName          // someone's name (each person gets their own colour)
	pMe            // "you"
	pRoom          // a room label: #lab
	pDM            // a DM label
	pNotice        // something worth noticing: someone changed their name
	pFile          // a file
	pCmd           // a key or a /command in help
	pHead          // a heading in help
)

type part struct {
	text string
	kind int
	who  string // whose name this is (picks the colour)
}

const (
	alignLeft = iota
	alignRight
	alignCenter
)

type para struct {
	parts []part
	align int
	max   int // wrap at this % of the screen width (chat bubbles); 0 = full width
}

var skins = []string{"plain", "chat", "irc", "compact", "log", "code", "json"}

func validSkin(s string) bool { return has(skins, s) }

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// lineInfo is everything a skin needs to know about one line.
type lineInfo struct {
	l        *Line
	name     string // the sender's current name, or "you"
	peer     string // for a DM: the other person's name
	dm       bool
	current  bool // in the chat you're looking at
	room     string
	text     string
	fileText string // "report.pdf · 2.4 MB"
	fileHint string // "/see 3  /get 3"
}

func describe(l *Line, v *View) lineInfo {
	in := lineInfo{l: l, text: l.Text, current: l.Key == v.Target}
	in.name = l.Nick
	if n, ok := v.Nicks[l.From]; ok && n != "" {
		in.name = n // names follow the person, so a rename shows everywhere
	}
	if l.Mine {
		in.name = "you"
	}
	if strings.HasPrefix(l.Key, "@") {
		in.dm = true
		in.peer = strings.TrimPrefix(v.label(l.Key), "@")
	} else {
		in.room = l.Key
	}
	if l.File != nil {
		in.fileText = "[file] " + l.File.Name + " · " + humanSize(l.File.Size)
		if !l.Mine {
			in.fileHint = fmt.Sprintf("/see %d  /get %d", l.FileNo, l.FileNo)
		} else {
			in.fileHint = fmt.Sprintf("/see %d", l.FileNo)
		}
	}
	return in
}

func nameKind(in lineInfo) int {
	if in.l.Mine {
		return pMe
	}
	return pName
}

func pad(s string, n int) string {
	if w := uniseg.StringWidth(s); w < n {
		s += strings.Repeat(" ", n-w)
	}
	return s
}

// fmtLine lays out one line in the chosen skin. prev is the line before it
// (the chat skin groups messages from the same person).
func fmtLine(l, prev *Line, v *View, skin string) []para {
	if l.Raw { // help and lists: as they are; headings stand out
		if f := strings.Split(l.Text, "\t"); len(f) == 3 { // rail, key, what it does
			ps := []part{{f[0], pMeta, ""}}
			switch {
			case f[1] == "" && f[2] != "":
				ps = append(ps, part{f[2], pHead, ""})
			case f[1] != "":
				ps = append(ps, part{pad(f[1], 16), pCmd, ""}, part{f[2], pText, ""})
			}
			return []para{{parts: ps}}
		}
		k := pMeta
		if !strings.HasPrefix(l.Text, " ") {
			k = pText
		}
		return []para{{parts: []part{{l.Text, k, ""}}}}
	}
	in := describe(l, v)
	t := time.UnixMilli(l.TS)
	textKind := pText
	if !in.current && !in.dm && !l.Sys {
		textKind = pFaint // other rooms stay quiet; DMs never do
	}
	body := func() []part {
		if l.File != nil {
			ps := []part{{in.fileText, pFile, ""}}
			if in.fileHint != "" {
				ps = append(ps, part{"  " + in.fileHint, pMeta, ""})
			}
			if in.text != "" {
				ps = append(ps, part{"  " + in.text, textKind, ""})
			}
			return ps
		}
		return []part{{in.text, textKind, ""}}
	}
	name := part{in.name, nameKind(in), l.From}
	one := func(ps ...part) []para { return []para{{parts: ps}} }

	switch skin {
	case "chat":
		if l.Sys {
			k := pMeta
			if l.Notice {
				k = pNotice
			}
			return []para{{parts: []part{{l.Text, k, ""}}, align: alignCenter}}
		}
		align := alignLeft
		if l.Mine {
			align = alignRight
		}
		var out []para
		grouped := prev != nil && !prev.Sys && prev.From == l.From && prev.Key == l.Key && l.TS-prev.TS < 2*60*1000
		if !grouped {
			head := []part{name}
			switch {
			case in.dm && !in.current && l.Mine:
				head = append(head, part{" to " + in.peer, pDM, ""})
			case in.dm && !in.current:
				head = append(head, part{" · DM", pDM, ""})
			case !in.current:
				head = append(head, part{" in " + in.room, pRoom, ""})
			}
			head = append(head, part{" · " + t.Format("15:04"), pMeta, ""})
			out = append(out, para{}, para{parts: head, align: align})
		}
		return append(out, para{parts: body(), align: align, max: 75})
	case "irc":
		ts := part{"[" + t.Format("15:04") + "] ", pMeta, ""}
		switch {
		case l.Sys:
			k := pMeta
			if l.Notice {
				k = pNotice
			}
			return one(ts, part{"-!- " + l.Text, k, ""})
		case in.dm && l.Mine:
			return one(append([]part{ts, {"-> *" + in.peer + "* ", pDM, ""}}, body()...)...)
		case in.dm:
			return one(append([]part{ts, {"*", pDM, ""}, name, {"* ", pDM, ""}}, body()...)...)
		case !in.current:
			return one(append([]part{ts, {in.room + " ", pRoom, ""}, {"<", pMeta, ""}, name, {"> ", pMeta, ""}}, body()...)...)
		}
		return one(append([]part{ts, {"<", pMeta, ""}, name, {"> ", pMeta, ""}}, body()...)...)
	case "compact":
		switch {
		case l.Sys:
			k := pMeta
			if l.Notice {
				k = pNotice
			}
			return one(part{"-- " + l.Text, k, ""})
		case in.dm && l.Mine:
			return one(append([]part{{"you > " + in.peer + ": ", pDM, ""}}, body()...)...)
		case in.dm:
			return one(append([]part{name, {" (DM): ", pDM, ""}}, body()...)...)
		case !in.current:
			return one(append([]part{{in.room + " ", pRoom, ""}, name, {": ", pMeta, ""}}, body()...)...)
		}
		return one(append([]part{name, {": ", pMeta, ""}}, body()...)...)
	case "log":
		ts := t.Format("15:04:05.000")
		if l.Sys {
			return one(part{ts + " WARN  ", pMeta, ""}, part{l.Text, pMeta, ""})
		}
		lvl, src := "INFO ", strings.TrimPrefix(in.room, "#")
		if in.dm {
			lvl, src = "DEBUG", "dm."+in.peer
		}
		return one(append([]part{{ts + " " + lvl + " " + src + "/" + in.name + ": ", pMeta, ""}}, body()...)...)
	case "code":
		no := part{fmt.Sprintf("%5d | ", l.Seq%100000), pMeta, ""}
		switch {
		case l.Sys:
			return one(no, part{"/* " + l.Text + " */", pMeta, ""})
		case in.dm:
			return one(append([]part{no, {"// [dm " + in.peer + "] " + in.name + ": ", pMeta, ""}}, body()...)...)
		case !in.current:
			return one(append([]part{no, {"// [" + in.room + "] " + in.name + ": ", pMeta, ""}}, body()...)...)
		}
		return one(append([]part{no, {"// " + in.name + ": ", pMeta, ""}}, body()...)...)
	case "json":
		rec := struct {
			T    string `json:"t"`
			Chat string `json:"chat,omitempty"`
			From string `json:"from,omitempty"`
			Text string `json:"text"`
			File string `json:"file,omitempty"`
		}{T: t.Format("15:04:05"), Text: l.Text}
		if !l.Sys {
			rec.From, rec.Chat = in.name, in.room
			if in.dm {
				rec.Chat = "dm:" + in.peer
			}
		}
		if l.File != nil {
			rec.File = l.File.Name + " " + in.fileHint
		}
		b, _ := json.Marshal(rec)
		k := pMeta
		if !l.Sys && (in.current || in.dm) {
			k = pText
		}
		return one(part{string(b), k, ""})
	}

	// plain: the default
	ts := part{t.Format("15:04") + "  ", pMeta, ""}
	switch {
	case l.Sys && l.Notice:
		return one(ts, part{"** " + l.Text, pNotice, ""})
	case l.Sys:
		return one(ts, part{"-- " + l.Text, pMeta, ""})
	}
	var label []part
	switch {
	case in.dm && !in.current && l.Mine: // "DM to rafa", not "DM to rafa  you"
		label = []part{{"DM to ", pDM, ""}}
		name = part{in.peer, pName, strings.TrimPrefix(l.Key, "@")}
	case in.dm && !in.current:
		label = []part{{"DM  ", pDM, ""}}
	case !in.current:
		label = []part{{in.room + "  ", pRoom, ""}}
	}
	name.text = pad(name.text, 8) + " "
	return one(append(append(append([]part{ts}, label...), name), body()...)...)
}

// plainText renders paragraphs as plain lines (line mode, tests).
func plainText(ps []para) string {
	var lines []string
	for _, p := range ps {
		var b strings.Builder
		for _, x := range p.parts {
			b.WriteString(x.text)
		}
		if p.align == alignRight && b.Len() > 0 {
			lines = append(lines, "    "+b.String())
		} else {
			lines = append(lines, b.String())
		}
	}
	return strings.Join(lines, "\n")
}
