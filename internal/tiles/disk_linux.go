package tiles

import (
	"os"
	"strings"
)

// countDisk keeps whole disks only. Linux lists every partition beside its disk (sda and sda1),
// and RAID, device-mapper and loop devices on top of the disks they use, so counting them all
// would count the same bytes two or three times. Whole disks are the ones in /sys/block.
func countDisk(name string) bool {
	for _, p := range []string{"loop", "ram", "zram", "dm-", "md", "sr", "fd"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	_, err := os.Stat("/sys/block/" + name)
	return err == nil
}
