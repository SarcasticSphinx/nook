# nook

Chat with everyone in the room, with no internet and no Wi-Fi. Mac,
Windows and Linux laptops talk over **Bluetooth**, passing messages on to
each other until everyone has them. When there is a shared Wi-Fi network,
there's a faster **Wi-Fi mode** too, and then people can even join from a
browser. Rooms, direct messages and file sharing, no accounts. It runs in any
terminal (VS Code's included) and looks like nothing much: press **Esc** and
it turns into an ordinary shell prompt.

```
 #general   @rafi 1                              3 here · Tab next chat
09:41  rafi     anyone done with question 3?
09:41  tanvir   not yet, stuck on part b
09:42  rafi     [file] diagram.png · 33 KB  /see 1  /get 1
↳ DM from rafi: psst, the trick is the second loop              Tab
#general> _
```

## Install

Nothing is needed first: no Go, no Homebrew, no admin password. Paste one
line into a terminal:

**Mac and Linux**

```sh
curl -fsSL https://github.com/SarcasticSphinx/nook/releases/latest/download/install.sh | sh
```

**Windows** (Command Prompt or PowerShell, Windows 7 and up)

```bat
powershell -ExecutionPolicy Bypass -c "[Net.ServicePointManager]::SecurityProtocol=3072; iex ((New-Object Net.WebClient).DownloadString('https://github.com/SarcasticSphinx/nook/releases/latest/download/install.ps1'))"
```

Or download `install.cmd` from the [latest release](https://github.com/SarcasticSphinx/nook/releases/latest)
and double-click it: it fetches and installs everything by itself. (If
Windows asks about a downloaded file, click *Run*, or *More info → Run anyway*.)

**No internet at all?**

| From | Mac / Linux | Windows |
|---|---|---|
| a nook folder (USB stick, AirDrop...) | `./install.sh` or double-click `install.command` | double-click `install.cmd` |
| someone running Wi-Fi mode | `curl -fsSL http://HOST:4747/install.sh \| sh` | the line shown on `http://HOST:4747/get` |

The installer copies the app to `~/.local/share/nook` and links it as
`~/.local/bin/nook` (Windows: `%LOCALAPPDATA%\nook`, added to your PATH for
your account only). Nothing else is installed. Installing from a nook folder
also copies the builds for every other computer, so when you host in Wi-Fi
mode, everyone can install from you. To remove it:
`curl -fsSL https://github.com/SarcasticSphinx/nook/releases/latest/download/install.sh | sh -s uninstall`
(from a nook folder: `sh install.sh uninstall`; Windows: `install.cmd uninstall`).

To update, run `nook update`. It checks GitHub for a newer release, downloads
the build for your computer, checks it against the release's `SHA256SUMS`,
and swaps it in. It needs the internet; without it, install again from a
newer nook folder or Wi-Fi host.

## Quick start

1. **Get nook onto every laptop** with one of the lines above, or pass a
   nook folder around on a USB stick.
2. **Turn Bluetooth on and type `nook`.** Laptops nearby find each other
   within a few seconds. Until the first one is found, the prompt says
   `(no one nearby yet)`.
3. On a Mac, the first time, macOS asks whether nook may use Bluetooth:
   click **Allow**.

`nook check` tests Bluetooth on its own and says plainly what works.

### Two modes

| | Bluetooth: `nook` (default) | Wi-Fi: `nook wifi` |
|---|---|---|
| Needs | Bluetooth switched on | one shared Wi-Fi network (no internet needed) |
| Host | none: every laptop passes messages on | one laptop hosts, picked automatically |
| Messages | usually under a second | instant |
| Files | up to 50 MB, slow (tens of KB/s) | up to 2 GB, at Wi-Fi speed |
| Join from a browser | no | yes |
| Direct messages and DM files | encrypted (they hop through other laptops) | go through the host, not encrypted |
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
| **Tab** / Shift+Tab | next / previous chat (the bar at the top shows your rooms and DMs, with unread counts; click one to open it). While you're typing a `/command`, Tab finishes it instead: the command, a name, a room, a skin. |
| **mouse wheel** | scroll up through the chat. At the top, older messages are fetched from the host, back to the very first one. `/mouse` turns this off so you can select text. |
| **Ctrl+G** / `/g` | back to #general from anywhere |
| `/dm name [text]` | private message (a name prefix is enough: `/dm raf hi`) |
| `/r text` | reply to whoever messaged you last, without leaving the chat you're in |
| `/join #room`, `/leave` | join or create a room, leave it (`#general` is everyone) |
| `/who`, `/rooms` | everyone connected, who's away, and their rooms; all rooms |
| `/mode base` | base mode: just the chat, the way nook first looked: no chat bar, no people list, the plain look. `/mode chat` brings it all back (with your skin). `--base` starts in base mode. |
| `/people off` | hide the list of everyone on the right. It's on by default when the window is wide enough: long names wrap onto a second row, a box of shortcuts (`/dm name`, `/help`, `/mode base`) stays at the bottom, and when there are more people than rows the list scrolls under the mouse wheel with a scrollbar. Click someone to DM them. `/people on` brings it back; `--no-people` starts without it. |
| `@name` | mention someone, like Messenger or WhatsApp: type `@` and Tab finishes the name (`@everyone` for the whole room). It's highlighted for them, and if they're in another chat their bar shows `#general @1`. Just writing their name without `@` doesn't count. |
| `/nick name` | change your name: everyone sees "rafi is now known as rafa", and your old messages show the new name |
| `/send path` | share a file with the chat you're in. Or drag the file into the terminal window and press Enter. |
| `/see 3` | look at file 3 without saving it. Text opens inside the window (`q` closes it, `g` saves it). Pictures and PDFs open in a real preview window: Quick Look on a Mac, the picture or PDF viewer on Windows and Linux. The copy it opens is a private temporary file, deleted when the window closes (Mac) or when nook quits. Where no preview window can open, pictures are drawn in the terminal instead. |
| `/get`, `/get 3`, `/files` | download the newest file (or number 3) to your Downloads folder; list the files shared so far |
| `/skin next` | change the look (see below) |
| `/dim`, `/fade 20` | faint text; messages disappear from the screen after 20 seconds (`/fade off`) |
| `/all` | every chat mixed on one screen, the way nook 0.2 showed them (again for one at a time) |
| `/colors`, `/bar` | colours on or off; the chat bar on or off |
| Ctrl+L / `/clear`, PgUp / PgDn, Up / Down | clear the screen, scroll back, previous messages you typed |
| `/info` | Bluetooth status, or the Wi-Fi address to share |
| `/key word` | join a Wi-Fi room that has a password |

`/help` shows all of this in the app.

**Games.** `/play` opens a games menu right in the terminal:

| Game | Alone | With others |
|---|---|---|
| **Snake**: eat, grow, don't crash | ✓ | 2-4, a two-minute free-for-all: bite other snakes to steal their tails, most points wins (Wi-Fi mode only: Bluetooth is too slow for it) |
| **Maze Muncher**: eat every dot, dodge four ghosts, big dots turn the tables | ✓ | |
| **Last Card**: match the colour or symbol, empty your hand first (skip, reverse, +2, wild, wild +4) | ✓ against 3 bots | 2-6, bots can fill seats |
| **Tic-tac-toe** | ✓ against the computer | 2 |
| **Four in a Row**: drop pieces, line up four | ✓ against the computer | 2 |

**Snake with others**: run your head into another snake's body and you bite
off everything from there to its tail, and grow by that much. Bite right
behind its head and you eat it whole. Head to head, the longer snake eats
the shorter (equal: both die). Walls and your own body still kill you. A
snake that dies spills into food and comes back 3 seconds later, small and
blinking: for 2 seconds it can't bite or be bitten. Points: a dot is 1, each
bitten piece 1, eating a whole snake 5 plus half its length. Most points
after two minutes wins.

Games with others happen in a room with a 6-digit code. In the menu, **O**
opens a room for everyone: it's announced in #general (`opened a Last Card
room for everyone · type /play 482913 to join`) and shows up under *open
rooms* in everyone's games menu, so anyone can jump in with Enter. **C** makes
a private room: share the code, and others type `/play 482913` (in the room,
**I** posts the code in your chat). `/play snake` starts a game alone.

The person who opens a room runs the game on their laptop; the others send
their key presses and get the picture back, through the Wi-Fi host or hop by
hop over Bluetooth. What happens when things go wrong:

- **Arriving late or to a full room**: you watch the round being played and
  get a seat in the next one (the host presses Enter for another round).
- **Someone drops off** (laptop asleep, Wi-Fi gone, quit): after a few
  seconds they're marked away. In Last Card a bot plays their cards, a snake
  spills into food until they return, and in the two-player board games the other wins if they're gone
  for 30 seconds. Coming back (same person, even after restarting nook)
  gives them their seat back.
- **Nobody moves**: in a game with others a turn lasts at most 30 seconds
  (Last Card) or 60 (board games); then the computer moves for them.
- **The room's host loses its connection**: the others see "waiting for
  …'s connection" and carry on when it's back; after 25 seconds the room
  closes and everyone is told. When the host leaves on purpose (Q twice),
  everyone is told at once.
- **A wrong code** says so after a few seconds instead of waiting forever.
- Each picture is numbered, so a late or doubled packet never shows an old
  state, and the host repeats it now and then so a lost one heals by itself.

Q leaves a game (twice with others, so it can't happen by accident), Esc
still hides everything (a game alone pauses), and the newest chat message
from elsewhere shows on the bottom row while you play.

**One chat at a time.** Like a messenger, the screen shows only the chat
you're in: a DM never mixes with #general. When something arrives
elsewhere, the chat bar counts it and one row above the input shows the
newest one (`↳ DM from rafi: ...`); Tab or a click takes you there.
`/all` mixes every chat on one screen instead, with labels saying where
each line is from (`DM  rafi`, `DM to rafi`, `#lab`).

**Skins.** `/skin NAME` or `--skin NAME`:

| Skin | Looks like |
|---|---|
| `plain` | the default: `09:41  rafi     anyone done?` |
| `chat` | messages grouped by person, yours on the right, each name in its own soft colour |
| `irc` | `[09:41] <rafi> anyone done?` |
| `compact` | `rafi: anyone done?`, nothing else |
| `log` | `09:41:07.120 INFO  general/rafi: anyone done?` |
| `code` | `   42 \| // rafi: anyone done?` |
| `json` | `{"t":"09:41:07","chat":"#general","from":"rafi","text":"anyone done?"}` |

Options: `--name NAME`, `--dim`, `--skin NAME`, `--no-color` (or the
`NO_COLOR` variable), `--wipe` (also clear the terminal's scrollback when
quitting), `--plain` (simple line mode for odd terminals). Wi-Fi mode only:
`--key PASSWORD` (lock the room), `nook join 192.168.0.105` (skip
discovery), `nook scan` (search harder when automatic finding is blocked),
`nook serve` (host without a chat screen). `nook help` lists everything.

### The browser version (Wi-Fi mode)

Open the host's address in any browser. It has two looks, switched with the
button at the top right or `/ui chat` and `/ui term`:

- **Chat**: a soft, raised messenger look that follows your system's light
  or dark theme. All / Unread / Mentions tabs over the chat list (Mentions
  counts messages that name you); the last message, time and unread count
  for each chat; who's here along the top; bubbles grouped by person, each
  name in its own colour. Instead of initials, everyone's picture is a
  little animal, bird or person (a lion, an owl, a dolphin...), picked from
  their id, so it's the same on every screen and every time they come back;
  "rafi is typing"; picture previews; files by
  dragging them onto the page, pasting, or the paperclip, with an eye
  button to look at one (pictures, PDFs and text) without saving it.
  Typing `@` lists the people in the chat to mention; mentions of you are
  highlighted and counted under Mentions.
  Typing `/` lists the commands. Scrolling up fetches older messages. On a
  phone the chat list is its own screen and a chat slides in over it.
- **Terminal**: the same plain text look as the app.

In both, Tab switches chats (or finishes a `/command`), and Esc swaps the
chat for a little runner game: the classic Chrome "No internet" T-Rex hopping over
cacti and ducking under pterodactyls. Hold Space longer to jump higher, Down ducks, the
score blinks at every 100, and it keeps your best. On a phone, tap to jump
and hold to go back. The tab
is called "Nook" and has no icon. The commands are the same as in the app, `/autohide` hides
the chat whenever you click away, and Ctrl+W closes the tab while
Ctrl+Shift+T brings it straight back, still signed in.

### Staying low-key

- Run it in the **VS Code terminal panel**: Ctrl+\` shows and hides the
  whole panel instantly. **Windows Terminal** has a built-in drop-down
  window on Win+\`. On a Mac, Cmd+H hides the app.
- **Rename the file** to something boring (`build`, `devserver.exe`...).
  The name shows up in the terminal tab.
- Muted colours only (none with `/colors` or `--no-color`), no title bar,
  no sounds, no notifications: new messages never make the screen flash or
  beep, and nothing changes while it's hidden.
- Over Bluetooth, nook advertises no name: only an anonymous service id.
- **Chat history is never written to disk**; it lives only in memory. Files
  are saved only when you `/get` them; `/see` keeps them in memory only. A Wi-Fi host keeps shared files in a
  temporary folder that it deletes when it quits.
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
  A rename is announced straight away, so it reaches everyone.
- **Catching up.** A laptop that arrives late gets the last 40 room messages
  from the first laptop it links to.
- **Small pieces.** Some Bluetooth links carry only 20 bytes at a time, so
  messages are compressed and cut into numbered pieces, then put back
  together, even when several laptops share one link.
- **Files** are announced like messages, but the file itself only travels
  when someone asks for it, and only along the path between the two laptops
  (the way the announcement came), so a download doesn't slow down the
  whole room. It's sent in 2 KB pieces, 16 of them on the way at once;
  missing pieces are asked for again, and a SHA-256 checksum checks the whole
  file at the end.
- **Private messages and DM files are encrypted** (X25519 key exchange, then
  AES-GCM) for their one recipient, with fresh keys every time nook starts.
  That stops anyone reading along; it does not prove who is who.
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
- **Files** go up to the host once and everyone downloads them from there.
  If the connection drops in the middle, the download carries on from where
  it stopped instead of starting over. A file shared in a DM can only be
  fetched by the two people in it. The host keeps files for 3 hours (8 GB
  at most).
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
  automatically. If the host just restarts, browsers reconnect where they
  are and keep their names. Messages from before are lost, on purpose.
- Names are tied to the device, so after quitting and coming back you keep
  your name and DMs, without saving anything to disk.

In both modes each person is limited to 4 messages a second (bursts of 8)
and 1000 characters per message, and control characters and terminal escape
codes are stripped so nobody can mess with your screen.

### Making file transfer fast: what we found

- **Wi-Fi is the fast path.** Shared files go through the host at Wi-Fi
  speed: megabytes per second. For anything bigger than a few MB, use Wi-Fi
  mode (a phone hotspot or any spare router works, no internet needed).
- **Bluetooth Low Energy is slow by nature.** The radio sends at 1 or 2
  Mbit/s, but apps get far less after protocol overhead, connection timing
  and small packets. Even tuned lab setups (2M PHY, 251-byte packets) top
  out around 100 to 170 KB/s, and laptops talking to each other get less.
  ([Memfault](https://interrupt.memfault.com/blog/ble-throughput-primer),
  [Nordic](https://docs.nordicsemi.com/r/bundle/sds_s140/page/sds/s1xx/ble_data_throughput/ble_data_throughput.html),
  [Novel Bits](https://novelbits.io/bluetooth-5-speed-maximum-throughput/))
- **What nook does about it:**
  - writes without waiting for an acknowledgement per packet ("write
    without response"), letting the system say when it's ready for more
    ([`canSendWriteWithoutResponse`](https://developer.apple.com/documentation/corebluetooth/cbperipheral/cansendwritewithoutresponse)
    on Macs), in the largest packets each link allows;
  - keeps 16 pieces in flight, so the link never sits idle waiting for a
    round trip, and asks again only for the pieces that went missing;
  - sends file pieces as raw bytes, with none of the 33% extra that
    encoding them as text would add;
  - routes pieces only along the path between sender and receiver.
- **What would be faster, and why nook doesn't use it.** L2CAP channels skip
  some of Bluetooth's overhead and Macs support them, but Windows gives apps
  no way to open them (apps only get GATT and classic RFCOMM:
  [Microsoft](https://learn.microsoft.com/en-us/windows/uwp/devices-sensors/bluetooth-low-energy-overview),
  [Stack Overflow](https://stackoverflow.com/questions/66391397/use-ble-l2cap-coc-on-windows)),
  so a mixed Mac and Windows room can't rely on them. Classic Bluetooth
  needs every pair of laptops to be paired first, and Apple's and
  Microsoft's direct Wi-Fi links (AWDL, Wi-Fi Direct) don't talk to each
  other.
- **What to expect over Bluetooth:** a 1 MB file in roughly 20 seconds to a
  few minutes, depending on the laptops and how many hops apart they are.
  These are estimates: Bluetooth file speed hasn't been measured on real
  laptops yet.

### Very low-end laptops

- One self-contained file of about 10 MB, with no runtime to install. It uses
  **about 15 MB of RAM** and no CPU while idle.
- The browser version is a single small page with no frameworks, so any
  browser from the last several years works.
- **Old systems:** Windows 7/8 use `nook-win7-x64.exe` / `nook-win7-x86.exe`
  (Wi-Fi mode only); macOS 10.13 to 12 use `nook-mac-legacy`; 32-bit
  Windows 10 uses `nook-win-x86.exe`. Anything older can use the browser.
  The installers pick the right one by themselves.

## The network options we compared

For about 30 laptops with no internet:

| Option | Devices | Verdict |
|---|---|---|
| Bluetooth mesh (nook's default) | any number in one room | **Works with Wi-Fi off.** Each laptop links to a few neighbours and passes messages on. Slower than Wi-Fi, especially for files. |
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
- **Mac "can't be opened"**: happens only if you downloaded the app itself
  with a browser. Use the one-line installer instead.
- **`nook: command not found`** right after installing: open a new terminal
  window (on Windows, restart VS Code if you use its terminal).
- **Wi-Fi mode can't find the room**: try `nook scan`, or ask the host to
  run `/info` and use `nook join ADDRESS`. If that fails too, the Wi-Fi is
  isolating devices: use a separate router or hotspot.
- **A file is "too big for Bluetooth"**: the limit is 50 MB. Use Wi-Fi mode
  for it.

## What has been tested

- The Bluetooth mesh logic, with simulated Bluetooth: 30 laptops each linked
  to only a few others, Windows-style shared links, 20-byte pieces, long
  messages in Bengali script, late arrivals catching up, people leaving,
  renames reaching everyone, and DMs staying private.
- Bluetooth file sharing, simulated: across 3 hops, over a Windows-style
  shared 20-byte link, a DM file staying private, and a link that loses 3%
  of its pieces. Every file arrived complete, checked by its checksum.
- Wi-Fi file sharing for real over loopback: an 8 MB file, a download cut
  off twice that carries on without sending anything twice, and DM files
  that nobody else can fetch.
- The browser version in a real browser: both looks, DMs, renames, files by
  drag and drop, image previews, the typing indicator, the Esc page, a phone
  sized screen, and the host restarting.
- The Mac Bluetooth driver starts on a Mac and asks for permission. It has
  **not yet been tested between two real laptops**.
- The Windows and Linux Bluetooth driver compiles for every target; it has
  **not yet been run on real hardware**.
- Wi-Fi mode, the installers and the full-screen view, end to end on a Mac.
  The Windows builds compile but haven't been run.

## Honest security notes

- Bluetooth mode: room messages and room files can be read by every laptop
  running nook nearby (that's what a room is). DMs and DM files are encrypted
  for their recipient, but nothing proves who is who: someone could pretend
  to be someone else.
- Wi-Fi mode: messages and files are **not encrypted** on the network, and
  the host's computer can read everything passing through it. `--key` keeps
  people without the password out of the room, but someone capturing Wi-Fi
  traffic could still read it.
- Downloaded files are saved as files, never opened or run by nook. Treat
  them like any file someone hands you.

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
| `meshfiles.go` | Bluetooth file sharing: offers, routing, pieces, retries, checksums |
| `frame.go`, `dmcrypt.go` | cutting messages into Bluetooth-sized pieces; encrypting DMs |
| `radio.go` | what a Bluetooth driver has to provide |
| `ble_darwin.m`, `ble_darwin.go` | the Mac driver (Apple's CoreBluetooth) |
| `ble_tinygo.go` | the Windows and Linux driver (tinygo.org/x/bluetooth) |
| `btmode.go` | starting Bluetooth mode, and `nook check` |
| `hub.go` | the chat server: rooms, DMs, history; hosting and handing over in Wi-Fi mode |
| `files.go` | the Wi-Fi host's file storage: uploads, downloads, clean-up |
| `transfer.go` | `/send`, `/get`, `/see` and `/files`, with progress and speed |
| `viewer.go` | `/see`: text and pictures inside the terminal |
| `preview.go` | `/see`: pictures and PDFs in a real preview window |
| `complete.go` | Tab completion |
| `discover.go` | finding Wi-Fi hosts (UDP broadcast) and `nook scan` |
| `client.go` | connection, reconnection and failover, commands, `/who` and `/help` |
| `format.go` | how a message looks in each skin |
| `tui.go`, `decoy.go` | the full-screen view (chat bar, people list) and the Esc fake shell |
| `plain.go` | line mode |
| `web/index.html` | the browser version, both looks (one file, no dependencies) |
| `dl.go`, `web/get.html` | the download page and one-line installers a Wi-Fi host serves |
| `install.sh`, `install.ps1`, `install.cmd`, `install.command` | the installers |
| `go.legacy.mod` | the older libraries used for the Windows 7/8 and old-Mac builds |
