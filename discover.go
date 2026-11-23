package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// discover shouts "is anyone hosting?" on every local network and collects
// the answers, best hub first.
//
// It needs no open ports on this computer: Windows Firewall lets unicast
// replies to our own broadcast back in for a few seconds, so people who
// only join never get a firewall prompt for this.
func discover(port int, wait time.Duration) []HubInfo {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil
	}
	defer conn.Close()

	targets := discoveryTargets(port)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for i := 1; i <= 3; i++ { // UDP can drop packets; ask a few times
			for _, t := range targets {
				_, _ = conn.WriteToUDP([]byte(qMagic), t)
			}
			select {
			case <-stop:
				return
			case <-time.After(time.Duration(i*200) * time.Millisecond):
			}
		}
	}()

	found := map[string]*HubInfo{}
	var order []string
	buf := make([]byte, 2048)
	deadline := time.Now().Add(wait)
	for {
		_ = conn.SetReadDeadline(deadline)
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, net.ErrClosed) || time.Now().After(deadline) {
				break
			}
			continue
		}
		if n <= len(rMagic) || string(buf[:len(rMagic)]) != rMagic {
			continue
		}
		var hi HubInfo
		if json.Unmarshal(buf[len(rMagic):n], &hi) != nil || hi.ID == "" || hi.Port <= 0 || hi.Port > 65535 {
			continue
		}
		f := found[hi.ID]
		if f == nil {
			cp := hi
			f = &cp
			found[hi.ID] = f
			order = append(order, hi.ID)
			// Someone answered: give other hubs a short moment, then stop.
			if soon := time.Now().Add(300 * time.Millisecond); soon.Before(deadline) {
				deadline = soon
			}
		} else {
			f.Users, f.Remote = hi.Users, hi.Remote
		}
		ip := from.IP.String()
		if from.IP.IsLoopback() {
			f.IP = ip // same computer: loopback is the surest way in
		} else {
			f.LAN = ip
			if f.IP == "" {
				f.IP = ip
			}
		}
	}
	out := make([]HubInfo, 0, len(order))
	for _, id := range order {
		out = append(out, *found[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return better(out[i], out[j]) })
	return out
}

// discoveryTargets is where discovery questions go: this computer, the
// all-networks broadcast address, and each local network's own broadcast
// address (more reliable on computers with several network adapters).
// Tests replace it to stay on loopback.
var discoveryTargets = func(port int) []*net.UDPAddr {
	targets := []*net.UDPAddr{
		{IP: net.IPv4(127, 0, 0, 1), Port: port},
		{IP: net.IPv4bcast, Port: port},
	}
	for _, b := range broadcastAddrs() {
		targets = append(targets, &net.UDPAddr{IP: b, Port: port})
	}
	return targets
}

// scan is the fallback for networks that block broadcasts: it knocks on
// port `port` of every address in this computer's /24 network(s).
func scan(port int, timeout time.Duration) []HubInfo {
	cl := &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		found = map[string]HubInfo{}
		sem   = make(chan struct{}, 64)
		nets  = map[string]bool{}
	)
	for _, ip := range lanIPs() {
		if ip.IsLinkLocalUnicast() {
			continue
		}
		prefix := fmt.Sprintf("%d.%d.%d.", ip[0], ip[1], ip[2])
		if nets[prefix] {
			continue
		}
		nets[prefix] = true
		for i := 1; i < 255; i++ {
			addr := prefix + strconv.Itoa(i)
			wg.Add(1)
			sem <- struct{}{}
			go func(addr string) {
				defer wg.Done()
				defer func() { <-sem }()
				resp, err := cl.Get("http://" + net.JoinHostPort(addr, strconv.Itoa(port)) + "/ping")
				if err != nil {
					return
				}
				defer resp.Body.Close()
				var hi HubInfo
				if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&hi) != nil || hi.ID == "" || hi.Port <= 0 {
					return
				}
				hi.IP, hi.LAN = addr, addr
				mu.Lock()
				if _, ok := found[hi.ID]; !ok {
					found[hi.ID] = hi
				}
				mu.Unlock()
			}(addr)
		}
	}
	wg.Wait()
	out := make([]HubInfo, 0, len(found))
	for _, h := range found {
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool { return better(out[i], out[j]) })
	return out
}
