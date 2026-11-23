package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Tests stay on loopback: no packets leave this computer.
func TestMain(m *testing.M) {
	discoveryTargets = func(port int) []*net.UDPAddr {
		return []*net.UDPAddr{{IP: net.IPv4(127, 0, 0, 1), Port: port}}
	}
	os.Exit(m.Run())
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func testHub(t *testing.T, key string) *Hub {
	t.Helper()
	h, err := StartHub(HubOpts{Key: key})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close("") })
	return h
}

func testClient(t *testing.T, h *Hub, nick, key string) *Client {
	t.Helper()
	c := NewClient(ClientOpts{Mode: modeJoin, URL: localURL(h.Port()), Nick: nick, Key: key, Token: deviceToken("test-" + nick)})
	c.Start()
	t.Cleanup(c.Close)
	return c
}

func online(t *testing.T, cs ...*Client) {
	t.Helper()
	for _, c := range cs {
		waitFor(t, "client online", func() bool { return c.State() == stOnline })
	}
}

func sees(c *Client, key, text string) bool {
	v := c.View(maxLines)
	for _, l := range v.Lines {
		if !l.Sys && l.Text == text && (key == "" || l.Key == key) {
			return true
		}
	}
	return false
}

func sysSays(c *Client, part string) bool {
	v := c.View(maxLines)
	for _, l := range v.Lines {
		if l.Sys && strings.Contains(l.Text, part) {
			return true
		}
	}
	return false
}

func target(c *Client) string { v := c.View(1); return v.Target }

func TestChat(t *testing.T) {
	h := testHub(t, "")
	a, b, c := testClient(t, h, "ali", ""), testClient(t, h, "bina", ""), testClient(t, h, "chen", "")
	online(t, a, b, c)
	waitFor(t, "three online", func() bool { return h.Online() == 3 })

	a.Input("hello all")
	for _, x := range []*Client{a, b, c} {
		x := x
		waitFor(t, "general message", func() bool { return sees(x, "#general", "hello all") })
	}

	// A private room.
	b.Input("/join #team")
	waitFor(t, "b in #team", func() bool { return target(b) == "#team" })
	c.Input("/join team")
	waitFor(t, "c in #team", func() bool { return target(c) == "#team" })
	waitFor(t, "b told c joined", func() bool { return sysSays(b, "chen joined #team") })
	b.Input("team only")
	waitFor(t, "c gets team msg", func() bool { return sees(c, "#team", "team only") })
	time.Sleep(100 * time.Millisecond)
	if sees(a, "", "team only") {
		t.Fatal("ali saw a #team message without joining")
	}

	// Direct messages, by name prefix, and replying.
	a.Input("/dm bin psst")
	waitFor(t, "b gets dm", func() bool { return sees(b, "@"+a.MeID(), "psst") })
	b.Input("/r what")
	waitFor(t, "a gets reply", func() bool { return sees(a, "@"+b.MeID(), "what") })
	time.Sleep(100 * time.Millisecond)
	if sees(c, "", "psst") || sees(c, "", "what") {
		t.Fatal("chen saw a private message")
	}

	// Renaming is visible to others and names are unique.
	c.Input("/nick carl")
	waitFor(t, "rename seen", func() bool { return sysSays(a, "chen is now known as carl") })
	a.Input("/nick carl")
	waitFor(t, "name taken", func() bool { return sysSays(a, "carl is taken") })

	// Control characters and terminal escapes never get through.
	a.Input("/join #general")
	a.Input("red\x1b[31m alert‮")
	waitFor(t, "cleaned text", func() bool { return sees(b, "#general", "red[31m alert") })

	// Someone arriving later gets the recent history, and can't read DMs.
	d := testClient(t, h, "dev", "")
	online(t, d)
	waitFor(t, "history", func() bool { return sees(d, "#general", "hello all") })
	if sees(d, "", "psst") || sees(d, "", "team only") {
		t.Fatal("history leaked a private message")
	}

	// Leaving moves you back to a room you're still in.
	c.Input("/leave")
	waitFor(t, "c left #team", func() bool { return target(c) == "#general" })
	waitFor(t, "b told c left", func() bool { return sysSays(b, "carl left #team") })
}

