// nook is a tiny chat for people on the same Wi-Fi, with no internet and
// nothing to install. One small program per computer; it finds the others by
// itself. Anyone without it can join from a web browser.
package main

import (
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

var version = "0.1.0"

//go:embed web/index.html
var indexHTML string

//go:embed web/get.html
var getHTML string

var getTmpl = template.Must(template.New("get").Parse(getHTML))

const usage = `nook: chat with the people around you over the same Wi-Fi. No internet needed.

  nook                 join the room on this network, or start one
  nook host            start the room on this computer (keep it open)
  nook serve           host only, without the chat screen
  nook join ADDRESS    join a host directly, e.g. nook join 192.168.1.20
  nook scan            search harder when automatic finding is blocked

options
  -n, --name NAME      your name (default: your login name)
  -k, --key WORD       room password: only people who have it can join
  -p, --port N         network port (default 4747)
      --plain          simple line mode instead of the full screen
      --dim            start with faint text
      --skin log       make messages look like log output
      --wipe           clear the terminal when you quit
      --id WORD        a second person on the same computer (testing)

while chatting
  Esc       hide: swaps in a normal shell prompt; Esc again to come back
  Ctrl+C    quit at once        Ctrl+L   clear the screen
  Tab       next room / DM      PgUp/PgDn  scroll back
  /join #room  /leave  /dm NAME [text]  /r text  /nick NAME
  /who  /rooms  /info  /dim  /fade 30  /skin log  /focus  /help

install it once, then just type nook:
  from the nook folder   Mac/Linux: ./install.sh    Windows: double-click install.cmd
  from a running host    curl -fsSL http://HOST:4747/install.sh | sh
                         irm http://HOST:4747/install.ps1 | iex   (Windows PowerShell)

no app? open the address from /info in any browser on the same Wi-Fi.
`

type args struct {
	cmd, arg            string
	name, key, skin, id string
	port                int
	plain, dim, wipe    bool
}

func parseArgs(in []string) (args, error) {
	a := args{cmd: "auto", port: defaultPort, skin: "plain"}
	var pos []string
	for i := 0; i < len(in); i++ {
		s := in[i]
		if s == "--" {
			pos = append(pos, in[i+1:]...)
			break
		}
		if !strings.HasPrefix(s, "-") || s == "-" {
			pos = append(pos, s)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(s, "-"), "=")
		need := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(in) {
				return "", fmt.Errorf("%s needs a value", s)
			}
			i++
			return in[i], nil
		}
		var err error
		switch name {
		case "n", "name":
			a.name, err = need()
		case "k", "key":
			a.key, err = need()
		case "id":
			a.id, err = need()
		case "p", "port":
			var v string
			if v, err = need(); err == nil {
				if a.port, err = strconv.Atoi(v); err != nil || a.port < 1 || a.port > 65535 {
					err = errors.New("the port must be a number from 1 to 65535")
				}
			}
		case "skin":
			if a.skin, err = need(); err == nil && a.skin != "plain" && a.skin != "log" {
				err = errors.New("skins: plain, log")
			}
		case "plain", "line":
			a.plain = true
		case "dim", "ghost":
			a.dim = true
		case "wipe":
			a.wipe = true
		case "h", "help":
			a.cmd = "help"
		case "v", "version":
			a.cmd = "version"
		default:
			return a, fmt.Errorf("unknown option %s", s)
		}
		if err != nil {
			return a, err
		}
	}
	if a.cmd == "help" || a.cmd == "version" || len(pos) == 0 {
		return a, nil
	}
	switch pos[0] {
	case "host", "serve", "scan", "help", "version":
		a.cmd = pos[0]
	case "join":
		if len(pos) < 2 {
			return a, errors.New("join needs an address, e.g. nook join 192.168.1.20")
		}
		a.cmd, a.arg = "join", pos[1]
	default:
		if strings.ContainsAny(pos[0], ".:") { // a bare address means join
			a.cmd, a.arg = "join", pos[0]
		} else {
			return a, fmt.Errorf("unknown command %q", pos[0])
		}
	}
	return a, nil
}

