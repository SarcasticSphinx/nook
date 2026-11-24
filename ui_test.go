package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
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
	v := b.c.View(maxLines, false)
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
	v := a.View(maxLines, false)
	unread := map[string]int{}
	for _, ch := range v.Chats {
		unread[ch.Key] = ch.Unread
	}
	if unread["#general"] != 1 {
		t.Fatalf("unread in #general = %d, want 1 (chats %+v)", unread["#general"], v.Chats)
	}
	a.Input("/g")
	waitFor(t, "back in #general", func() bool { return target(a) == "#general" })
	v = a.View(maxLines, false)
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

// The full-screen view shows one chat at a time, like a messenger: a DM
// never mixes with #general; news from elsewhere waits on one row.
// Tab completes, Ctrl+N switches, the wheel scrolls and fetches older
// messages, and /see opens a file without saving it.
func TestChatsStaySeparate(t *testing.T) {
	h := testHub(t, "")
	h.mu.Lock() // an old conversation: more than a connection brings along
	rm := h.rooms[general]
	for i := 1; i <= 250; i++ {
		h.nextID++
		rm.hist = appendCap(rm.hist, Msg{ID: h.nextID, TS: nowMs() - int64(300-i)*1000, Room: general, From: "zzzzzzzzzzzz", Nick: "old", Text: fmt.Sprintf("old message %03d", i)}, roomHist)
	}
	h.mu.Unlock()
	friend := testClient(t, h, "friend", "")
	online(t, friend)

	dl := t.TempDir()
	old := downloadsDir
	downloadsDir = func() string { return dl }
	t.Cleanup(func() { downloadsDir = old })

	s := tcell.NewSimulationScreen("UTF-8")
	s.SetSize(90, 30)
	c := NewClient(ClientOpts{Mode: modeJoin, URL: localURL(h.Port()), Nick: "me", Token: deviceToken("separate")})
	done := make(chan error, 1)
	go func() { done <- runTUI(c, uiOpts{Skin: "plain"}, s) }()
	waitFor(t, "connected", func() bool { return c.State() == stOnline })
	shows := func(what string) func() bool {
		return func() bool { return strings.Contains(screenText(s), what) }
	}
	typeText := func(text string) {
		for _, r := range text {
			s.InjectKey(tcell.KeyRune, r, tcell.ModNone)
		}
	}

	friend.Input("in general")
	waitFor(t, "general message", shows("in general"))
	friend.Input("/dm me a secret")
	waitFor(t, "a peek at the DM", shows("DM from friend"))
	if strings.Contains(screenText(s), "a secret") && !strings.Contains(screenText(s), "DM from friend: a secret") {
		t.Fatal("the DM was mixed into #general")
	}
	s.InjectKey(tcell.KeyCtrlN, 0, tcell.ModNone)
	waitFor(t, "in the DM", shows("DM friend> "))
	waitFor(t, "DM shown", shows("a secret"))
	if strings.Contains(screenText(s), "in general") {
		t.Fatal("#general showed inside the DM")
	}
	waitFor(t, "start of the DM", shows("the start of your chat with friend"))
	s.InjectKey(tcell.KeyCtrlG, 0, tcell.ModNone)
	waitFor(t, "back in general", shows("#general> "))

	// Tab completes commands and names
	typeText("/he")
	s.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	waitFor(t, "completed /help", shows("#general> /help "))
	s.InjectKey(tcell.KeyCtrlU, 0, tcell.ModNone)
	typeText("/dm fr")
	s.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	waitFor(t, "completed the name", shows("#general> /dm friend "))
	s.InjectKey(tcell.KeyCtrlU, 0, tcell.ModNone)

	// the wheel goes back through the chat, and further back than the
	// connection brought: the very first message comes from the host
	for i := 0; i < 200 && !strings.Contains(screenText(s), "old message 001"); i++ {
		s.InjectMouse(5, 10, tcell.WheelUp, tcell.ModNone)
		time.Sleep(10 * time.Millisecond)
	}
	waitFor(t, "the first message", shows("old message 001"))
	waitFor(t, "the start", shows("the start of #general"))
	s.InjectKey(tcell.KeyEnd, 0, tcell.ModNone)
	for i := 0; i < 200 && !strings.Contains(screenText(s), "in general"); i++ {
		s.InjectMouse(5, 10, tcell.WheelDown, tcell.ModNone)
	}
	waitFor(t, "back at the bottom", shows("in general"))

	// /see: a text file opens inside the window, nothing is saved
	src := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(src, []byte("first line\n\tindented line\n"), 0o644)
	friend.Input("/g")
	waitFor(t, "friend back in general", func() bool { return target(friend) == "#general" })
	friend.Input("/send " + src)
	waitFor(t, "file offered with /see", shows("/see 1  /get 1"))
	typeText("/see 1")
	s.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, "file shown", shows("    indented line"))
	waitFor(t, "viewer bar", shows("q close"))
	s.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	waitFor(t, "viewer closed", shows("#general> "))
	if ents, _ := os.ReadDir(dl); len(ents) != 0 {
		t.Fatalf("/see saved something: %v", ents)
	}

	// a picture is drawn with half blocks
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	pic := filepath.Join(t.TempDir(), "pic.png")
	os.WriteFile(pic, buf.Bytes(), 0o644)
	friend.Input("/send " + pic)
	waitFor(t, "picture offered", shows("/see 2"))
	typeText("/see 2")
	s.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, "picture drawn", shows("▀▀▀▀▀▀▀▀▀▀"))
	s.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	s.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	<-done
}
