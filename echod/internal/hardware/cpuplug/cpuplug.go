//go:build linux && !dot && !spot

// The governor: the 2nd gen Show 5's mainline kernel powers an offline core down entirely, where an
// idle one sits powered in WFI: the MT8163 has nothing that powers an idle core off by itself. Two cores
// always stay up, so a burst of work never waits on a core coming back, and a busy minute brings the
// others back within a second or two.

package cpuplug

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

const (
	cpuDir = "/sys/devices/system/cpu"
	// keep is how many cores stay online.
	keep = 2
	// every is how often the load is read.
	every = time.Second
	// upAt brings a core back after two readings in a row above it, of the online cores' time busy;
	// downAt takes one away after downFor of readings below it.
	upAt    = 0.70
	downAt  = 0.30
	downFor = 20
)

func init() {
	// Only on a mainline kernel for the 2nd gen Show 5 that powers an offline core down: the LineageOS
	// kernel has a hotplug governor of its own, the Show 8 powers idle cores off by itself (MCDI), and
	// the 1st gen boards' kernels cannot power one down.
	if layout.Board == "cronos" && layout.CronosMainline() && spare() > 0 {
		component.Register(component.Hardware, &Governor{}, component.Order(90))
	}
}

// spare is how many cores can go offline: those after keep that have an online switch (the kernel
// says it can take them down).
func spare() int {
	n := 0
	for c := keep; ; c++ {
		if _, err := os.Stat(fmt.Sprintf("%s/cpu%d/online", cpuDir, c)); err != nil {
			return n
		}
		n++
	}
}

// Governor brings the spare cores up and down.
type Governor struct{}

func (g *Governor) Name() string { return "cpuplug" }

func (g *Governor) Run(ctx context.Context) error {
	n := keep + spare()
	defer func() { // everything back up for whatever runs next (a new daemon, kexec)
		for c := keep; c < n; c++ {
			set(c, true)
		}
	}()
	t := time.NewTicker(every)
	defer t.Stop()
	prev, _ := readStat()
	high, low := 0, 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		cur, err := readStat()
		if err != nil {
			continue
		}
		load := busy(prev, cur)
		prev = cur
		online := keep
		for c := keep; c < n; c++ {
			if isOnline(c) {
				online = c + 1
			}
		}
		switch {
		case load > upAt:
			high, low = high+1, 0
		case load < downAt:
			high, low = 0, low+1
		default:
			high, low = 0, 0
		}
		if high >= 2 && online < n {
			set(online, true)
			high = 0
		} else if low >= downFor && online > keep {
			set(online-1, false)
			low = 0
		}
	}
}

func isOnline(c int) bool {
	b, err := os.ReadFile(fmt.Sprintf("%s/cpu%d/online", cpuDir, c))
	return err == nil && strings.TrimSpace(string(b)) == "1"
}

func set(c int, on bool) {
	v := "0"
	if on {
		v = "1"
	}
	if err := os.WriteFile(fmt.Sprintf("%s/cpu%d/online", cpuDir, c), []byte(v), 0); err != nil {
		slog.Warn("cpuplug: a core", "cpu", c, "online", on, "err", err)
	}
}

// stat is each online core's busy and total time, from /proc/stat.
type stat map[string][2]uint64

func readStat() (stat, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := stat{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 6 || !strings.HasPrefix(fs[0], "cpu") || fs[0] == "cpu" {
			continue
		}
		var total, idle uint64
		for i, s := range fs[1:] {
			v, _ := strconv.ParseUint(s, 10, 64)
			total += v
			if i == 3 || i == 4 { // idle, iowait
				idle += v
			}
		}
		out[fs[0]] = [2]uint64{total - idle, total}
	}
	return out, sc.Err()
}

// busy is the share of the online cores' time spent working between two readings; a core that came
// or went between them is left out.
func busy(a, b stat) float64 {
	var work, total uint64
	for k, v := range b {
		p, ok := a[k]
		if !ok || v[1] < p[1] {
			continue
		}
		work += v[0] - p[0]
		total += v[1] - p[1]
	}
	if total == 0 {
		return 0
	}
	return float64(work) / float64(total)
}
