package addons

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// headroomMB is what is left for the daemon and the kernel's caches when an add-on is weighed against
// the memory available: an add-on that would take the last of it would have the kernel kill the
// daemon's own speaker and microphones to make room. A Dot 2 has about 445 MB available once TECHO5 is
// up, so a 40 MB add-on fits there with plenty to spare and a 400 MB one does not.
const headroomMB = 100

// memAvailableMB is MemAvailable from /proc/meminfo, in megabytes; a variable, for tests.
var memAvailableMB = func() (int, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		k, v, ok := strings.Cut(s.Text(), ":")
		if !ok || k != "MemAvailable" {
			continue
		}
		kb, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(v), " kB"))
		if err != nil {
			return 0, err
		}
		return kb / 1024, nil
	}
	return 0, errors.New("no MemAvailable in /proc/meminfo")
}

// freeMB is the room left on the filesystem holding path, in megabytes; a variable, for tests.
var freeMB = func(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize) >> 20, nil
}

// fits says whether an add-on that says it uses mb fits in the memory available now, and if not, in
// words for the card. Memory that cannot be read is not a reason to refuse.
func fits(mb int) (bool, string) {
	have, err := memAvailableMB()
	if err != nil {
		return true, ""
	}
	if mb+headroomMB > have {
		return false, "needs about " + strconv.Itoa(mb) + " MB of memory, and this device has " + strconv.Itoa(have) +
			" MB available, " + strconv.Itoa(headroomMB) + " MB of which TECHO5 keeps for itself"
	}
	return true, ""
}
