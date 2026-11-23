package main

import (
	"debug/macho"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// The hub hands out the app itself, so people need nothing but the Wi-Fi:
// one person brings the files (USB stick, AirDrop...), everyone else
// downloads from the host at http://<host>:4747/get.

//go:embed install.sh
var installSh string

//go:embed install.ps1
var installPs1 string

type dlFile struct{ Name, Group, Desc string }

var dlCatalog = []dlFile{
	{"nook-mac", "mac", "macOS 13 or newer (Apple Silicon and Intel)"},
	{"nook-mac-arm64", "mac", "Apple Silicon Mac"},
	{"nook-mac-intel", "mac", "Intel Mac"},
	{"nook-mac-legacy", "mac", "older Macs: macOS 10.13 to 12"},
	{"nook-win-x64.exe", "win", "Windows 10 / 11, 64-bit (most PCs)"},
	{"nook-win-arm64.exe", "win", "Windows on ARM (Snapdragon laptops)"},
	{"nook-win-x86.exe", "win", "Windows 10, 32-bit"},
	{"nook-win7-x64.exe", "win7", "Windows 7 / 8, 64-bit"},
	{"nook-win7-x86.exe", "win7", "Windows 7 / 8, 32-bit"},
	{"nook-linux-x64", "linux", "Linux, 64-bit"},
}

var dlGroupTitles = []struct{ Key, Title string }{
	{"mac", "Mac"}, {"win", "Windows 10 / 11"}, {"win7", "Windows 7 / 8"}, {"linux", "Linux"},
}

// files maps download names to files next to this program. This program
// itself is always offered, so whoever hosts can at least share their copy.
func (h *Hub) files() map[string]string {
	m := map[string]string{}
	exe, err := os.Executable()
	if err != nil {
		return m
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	dir := filepath.Dir(exe)
	for _, f := range dlCatalog {
		p := filepath.Join(dir, f.Name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			m[f.Name] = p
		}
	}
	if self := selfName(exe); self != "" {
		if _, ok := m[self]; !ok {
			m[self] = exe
		}
	}
	return m
}

func selfName(exe string) string {
	legacy := strings.HasPrefix(runtime.Version(), "go1.20")
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64", "darwin/amd64":
		switch {
		case legacy:
			return "nook-mac-legacy"
		case isUniversal(exe):
			return "nook-mac"
		case runtime.GOARCH == "arm64":
			return "nook-mac-arm64"
		}
		return "nook-mac-intel"
	case "windows/amd64":
		if legacy {
			return "nook-win7-x64.exe"
		}
		return "nook-win-x64.exe"
	case "windows/386":
		if legacy {
			return "nook-win7-x86.exe"
		}
		return "nook-win-x86.exe"
	case "windows/arm64":
		return "nook-win-arm64.exe"
	case "linux/amd64":
		return "nook-linux-x64"
	}
	return ""
}

// isUniversal reports whether a Mac binary holds both Intel and ARM code.
func isUniversal(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 4)
	if _, err := f.Read(b); err != nil {
		return false
	}
	return b[0] == 0xca && b[1] == 0xfe && b[2] == 0xba && b[3] == 0xbe
}

type dlItem struct{ Name, Desc, Size, Cmd, Link string }

type dlGroup struct {
	Title string
	Mine  bool
	Items []dlItem
}

type dlPage struct {
	URLs    []string
	Base    string
	OS      string
	Groups  []dlGroup
	Locked  bool
	Version string
}

