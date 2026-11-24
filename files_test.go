package main

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeRandom(t *testing.T, dir, name string, n int) (string, []byte) {
	t.Helper()
	data := make([]byte, n)
	rand.Read(data)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p, data
}

func savedFile(t *testing.T, dir, name string) []byte {
	b, _ := os.ReadFile(filepath.Join(dir, name))
	return b
}

func TestWiFiFiles(t *testing.T) {
	down := t.TempDir()
	old := downloadsDir
	downloadsDir = func() string { return down }
	defer func() { downloadsDir = old }()

	h := testHub(t, "")
	a, b, c := testClient(t, h, "ana", ""), testClient(t, h, "ben", ""), testClient(t, h, "cal", "")
	online(t, a, b, c)
	src := t.TempDir()

	// A room file, sent by dragging its path in (macOS-style escaped spaces).
	path, data := writeRandom(t, src, "big report.bin", 8<<20)
	start := time.Now()
	a.Input(strings.ReplaceAll(path, " ", `\ `))
	waitFor(t, "file offer arrives", func() bool {
		v := b.View(maxLines, false)
		for _, l := range v.Lines {
			if l.File != nil && l.File.Name == "big report.bin" && l.File.Size == int64(len(data)) {
				return true
			}
		}
		return false
	})
	b.Input("/get")
	waitLong(t, "download saved", 30*time.Second, func() bool { return bytes.Equal(savedFile(t, down, "big report.bin"), data) })
	t.Logf("8 MB up and down through the host in %v", time.Since(start).Round(time.Millisecond))
	waitFor(t, "saved notice", func() bool { return sysSays(b, "saved big report.bin") })

	// A DM file: only the two people can fetch it, even with a direct link.
	dmPath, dmData := writeRandom(t, src, "secret.txt", 3000)
	a.Input("/dm ben")
	a.Input("/send " + dmPath)
	var fileID string
	waitFor(t, "DM file arrives", func() bool {
		for _, l := range b.View(maxLines, false).Lines {
			if l.File != nil && l.File.Name == "secret.txt" {
				fileID = l.File.ID
				return true
			}
		}
		return false
	})
	_, csid, _ := c.conn()
	resp, err := http.Get(localURL(h.Port()) + "/file/" + fileID + "?sid=" + csid)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a third person could fetch a DM file: %d", resp.StatusCode)
	}
	_, bsid, _ := b.conn()
	resp, err = http.Get(localURL(h.Port()) + "/file/" + fileID + "?sid=" + bsid)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, dmData) || resp.Header.Get("Content-Type") != "application/octet-stream" ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("DM file download wrong: %d bytes, %q, %q", len(got), resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"))
	}
	if sees(c, "", "") && hasFileLine(c, "secret.txt") {
		t.Fatal("the third person was shown the DM file")
	}
}

// cutWriter passes on the first n bytes of a response and then fails, as if
// the Wi-Fi had dropped in the middle of a download.
type cutWriter struct {
	http.ResponseWriter
	left int
	sent *int64
}

var errCut = errors.New("cut off")

func (w *cutWriter) Write(p []byte) (int, error) {
	if w.left <= 0 {
		return 0, errCut
	}
	if len(p) > w.left {
		p = p[:w.left]
	}
	n, err := w.ResponseWriter.Write(p)
	w.left -= n
	*w.sent += int64(n)
	if err == nil && w.left <= 0 {
		w.Flush()
		err = errCut
	}
	return n, err
}

func (w *cutWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// countWriter counts what a response sends.
type countWriter struct {
	http.ResponseWriter
	sent *int64
}

func (w *countWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	*w.sent += int64(n)
	return n, err
}

func (w *countWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// A download that breaks off twice carries on where it stopped, instead of
// starting over, and the file still arrives exactly.
func TestWiFiDownloadResumes(t *testing.T) {
	down := t.TempDir()
	oldDir, oldPause := downloadsDir, fetchPause
	downloadsDir = func() string { return down }
	fetchPause = 10 * time.Millisecond
	defer func() { downloadsDir, fetchPause = oldDir, oldPause }()

	h := testHub(t, "")
	target, _ := url.Parse(localURL(h.Port()))
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.FlushInterval = -1 // keep the event stream flowing
	rp.ErrorLog = log.New(io.Discard, "", 0)
	var mu sync.Mutex
	var ranges []string
	var served int64
	fileReqs := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/file/") {
			rp.ServeHTTP(w, r)
			return
		}
		mu.Lock()
		fileReqs++
		n := fileReqs
		ranges = append(ranges, r.Header.Get("Range"))
		mu.Unlock()
		var sent int64
		defer func() {
			mu.Lock()
			served += sent
			mu.Unlock()
		}()
		if n <= 2 { // the first two tries break off after 1 MB
			rp.ServeHTTP(&cutWriter{ResponseWriter: w, left: 1 << 20, sent: &sent}, r)
			return
		}
		rp.ServeHTTP(&countWriter{ResponseWriter: w, sent: &sent}, r)
	}))
	defer proxy.Close()

	a := testClient(t, h, "ana", "")
	b := NewClient(ClientOpts{Mode: modeJoin, URL: proxy.URL, Nick: "ben", Token: deviceToken("test-ben")})
	b.Start()
	defer b.Close()
	online(t, a, b)

	path, data := writeRandom(t, t.TempDir(), "lecture.mp4", 6<<20)
	a.Input("/send " + path)
	waitFor(t, "file offer arrives", func() bool { return hasFileLine(b, "lecture.mp4") })
	b.Input("/get")
	waitLong(t, "download saved", 30*time.Second, func() bool { return bytes.Equal(savedFile(t, down, "lecture.mp4"), data) })

	mu.Lock()
	defer mu.Unlock()
	if len(ranges) < 3 || ranges[0] != "" {
		t.Fatalf("expected a first try and then resumed ones, got %q", ranges)
	}
	for _, r := range ranges[1:] {
		if !strings.HasPrefix(r, "bytes=") || r == "bytes=0-" {
			t.Fatalf("a retry started over instead of resuming: %q", ranges)
		}
	}
	if served > int64(len(data))+(1<<20) { // starting over would send 8 MB
		t.Fatalf("sent %d bytes for a %d-byte file: it started over", served, len(data))
	}
	if _, err := os.Stat(filepath.Join(down, "lecture.mp4.part")); !os.IsNotExist(err) {
		t.Fatal("the .part file was left behind")
	}
	t.Logf("tries: %q, %d bytes sent for %d", ranges, served, len(data))
}

func hasFileLine(c *Client, name string) bool {
	for _, l := range c.View(maxLines, false).Lines {
		if l.File != nil && l.File.Name == name {
			return true
		}
	}
	return false
}

func TestFilePaths(t *testing.T) {
	if got := cleanFileName(`..\..\evil/../../x<y>.txt`); got != "xy.txt" {
		t.Fatalf("cleanFileName = %q", got)
	}
	if got := cleanFileName("   "); got != "file" {
		t.Fatalf("empty name = %q", got)
	}
	if got := filePath(`'/tmp/a b.txt'`); got != "/tmp/a b.txt" {
		t.Fatalf("quoted path = %q", got)
	}
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "x.txt"), nil, 0o644)
	if p := freePath(d, "x.txt"); filepath.Base(p) != "x (2).txt" {
		t.Fatalf("freePath = %q", p)
	}
}
