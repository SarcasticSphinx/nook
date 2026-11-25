package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

// Bluetooth writes are small (20 bytes on some links), so every packet is
// cut into frames:
//
//	[stream u16][seq u16][index u8][count u8][data...]
//
// stream is random per laptop and seq counts its packets, so frames from
// several laptops arriving on one shared link never get mixed up.
const (
	frameHdr  = 6
	maxPacket = 16 << 10
	maxParts  = 512
)

var errTooBig = errors.New("message too long for this Bluetooth link")

func splitFrames(stream, seq uint16, pkt []byte, mtu int) ([][]byte, error) {
	room := mtu - frameHdr
	if room < 1 {
		return nil, errTooBig
	}
	n := (len(pkt) + room - 1) / room
	if n == 0 {
		n = 1
	}
	if n > 255 {
		return nil, errTooBig
	}
	frames := make([][]byte, n)
	for i := range frames {
		end := (i + 1) * room
		if end > len(pkt) {
			end = len(pkt)
		}
		chunk := pkt[i*room : end]
		f := make([]byte, frameHdr+len(chunk))
		binary.BigEndian.PutUint16(f[0:], stream)
		binary.BigEndian.PutUint16(f[2:], seq)
		f[4], f[5] = byte(i), byte(n)
		copy(f[frameHdr:], chunk)
		frames[i] = f
	}
	return frames, nil
}

type partKey struct {
	link        int64
	stream, seq uint16
}

type partial struct {
	frags   [][]byte
	got     int
	size    int
	started time.Time
}

// assembler puts frames back together into packets.
type assembler struct {
	parts map[partKey]*partial
}

func (a *assembler) add(link int64, f []byte, now time.Time) []byte {
	if len(f) < frameHdr || f[5] == 0 || f[4] >= f[5] {
		return nil
	}
	k := partKey{link, binary.BigEndian.Uint16(f[0:]), binary.BigEndian.Uint16(f[2:])}
	idx, count, data := int(f[4]), int(f[5]), f[frameHdr:]
	if count == 1 {
		return append([]byte(nil), data...)
	}
	if a.parts == nil {
		a.parts = map[partKey]*partial{}
	}
	p := a.parts[k]
	if p == nil || len(p.frags) != count {
		if len(a.parts) >= maxParts {
			a.prune(now.Add(time.Hour)) // under attack or badly behind: start over
		}
		p = &partial{frags: make([][]byte, count), started: now}
		a.parts[k] = p
	}
	if p.frags[idx] == nil {
		p.frags[idx] = append([]byte(nil), data...)
		p.got++
		p.size += len(data)
	}
	if p.size > maxPacket {
		delete(a.parts, k)
		return nil
	}
	if p.got < count {
		return nil
	}
	delete(a.parts, k)
	return bytes.Join(p.frags, nil)
}

// prune forgets packets that never completed.
func (a *assembler) prune(now time.Time) {
	for k, p := range a.parts {
		if now.Sub(p.started) > 20*time.Second {
			delete(a.parts, k)
		}
	}
}

func (a *assembler) dropLink(link int64) {
	for k := range a.parts {
		if k.link == link {
			delete(a.parts, k)
		}
	}
}

// pkt is one mesh message. Keys are short because every byte costs time
// over Bluetooth.
type pkt struct {
	T     string   `json:"t"`           // ann msg dm bye sync g (a game packet)
	MID   string   `json:"m,omitempty"` // unique id, so each laptop handles it once
	TTL   int      `json:"l,omitempty"` // hops left
	Age   int64    `json:"a,omitempty"` // ms since it was sent (catch-up copies)
	From  string   `json:"f,omitempty"`
	Nick  string   `json:"n,omitempty"`
	Room  string   `json:"r,omitempty"`
	To    string   `json:"o,omitempty"`
	Text  string   `json:"x,omitempty"`
	Rooms []string `json:"rs,omitempty"`
	Pub   string   `json:"k,omitempty"` // sender's key for direct messages
	Box   string   `json:"b,omitempty"` // an encrypted direct message
	Hist  bool     `json:"h,omitempty"` // a catch-up copy: don't pass it on

	// files (fo: an offer, fq: a request for pieces)
	FID   string `json:"fi,omitempty"`
	FName string `json:"fn,omitempty"`
	Size  int64  `json:"sz,omitempty"`
	N     int    `json:"nc,omitempty"`
	Sum   string `json:"su,omitempty"`
	Want  []int  `json:"w,omitempty"`
}

var flatePool = sync.Pool{New: func() interface{} {
	w, _ := flate.NewWriter(io.Discard, flate.DefaultCompression)
	return w
}}

// encodePkt is JSON, compressed when that makes it smaller.
func encodePkt(p *pkt) []byte {
	j, _ := json.Marshal(p)
	if len(j) > 120 {
		var buf bytes.Buffer
		buf.WriteByte('Z')
		w := flatePool.Get().(*flate.Writer)
		w.Reset(&buf)
		w.Write(j)
		w.Close()
		flatePool.Put(w)
		if buf.Len() < len(j) {
			return buf.Bytes()
		}
	}
	return append([]byte{'J'}, j...)
}

func decodePkt(b []byte) (*pkt, error) {
	if len(b) < 2 {
		return nil, errors.New("short packet")
	}
	body := b[1:]
	switch b[0] {
	case 'J':
	case 'Z':
		r := flate.NewReader(bytes.NewReader(body))
		defer r.Close()
		var err error
		if body, err = io.ReadAll(io.LimitReader(r, maxPacket*4)); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unknown packet")
	}
	var p pkt
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
