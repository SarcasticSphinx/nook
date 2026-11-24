// nook is a tiny chat for the people in one room, with no internet: over
// Bluetooth by default, or over a shared Wi-Fi network, where anyone without
// the app can join from a web browser. One small program per computer; it
// finds the others by itself.
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

var version = "0.4.0"

//go:embed web/index.html
var indexHTML string

//go:embed web/get.html
var getHTML string

var getTmpl = template.Must(template.New("get").Parse(getHTML))

const usage = `nook: chat with the people around you. No internet or Wi-Fi needed.

  nook                 chat over Bluetooth with the laptops nearby (default)
  nook check           test Bluetooth on this laptop
  nook wifi            chat over a shared Wi-Fi network instead: faster, and
                       people can join from a web browser
  nook host            Wi-Fi: start the room on this computer (keep it open)
  nook serve           Wi-Fi: host only, without the chat screen
  nook join ADDRESS    Wi-Fi: join a host directly, e.g. nook join 192.168.1.20
  nook scan            Wi-Fi: search harder when automatic finding is blocked

options
  -n, --name NAME      your name (default: your login name)
  -k, --key WORD       Wi-Fi room password: only people who have it can join
  -p, --port N         Wi-Fi network port (default 4747)
      --plain          simple line mode instead of the full screen
      --dim            start with faint text
      --skin NAME      plain chat irc compact log code json (try /skin next)
      --no-color       no colours at all (so does the NO_COLOR variable)
      --no-people      start without the people list on the right
      --wipe           clear the terminal when you quit
      --id WORD        a second person on the same computer (testing)

while chatting
  Esc       hide: swaps in a normal shell prompt; Esc again to come back
  Ctrl+C    quit at once        Ctrl+L   clear the screen
  Tab       next chat (Shift+Tab: previous; while typing a /command: finish it)
  Ctrl+G    back to #general    mouse wheel: scroll up the chat
  /join #room  /leave  /dm NAME [text]  /r text  /nick NAME  /g
  /send FILE  /see N  /get N  /files   (or drag a file into the window)
  /who  /people  /rooms  /info  /dim  /fade 30  /skin next  /help

install it once, then just type nook:
  from the nook folder   Mac/Linux: ./install.sh    Windows: double-click install.cmd
  from the internet      curl -fsSL https://github.com/SarcasticSphinx/nook/releases/latest/download/install.sh | sh
  from a Wi-Fi host      curl -fsSL http://HOST:4747/install.sh | sh   (Windows: see http://HOST:4747/get)

no app? in Wi-Fi mode, open the address from /info in any browser.
`

type args struct {
	cmd, arg            string
	name, key, skin, id string
	port                int
	plain, dim, wipe    bool
	wifi, noColor       bool
	noPeople            bool
}

func parseArgs(in []string) (args, error) {
	a := args{cmd: "bt", port: defaultPort, skin: "plain"}
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
			if a.skin, err = need(); err == nil && !validSkin(a.skin) {
				err = errors.New("skins: " + strings.Join(skins, ", "))
			}
		case "no-people":
			a.noPeople = true
		case "no-color", "no-colour", "nocolor", "nocolour":
			a.noColor = true
		case "plain", "line":
			a.plain = true
		case "dim", "ghost":
			a.dim = true
		case "wipe":
			a.wipe = true
		case "wifi", "lan":
			a.wifi = true
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
	if a.wifi && a.cmd == "bt" {
		a.cmd = "wifi"
	}
	if a.cmd == "help" || a.cmd == "version" || len(pos) == 0 {
		return a, nil
	}
	switch pos[0] {
	case "host", "serve", "scan", "help", "version", "check", "wifi":
		a.cmd = pos[0]
	case "bt", "bluetooth":
		a.cmd = "bt"
	case "lan":
		a.cmd = "wifi"
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
	if a.cmd == "bt" || a.cmd == "check" {
		if code, ok := ownBluetooth(); ok {
			os.Exit(code)
		}
	}
	if a.cmd == "check" {
		btCheck()
		return
	}
	name := a.name
	if name == "" {
		name = defaultName(a.id)
	}
	ui := uiOpts{Skin: a.skin, Ghost: a.dim, Wipe: a.wipe, NoPeople: a.noPeople, NoColor: a.noColor || os.Getenv("NO_COLOR") != ""}
	noBluetooth := false
	if a.cmd == "bt" {
		if runBluetooth(a, name, ui) {
			return
		}
		noBluetooth = true // this build has none: use Wi-Fi mode
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
	if noBluetooth {
		c.Sys("this copy of nook has no Bluetooth, so it's using Wi-Fi mode")
	}
	runUI(c, ui, a.plain)
}

// runUI shows the chat full screen, or as plain lines where that can't work.
func runUI(c *Client, ui uiOpts, plain bool) {
	if plain || !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
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
		fmt.Printf("            powershell -ExecutionPolicy Bypass -c \"iex ((New-Object Net.WebClient).DownloadString('%s/install.ps1'))\"   (Windows)\n", u)
		fmt.Printf("            then: nook wifi      all options: %s/get\n", u)
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
