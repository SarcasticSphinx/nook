package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// fileBackend moves files: over Wi-Fi through the host (httpFiles), or
// over Bluetooth through the mesh.
type fileBackend interface {
	// Send shares a file into a chat ("#room" or "@id").
	Send(path, to string, progress func(done, total int64)) error
	// Fetch downloads a shared file to dest.
	Fetch(f *FileInfo, from, dest string, progress func(done, total int64)) error
}

// httpFiles is Wi-Fi mode: upload to the host, download from it.
type httpFiles struct{ c *Client }

type countingReader struct {
	r        io.Reader
	done     int64
	total    int64
	progress func(done, total int64)
	last     time.Time
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.done += int64(n)
	if time.Since(cr.last) > 200*time.Millisecond || err != nil {
		cr.last = time.Now()
		cr.progress(cr.done, cr.total)
	}
	return n, err
}

func (hf httpFiles) Send(path, to string, progress func(done, total int64)) error {
	base, sid, on := hf.c.conn()
	if !on {
		return errors.New("offline")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	q := url.Values{"sid": {sid}, "to": {to}, "name": {filepath.Base(path)}}
	req, err := http.NewRequest(http.MethodPost, base+"/upload?"+q.Encode(), &countingReader{r: f, total: st.Size(), progress: progress})
	if err != nil {
		return err
	}
	req.ContentLength = st.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := hf.c.sc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out Resp
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out) != nil {
		return errors.New("the host didn't answer properly")
	}
	if !out.OK {
		return errors.New(out.Err)
	}
	return nil
}

// fetchPause is the wait between tries when a download breaks off; a
// variable so tests can be quick.
var fetchPause = time.Second

// errGone: the host doesn't have the file (expired, or the host restarted).
var errGone = errors.New("the host doesn't have this file any more · ask for it again")

// Fetch downloads through the host into dest.part, then renames it into
// place. When the connection breaks (a Wi-Fi hiccup, the app reconnecting
// to the host), it carries on from where it stopped: the host answers
// Range requests, so nothing is downloaded twice.
func (hf httpFiles) Fetch(fi *FileInfo, from, dest string, progress func(done, total int64)) error {
	part := dest + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	var have int64
	stuck, gone := 0, 0
	buf := make([]byte, 256<<10)
	for {
		got, err := hf.fetchFrom(fi, f, &have, buf, progress)
		if err == nil {
			break
		}
		if err == errGone {
			gone++ // maybe just a session that is reconnecting: try a little
		}
		if got > 0 {
			stuck = 0
		} else {
			stuck++
		}
		var perm permanentErr
		if errors.As(err, &perm) || gone >= 3 || stuck >= 10 {
			f.Close()
			os.Remove(part)
			if perm.err != nil {
				return perm.err
			}
			return err
		}
		time.Sleep(fetchPause * time.Duration(imin(stuck+1, 5)))
	}
	err = f.Close()
	if err == nil {
		err = os.Rename(part, dest)
	}
	if err != nil {
		os.Remove(part)
	}
	return err
}

type permanentErr struct{ err error }

func (p permanentErr) Error() string { return p.err.Error() }

// fetchFrom asks the host for the file from byte *have on and appends it.
func (hf httpFiles) fetchFrom(fi *FileInfo, f *os.File, have *int64, buf []byte, progress func(done, total int64)) (int64, error) {
	base, sid, on := hf.c.conn()
	if !on {
		return 0, errors.New("offline")
	}
	req, err := http.NewRequest(http.MethodGet, base+"/file/"+url.PathEscape(fi.ID)+"?sid="+url.QueryEscape(sid), nil)
	if err != nil {
		return 0, permanentErr{err}
	}
	if *have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", *have))
	}
	resp, err := hf.c.sc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPartialContent:
		var start int64 = -1
		fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-", &start)
		if start != *have {
			*have = 0 // not what we asked for: start over
			return 0, errors.New("the host sent the wrong part")
		}
	case http.StatusOK: // the whole file: the first try, or Range ignored
		*have = 0
	case http.StatusRequestedRangeNotSatisfiable:
		if *have == fi.Size {
			return 0, nil
		}
		*have = 0
		return 0, errors.New("the file changed on the host")
	case http.StatusNotFound:
		return 0, errGone
	case http.StatusServiceUnavailable, http.StatusTooManyRequests:
		return 0, errors.New("the host is busy")
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = resp.Status
		}
		return 0, permanentErr{errors.New(msg)}
	}
	if _, err := f.Seek(*have, io.SeekStart); err != nil {
		return 0, permanentErr{err}
	}
	if err := f.Truncate(*have); err != nil {
		return 0, permanentErr{err}
	}
	n, err := io.CopyBuffer(f, &countingReader{r: resp.Body, done: *have, total: fi.Size, progress: progress}, buf)
	*have += n
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) { // the disk, not the network: full, or no permission
			return n, permanentErr{err}
		}
		return n, err
	}
	if *have > fi.Size {
		return n, permanentErr{errors.New("the file on the host isn't the size it said")}
	}
	if *have < fi.Size {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}

