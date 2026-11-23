# nook

Chat with everyone in the room, with no internet. Windows and Mac laptops
on the same Wi-Fi find each other by themselves: rooms, direct messages,
nothing to install, no accounts. It runs in any terminal (VS Code's
included) or in a plain browser tab, and it looks like nothing much. Press
**Esc** and it turns into an ordinary shell prompt.

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
| a running host, no internet needed | `curl -fsSL http://HOST:4747/install.sh \| sh` | `irm http://HOST:4747/install.ps1 \| iex` |

Windows 7/8: `powershell -c "iex (New-Object Net.WebClient).DownloadString('http://HOST:4747/install.ps1')"`

The installer copies the app to `~/.local/share/nook` and links it as
`~/.local/bin/nook` (Windows: `%LOCALAPPDATA%\nook`, added to your PATH for
your account only, no admin needed). Nothing else is installed. Installing
from this folder also copies the builds for every other computer, so when you
host, everyone can install from you. To remove it: `./install.sh uninstall`
(Windows: `install.cmd uninstall`).

## Quick start

1. **Put everyone on the same Wi-Fi.** No internet is needed, only a network.
   An old or cheap Wi-Fi router with nothing plugged into it works best (see
   [The network](#the-network)).
2. **One person installs from this folder and starts the room:** `nook host`.
   It prints the browser address and the one-line installers for everyone
   else, e.g. `curl -fsSL http://192.168.0.105:4747/install.sh | sh`.
   On Windows, click **Allow** on the firewall window and tick both Private
   and Public.
3. **Everyone else** picks one:
   - **Browser, nothing to install:** open `http://192.168.0.105:4747`.
   - **App:** paste the one-line installer, then type `nook`. It finds the
     room on its own. `http://192.168.0.105:4747/get` lists every option.

If nobody runs `host`, that's fine too: the first person to run `nook`
becomes the host automatically, and if two people start at the same moment
the two rooms merge by themselves.

### Trying it on one computer

```sh
nook host           # terminal tab 1: start a room
nook --id friend    # terminal tab 2: a second person
```

Then open `http://localhost:4747` in a browser for a third. Try `/dm`,
`/join #lab`, Esc, and Ctrl+C on the host tab (the other tab takes over
within a few seconds).

## Using it

| Key / command | What it does |
|---|---|
| **Esc** | hide: the screen becomes a normal shell prompt (`you@laptop folder %` or `PS C:\...>`). You can type `ls`, `cd`, `pwd`, `clear` in it. Esc again brings the chat back. |
| **Ctrl+C** | quit instantly. The terminal looks exactly as before. Press Up, Enter to come back; recent history and your DMs are waiting. |
| **Ctrl+L** / `/clear` | clear the screen (PgUp still scrolls back) |
| **Tab** / Shift+Tab | switch between your rooms and DMs; the prompt shows where you're typing |
| PgUp / PgDn, Up / Down | scroll back, previous messages you typed |
| `/join #room` | join or create a room (`#general` is everyone) |
| `/leave` | leave the current room |
| `/dm name [text]` | private message (a name prefix is enough: `/dm raf hi`) |
| `/r text` | reply to whoever messaged you last |
| `/nick name` | change your name |
| `/who`, `/who #room`, `/rooms` | who's online, what rooms exist |
| `/info` | the address to share, and the download page |
| `/dim` | faint text, hard to read from a distance or an angle |
| `/fade 20` | messages disappear from the screen after 20 seconds (`/fade off`) |
| `/skin log` | messages look like program log output (`/skin plain` to undo) |
| `/focus` | show only the chat you're in |
| `/key word` | join a room that has a password |

Options: `--name NAME`, `--key PASSWORD` (lock the room), `--dim`,
`--skin log`, `--wipe` (also clear the terminal's scrollback when quitting),
`--plain` (simple line mode for odd terminals), `nook join 192.168.0.105`
(skip discovery), `nook scan` (search harder when automatic finding is
blocked), `nook serve` (host without a chat screen). `nook help` lists
everything.

The **browser version** works the same way: Esc swaps in a page of study
notes (the tab is called "notes" and has no icon), the commands are the
same, `/autohide` hides the chat whenever you click away, and Ctrl+W closes
the tab while Ctrl+Shift+T brings it straight back, still signed in.

### Staying low-key

- Run it in the **VS Code terminal panel**: Ctrl+\` shows and hides the
  whole panel instantly. **Windows Terminal** has a built-in drop-down
  window on Win+\`. On a Mac, Cmd+H hides the app.
- **Rename the file** to something boring (`build`, `devserver.exe`...).
  The name shows up in the terminal tab.
- No colours, no title bar, no sounds, no notifications: new messages never
  make the screen flash or beep, and nothing changes while it's hidden.
- **Nothing is written to disk.** History lives only in the host's memory
  and is gone when the host stops.
- For a real *narrow viewing angle*, a cheap **privacy screen filter** does
  what software can't. Lower brightness plus `/dim` gets you part of the way.

## The network

Laptops don't need the internet to talk, only a shared network. What we
found when comparing the options for about 30 laptops:

| Option | Devices | Verdict |
|---|---|---|
| Any Wi-Fi router, no internet cable | 30+ | **Best.** Even an old or $15 travel router handles 30 laptops of text chat easily. |
| The room's existing Wi-Fi | many | Works if it lets devices talk to each other. Many campus and guest networks turn on "client isolation", which blocks that. Test it: `nook host` on one laptop, open its address on another. |
| Windows Mobile Hotspot | 8 max | Fine for a small group; Microsoft caps it at 8 devices. |
| Phone hotspot | a handful | Fine for a small group. |
| Mac Internet Sharing | few | Needs another connection to share; awkward. |
| Ad-hoc / computer-to-computer Wi-Fi | n/a | Mostly gone from current macOS and Windows drivers. |
| Bluetooth | 7 per link group | **Not practical for 30.** Classic Bluetooth connects at most 7 devices at a time, BLE needs different code on every OS (and roles Windows/Mac don't support well), and it's slow with pairing prompts. |
| Ethernet switch | 30+ | Perfect, if your laptops still have Ethernet ports. |

## How it works

```
 laptop (Mac) ──┐                       browsers just open
 laptop (Win) ──┼── same Wi-Fi ── host ─ http://host:4747
 laptop (Win) ──┘    (any laptop running nook; picked automatically)
```

- **One laptop is the host**: it runs a tiny web server inside the same
  program. Everyone else connects to it, the terminal app and browsers
  alike, over plain HTTP: messages stream down as Server-Sent Events and go
  up as small JSON posts.
- **Finding the host**: `nook` broadcasts a "who's hosting?" packet on UDP
  port 4747 and the host answers. Joiners need no open ports: Windows
  Firewall lets replies to your own broadcast back in for a few seconds, so
  only the host ever sees a firewall prompt.
- **No host? Become one.** If nobody answers, `nook` waits a random moment
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
- Each person is limited to 4 messages a second (bursts of 8) and 1000
  characters per message. Control characters and terminal escape codes are
  stripped so nobody can mess with your screen.

### Very low-end laptops

- One self-contained file of about 10 MB, with no runtime to install. It uses
  **about 15 MB of RAM** and no CPU while idle; 30 people is a trivial load
  for the host.
- The browser version is a single small page with no frameworks, so any
  browser from the last several years works.
- **Old systems:** Windows 7/8 use `nook-win7-x64.exe` / `nook-win7-x86.exe`;
  macOS 10.13 to 12 use `nook-mac-legacy`; 32-bit Windows 10 uses
  `nook-win-x86.exe`. Anything older can use the browser.

## First run and troubleshooting

- **Windows firewall window**: only matters on the host. Tick both
  *Private* and *Public* and click Allow (it needs an admin account). If
  nobody can join a Windows host, a Mac should host instead.
- **Windows won't run it** (SmartScreen or Smart App Control can block apps
  that aren't from a known publisher): click *More info → Run anyway*, or
  use the browser version.
- **Mac "local network" prompt**: VS Code, iTerm and browsers may ask once
  to "find devices on your local network". Click Allow. Apple's Terminal
  app doesn't ask.
- **Mac "can't be opened"**: happens only if you downloaded with a browser.
  Run `xattr -c nook` once, or use the `curl` command from the `/get` page.
- **`nook: command not found`** right after installing: open a new terminal
  window (on Windows, restart VS Code if you use its terminal).
- **Can't find the room**: try `nook scan`, or ask the host to run `/info`
  and use `nook join ADDRESS`. If that fails too, the Wi-Fi is isolating
  devices: use a separate router or hotspot.

## Honest security notes

- Messages are **not encrypted** on the network. `--key` keeps people
  without the password out of the room, but someone capturing Wi-Fi traffic
  on the same network could read it.
- The host's computer can read every message passing through it.
- Anyone on the same Wi-Fi can open the host's address, so use `--key` if
  that matters.

## Building

Needs only [Go](https://go.dev/dl) (no C compiler). From any OS:

```sh
./build.sh                                    # everything into dist/
GO_LEGACY=/path/to/go1.20.14/bin/go ./build.sh # plus Windows 7/8 and old-Mac builds
go test -race ./...                           # tests (all on loopback)
go run . --id test                            # try it
```

| File | What it is |
|---|---|
| `hub.go` | the host: rooms, DMs, history, handing over, web endpoints |
| `discover.go` | finding hosts (UDP broadcast) and `nook scan` |
| `client.go` | connection, reconnection and failover, commands |
| `tui.go`, `decoy.go` | the full-screen view and the Esc fake shell |
| `plain.go` | line mode |
| `web/index.html` | the browser version (one file, no dependencies) |
| `dl.go`, `web/get.html` | the download page and one-line installers the host serves |
| `install.sh`, `install.ps1`, `install.cmd`, `install.command` | the installers |
