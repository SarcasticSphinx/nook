package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// nook update: fetch the newest release from GitHub and swap it in for the
// program that is running. Needs the internet; nothing else.

const repoURL = "https://github.com/SarcasticSphinx/nook"

// updateAsset is the release file that replaces this build. The old-system
// builds (Windows 7/8, macOS 10.13 to 12) are the ones made with Go 1.20,
// so a copy updates to the same kind it already is.
func updateAsset(goos, goarch, gover string) string {
	legacy := strings.HasPrefix(gover, "go1.20")
	switch goos {
	case "darwin":
		if legacy {
			return "nook-mac-legacy"
		}
		return "nook-mac"
	case "windows":
		switch goarch {
		case "arm64":
			return "nook-win-arm64.exe"
		case "amd64":
			if legacy {
				return "nook-win7-x64.exe"
			}
			return "nook-win-x64.exe"
		case "386":
			if legacy {
				return "nook-win7-x86.exe"
			}
			return "nook-win-x86.exe"
		}
	case "linux":
		if goarch == "amd64" {
			return "nook-linux-x64"
		}
	}
	return ""
}

// newerVersion reports whether version a (like "0.4.1" or "v0.4.1") is
// later than b.
func newerVersion(a, b string) bool {
	pa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// sumFor finds a file's SHA-256 in a SHA256SUMS listing.
func sumFor(sums, name string) string {
	sc := bufio.NewScanner(strings.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

var updateClient = &http.Client{Timeout: 5 * time.Minute}

// latestTag asks GitHub which release is the latest, from where
// /releases/latest redirects to (no API, so no rate limit).
func latestTag() (string, error) {
	c := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := c.Get(repoURL + "/releases/latest")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, "/tag/")
	if i < 0 {
		return "", fmt.Errorf("GitHub answered %s", resp.Status)
	}
	return loc[i+len("/tag/"):], nil
}

func fetchTo(url string, w io.Writer) error {
	resp, err := updateClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

func runUpdate() {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe) // ~/.local/bin/nook is a link
	}
	if err != nil {
		die("can't find this program's file: %v", err)
	}
	os.Remove(exe + ".old") // left behind by an update on Windows

	asset := updateAsset(runtime.GOOS, runtime.GOARCH, runtime.Version())
	if asset == "" {
		die("there is no ready-made nook for %s/%s to update to", runtime.GOOS, runtime.GOARCH)
	}
	fmt.Println("checking for a newer nook ...")
	tag, err := latestTag()
	if err != nil {
		die("can't reach GitHub (%v). Updating needs the internet.", err)
	}
	if !newerVersion(tag, version) {
		fmt.Printf("nook %s is the latest version\n", version)
		return
	}
	base := repoURL + "/releases/download/" + tag + "/"

	var sums strings.Builder
	if err := fetchTo(base+"SHA256SUMS", &sums); err != nil {
		die("download failed: %v", err)
	}
	want := sumFor(sums.String(), asset)
	if want == "" {
		die("release %s has no %s", tag, asset)
	}

	fmt.Printf("downloading nook %s (%s) ...\n", strings.TrimPrefix(tag, "v"), asset)
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".nook-update-*")
	if err != nil {
		die("can't write next to %s: %v", exe, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	h := sha256.New()
	err = fetchTo(base+asset, io.MultiWriter(tmp, h))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		die("download failed: %v", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		die("the download doesn't match its checksum; nothing was changed")
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		die("%v", err)
	}
	if out, err := exec.Command(tmpName, "version").Output(); err != nil || !strings.HasPrefix(string(out), "nook ") {
		die("the new version doesn't run on this computer; nothing was changed")
	}

	if err := swapIn(tmpName, exe); err != nil {
		die("can't replace %s: %v", exe, err)
	}
	fmt.Printf("updated nook %s → %s\n", version, strings.TrimPrefix(tag, "v"))
}

// swapIn moves the new file over the old one. It renames rather than
// writing in place: macOS kills a signed app that was overwritten, and
// Windows can't overwrite a running .exe but can rename it.
func swapIn(newFile, exe string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(newFile, exe)
	}
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(newFile, exe); err != nil {
		if rerr := os.Rename(old, exe); rerr != nil {
			return errors.New(err.Error() + "; the old version is at " + old)
		}
		return err
	}
	return nil
}
