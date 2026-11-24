package main

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// A terminal can only draw a picture with coloured blocks, so /see shows
// pictures and PDFs in a real preview window instead: Quick Look on a Mac
// (it shows any file and runs nothing), the picture or PDF viewer
// elsewhere. The file goes into a private temporary folder, never into
// Downloads, and is deleted when the window closes (Mac) or when nook quits.

// systemPreview is swapped out in tests so no window opens.
var systemPreview = openPreview

var (
	previewMu   sync.Mutex
	previewDirs []string // to delete when nook quits
)

func openPreview(name string, data []byte) error {
	kind := http.DetectContentType(data)
	safe := strings.HasPrefix(kind, "image/") || kind == "application/pdf"
	var cmd func(path string) *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = func(p string) *exec.Cmd { return exec.Command("qlmanage", "-p", p) }
	case "windows":
		if !safe { // only open what a viewer opens: never run a program
			return errors.New("not a picture or a PDF")
		}
		cmd = func(p string) *exec.Cmd { return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", p) }
	default:
		if !safe || (os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "") {
			return errors.New("no preview window here")
		}
		cmd = func(p string) *exec.Cmd { return exec.Command("xdg-open", p) }
	}
	dir, err := os.MkdirTemp("", "nook-see-")
	if err != nil {
		return err
	}
	p := filepath.Join(dir, cleanFileName(name))
	if err := os.WriteFile(p, data, 0o600); err != nil {
		os.RemoveAll(dir)
		return err
	}
	c := cmd(p)
	if err := c.Start(); err != nil {
		os.RemoveAll(dir)
		return err
	}
	if runtime.GOOS == "darwin" { // Quick Look stays open until the window closes
		go func() { c.Wait(); os.RemoveAll(dir) }()
		return nil
	}
	go c.Wait()
	previewMu.Lock()
	previewDirs = append(previewDirs, dir) // the viewer may still have it open
	previewMu.Unlock()
	return nil
}

// cleanPreviews deletes what /see left for other programs to show.
func cleanPreviews() {
	previewMu.Lock()
	defer previewMu.Unlock()
	for _, d := range previewDirs {
		os.RemoveAll(d)
	}
	previewDirs = nil
}
