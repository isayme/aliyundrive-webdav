package adrive

import (
	"io/fs"
	"time"
)

type StatInfo struct {
	name      string
	size      int64
	updatedAt time.Time
	typ       string
}

func (si *StatInfo) Name() string {
	return si.name
}

func (si *StatInfo) Size() int64 {
	return si.size
}

func (si *StatInfo) Mode() fs.FileMode {
	if si.IsDir() {
		return fs.ModeDir | 0777
	}

	return 0666
}

func (si *StatInfo) ModTime() time.Time {
	return si.updatedAt
}

func (si *StatInfo) IsDir() bool {
	return si.typ == "folder"
}

func (si *StatInfo) Sys() interface{} {
	return nil
}
