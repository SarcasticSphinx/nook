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
	"sort"
	"strings"
	"time"
	"unicode"
)

// Files in Wi-Fi mode: the sender streams the file to the host once, the
// host keeps it in a private temporary folder, and everyone who wants it
// downloads it from there at full Wi-Fi speed (with resume). The folder is
// deleted when the host stops, and files older than a few hours go sooner.

const (
	maxFileSize  int64 = 2 << 30 // per file
	maxFileTotal int64 = 8 << 30 // everything the host keeps at once
	fileKeep           = 3 * time.Hour
)

type storedFile struct {
	info     FileInfo
	path     string
	room     string // "" for a DM
	from, to string
	created  time.Time
}

// cleanFileName keeps just a safe base name.
func cleanFileName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			continue
		}
		b.WriteRune(r)
	}
	name = strings.TrimSpace(strings.Trim(b.String(), ". "))
	if r := []rune(name); len(r) > 120 {
		ext := filepath.Ext(name)
		name = string(r[:120-len([]rune(ext))]) + ext
	}
	if name == "" {
		name = "file"
	}
	return name
}

func (h *Hub) fileDirLocked() (string, error) {
	if h.fileDir == "" {
		d, err := os.MkdirTemp("", "nook-files-")
		if err != nil {
			return "", err
		}
		h.fileDir = d
	}
	return h.fileDir, nil
}

// pruneFilesLocked drops expired files, then the oldest ones while the
// total is over the limit (keeping room for `incoming` more bytes).
func (h *Hub) pruneFilesLocked(incoming int64) {
	var all []*storedFile
	total := int64(0)
	for id, f := range h.shared {
		if time.Since(f.created) > fileKeep {
			os.Remove(f.path)
			delete(h.shared, id)
			continue
		}
		all = append(all, f)
		total += f.info.Size
	}
	sort.Slice(all, func(i, j int) bool { return all[i].created.Before(all[j].created) })
	for _, f := range all {
		if total+incoming <= maxFileTotal {
			break
		}
		os.Remove(f.path)
		delete(h.shared, f.info.ID)
		total -= f.info.Size
	}
}

// canSeeLocked: room members can fetch a room's files; DMs only the two.
func (h *Hub) canSeeLocked(u *user, f *storedFile) bool {
	if f.room != "" {
		r := h.rooms[f.room]
		return r != nil && r.members[u.id]
	}
	return u.id == f.from || u.id == f.to
}

// targetLocked checks a "#room" or "@id" target for a user.
func (h *Hub) targetLocked(u *user, to string) (room string, peer *user, err error) {
	switch {
	case strings.HasPrefix(to, "#"):
		rm := h.rooms[cleanRoom(to)]
		if rm == nil || !rm.members[u.id] {
			return "", nil, errors.New("you're not in " + to + " (/join it first)")
		}
		return rm.name, nil, nil
	case strings.HasPrefix(to, "@"):
		if p := h.users[to[1:]]; p != nil {
			return "", p, nil
		}
		return "", nil, errors.New("that person isn't here")
	}
	return "", nil, errors.New("pick a room or a person first")
}

// postFileLocked shows a file in a chat, like a message.
func (h *Hub) postFileLocked(u *user, room string, peer *user, info FileInfo, text string) Msg {
	h.nextID++
	m := Msg{ID: h.nextID, TS: nowMs(), From: u.id, Nick: u.nick, Text: text, File: &info}
	b := []byte(nil)
	if room != "" {
		m.Room = room
		rm := h.rooms[room]
		rm.hist = appendCap(rm.hist, m, roomHist)
		b = sse(Ev{T: "msg", Msg: &m})
		for id := range rm.members {
			if x := h.users[id]; x != nil {
				h.toUserLocked(x, b)
			}
		}
	} else {
		m.To, m.ToNick = peer.id, peer.nick
		k := pairKey(u.id, peer.id)
		h.dms[k] = appendCap(h.dms[k], m, dmHist)
		b = sse(Ev{T: "msg", Msg: &m})
		h.toUserLocked(u, b)
		if peer != u {
			h.toUserLocked(peer, b)
		}
	}
	return m
}

