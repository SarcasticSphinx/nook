package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif" // pictures /see can show
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

// /see: a shared file shown inside the chat window without saving it.
// Text is shown as text, pictures are drawn with coloured half-blocks
// (two pixels per character cell), anything else is described.

const (
	pvText = iota
	pvImage
	pvOther
)

type preview struct {
	kind int
	text []string
	img  image.Image
	note string // what it is, for files that can't be shown
}

func makePreview(name string, data []byte) preview {
	if img, format, err := image.Decode(bytes.NewReader(data)); err == nil {
		b := img.Bounds()
		return preview{kind: pvImage, img: img, note: fmt.Sprintf("%s picture · %d×%d", strings.ToUpper(format), b.Dx(), b.Dy())}
	}
	if looksText(data) {
		s := strings.ReplaceAll(string(data), "\r\n", "\n")
		s = strings.TrimSuffix(s, "\n")
		lines := strings.Split(s, "\n")
		for i, l := range lines {
			lines[i] = cleanShown(l)
		}
		return preview{kind: pvText, text: lines, note: fmt.Sprintf("%d lines", len(lines))}
	}
	kind := "a file nook can't show"
	switch strings.ToLower(name[strings.LastIndex(name, ".")+1:]) {
	case "pdf":
		kind = "a PDF"
	case "zip", "rar", "7z", "gz", "tar":
		kind = "an archive"
	case "mp4", "mov", "mkv", "avi", "webm":
		kind = "a video"
	case "mp3", "wav", "m4a", "ogg", "flac":
		kind = "audio"
	case "webp", "heic", "bmp", "tiff", "svg":
		kind = "a picture in a format nook can't draw"
	case "doc", "docx", "xls", "xlsx", "ppt", "pptx":
		kind = "an Office document"
	}
	return preview{kind: pvOther, note: kind + " (" + humanSize(int64(len(data))) + ")"}
}

// looksText: valid UTF-8 with no NUL bytes in the first 8 KB.
func looksText(b []byte) bool {
	if len(b) > 8192 {
		b = b[:8192]
		for i := 0; i < 4 && len(b) > 0 && !utf8.Valid(b); i++ {
			b = b[:len(b)-1] // a character cut in half at the edge
		}
	}
	return utf8.Valid(b) && bytes.IndexByte(b, 0) < 0
}

// cleanShown expands tabs and drops control characters, so a file can't
// move the cursor or change the terminal.
func cleanShown(s string) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch {
		case r == '\t':
			n := 4 - col%4
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case r < 32 || r == 127 || (r >= 0x80 && r < 0xa0):
		default:
			b.WriteRune(r)
			col++
		}
	}
	return b.String()
}

// rememberSeen keeps the last few files opened with /see in memory.
func rememberSeen(id string, data []byte) {
	n := 0
	seeCache.Range(func(k, _ interface{}) bool { n++; return true })
	if n >= 4 {
		seeCache.Range(func(k, _ interface{}) bool { seeCache.Delete(k); return true })
	}
	seeCache.Store(id, data)
}

// ---- the full-screen viewer ----

type viewer struct {
	fi  FileInfo
	no  int
	pv  preview
	off int // first text row shown
}

func (u *tui) openViewer(fi FileInfo, no int, data []byte) {
	u.view = &viewer{fi: fi, no: no, pv: makePreview(fi.Name, data)}
}

// viewerKey handles a key while a file is open; false closes the viewer.
func (u *tui) viewerKey(ev *tcell.EventKey) {
	v := u.view
	page := imax(1, u.page()-1)
	switch ev.Key() {
	case tcell.KeyUp:
		v.off--
	case tcell.KeyDown, tcell.KeyEnter:
		v.off++
	case tcell.KeyPgUp:
		v.off -= page
	case tcell.KeyPgDn:
		v.off += page
	case tcell.KeyHome:
		v.off = 0
	case tcell.KeyEnd:
		v.off = len(v.pv.text)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		u.view = nil
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'q', 'Q':
			u.view = nil
		case ' ', 'j':
			v.off++
		case 'k':
			v.off--
		case 'g':
			u.view = nil
			u.c.Input(fmt.Sprintf("/get %d", v.no))
		}
	}
	if u.view != nil {
		v.off = imax(0, imin(v.off, len(v.pv.text)-1))
	}
}

