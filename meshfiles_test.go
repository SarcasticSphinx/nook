package main

import (
	"bytes"
	"testing"
	"time"
)

func fileLine(c *Client, name string) *Line {
	v := c.View(maxLines, false)
	for i := range v.Lines {
		if l := &v.Lines[i]; l.File != nil && l.File.Name == name {
			return l
		}
	}
	return nil
}

func useDownloads(t *testing.T) string {
	d := t.TempDir()
	old := downloadsDir
	downloadsDir = func() string { return d }
	t.Cleanup(func() { downloadsDir = old })
	return d
}

// A file crosses three hops; the laptops in between only pass pieces on.
func TestBluetoothFileAcrossHops(t *testing.T) {
	down := useDownloads(t)
	n := &simNet{mtu: 64}
	a := newSimNode(t, n, "ana", testMeshCfg)
	b := newSimNode(t, n, "bo", testMeshCfg)
	c := newSimNode(t, n, "cy", testMeshCfg)
	d := newSimNode(t, n, "dee", testMeshCfg)
	n.connect(a.rad, b.rad)
	n.connect(b.rad, c.rad)
	n.connect(c.rad, d.rad)
	waitLong(t, "chain connected", 20*time.Second, func() bool { return d.hub.Online() == 4 && a.hub.Online() == 4 })
	path, data := writeRandom(t, t.TempDir(), "photo.jpg", 150<<10)
	a.c.Input("/send " + path)
	waitFor(t, "offer reaches the far end", func() bool { return fileLine(d.c, "photo.jpg") != nil })
	start := time.Now()
	d.c.Input("/get")
	waitLong(t, "file arrives intact", 60*time.Second, func() bool { return bytes.Equal(savedFile(t, down, "photo.jpg"), data) })
	t.Logf("150 KB over 3 simulated hops in %v", time.Since(start).Round(time.Millisecond))
	if fileLine(b.c, "photo.jpg") == nil {
		t.Fatal("people in between should still see the offer")
	}
}

// Windows-style shared link with 20-byte frames: a DM file reaches only its
// recipient, and nobody else can read it.
func TestBluetoothDMFileSharedLink(t *testing.T) {
	down := useDownloads(t)
	n := &simNet{mtu: 20}
	p := newSimNode(t, n, "win", testMeshCfg)
	a := newSimNode(t, n, "ana", testMeshCfg)
	b := newSimNode(t, n, "bo", testMeshCfg)
	c := newSimNode(t, n, "cy", testMeshCfg)
	n.share(p.rad, a.rad, b.rad, c.rad)
	waitLong(t, "all online", 20*time.Second, func() bool { return a.hub.Online() == 4 && c.hub.Online() == 4 })
	path, data := writeRandom(t, t.TempDir(), "answers.txt", 20<<10)
	a.c.Input("/dm cy")
	a.c.Input("/send " + path)
	waitFor(t, "DM offer arrives", func() bool { return fileLine(c.c, "answers.txt") != nil })
	c.c.Input("/get")
	waitLong(t, "DM file arrives", 60*time.Second, func() bool { return bytes.Equal(savedFile(t, down, "answers.txt"), data) })
	time.Sleep(200 * time.Millisecond)
	if fileLine(b.c, "answers.txt") != nil || fileLine(p.c, "answers.txt") != nil {
		t.Fatal("someone else was shown a DM file")
	}
}

// Pieces lost in the air are asked for again.
func TestBluetoothFileLossyLink(t *testing.T) {
	down := useDownloads(t)
	n := &simNet{mtu: 128}
	a := newSimNode(t, n, "ana", testMeshCfg)
	b := newSimNode(t, n, "bo", testMeshCfg)
	n.connect(a.rad, b.rad)
	waitLong(t, "connected", 20*time.Second, func() bool { return b.hub.Online() == 2 })
	n.mu.Lock()
	n.loss = 0.03
	n.mu.Unlock()
	path, data := writeRandom(t, t.TempDir(), "notes.pdf", 80<<10)
	a.c.Input("/send " + path)
	waitLong(t, "offer", 20*time.Second, func() bool { return fileLine(b.c, "notes.pdf") != nil })
	b.c.Input("/get")
	waitLong(t, "file arrives despite losses", 90*time.Second, func() bool { return bytes.Equal(savedFile(t, down, "notes.pdf"), data) })
}
