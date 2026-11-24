package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// runPlain is the no-frills line mode: messages are printed as they arrive
// and you type below them. It works anywhere text works (pipes, odd
// terminals, VS Code's debug console...).
func runPlain(c *Client, o uiOpts) {
	var (
		mu      sync.Mutex
		printed int64
		last    *Line
	)
	flush := func() {
		mu.Lock()
		defer mu.Unlock()
		v := c.View(maxLines, false)
		var fresh []Line
		for _, l := range v.Lines {
			if l.Seq > printed {
				fresh = append(fresh, l)
			}
		}
		sort.Slice(fresh, func(i, j int) bool { return fresh[i].Seq < fresh[j].Seq })
		skin := o.Skin
		if !validSkin(skin) {
			skin = "plain"
		}
		for i := range fresh {
			fmt.Println(plainText(fmtLine(&fresh[i], last, &v, skin)))
			printed = fresh[i].Seq
			last = &fresh[i]
		}
	}
	c.SetVerbose(true)
	c.SetOnChange(flush)
	c.Start()
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch strings.ToLower(line) {
		case "/quit", "/q", "/exit":
			c.Close()
			return
		}
		c.Input(line)
	}
	c.Close()
}
