package platform

import (
	"time"

	"golang.org/x/sys/unix"
)

// BootTime is the wall-clock time the system last started, kern.boottime.
// The kernel moves it when the wall clock is set, so it is read at each call.
func BootTime() (time.Time, error) {
	boot, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(boot.Sec, int64(boot.Usec)*int64(time.Microsecond)), nil
}