func (u *tui) drawViewer(w, h int) {
	v := u.view
	s := u.s
	head := " " + v.fi.Name + " · " + humanSize(v.fi.Size)
	if v.pv.note != "" {
		head += " · " + v.pv.note
	}
	keys := " q close · g save · Esc hide "
	barSt := u.style(part{kind: pText}).Reverse(true)
	if u.o.Ghost {
		barSt = stMeta.Dim(true).Reverse(true)
	}
	for x := 0; x < w; x++ {
		s.SetContent(x, 0, ' ', nil, barSt)
	}
	u.put(0, 0, w-len(keys), head, barSt)
	if w-len(keys) > len(head)+1 {
		u.put(w-len(keys), 0, w, keys, barSt)
	}
	top, area := 1, h-1
	switch v.pv.kind {
	case pvText:
		gutter := len(fmt.Sprint(len(v.pv.text))) + 2
		meta, text := u.style(part{kind: pMeta}), u.style(part{kind: pText})
		y := top
		for i := v.off; i < len(v.pv.text) && y < top+area; i++ {
			u.put(0, y, w, fmt.Sprintf("%*d  ", gutter-2, i+1), meta)
			rows := hardWrap(v.pv.text[i], imax(1, w-gutter), text)
			for _, r := range rows {
				if y >= top+area {
					break
				}
				x := gutter
				for _, c := range r {
					s.SetContent(x, y, c.r[0], c.r[1:], c.st)
					x += c.w
				}
				y++
			}
		}
		s.HideCursor()
	case pvImage:
		u.drawImage(v.pv.img, top, w, area)
		s.HideCursor()
	default:
		msg := "this is " + v.pv.note + " · press g to save it to Downloads"
		u.put(imax(0, (w-len(msg))/2), top+area/2, w, msg, u.style(part{kind: pMeta}))
		s.HideCursor()
	}
}

// drawImage fits a picture into w × rows cells, two pixels per cell (the
// top one as the "▀" colour, the bottom one as the background), averaging
// the pixels each one covers.
func (u *tui) drawImage(img image.Image, top, w, rows int) {
	b := img.Bounds()
	iw, ih := b.Dx(), b.Dy()
	if iw == 0 || ih == 0 || w < 1 || rows < 1 {
		return
	}
	// cells: cw wide, ch tall, so pixels: cw × 2ch
	cw, ch2 := iw, ih
	if cw > w {
		cw, ch2 = w, ih*w/iw
	}
	if ch2 > rows*2 {
		cw, ch2 = cw*rows*2/ch2, rows*2
	}
	cw, ch2 = imax(1, cw), imax(2, ch2)
	x0 := (w - cw) / 2
	avg := func(px, py int) tcell.Color {
		sx0, sx1 := b.Min.X+px*iw/cw, b.Min.X+(px+1)*iw/cw
		sy0, sy1 := b.Min.Y+py*ih/ch2, b.Min.Y+(py+1)*ih/ch2
		if sx1 <= sx0 {
			sx1 = sx0 + 1
		}
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		var r, g, bl, n uint64
		stepX, stepY := imax(1, (sx1-sx0)/4), imax(1, (sy1-sy0)/4) // sample, don't sum millions
		for y := sy0; y < sy1; y += stepY {
			for x := sx0; x < sx1; x += stepX {
				cr, cg, cb, ca := img.At(x, y).RGBA()
				// over a dark background, so transparent parts look dark
				r += uint64(cr) * uint64(ca) >> 16
				g += uint64(cg) * uint64(ca) >> 16
				bl += uint64(cb) * uint64(ca) >> 16
				n++
			}
		}
		return tcell.NewRGBColor(int32(r/n>>8), int32(g/n>>8), int32(bl/n>>8))
	}
	for cy := 0; cy*2 < ch2; cy++ {
		for cx := 0; cx < cw; cx++ {
			st := tcell.StyleDefault.Foreground(avg(cx, cy*2))
			if cy*2+1 < ch2 {
				st = st.Background(avg(cx, cy*2+1))
			}
			u.s.SetContent(x0+cx, top+cy, '▀', nil, st)
		}
	}
}
