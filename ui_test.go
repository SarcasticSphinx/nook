package main

import (
	"strings"
	"testing"
	"time"
)

// A rename reaches everyone, with a notice, and even old messages show the
// new name, in Bluetooth mode too.
func TestRenameReachesEveryoneOverBluetooth(t *testing.T) {
	n := &simNet{mtu: 64}
	a := newSimNode(t, n, "ali", testMeshCfg)
	b := newSimNode(t, n, "bina", testMeshCfg)
	n.connect(a.rad, b.rad)
	waitFor(t, "both online", func() bool { return b.hub.Online() == 2 && a.hub.Online() == 2 })
	a.c.Input("before the rename")
	waitFor(t, "message arrives", func() bool { return sees(b.c, "#general", "before the rename") })
	a.c.Input("/nick alibaba")
	waitFor(t, "rename notice on the other laptop", func() bool { return sysSays(b.c, "ali is now known as alibaba") })
	waitFor(t, "rename notice for yourself", func() bool { return sysSays(a.c, "you are now known as alibaba") })
	v := b.c.View(maxLines)
	for i := range v.Lines {
		if l := &v.Lines[i]; l.Text == "before the rename" {
			if got := plainText(fmtLine(l, nil, &v, "plain")); !strings.Contains(got, "alibaba") {
				t.Fatalf("old message still shows the old name: %q", got)
			}
		}
	}
	b.c.Input("/dm alibaba hi again")
	waitFor(t, "DM by the new name", func() bool { return sees(a.c, "@"+b.c.MeID(), "hi again") })
}

func TestBackToGeneralAndUnread(t *testing.T) {
	h := testHub(t, "")
	a, b := testClient(t, h, "ana", ""), testClient(t, h, "ben", "")
	online(t, a, b)
	a.Input("/dm ben hello there")
	waitFor(t, "a is in the DM", func() bool { return target(a) == "@"+b.MeID() })
	b.Input("in general")
	waitFor(t, "a got it", func() bool { return sees(a, "#general", "in general") })
	v := a.View(maxLines)
	unread := map[string]int{}
	for _, ch := range v.Chats {
		unread[ch.Key] = ch.Unread
	}
	if unread["#general"] != 1 {
		t.Fatalf("unread in #general = %d, want 1 (chats %+v)", unread["#general"], v.Chats)
	}
	a.Input("/g")
	waitFor(t, "back in #general", func() bool { return target(a) == "#general" })
	v = a.View(maxLines)
	for _, ch := range v.Chats {
		if ch.Key == "#general" && ch.Unread != 0 {
			t.Fatal("#general still unread after going there")
		}
	}
	a.Input("/who")
	waitFor(t, "who list", func() bool { return sysSays(a, "people here: 2") })
}

// Every skin can draw every kind of line.
func TestSkinsDrawEverything(t *testing.T) {
	now := time.Now().UnixMilli()
	v := View{Target: "#general", Nicks: map[string]string{"aaaaaaaaaaaa": "rafi", "bbbbbbbbbbbb": "me"}, MeID: "bbbbbbbbbbbb"}
	lines := []Line{
		{Seq: 1, TS: now, Key: "#general", From: "aaaaaaaaaaaa", Nick: "old", Text: "hello"},
		{Seq: 2, TS: now, Key: "#lab", From: "aaaaaaaaaaaa", Text: "in the lab"},
		{Seq: 3, TS: now, Key: "@aaaaaaaaaaaa", From: "aaaaaaaaaaaa", Text: "psst"},
		{Seq: 4, TS: now, Key: "@aaaaaaaaaaaa", From: "bbbbbbbbbbbb", Text: "ok", Mine: true},
		{Seq: 5, TS: now, Sys: true, Text: "something happened"},
		{Seq: 6, TS: now, Sys: true, Notice: true, Text: "old is now known as rafi"},
		{Seq: 7, TS: now, Sys: true, Raw: true, Text: "  /g  back to #general"},
		{Seq: 8, TS: now, Key: "#general", From: "aaaaaaaaaaaa", File: &FileInfo{ID: "f", Name: "notes.pdf", Size: 2 << 20}, FileNo: 3},
	}
	u := &tui{}
	for _, skin := range skins {
		u.o.Skin = skin
		for i := range lines {
			var prev *Line
			if i > 0 {
				prev = &lines[i-1]
			}
			ps := fmtLine(&lines[i], prev, &v, skin)
			text := plainText(ps)
			if text == "" && !lines[i].Sys {
				t.Fatalf("%s: line %d drew nothing", skin, i)
			}
			for _, p := range ps {
				for _, r := range u.wrap(p, 50) {
					w := r.x
					for _, c := range r.cells {
						w += c.w
					}
					if w > 50 {
						t.Fatalf("%s: row wider than the screen", skin)
					}
				}
			}
		}
		dm := plainText(fmtLine(&lines[2], nil, &v, skin))
		if !strings.Contains(strings.ToLower(dm), "dm") && !strings.Contains(dm, "*rafi*") {
			t.Fatalf("%s: a DM isn't marked as one: %q", skin, dm)
		}
		file := plainText(fmtLine(&lines[7], nil, &v, skin))
		if !strings.Contains(file, "notes.pdf") || !strings.Contains(file, "/get 3") {
			t.Fatalf("%s: file line is missing its name or /get: %q", skin, file)
		}
	}
	if got := plainText(fmtLine(&lines[0], nil, &v, "plain")); !strings.Contains(got, "rafi") || strings.Contains(got, "old") {
		t.Fatalf("names should follow the person: %q", got)
	}
	// your own DM, seen from #general: who it went to, without a stray "you"
	if got := plainText(fmtLine(&lines[3], nil, &v, "plain")); !strings.Contains(got, "DM to rafi") || strings.Contains(got, "you") {
		t.Fatalf("own DM label: %q", got)
	}
}
