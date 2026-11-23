package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultPort = 4747
	maxText     = 1000
	maxNick     = 16
	maxRoom     = 24
)

// quietLog swallows net/http's internal error log so it can never scribble
// over the full-screen UI.
var quietLog = log.New(io.Discard, "", 0)

func nowMs() int64 { return time.Now().UnixMilli() }

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func hashHex(s string, n int) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:n]
}

// lockHash lets hubs and clients compare room passwords without sending
// them around in discovery packets.
func lockHash(key string) string {
	if key == "" {
		return ""
	}
	return hashHex("nook-lock|"+key, 12)
}

// cleanText makes user text safe to show in any terminal or page: one line,
// no control characters or bidi overrides, trimmed, at most maxText runes.
func cleanText(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			r = ' '
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
			continue
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0xfeff:
			continue
		}
		b.WriteRune(r)
		if n++; n >= maxText {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

func nickRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
		r == '_' || r == '-' || r == '.'
}

// cleanNick keeps names to plain ASCII so nobody can spoof someone else with
// look-alike letters.
func cleanNick(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimPrefix(strings.TrimSpace(s), "@") {
		if b.Len() >= maxNick {
			break
		}
		if nickRune(r) {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), ".-")
}

func cleanRoom(s string) string {
	s = strings.TrimLeft(strings.ToLower(strings.TrimSpace(s)), "#")
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= maxRoom {
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func validTok(t string) bool {
	if len(t) < 16 || len(t) > 80 {
		return false
	}
	for _, r := range t {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func localURL(port int) string { return "http://127.0.0.1:" + strconv.Itoa(port) }

// lanIPs lists this computer's IPv4 addresses, private-network ones first.
func lanIPs() []net.IP {
	var out, linkLocal []net.IP
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil {
				continue
			}
			if ip.IsLinkLocalUnicast() {
				linkLocal = append(linkLocal, ip)
				continue
			}
			out = append(out, ip)
		}
	}
	if len(out) == 0 {
		return linkLocal
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].IsPrivate() && !out[j].IsPrivate() })
	return out
}

// broadcastAddrs returns the directed broadcast address of every IPv4
// network this computer is on (e.g. 192.168.1.255).
func broadcastAddrs() []net.IP {
	var out []net.IP
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || ifc.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || ip.IsLinkLocalUnicast() {
				continue
			}
			m := ipn.Mask
			if len(m) == net.IPv6len {
				m = m[12:]
			}
			if len(m) != net.IPv4len {
				continue
			}
			b := make(net.IP, net.IPv4len)
			for i := range b {
				b[i] = ip[i] | ^m[i]
			}
			out = append(out, b)
		}
	}
	return out
}

func hostOf(addr string) string {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return h
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func osUser() string {
	for _, k := range []string{"USER", "USERNAME", "LOGNAME"} {
		if v := os.Getenv(k); v != "" {
			if i := strings.LastIndexAny(v, `\/`); i >= 0 {
				v = v[i+1:]
			}
			return v
		}
	}
	return ""
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func without(list []string, s string) []string {
	out := list[:0:0]
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

func imax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func imin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