func TestComeBackKeepsNameAndDMs(t *testing.T) {
	h := testHub(t, "")
	a, b := testClient(t, h, "ali", ""), testClient(t, h, "bina", "")
	online(t, a, b)
	a.Input("/dm bina see you")
	waitFor(t, "dm", func() bool { return sees(b, "", "see you") })
	b.Close() // vanish...
	b2 := testClient(t, h, "bina", "")
	online(t, b2)
	waitFor(t, "dm survives restart", func() bool { return sees(b2, "", "see you") })
	if v := b2.View(1); v.Nicks[b2.MeID()] != "bina" {
		t.Fatalf("came back as %q", v.Nicks[b2.MeID()])
	}
}

func TestKey(t *testing.T) {
	h := testHub(t, "sesame")
	bad := testClient(t, h, "eve", "nope")
	waitFor(t, "wrong key refused", func() bool { return sysSays(bad, "wrong key") })
	if bad.State() == stOnline || h.Online() != 0 {
		t.Fatal("wrong key got in")
	}
	bad.Input("/key sesame")
	online(t, bad)
	if h.Online() != 1 {
		t.Fatalf("online = %d", h.Online())
	}
}

func TestRateLimit(t *testing.T) {
	h := testHub(t, "")
	a := testClient(t, h, "spam", "")
	online(t, a)
	for i := 0; i < 30; i++ {
		a.Input("x")
	}
	waitFor(t, "slow down", func() bool { return sysSays(a, "slow down") })
}

func TestDiscovery(t *testing.T) {
	port := freeUDPPort(t)
	h, err := StartHub(HubOpts{Port: 0, UDPPort: port})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close("")
	hubs := discover(port, 800*time.Millisecond)
	if len(hubs) != 1 || hubs[0].ID != h.Info().ID || hubs[0].Port != h.Port() || hubs[0].IP != "127.0.0.1" {
		t.Fatalf("discover = %+v", hubs)
	}
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// When the host quits, the others pick a new host by themselves.
func TestHostLeaves(t *testing.T) {
	port := freeUDPPort(t)
	x := NewClient(ClientOpts{Mode: modeAuto, Port: port, Nick: "x", Token: deviceToken("x")})
	x.Start()
	waitFor(t, "x hosts", func() bool { return x.State() == stOnline && x.Hosting() })
	y := NewClient(ClientOpts{Mode: modeAuto, Port: port, Nick: "y", Token: deviceToken("y")})
	y.Start()
	defer y.Close()
	waitFor(t, "y joins x", func() bool { return y.State() == stOnline })
	if y.Hosting() {
		t.Fatal("y started a second room instead of joining")
	}
	y.Input("before")
	waitFor(t, "x sees y", func() bool { return sees(x, "", "before") })
	x.Close()
	waitFor(t, "y takes over", func() bool { return y.State() == stOnline && y.Hosting() })
	y.Input("after")
	waitFor(t, "y can still chat", func() bool { return sees(y, "", "after") })
}

// A hub that finds a better one sends its people there.
func TestHandOver(t *testing.T) {
	winner := testHub(t, "")
	old := discoverFn
	defer func() { discoverFn = old }()
	discoverFn = func(int, time.Duration) []HubInfo {
		inf := winner.Info()
		inf.IP, inf.LAN, inf.Prio = "127.0.0.1", "127.0.0.1", 1
		return []HubInfo{inf}
	}
	loser, err := StartHub(HubOpts{Port: 0, UDPPort: freeUDPPort(t), Elect: true})
	if err != nil {
		t.Fatal(err)
	}
	defer loser.Close("")
	c := NewClient(ClientOpts{Mode: modeJoin, URL: localURL(loser.Port()), Nick: "z", Token: deviceToken("z")})
	c.Start()
	defer c.Close()
	waitFor(t, "moved to the winner", func() bool {
		return c.State() == stOnline && c.URL() == localURL(winner.Port())
	})
	if winner.Online() != 1 {
		t.Fatalf("winner has %d online", winner.Online())
	}
}

func TestBetter(t *testing.T) {
	a := HubInfo{ID: "a", Started: 100}
	b := HubInfo{ID: "b", Started: 200}
	if !better(a, b) || better(b, a) {
		t.Fatal("older hub should win")
	}
	b.Remote = 3
	if !better(b, a) {
		t.Fatal("a hub people can reach should beat one they can't")
	}
	a.Prio = 1
	if !better(a, b) {
		t.Fatal("an explicit host should win")
	}
}

func TestPages(t *testing.T) {
	h := testHub(t, "")
	base := localURL(h.Port())
	get := func(path string) (*http.Response, string) {
		r, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r, string(b)
	}
	if r, body := get("/"); r.StatusCode != 200 || !strings.Contains(body, "EventSource") || !strings.Contains(r.Header.Get("Content-Security-Policy"), "default-src") {
		t.Fatal("web client not served")
	}
	if r, body := get("/get"); r.StatusCode != 200 || !strings.Contains(body, "Join the chat") {
		t.Fatalf("download page: %d", r.StatusCode)
	}
	if r, _ := get("/dl/../../etc/passwd"); r.StatusCode != 404 {
		t.Fatalf("download path escape: %d", r.StatusCode)
	}
	r, body := get("/ping")
	var inf HubInfo
	if json.Unmarshal([]byte(body), &inf) != nil || inf.ID == "" || r.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("bad /ping")
	}
}