func (h *Hub) getPage(w http.ResponseWriter, r *http.Request) {
	files := h.files()
	urls := h.URLs()
	base := h.baseFor(r)
	if isLoopback(hostOf(r.Host)) && len(urls) > 0 {
		base = urls[0] // the host viewing its own page: show what others should use
	}
	ua := r.UserAgent()
	mine := "linux"
	switch {
	case strings.Contains(ua, "Windows NT 6."):
		mine = "win7"
	case strings.Contains(ua, "Windows"):
		mine = "win"
	case strings.Contains(ua, "Macintosh"), strings.Contains(ua, "Mac OS X"):
		mine = "mac"
	}
	byKey := map[string]*dlGroup{}
	var page dlPage
	for _, g := range dlGroupTitles {
		page.Groups = append(page.Groups, dlGroup{Title: g.Title, Mine: g.Key == mine})
	}
	for i, g := range dlGroupTitles {
		byKey[g.Key] = &page.Groups[i]
	}
	for _, f := range dlCatalog {
		p, ok := files[f.Name]
		if !ok {
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		u := base + "/dl/" + f.Name
		size := fmt.Sprintf("%.1f MB", float64(st.Size())/1e6)
		if isUniversal(p) {
			size = fmt.Sprintf("about %.0f MB", float64(st.Size())/2e6) // only one half is sent
		}
		it := dlItem{Name: f.Name, Desc: f.Desc, Size: size, Link: "/dl/" + f.Name}
		switch {
		case f.Name == "nook-mac" || f.Name == "nook-mac-legacy":
			// fetch only the half of the universal file this Mac needs
			it.Cmd = `curl -o nook "` + u + `?arch=$(uname -m)" && chmod +x nook && ./nook`
		case f.Group == "mac" || f.Group == "linux":
			it.Cmd = "curl -o nook " + u + " && chmod +x nook && ./nook"
		case f.Group == "win":
			it.Cmd = "curl.exe -o nook.exe " + u + "\n.\\nook.exe"
		case f.Group == "win7":
			it.Cmd = "powershell -c \"(New-Object Net.WebClient).DownloadFile('" + u + "','nook.exe')\"\nnook.exe"
		}
		byKey[f.Group].Items = append(byKey[f.Group].Items, it)
	}
	var groups []dlGroup
	for _, g := range page.Groups { // the visitor's own system first
		if g.Mine && len(g.Items) > 0 {
			groups = append(groups, g)
		}
	}
	for _, g := range page.Groups {
		if !g.Mine && len(g.Items) > 0 {
			groups = append(groups, g)
		}
	}
	page.Groups, page.URLs, page.Locked, page.Version = groups, urls, h.opts.Key != "", version
	page.Base, page.OS = base, mine
	if len(page.URLs) == 0 {
		page.URLs = []string{base}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = getTmpl.Execute(w, page)
}

func (h *Hub) download(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/dl/")
	files := h.files()
	if name == "auto" { // the installers ask for "whatever fits this computer"
		q := r.URL.Query()
		osver, _ := strconv.Atoi(q.Get("osver"))
		if name = pickFile(files, q.Get("os"), q.Get("arch"), osver); name == "" {
			http.Error(w, "this host has no build for your computer: join in a browser instead", http.StatusNotFound)
			return
		}
	}
	p, ok := files[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	if arch := r.URL.Query().Get("arch"); arch != "" && serveSlice(w, r, p, arch) {
		return
	}
	http.ServeFile(w, r, p)
}

// serveSlice sends just one architecture out of a universal Mac binary
// (what `lipo -thin` would produce), halving the download. It reports
// false if the file isn't universal or lacks that architecture.
func serveSlice(w http.ResponseWriter, r *http.Request, path, arch string) bool {
	want := map[string]macho.Cpu{"arm64": macho.CpuArm64, "x86_64": macho.CpuAmd64}[arch]
	if want == 0 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	fat, err := macho.NewFatFile(f)
	if err != nil {
		return false
	}
	for _, a := range fat.Arches {
		if a.Cpu == want {
			st, _ := f.Stat()
			http.ServeContent(w, r, "", st.ModTime(), io.NewSectionReader(f, int64(a.Offset), int64(a.Size)))
			return true
		}
	}
	return false
}

// pickFile chooses the build for a computer out of what this host has.
// os and arch are what `uname -s` / `uname -m` or Windows report; osver is
// the major macOS or Windows version (0 if unknown).
func pickFile(files map[string]string, goos, arch string, osver int) string {
	var cands []string
	switch strings.ToLower(goos) {
	case "darwin":
		thin := "nook-mac-intel"
		if arch == "arm64" {
			thin = "nook-mac-arm64"
		}
		if osver > 0 && osver < 13 {
			cands = []string{"nook-mac-legacy"}
		} else {
			cands = []string{"nook-mac", thin, "nook-mac-legacy"}
		}
	case "linux":
		if arch == "x86_64" || arch == "amd64" {
			cands = []string{"nook-linux-x64"}
		}
	case "windows":
		old := osver > 0 && osver < 10
		switch strings.ToUpper(arch) {
		case "ARM64":
			cands = []string{"nook-win-arm64.exe", "nook-win-x64.exe", "nook-win-x86.exe"}
		case "AMD64":
			if old {
				cands = []string{"nook-win7-x64.exe", "nook-win7-x86.exe"}
			} else {
				cands = []string{"nook-win-x64.exe", "nook-win7-x64.exe", "nook-win-x86.exe", "nook-win7-x86.exe"}
			}
		default:
			if old {
				cands = []string{"nook-win7-x86.exe"}
			} else {
				cands = []string{"nook-win-x86.exe", "nook-win7-x86.exe"}
			}
		}
	}
	for _, c := range cands {
		if _, ok := files[c]; ok {
			return c
		}
	}
	return ""
}

// baseFor is the address the visitor used to reach us, which is the one
// that works for their download too.
func (h *Hub) baseFor(r *http.Request) string {
	if validHost(r.Host) {
		return "http://" + r.Host
	}
	if u := h.URLs(); len(u) > 0 {
		return u[0]
	}
	return localURL(h.port)
}

// validHost keeps a forged Host header out of the scripts we hand out.
func validHost(s string) bool {
	if s == "" || len(s) > 255 {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune(".-:[]", c)) {
			return false
		}
	}
	return true
}

// installer serves install.sh or install.ps1 with this host's address filled
// in, so one line installs the app with no internet:
//
//	curl -fsSL http://HOST:4747/install.sh | sh
//	irm http://HOST:4747/install.ps1 | iex
func (h *Hub) installer(w http.ResponseWriter, r *http.Request) {
	base := h.baseFor(r)
	body := strings.Replace(installSh, `NOOK_BASE="${NOOK_BASE:-}"`, `NOOK_BASE="`+base+`"`, 1)
	if strings.HasSuffix(r.URL.Path, ".ps1") {
		body = strings.Replace(installPs1, "$NookBase = $env:NOOK_BASE", "$NookBase = '"+base+"'", 1)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, body)
}
