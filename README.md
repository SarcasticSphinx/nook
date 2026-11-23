# nook

Chat with everyone in the room, with no internet and no Wi-Fi. Mac,
Windows and Linux laptops talk over **Bluetooth**, passing messages on to
each other until everyone has them. When there is a shared Wi-Fi network,
there's a faster **Wi-Fi mode** too, and then people can even join from a
browser. Rooms, direct messages, no accounts. It runs in any terminal (VS
Code's included) and looks like nothing much: press **Esc** and it turns
into an ordinary shell prompt.

```
09:41  rafi     anyone done with question 3?
09:41  tanvir   not yet, stuck on part b
09:42  @rafi rafi   psst, check your DMs
#general> _
```

## Install

Install once, then just type `nook` in any terminal.

| From | Mac / Linux | Windows |
|---|---|---|
| this folder (USB stick, AirDrop...) | `./install.sh` (or double-click `install.command`) | double-click `install.cmd` |
| someone running Wi-Fi mode | `curl -fsSL http://HOST:4747/install.sh \| sh` | `irm http://HOST:4747/install.ps1 \| iex` |

Windows 7/8: `powershell -c "iex (New-Object Net.WebClient).DownloadString('http://HOST:4747/install.ps1')"`

The installer copies the app to `~/.local/share/nook` and links it as
`~/.local/bin/nook` (Windows: `%LOCALAPPDATA%\nook`, added to your PATH for
your account only, no admin needed). Nothing else is installed. Installing
from this folder also copies the builds for every other computer, so when you
host in Wi-Fi mode, everyone can install from you. To remove it:
`./install.sh uninstall` (Windows: `install.cmd uninstall`).

## Quick start

1. **Get nook onto every laptop**: pass this folder around on a USB stick
   (or AirDrop it between Macs) and run the installer.
2. **Turn Bluetooth on and type `nook`.** That's it: laptops nearby find each
   other within a few seconds. Until the first one is found, the prompt says
   `(no one nearby yet)`.
3. On a Mac, the first time, macOS asks whether nook may use Bluetooth:
   click **Allow**.

`nook check` tests Bluetooth on its own and says plainly what works.

### Two modes

| | Bluetooth: `nook` (default) | Wi-Fi: `nook wifi` |
|---|---|---|
| Needs | Bluetooth switched on | one shared Wi-Fi network (no internet needed) |
| Host | none: every laptop passes messages on | one laptop hosts, picked automatically |
| Speed | usually under a second per message | instant |
| Join from a browser | no | yes |
| Direct messages | encrypted (they hop through other laptops) | go through the host, not encrypted |
| History | recent messages, from the laptops near you | kept by the host until it quits |
| Windows 7/8 | no | yes |

In Wi-Fi mode, the first person to run `nook wifi` becomes the host; if two
start at the same moment, the rooms merge by themselves. `nook host` starts
the room on purpose, and anyone without the app can open the host's address
(shown by `/info`) in a browser.

### Trying Wi-Fi mode on one computer

```sh
nook host                # terminal tab 1: start a room
nook wifi --id friend    # terminal tab 2: a second person
```

Then open `http://localhost:4747` in a browser for a third. Bluetooth mode
needs two laptops: one computer can't link to itself.

## Using it

| Key / command | What it does |
|---|---|
| **Esc** | hide: the screen becomes a normal shell prompt (`you@laptop folder %` or `PS C:\...>`). You can type `ls`, `cd`, `pwd`, `clear` in it. Esc again brings the chat back. |
| **Ctrl+C** | quit instantly. The terminal looks exactly as before. Press Up, Enter to come back. |
| **Ctrl+L** / `/clear` | clear the screen (PgUp still scrolls back) |
| **Tab** / Shift+Tab | switch between your rooms and DMs; the prompt shows where you're typing |
| PgUp / PgDn, Up / Down | scroll back, previous messages you typed |
| `/join #room` | join or create a room (`#general` is everyone) |
| `/leave` | leave the current room |
| `/dm name [text]` | private message (a name prefix is enough: `/dm raf hi`) |
| `/r text` | reply to whoever messaged you last |
| `/nick name` | change your name |
| `/who`, `/who #room`, `/rooms` | who's around, what rooms exist |
| `/info` | Bluetooth status, or the Wi-Fi address to share |
| `/dim` | faint text, hard to read from a distance or an angle |
| `/fade 20` | messages disappear from the screen after 20 seconds (`/fade off`) |
| `/skin log` | messages look like program log output (`/skin plain` to undo) |
| `/focus` | show only the chat you're in |
| `/key word` | join a Wi-Fi room that has a password |

Options: `--name NAME`, `--dim`, `--skin log`, `--wipe` (also clear the
terminal's scrollback when quitting), `--plain` (simple line mode for odd
terminals). Wi-Fi mode only: `--key PASSWORD` (lock the room),
`nook join 192.168.0.105` (skip discovery), `nook scan` (search harder when
automatic finding is blocked), `nook serve` (host without a chat screen).
`nook help` lists everything.

The **browser version** (Wi-Fi mode) works the same way: Esc swaps in a page
of study notes (the tab is called "notes" and has no icon), the commands are
the same, `/autohide` hides the chat whenever you click away, and Ctrl+W
closes the tab while Ctrl+Shift+T brings it straight back, still signed in.

### Staying low-key

- Run it in the **VS Code terminal panel**: Ctrl+\` shows and hides the
  whole panel instantly. **Windows Terminal** has a built-in drop-down
  window on Win+\`. On a Mac, Cmd+H hides the app.
- **Rename the file** to something boring (`build`, `devserver.exe`...).
  The name shows up in the terminal tab.
- No colours, no title bar, no sounds, no notifications: new messages never
  make the screen flash or beep, and nothing changes while it's hidden.
- Over Bluetooth, nook advertises no name: only an anonymous service id.
- **Nothing is written to disk.** History lives only in memory.
- For a real *narrow viewing angle*, a cheap **privacy screen filter** does
  what software can't. Lower brightness plus `/dim` gets you part of the way.

## How it works

### Bluetooth mode

```
 Mac ─── Win ─── Mac          each laptop links to a few others;
  │       │       │           every message is passed on, link by link,
 Win ─── Mac ─── Win ── ...   until every laptop has it
```

- **No host.** Every laptop advertises an anonymous Bluetooth Low Energy
  service and connects to up to 4 nooks it hears, picked at random so that
  thirty laptops spread out instead of all piling onto one. Others connect
  to it too.
- **Messages hop.** Each laptop passes every new message on to its other
  links. A message id stops repeats, and a hop limit (7) stops runaways.
  Room messages reach everyone, and only people in the room see them.
- **The same screen as Wi-Fi mode.** Each laptop runs its own private chat
  server that only it can reach, and the mesh keeps it in step with the
  others. That is why every command works the same in both modes.
- **Who's around.** Every laptop says "I'm here" about every 30 seconds.
  Silence for 95 seconds means gone, and quitting says goodbye at once.
- **Catching up.** A laptop that arrives late gets the last 40 room messages
  from the first laptop it links to.
- **Small pieces.** Some Bluetooth links carry only 20 bytes at a time, so
  messages are compressed and cut into numbered pieces, then put back
  together, even when several laptops share one link.
- **Private messages are encrypted** (X25519 key exchange, then AES-GCM)
  for their one recipient, with fresh keys every time nook starts. That stops
  anyone reading along; it does not prove who is who.
- **Windows** laptops can't tell apart the laptops connected to them (a
  limit of the Bluetooth library), so whatever they pass on reaches all of
  those at once. The mesh is built to work with that.
- **Macs** ask for Bluetooth permission per app. nook starts itself as its
  own app, so macOS asks about nook rather than about your terminal.

### Wi-Fi mode

```
 laptop (Mac) ──┐                       browsers just open
 laptop (Win) ──┼── same Wi-Fi ── host ─ http://host:4747
 laptop (Win) ──┘    (any laptop running nook; picked automatically)
```

- **One laptop is the host**: it runs a tiny web server inside the same
  program. Everyone else connects to it, the terminal app and browsers
  alike, over plain HTTP: messages stream down as Server-Sent Events and go
  up as small JSON posts.
- **Finding the host**: `nook wifi` broadcasts a "who's hosting?" packet on
  UDP port 4747 and the host answers. Joiners need no open ports: Windows
  Firewall lets replies to your own broadcast back in for a few seconds, so
  only the host ever sees a firewall prompt.
- **No host? Become one.** If nobody answers, nook waits a random moment
  (longer on Windows, so a Mac tends to win, since a Mac shows no firewall
  prompt) and hosts.
- **Two hosts merge.** Hosts keep listening for each other. The one people
  can actually reach (and otherwise the older one) wins, and the other
  sends its people over.
- **The host leaves?** It tells everyone who should take over (preferring a
  Mac). The rest find the new host within a few seconds and browsers follow
  automatically. Messages from before are lost, on purpose.
- Names are tied to the device, so after quitting and coming back you keep
  your name and DMs, without saving anything to disk.

In both modes each person is limited to 4 messages a second (bursts of 8)
and 1000 characters per message, and control characters and terminal escape
codes are stripped so nobody can mess with your screen.

### Very low-end laptops

- One self-contained file of about 10 MB, with no runtime to install. It uses
  **about 15 MB of RAM** and no CPU while idle.
- The browser version is a single small page with no frameworks, so any
  browser from the last several years works.
- **Old systems:** Windows 7/8 use `nook-win7-x64.exe` / `nook-win7-x86.exe`
  (Wi-Fi mode only); macOS 10.13 to 12 use `nook-mac-legacy`; 32-bit
  Windows 10 uses `nook-win-x86.exe`. Anything older can use the browser.

## The network options we compared

For about 30 laptops with no internet:

| Option | Devices | Verdict |
|---|---|---|
| Bluetooth mesh (nook's default) | any number in one room | **Works with Wi-Fi off.** Each laptop links to a few neighbours and passes messages on. Slower than Wi-Fi. |
| Any Wi-Fi router, no internet cable | 30+ | **Fastest.** Even an old or $15 travel router handles 30 laptops of text chat easily. |
| The room's existing Wi-Fi | many | Works if it lets devices talk to each other. Many campus and guest networks turn on "client isolation", which blocks that. Test it: `nook host` on one laptop, open its address on another. |
| Windows Mobile Hotspot | 8 max | Fine for a small group; Microsoft caps it at 8 devices. |
| Phone hotspot | a handful | Fine for a small group. |
| Plain Bluetooth connections | 7 at a time | Classic Bluetooth connects at most 7 devices to one laptop, which is why nook uses a mesh. |
| Ad-hoc / computer-to-computer Wi-Fi | n/a | Mostly gone from current macOS and Windows drivers. |

## First run and troubleshooting

- **`(no one nearby yet)`** stays in the prompt: the others need nook running
  with Bluetooth on, within a few metres. `nook check` on two laptops shows
  whether they can see each other.
- **Mac Bluetooth popup**: click Allow. If you clicked Don't Allow, switch
  nook on in System Settings > Privacy & Security > Bluetooth. A newly
  built copy of nook asks again.
- **Windows Bluetooth**: switch it on in Settings > Bluetooth & devices.
  Some older Bluetooth chips can't be found by others (`/info` says so); they
  still connect to the laptops around them.
- **Windows firewall window** (Wi-Fi mode): only matters on the host. Tick
  both *Private* and *Public* and click Allow (it needs an admin account). If
  nobody can join a Windows host, a Mac should host instead.
- **Windows won't run it** (SmartScreen or Smart App Control can block apps
  that aren't from a known publisher): click *More info → Run anyway*, or
  use Wi-Fi mode's browser version.
- **Mac "local network" prompt** (Wi-Fi mode): VS Code, iTerm and browsers
  may ask once to "find devices on your local network". Click Allow.
- **Mac "can't be opened"**: happens only if you downloaded with a browser.
  Run `xattr -c nook` once, or use the installer.
- **`nook: command not found`** right after installing: open a new terminal
  window (on Windows, restart VS Code if you use its terminal).
- **Wi-Fi mode can't find the room**: try `nook scan`, or ask the host to
  run `/info` and use `nook join ADDRESS`. If that fails too, the Wi-Fi is
  isolating devices: use a separate router or hotspot.

## What has been tested

- The Bluetooth mesh logic, with simulated Bluetooth: 30 laptops each linked
  to only a few others, Windows-style shared links, 20-byte pieces, long
  messages in Bengali script, late arrivals catching up, people leaving, and
  DMs staying private.
- The Mac Bluetooth driver starts on a Mac and asks for permission. It has
  **not yet been tested between two real laptops**.
- The Windows and Linux Bluetooth driver compiles for every target; it has
  **not yet been run on real hardware**.
- Wi-Fi mode, the browser version, the installers and the full-screen view,
  end to end on a Mac. The Windows builds compile but haven't been run.

## Honest security notes

- Bluetooth mode: room messages are readable by every laptop running nook
  nearby (that's what a room is). DMs are encrypted for their recipient, but
  nothing proves who is who: someone could pretend to be someone else.
- Wi-Fi mode: messages are **not encrypted** on the network, and the host's
  computer can read every message passing through it. `--key` keeps people
  without the password out of the room, but someone capturing Wi-Fi traffic
  could still read it.

## Building

Needs [Go](https://go.dev/dl) 1.25 or newer. Mac builds with Bluetooth need a
Mac with Apple's command line tools (`xcode-select --install`); built
anywhere else, they are Wi-Fi only. Windows and Linux builds work from any
computer.

```sh
./build.sh                                    # everything into dist/
GO_LEGACY=/path/to/go1.20.14/bin/go ./build.sh # plus Windows 7/8 and old-Mac builds
go test -race ./...                           # tests (simulated Bluetooth, loopback Wi-Fi)
go run . wifi --id test                       # try Wi-Fi mode
```

| File | What it is |
|---|---|
| `mesh.go` | Bluetooth mode: passing messages on, catching up, who's around |
| `frame.go`, `dmcrypt.go` | cutting messages into Bluetooth-sized pieces; encrypting DMs |
| `radio.go` | what a Bluetooth driver has to provide |
| `ble_darwin.m`, `ble_darwin.go` | the Mac driver (Apple's CoreBluetooth) |
| `ble_tinygo.go` | the Windows and Linux driver (tinygo.org/x/bluetooth) |
| `btmode.go` | starting Bluetooth mode, and `nook check` |
| `hub.go` | the chat server: rooms, DMs, history; hosting and handing over in Wi-Fi mode |
| `discover.go` | finding Wi-Fi hosts (UDP broadcast) and `nook scan` |
| `client.go` | connection, reconnection and failover, commands |
| `tui.go`, `decoy.go` | the full-screen view and the Esc fake shell |
| `plain.go` | line mode |
| `web/index.html` | the browser version (one file, no dependencies) |
| `dl.go`, `web/get.html` | the download page and one-line installers a Wi-Fi host serves |
| `install.sh`, `install.ps1`, `install.cmd`, `install.command` | the installers |
| `go.legacy.mod` | the older libraries used for the Windows 7/8 and old-Mac builds |