// downloadsDir is where received files go: Downloads, else your home.
// (A variable so tests can point it somewhere else.)
var downloadsDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	if d := filepath.Join(home, "Downloads"); isDir(d) {
		return d
	}
	return home
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// freePath finds a name that isn't taken: notes.pdf, notes (2).pdf, ...
func freePath(dir, name string) string {
	p := filepath.Join(dir, name)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			if _, err := os.Stat(p + ".part"); os.IsNotExist(err) {
				return p
			}
		}
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
	}
}

// filePath understands a path typed or dragged into the terminal: quoted
// ('...' or "..."), or with backslash-escaped spaces as macOS writes them.
func filePath(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	} else if runtime.GOOS != "windows" && strings.Contains(s, `\`) {
		var b strings.Builder
		for i := 0; i < len(s); i++ {
			if s[i] == '\\' && i+1 < len(s) {
				i++
			}
			b.WriteByte(s[i])
		}
		s = b.String()
	}
	if strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			s = filepath.Join(home, s[2:])
		}
	}
	return s
}

// asFile reports whether a whole typed line is a file you dragged in.
func asFile(line string) (string, bool) {
	p := filePath(line)
	if p == "" || !(filepath.IsAbs(p) || strings.HasPrefix(p, ".")) {
		return "", false
	}
	st, err := os.Stat(p)
	return p, err == nil && st.Mode().IsRegular()
}

type rate struct {
	start time.Time
}

func (r rate) text(verb, name string, done, total int64) string {
	el := time.Since(r.start).Seconds()
	s := fmt.Sprintf("%s %s", verb, name)
	if total > 0 {
		s += fmt.Sprintf(" %d%%", done*100/total)
	}
	if el > 0.5 && done > 0 {
		bps := float64(done) / el
		s += " · " + humanSize(int64(bps)) + "/s"
		if total > done && bps > 0 {
			s += fmt.Sprintf(" · %s left", (time.Duration(float64(total-done)/bps) * time.Second).Round(time.Second))
		}
	}
	return s
}

var saved sync.Map // file id -> where it was saved

func (c *Client) files() fileBackend {
	if c.o.Files != nil {
		return c.o.Files
	}
	return httpFiles{c}
}

// send shares a file into the chat you're in.
func (c *Client) send(arg string) {
	p := filePath(arg)
	if p == "" {
		c.Sys("usage: /send path   (or drag a file into the window and press Enter)")
		return
	}
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		c.Sys("can't find a file at " + p)
		return
	}
	c.mu.Lock()
	to := c.target
	c.mu.Unlock()
	if to == "" {
		c.Sys("you're not in a chat · /g")
		return
	}
	name := filepath.Base(p)
	id := "send:" + p
	r := rate{time.Now()}
	go func() {
		err := c.files().Send(p, to, func(done, total int64) {
			c.setBusy(id, r.text("sending", name, done, total))
		})
		c.setBusy(id, "")
		if err != nil {
			c.Sys("couldn't send " + name + ": " + err.Error())
		}
	}()
}

// pickFile finds file number arg, or with arg "", the newest one someone
// else shared.
func (c *Client) pickFile(arg string) (l Line, ok bool) {
	arg = strings.TrimPrefix(strings.TrimSpace(arg), "#")
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.lines) - 1; i >= 0; i-- {
		x := &c.lines[i]
		if x.File != nil && (arg == "" && !x.Mine || arg == fmt.Sprint(x.FileNo)) {
			cp := *x
			cp.File = &FileInfo{}
			*cp.File = *x.File
			return cp, true
		}
	}
	return Line{}, false
}

// get downloads file number n (or the newest one).
func (c *Client) get(arg string) {
	pick, ok := c.pickFile(arg)
	if !ok {
		c.Sys("no such file · /files lists them")
		return
	}
	fi, from := *pick.File, pick.From
	if where, ok := saved.Load(fi.ID); ok {
		c.Sys("already saved: " + where.(string))
		return
	}
	dest := freePath(downloadsDir(), cleanFileName(fi.Name))
	id := "get:" + fi.ID
	r := rate{time.Now()}
	go func() {
		err := c.files().Fetch(&fi, from, dest, func(done, total int64) {
			c.setBusy(id, r.text("getting", fi.Name, done, total))
		})
		c.setBusy(id, "")
		if err != nil {
			c.Sys("couldn't get " + fi.Name + ": " + err.Error())
			return
		}
		saved.Store(fi.ID, dest)
		c.mu.Lock()
		c.insertLocked(Line{Sys: true, Notice: true, Key: pick.Key, TS: nowMs(), Text: "saved " + fi.Name + " to " + dest})
		c.mu.Unlock()
		c.changed()
	}()
}

func (c *Client) listFiles() {
	c.mu.Lock()
	var out []string
	for i := range c.lines {
		l := &c.lines[i]
		if l.File == nil {
			continue
		}
		who := c.nickLocked(l.From)
		if l.Mine {
			who = "you"
		}
		s := fmt.Sprintf("  %3d  %s  %s  from %s", l.FileNo, l.File.Name, humanSize(l.File.Size), who)
		if where, ok := saved.Load(l.File.ID); ok {
			s += "  (saved: " + where.(string) + ")"
		}
		out = append(out, s)
	}
	c.mu.Unlock()
	if len(out) == 0 {
		c.Sys("no files yet · /send path to share one")
		return
	}
	c.sysLines(append([]string{"files · /see N to look · /get N to save"}, out...)...)
}

// maxSee is the biggest file /see opens (it is kept in memory, not saved).
const maxSee = 20 << 20

// SetViewer gives the full-screen view a way to show a file; without one
// (line mode) /see prints text files and describes the rest.
func (c *Client) SetViewer(f func(fi FileInfo, no int, data []byte)) {
	c.mu.Lock()
	c.viewer = f
	c.mu.Unlock()
}

// see shows a shared file without saving it: fetched into memory through a
// private temporary file that is deleted at once.
func (c *Client) see(arg string) {
	pick, ok := c.pickFile(arg)
	if !ok {
		c.Sys("no such file · /files lists them")
		return
	}
	fi, from := *pick.File, pick.From
	if fi.Size > maxSee {
		c.Sys(fmt.Sprintf("%s is too big to look at here (%s) · /get %d saves it", fi.Name, humanSize(fi.Size), pick.FileNo))
		return
	}
	show := func(data []byte) {
		c.mu.Lock()
		v := c.viewer
		c.mu.Unlock()
		if v != nil {
			v(fi, pick.FileNo, data)
			return
		}
		pv := makePreview(fi.Name, data)
		if pv.kind != pvText {
			c.Sys(fi.Name + ": " + pv.note + fmt.Sprintf(" · /get %d saves it", pick.FileNo))
			return
		}
		lines := []string{"── " + fi.Name + " ──"}
		for i, l := range pv.text {
			if i == 300 {
				lines = append(lines, fmt.Sprintf("  ... %d more lines · /get %d", len(pv.text)-300, pick.FileNo))
				break
			}
			lines = append(lines, "  "+l)
		}
		c.sysLines(lines...)
	}
	if where, ok := saved.Load(fi.ID); ok {
		if data, err := os.ReadFile(where.(string)); err == nil {
			show(data)
			return
		}
	}
	if b, ok := seeCache.Load(fi.ID); ok {
		show(b.([]byte))
		return
	}
	if pick.Mine && c.o.Files != nil {
		c.Sys("that's your own file · it's on your laptop")
		return
	}
	id := "see:" + fi.ID
	r := rate{time.Now()}
	go func() {
		dir, err := os.MkdirTemp("", "nook-see-")
		var data []byte
		if err == nil {
			dest := filepath.Join(dir, "f")
			err = c.files().Fetch(&fi, from, dest, func(done, total int64) {
				c.setBusy(id, r.text("opening", fi.Name, done, total))
			})
			if err == nil {
				data, err = os.ReadFile(dest)
			}
			os.RemoveAll(dir)
		}
		c.setBusy(id, "")
		if err != nil {
			c.Sys("couldn't open " + fi.Name + ": " + err.Error())
			return
		}
		rememberSeen(fi.ID, data) // looking again is instant (Bluetooth is slow)
		show(data)
	}()
}

var seeCache sync.Map // file id -> its bytes, for files opened with /see
