package imagebuild

import (
	"io/fs"
	"syscall"
	"time"
)

func statOf(fi fs.FileInfo) (fileStat, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fileStat{}, false
	}
	return fileStat{ctime: time.Unix(st.Ctim.Unix()), dev: uint64(st.Dev), ino: st.Ino, nlink: uint64(st.Nlink)}, true
}