func TestArgs(t *testing.T) {
	a, err := parseArgs([]string{"join", "192.168.1.20", "--name", "sam", "-k=pw", "--dim"})
	if err != nil || a.cmd != "join" || a.arg != "192.168.1.20" || a.name != "sam" || a.key != "pw" || !a.dim {
		t.Fatalf("%+v %v", a, err)
	}
	if a, _ := parseArgs([]string{"10.0.0.5:5000"}); a.cmd != "join" {
		t.Fatal("bare address should join")
	}
	for in, want := range map[string]string{
		"192.168.1.20":              "http://192.168.1.20:4747",
		"192.168.1.20:5000":         "http://192.168.1.20:5000",
		"http://192.168.1.20:4747/": "http://192.168.1.20:4747",
	} {
		if got, err := joinURL(in, 4747); err != nil || got != want {
			t.Fatalf("joinURL(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := parseArgs([]string{"--port", "99999"}); err == nil {
		t.Fatal("bad port accepted")
	}
	for _, s := range skins {
		if a, err := parseArgs([]string{"--skin", s, "--no-color"}); err != nil || a.skin != s || !a.noColor {
			t.Fatalf("--skin %s: %+v %v", s, a, err)
		}
	}
	if _, err := parseArgs([]string{"--skin", "neon"}); err == nil {
		t.Fatal("unknown skin accepted")
	}
}

func TestWrap(t *testing.T) {
	u := &tui{}
	rows := u.wrap(para{parts: []part{{"09:41  ali      ", pMeta, ""}, {"one two three four five six", pText, ""}}}, 40)
	if len(rows) != 2 || rows[1].x != 16 {
		t.Fatalf("rows=%d indent=%d", len(rows), rows[len(rows)-1].x)
	}
	rows = u.wrap(para{parts: []part{{"x ", pMeta, ""}, {strings.Repeat("y", 25), pText, ""}}}, 10)
	for _, r := range rows {
		w := r.x
		for _, c := range r.cells {
			w += c.w
		}
		if w > 10 {
			t.Fatalf("row too wide: %d", w)
		}
	}
	rows = u.wrap(para{parts: []part{{"> ", pMeta, ""}, {"中文字符测试", pText, ""}}}, 8) // double-width
	for _, r := range rows {
		w := r.x
		for _, c := range r.cells {
			w += c.w
		}
		if w > 8 {
			t.Fatalf("wide row too wide: %d", w)
		}
	}
}

// screenText reads the simulated screen from the UI goroutine, between draws.
func screenText(s tcell.SimulationScreen) string {
	ch := make(chan string, 1)
	for s.PostEvent(tcell.NewEventInterrupt(func() { ch <- rawScreenText(s) })) != nil {
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case txt := <-ch:
		return txt
	case <-time.After(2 * time.Second):
		return ""
	}
}

func rawScreenText(s tcell.SimulationScreen) string {
	cells, w, h := s.GetContents()
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if r := cells[y*w+x].Runes; len(r) > 0 {
				b.WriteString(string(r))
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestScreen(t *testing.T) {
	h := testHub(t, "")
	friend := testClient(t, h, "friend", "")
	online(t, friend)

	s := tcell.NewSimulationScreen("UTF-8")
	c := NewClient(ClientOpts{Mode: modeJoin, URL: localURL(h.Port()), Nick: "me", Token: deviceToken("screen")})
	done := make(chan error, 1)
	go func() { done <- runTUI(c, uiOpts{Skin: "plain"}, s) }()
	waitFor(t, "connected", func() bool { return c.State() == stOnline })
	typeText := func(text string) {
		for _, r := range text {
			s.InjectKey(tcell.KeyRune, r, tcell.ModNone)
		}
	}
	shows := func(what string) func() bool {
		return func() bool { return strings.Contains(screenText(s), what) }
	}

	typeText("hi from the terminal")
	s.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, "friend receives it", func() bool { return sees(friend, "#general", "hi from the terminal") })
	friend.Input("got it")
	waitFor(t, "reply on screen", shows("got it"))
	waitFor(t, "prompt", shows("#general> "))

	// Esc: the chat disappears and a shell prompt takes its place.
	s.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	waitFor(t, "decoy", func() bool {
		txt := screenText(s)
		return !strings.Contains(txt, "got it") && !strings.Contains(txt, "#general")
	})
	friend.Input("secret while hidden")
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(screenText(s), "secret") {
		t.Fatal("a message showed up while hidden")
	}
	typeText("pwd")
	s.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	cwd, _ := os.Getwd()
	waitFor(t, "fake shell answers pwd", func() bool { return strings.Contains(strings.ReplaceAll(screenText(s), "\n", ""), cwd) })

	// Esc again: the chat is back, including what arrived meanwhile.
	s.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	waitFor(t, "chat back", shows("secret while hidden"))

	// Ctrl+L clears the view.
	s.InjectKey(tcell.KeyCtrlL, 0, tcell.ModNone)
	waitFor(t, "cleared", func() bool { return !strings.Contains(screenText(s), "got it") })

	s.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Ctrl+C did not quit")
	}
}

func TestPickFile(t *testing.T) {
	all := map[string]string{}
	for _, f := range dlCatalog {
		all[f.Name] = f.Name
	}
	for _, c := range []struct {
		os, arch string
		ver      int
		want     string
	}{
		{"Darwin", "arm64", 15, "nook-mac"},
		{"Darwin", "x86_64", 12, "nook-mac-legacy"},
		{"Darwin", "arm64", 0, "nook-mac"},
		{"Linux", "x86_64", 0, "nook-linux-x64"},
		{"Linux", "aarch64", 0, ""},
		{"windows", "AMD64", 10, "nook-win-x64.exe"},
		{"windows", "AMD64", 6, "nook-win7-x64.exe"},
		{"windows", "x86", 10, "nook-win-x86.exe"},
		{"windows", "x86", 6, "nook-win7-x86.exe"},
		{"windows", "ARM64", 10, "nook-win-arm64.exe"},
		{"plan9", "386", 0, ""},
	} {
		if got := pickFile(all, c.os, c.arch, c.ver); got != c.want {
			t.Errorf("pickFile(%s %s %d) = %q, want %q", c.os, c.arch, c.ver, got, c.want)
		}
	}
	// A host that only has its own thin Apple Silicon build still serves
	// Apple Silicon Macs, and says no to Intel ones.
	thin := map[string]string{"nook-mac-arm64": "x"}
	if pickFile(thin, "Darwin", "arm64", 14) != "nook-mac-arm64" || pickFile(thin, "Darwin", "x86_64", 14) != "" {
		t.Error("thin fallback")
	}
}

func TestInstallers(t *testing.T) {
	h := testHub(t, "")
	base := localURL(h.Port())
	get := func(path, host string) string {
		req, _ := http.NewRequest("GET", base+path, nil)
		if host != "" {
			req.Host = host
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		if r.StatusCode != 200 {
			t.Fatalf("%s: %d", path, r.StatusCode)
		}
		return string(b)
	}
	if sh := get("/install.sh", ""); !strings.Contains(sh, `NOOK_BASE="`+base+`"`) {
		t.Fatal("install.sh doesn't point at this host")
	}
	if ps := get("/install.ps1", ""); !strings.Contains(ps, "$NookBase = '"+base+"'") {
		t.Fatal("install.ps1 doesn't point at this host")
	}
	// A forged Host header must never end up inside the script.
	if sh := get("/install.sh", `x";rm -rf ~;"`); strings.Contains(sh, "rm -rf ~") {
		t.Fatal("Host header injected into the script")
	}
	r, err := http.Get(base + "/dl/auto?os=plan9&arch=386")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatalf("auto download for an unknown system: %d", r.StatusCode)
	}
}
