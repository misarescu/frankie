package imagebuild

import "time"

// fileStat is the part of a file's status a layer needs beyond fs.FileInfo.
type fileStat struct {
	ctime time.Time
	dev   uint64
	ino   uint64
	nlink uint64
}