// LocalFile shows a file the person here is sharing over Bluetooth (the
// mesh keeps the file; this just puts it in the chat).
func (h *Hub) LocalFile(from, to string, info FileInfo) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[from]
	if u == nil || h.closed {
		return errors.New(errNotConnected)
	}
	room, peer, err := h.targetLocked(u, to)
	if err != nil {
		return err
	}
	h.postFileLocked(u, room, peer, info, "")
	return nil
}

func jsonReply(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// upload: POST /upload?sid=..&to=#room|@id&name=..  with the file as the body.
func (h *Hub) upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	if r.ContentLength > maxFileSize {
		jsonReply(w, Resp{Err: "too big: the limit is " + humanSize(maxFileSize)})
		return
	}
	h.mu.Lock()
	s := h.sessions[q.Get("sid")]
	if s == nil || h.closed {
		h.mu.Unlock()
		jsonReply(w, Resp{Err: errNotConnected})
		return
	}
	u := s.u
	_, _, err := h.targetLocked(u, q.Get("to"))
	if err == nil && !u.allow(time.Now()) {
		err = errors.New("slow down a little")
	}
	var dir string
	if err == nil {
		h.pruneFilesLocked(r.ContentLength)
		dir, err = h.fileDirLocked()
	}
	h.mu.Unlock()
	if err != nil {
		jsonReply(w, Resp{Err: err.Error()})
		return
	}
	f, err := os.CreateTemp(dir, "f-")
	if err != nil {
		jsonReply(w, Resp{Err: "the host can't store files right now"})
		return
	}
	buf := make([]byte, 256<<10)
	n, err := io.CopyBuffer(f, http.MaxBytesReader(w, r.Body, maxFileSize), buf)
	f.Close()
	if err == nil && r.ContentLength >= 0 && n != r.ContentLength {
		err = errors.New("the upload was cut short")
	}
	if err != nil {
		os.Remove(f.Name())
		jsonReply(w, Resp{Err: "upload failed: " + err.Error()})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	room, peer, err := h.targetLocked(u, q.Get("to"))
	if err != nil || h.closed {
		os.Remove(f.Name())
		jsonReply(w, Resp{Err: "not sent: you left that chat"})
		return
	}
	info := FileInfo{ID: randHex(8), Name: cleanFileName(q.Get("name")), Size: n}
	to := ""
	if peer != nil {
		to = peer.id
	}
	if h.shared == nil {
		h.shared = map[string]*storedFile{}
	}
	h.shared[info.ID] = &storedFile{info: info, path: f.Name(), room: room, from: u.id, to: to, created: time.Now()}
	h.postFileLocked(u, room, peer, info, cleanText(q.Get("text")))
	jsonReply(w, Resp{OK: true})
}

// fileGet: GET /file/ID?sid=..  Always offered as a download, never shown as
// a page, so a shared HTML file can't run inside the chat. Pictures may be
// shown inline (&inline=1) for the browser's chat view.
func (h *Hub) fileGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/file/")
	h.mu.Lock()
	s := h.sessions[r.URL.Query().Get("sid")]
	f := h.shared[id]
	ok := s != nil && f != nil && h.canSeeLocked(s.u, f)
	h.mu.Unlock()
	if !ok {
		http.Error(w, "that file isn't available (it may have expired)", http.StatusNotFound)
		return
	}
	fd, err := os.Open(f.path)
	if err != nil {
		http.Error(w, "that file is gone", http.StatusNotFound)
		return
	}
	defer fd.Close()
	head := make([]byte, 512)
	n, _ := fd.Read(head)
	kind := http.DetectContentType(head[:n])
	disp := "attachment"
	if r.URL.Query().Get("inline") == "1" && (kind == "image/png" || kind == "image/jpeg" || kind == "image/gif" || kind == "image/webp") {
		disp = "inline"
	} else {
		kind = "application/octet-stream"
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename*=UTF-8''%s", disp, url.PathEscape(f.info.Name)))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, "", f.created, fd)
}
