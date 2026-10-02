//go:build !windows

package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// reclaimProfile ends a browser left running on dir by an earlier run of this program (one that was
// killed or crashed, so never got to close it). Chrome allows one process per profile and would
// refuse to start with "SingletonLock: File exists". Only a process whose command line names dir
// is touched; a lock of a dead process is Chrome's own to clear.
func reclaimProfile(dir string) {
	link, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return
	}
	i := strings.LastIndex(link, "-")
	if i < 0 {
		return
	}
	pid, err := strconv.Atoi(link[i+1:])
	if err != nil || pid <= 1 || pid == os.Getpid() {
		return
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil || !strings.Contains(string(out), dir) {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for range 30 {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	time.Sleep(200 * time.Millisecond)
}
