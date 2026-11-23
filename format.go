package main

import (
	"strings"
	"time"
)

// A line is drawn as a few parts, each with a kind that decides its look.
const (
	pText  = iota // the message itself
	pMeta         // time, names, labels: drawn faint
	pFaint        // messages from a chat you're not looking at
)

type part struct {
	text string
	kind int
}

// fmtLine lays out one line in the chosen skin.
//
//	plain: 09:41  ali      ok what did you get for 4
//	log:   09:41:02.381 INFO  general/ali: ok what did you get for 4
func fmtLine(l *Line, v *View, skin string) []part {
	t := time.UnixMilli(l.TS)
	other := !l.Sys && l.Key != v.Target
	kind := pText
	if other {
		kind = pFaint
	}
	if skin == "log" {
		ts := t.Format("15:04:05.000")
		if l.Sys {
			return []part{{ts + " WARN  ", pMeta}, {l.Text, pMeta}}
		}
		lvl, src := "INFO ", strings.TrimPrefix(l.Key, "#")
		if strings.HasPrefix(l.Key, "@") {
			lvl, src = "DEBUG", "dm"+v.label(l.Key)
		}
		return []part{{ts + " " + lvl + " " + src + "/" + l.Nick + ": ", pMeta}, {l.Text, kind}}
	}
	ts := t.Format("15:04")
	if l.Sys {
		return []part{{ts + "  -- ", pMeta}, {l.Text, pMeta}}
	}
	label := ""
	if other {
		label = v.label(l.Key) + " "
	}
	nick := l.Nick
	for len(nick) < 8 { // names are ASCII, so bytes == columns
		nick += " "
	}
	return []part{{ts + "  " + label + nick + " ", pMeta}, {l.Text, kind}}
}

func plainText(parts []part) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.text)
	}
	return b.String()
}