// joinURL turns "192.168.1.20", "192.168.1.20:4747" or a pasted
// "http://192.168.1.20:4747/" into the hub's base URL.
func joinURL(s string, port int) (string, error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("can't understand the address %q", s)
	}
	p := u.Port()
	if p == "" {
		p = strconv.Itoa(port)
	}
	return "http://" + net.JoinHostPort(u.Hostname(), p), nil
}

// deviceToken identifies this computer to the hub without storing anything
// on disk: come back after quitting and you keep your name and your DMs.
func deviceToken(seed string) string {
	host, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	return hashHex("nook-device|"+host+"|"+osUser()+"|"+home+"|"+seed, 32)
}

func defaultName(id string) string {
	n := cleanNick(osUser())
	if n == "" {
		n = "anon"
	}
	if id != "" {
		n = cleanNick(id)
	}
	return n
}

func die(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "nook: "+format+"\n", a...)
	os.Exit(1)
}

func main() {
	a, err := parseArgs(os.Args[1:])
	if err != nil {
		die("%v\n(run: nook help)", err)
	}
	switch a.cmd {
	case "help":
		fmt.Print(usage)
		return
	case "version":
		fmt.Printf("nook %s (%s, %s/%s)\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return
	case "serve":
		serve(a)
		return
	}
	name := a.name
	if name == "" {
		name = defaultName(a.id)
	}
	co := ClientOpts{Mode: modeAuto, Port: a.port, Key: a.key, Nick: name, Token: deviceToken(a.id)}
	switch a.cmd {
	case "host":
		co.Mode = modeHost
	case "join":
		if co.URL, err = joinURL(a.arg, a.port); err != nil {
			die("%v", err)
		}
		co.Mode = modeJoin
	case "scan":
		fmt.Fprintln(os.Stderr, "looking for a room on this network...")
		hubs := discover(a.port, 1500*time.Millisecond)
		if len(hubs) == 0 {
			hubs = scan(a.port, 800*time.Millisecond)
		}
		if len(hubs) == 0 {
			die("no room found. Ask the host to run /info and then: nook join ADDRESS")
		}
		co.Mode, co.URL = modeJoin, hubs[0].URL()
		fmt.Fprintln(os.Stderr, "found", co.URL)
	}
	c := NewClient(co)
	ui := uiOpts{Skin: a.skin, Ghost: a.dim, Wipe: a.wipe}
	if a.plain || !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		runPlain(c, ui)
		return
	}
	if err := runTUI(c, ui, nil); err != nil {
		fmt.Fprintf(os.Stderr, "nook: full screen not available here (%v), using line mode\n", err)
		runPlain(c, ui)
	}
}

// serve hosts without a chat screen and prints only how many are online:
// never any message text.
func serve(a args) {
	h, err := StartHub(HubOpts{Port: a.port, UDPPort: a.port, Prio: 1, Key: a.key})
	if err != nil {
		die("can't host: %v", err)
	}
	fmt.Printf("nook %s · hosting · Ctrl+C to stop\n", version)
	urls := h.URLs()
	if len(urls) == 0 {
		fmt.Printf("  not on a network yet: join the Wi-Fi (listening on port %d)\n", h.Port())
	}
	for _, u := range urls {
		fmt.Printf("  browser:  %s\n", u)
	}
	if len(urls) > 0 {
		u := urls[0]
		fmt.Printf("  install:  curl -fsSL %s/install.sh | sh     (Mac, Linux)\n", u)
		fmt.Printf("            irm %s/install.ps1 | iex         (Windows PowerShell)\n", u)
		fmt.Printf("            all options: %s/get\n", u)
	}
	if a.key != "" {
		fmt.Println("  locked: people need --key (or type the key in the browser)")
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	last := -1
	for {
		select {
		case <-sig:
			fmt.Println()
			h.Close("")
			return
		case <-t.C:
			if n := h.Online(); n != last {
				last = n
				fmt.Printf("\r  %d online   ", n)
			}
		}
	}
}
