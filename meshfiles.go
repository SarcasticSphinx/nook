package main

import (
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Files over Bluetooth. Bluetooth is slow (tens of KB/s at best), so a file
// must never be flooded to everyone the way chat is:
//
//  1. the sender floods only a small offer (name, size, checksum);
//  2. each laptop remembers which link the offer arrived on: that is the
//     way back to the sender;
//  3. whoever types /get sends a request back along that path, and each
//     laptop it passes remembers the way to the requester;
//  4. the sender answers with numbered 2 KB pieces that travel only along
//     that path. 16 pieces are kept in flight, missing ones are asked for
//     again, and the whole file is checked (SHA-256) at the end.
//
// Chat always goes first: pieces wait in the slow lane of every link.
// DM files (name and contents) are encrypted for the one recipient.

const (
	chunkSize  = 2048
	fileWindow = 16       // pieces in flight per download
	maxBTFile  = 50 << 20 // Bluetooth is slow: keep files reasonable
	chunkHdr   = 1 + 8 + 6 + 4
)

type route struct {
	link int64
	at   time.Time
}

type offer struct {
	from string
	n    int
	size int64
	sum  string
	dm   bool
	pub  string // the sender's key (DM files)
}

type outFile struct {
	path, name, to, fid, sum string
	size                     int64
	n                        int
	aead                     cipher.AEAD // DM files
	reply                    chan error
}

type inFile struct {
	fi       FileInfo
	from     string
	dest     string
	progress func(done, total int64)
	reply    chan error

	f      *os.File
	off    *offer
	aead   cipher.AEAD
	have   []bool
	count  int
	asked  map[int]time.Time
	next   int
	tries  int
	shown  time.Time
	direct bool
}

type meshFiles struct {
	mine    map[string]*outFile
	getting map[string]*inFile
	offers  map[string]*offer
	toward  map[string]route // file id -> the link toward its sender
	back    map[string]route // file id|requester -> the link toward them
}

func (x *meshFiles) init() {
	if x.mine == nil {
		x.mine, x.getting, x.offers = map[string]*outFile{}, map[string]*inFile{}, map[string]*offer{}
		x.toward, x.back = map[string]route{}, map[string]route{}
	}
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func chunkNonce(fid string, idx int) []byte {
	s := sha256.Sum256([]byte(fmt.Sprintf("nook-piece|%s|%d", fid, idx)))
	return s[:12]
}

// ---- fileBackend: called from the chat, outside the mesh loop ----

// Send shares a file over Bluetooth (an offer; pieces go to whoever asks).
func (m *Mesh) Send(path, to string, progress func(done, total int64)) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Size() > maxBTFile {
		return fmt.Errorf("too big for Bluetooth (limit %s): use Wi-Fi mode for big files", humanSize(maxBTFile))
	}
	sum, err := fileSum(path)
	if err != nil {
		return err
	}
	o := &outFile{path: path, name: cleanFileName(filepath.Base(path)), size: st.Size(), to: to,
		fid: randHex(8), sum: sum, reply: make(chan error, 1)}
	o.n = int((o.size + chunkSize - 1) / chunkSize)
	m.postWait(meshEv{kind: evSendFile, send: o})
	select {
	case err = <-o.reply:
	case <-m.quit:
		return errors.New("closing")
	}
	if err == nil {
		progress(o.size, o.size)
	}
	return err
}

// Fetch downloads a file someone shared over Bluetooth.
func (m *Mesh) Fetch(fi *FileInfo, from, dest string, progress func(done, total int64)) error {
	in := &inFile{fi: *fi, from: from, dest: dest, progress: progress, reply: make(chan error, 1)}
	m.postWait(meshEv{kind: evFetch, fetch: in})
	select {
	case err := <-in.reply:
		return err
	case <-m.quit:
		return errors.New("closing")
	}
}

// ---- inside the mesh loop ----

func (m *Mesh) shareFile(o *outFile) error {
	if m.me == "" {
		return errors.New("not ready yet")
	}
	m.xfer.init()
	p := &pkt{T: "fo", MID: randHex(8), TTL: m.cfg.TTL, From: m.me, Nick: m.nick, FID: o.fid, Size: o.size, N: o.n}
	if strings.HasPrefix(o.to, "@") {
		peer := m.peers[o.to[1:]]
		if peer == nil || peer.pub == "" {
			return errors.New("they haven't been seen over Bluetooth yet")
		}
		aead, err := dmCipher(m.key, peer.pub, m.me, peer.id)
		if err != nil {
			return err
		}
		meta, _ := json.Marshal(map[string]string{"n": o.name, "s": o.sum})
		box, err := sealDM(m.key, peer.pub, m.me, peer.id, string(meta))
		if err != nil {
			return err
		}
		o.aead = aead
		p.To, p.Box, p.Pub = peer.id, box, m.pub
	} else {
		p.Room, p.FName, p.Sum = strings.TrimPrefix(o.to, "#"), o.name, o.sum
	}
	if err := m.hub.LocalFile(m.me, o.to, FileInfo{ID: o.fid, Name: o.name, Size: o.size}); err != nil {
		return err
	}
	m.xfer.mine[o.fid] = o
	now := time.Now()
	m.dup(p.MID, now)
	if p.Room != "" {
		m.remember(p, now)
	}
	m.broadcast(p, true)
	m.say("shared " + o.name + " · people can /get it while you're around")
	return nil
}

// onOffer handles a file offer; false means: don't pass it on.
func (m *Mesh) onOffer(from int64, p *pkt, at time.Time) bool {
	m.xfer.init()
	if p.FID == "" || len(p.FID) != 16 || p.N < 0 || p.Size < 0 || p.Size > maxBTFile {
		return false
	}
	if _, ok := m.xfer.toward[p.FID]; !ok {
		m.xfer.toward[p.FID] = route{from, time.Now()}
	}
	if p.To != "" && p.To != m.me {
		return true // someone else's DM file: just pass it on
	}
	pr := m.peerFor(p.From, p.Nick)
	if !p.Hist && at.After(pr.lastSeen) {
		pr.lastSeen, pr.on = at, true
	}
	o := &offer{from: p.From, n: p.N, size: p.Size, sum: p.Sum, pub: p.Pub}
	name := p.FName
	if p.To == m.me {
		meta, err := openDM(m.key, p.Pub, p.From, p.To, p.Box)
		var md map[string]string
		if err != nil || json.Unmarshal([]byte(meta), &md) != nil {
			return false
		}
		name, o.sum, o.dm = md["n"], md["s"], true
	}
	m.xfer.offers[p.FID] = o
	m.hub.RemoteUser(pr.id, pr.nick, pr.on, nil)
	m.hub.RemoteMsg(Msg{TS: at.UnixMilli(), Room: p.Room, From: p.From, To: p.To, File: &FileInfo{ID: p.FID, Name: name, Size: p.Size}})
	if p.Room != "" {
		m.remember(p, at)
	}
	return p.To == ""
}

func (m *Mesh) startFetch(in *inFile) {
	m.xfer.init()
	o := m.xfer.offers[in.fi.ID]
	switch {
	case o == nil:
		in.reply <- errors.New("the sender isn't reachable over Bluetooth any more")
		return
	case m.xfer.getting[in.fi.ID] != nil:
		in.reply <- errors.New("already downloading it")
		return
	}
	in.off, in.from = o, o.from
	if o.dm {
		aead, err := dmCipher(m.key, o.pub, o.from, m.me)
		if err != nil {
			in.reply <- err
			return
		}
		in.aead = aead
	}
	f, err := os.Create(in.dest + ".part")
	if err != nil {
		in.reply <- err
		return
	}
	in.f, in.have, in.asked = f, make([]bool, o.n), map[int]time.Time{}
	m.xfer.getting[in.fi.ID] = in
	if o.n == 0 {
		m.finishFetch(in, nil)
		return
	}
	m.askMore(in)
}

// askMore keeps the window full.
func (m *Mesh) askMore(in *inFile) {
	now := time.Now()
	var want []int
	for len(in.asked) < fileWindow && in.next < len(in.have) {
		if !in.have[in.next] {
			in.asked[in.next] = now
			want = append(want, in.next)
		}
		in.next++
	}
	m.request(in, want)
}

// request asks the sender for pieces, along the path the offer came.
func (m *Mesh) request(in *inFile, want []int) {
	if len(want) == 0 {
		return
	}
	p := &pkt{T: "fq", MID: randHex(8), TTL: m.cfg.TTL, From: m.me, To: in.from, FID: in.fi.ID, Want: want}
	m.dup(p.MID, time.Now())
	b := encodePkt(p)
	if r, ok := m.xfer.toward[in.fi.ID]; ok {
		if l := m.links[r.link]; l != nil {
			m.enqueue(l, b, true) // tiny, and keeps the pipe full: fast lane
			return
		}
	}
	for _, l := range m.links { // no known path: ask everywhere
		m.enqueue(l, b, true)
	}
}

func (m *Mesh) onRequest(from int64, p *pkt) {
	m.xfer.init()
	if len(p.FID) != 16 { // (handle has already checked the sender and the id)
		return
	}
	m.xfer.back[p.FID+"|"+p.From] = route{from, time.Now()}
	if p.To == m.me {
		o, l := m.xfer.mine[p.FID], m.links[from]
		if o == nil || l == nil {
			return
		}
		f, err := os.Open(o.path)
		if err != nil {
			return
		}
		defer f.Close()
		buf := make([]byte, chunkSize)
		for i, idx := range p.Want {
			if i >= 64 || idx < 0 || idx >= o.n {
				continue
			}
			n, err := f.ReadAt(buf, int64(idx)*chunkSize)
			if err != nil && err != io.EOF {
				return
			}
			m.enqueue(l, m.chunkPacket(o, p.From, idx, buf[:n]), false)
		}
		return
	}
	if r, ok := m.xfer.toward[p.FID]; ok { // pass it on toward the sender
		if l := m.links[r.link]; l != nil && (l.id != from || l.shared) {
			m.enqueue(l, encodePkt(p), true)
			return
		}
	}
	if p.TTL > 1 {
		q := *p
		q.TTL--
		b := encodePkt(&q)
		for id, l := range m.links {
			if id != from || l.shared {
				m.enqueue(l, b, true)
			}
		}
	}
}

// chunkPacket: 'C' | file id (8) | requester (6) | piece number (4) | data
func (m *Mesh) chunkPacket(o *outFile, to string, idx int, data []byte) []byte {
	if o.aead != nil {
		data = o.aead.Seal(nil, chunkNonce(o.fid, idx), data, []byte(o.fid))
	}
	b := make([]byte, chunkHdr, chunkHdr+len(data))
	b[0] = 'C'
	hex.Decode(b[1:9], []byte(o.fid))
	hex.Decode(b[9:15], []byte(to))
	binary.BigEndian.PutUint32(b[15:19], uint32(idx))
	return append(b, data...)
}

func parseChunk(b []byte) (fid, to string, idx int, data []byte, ok bool) {
	if len(b) < chunkHdr || b[0] != 'C' {
		return "", "", 0, nil, false
	}
	return hex.EncodeToString(b[1:9]), hex.EncodeToString(b[9:15]), int(binary.BigEndian.Uint32(b[15:19])), b[chunkHdr:], true
}

func (m *Mesh) onChunk(from int64, b []byte) {
	fid, to, idx, data, ok := parseChunk(b)
	if !ok {
		return
	}
	m.xfer.init()
	if to != m.me { // pass it on toward whoever asked
		if r, ok := m.xfer.back[fid+"|"+to]; ok {
			if l := m.links[r.link]; l != nil && (l.id != from || l.shared) {
				m.enqueue(l, b, false)
			}
		}
		return
	}
	in := m.xfer.getting[fid]
	if in == nil || idx < 0 || idx >= len(in.have) || in.have[idx] {
		return
	}
	if in.aead != nil {
		pt, err := in.aead.Open(nil, chunkNonce(fid, idx), data, []byte(fid))
		if err != nil {
			return
		}
		data = pt
	}
	if _, err := in.f.WriteAt(data, int64(idx)*chunkSize); err != nil {
		m.finishFetch(in, err)
		return
	}
	in.have[idx], in.count, in.tries = true, in.count+1, 0
	delete(in.asked, idx)
	if in.count == len(in.have) {
		m.finishFetch(in, nil)
		return
	}
	if time.Since(in.shown) > 150*time.Millisecond {
		in.shown = time.Now()
		done := int64(in.count) * chunkSize
		if done > in.fi.Size {
			done = in.fi.Size
		}
		in.progress(done, in.fi.Size)
	}
	m.askMore(in)
}

func (m *Mesh) finishFetch(in *inFile, err error) {
	delete(m.xfer.getting, in.fi.ID)
	part := in.dest + ".part"
	if cerr := in.f.Close(); err == nil {
		err = cerr
	}
	if err == nil && in.off.sum != "" {
		if sum, e := fileSum(part); e != nil || sum != in.off.sum {
			err = errors.New("the file arrived damaged; try /get again")
		}
	}
	if err == nil {
		err = os.Rename(part, in.dest)
	}
	if err != nil {
		os.Remove(part)
	} else {
		in.progress(in.fi.Size, in.fi.Size)
	}
	in.reply <- err
}

// fileTick asks again for pieces that didn't arrive, and gives up on a
// sender who has gone quiet.
func (m *Mesh) fileTick(now time.Time) {
	for _, in := range m.xfer.getting {
		var again []int
		for idx, t := range in.asked {
			if now.Sub(t) > m.cfg.Retry {
				again = append(again, idx)
				in.asked[idx] = now
			}
		}
		if len(again) == 0 {
			continue
		}
		if in.tries++; in.tries > 15 {
			m.finishFetch(in, errors.New("lost touch with the sender"))
			continue
		}
		sort.Ints(again)
		m.request(in, again)
	}
	for k, r := range m.xfer.toward {
		if now.Sub(r.at) > 3*time.Hour {
			delete(m.xfer.toward, k)
		}
	}
	for k, r := range m.xfer.back {
		if now.Sub(r.at) > time.Hour {
			delete(m.xfer.back, k)
		}
	}
}

// dropFetches cleans up half-finished downloads when nook quits.
func (m *Mesh) dropFetches() {
	for _, in := range m.xfer.getting {
		in.f.Close()
		os.Remove(in.dest + ".part")
		select {
		case in.reply <- errors.New("closing"):
		default:
		}
	}
}
