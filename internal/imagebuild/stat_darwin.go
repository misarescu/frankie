package imagebuild

import (
	"io/fs"
	"syscall"
	"time"
)

// statOf on macOS lets the package's tests run on a developer's machine;
// builds run on Linux.
func statOf(fi fs.FileInfo) (fileStat, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fileStat{}, false
	}
	return fileStat{ctime: time.Unix(st.Ctimespec.Unix()), dev: uint64(st.Dev), ino: st.Ino, nlink: uint64(st.Nlink)}, true
}
