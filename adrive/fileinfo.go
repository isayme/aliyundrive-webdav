package adrive

import (
	"io/fs"
	"time"

	"github.com/isayme/go-alipanopen"
)

var _ fs.FileInfo = &FileInfo{}

type FileInfo struct {
	*alipanopen.File
}

func NewFileInfo(file *alipanopen.File) *FileInfo {
	return &FileInfo{
		File: file,
	}
}

func (f *FileInfo) Name() string {
	return f.FileName
}

func (f *FileInfo) Size() int64 {
	return f.FileSize
}

func (f *FileInfo) Mode() fs.FileMode {
	var mode fs.FileMode = 0660
	if f.IsDir() {
		mode = mode | fs.ModeDir
	}

	return mode
}

func (f *FileInfo) ModTime() time.Time {
	return f.UpdatedAt
}

func (f *FileInfo) IsDir() bool {
	return f.Type == alipanopen.FILE_TYPE_FOLDER
}

func (f *FileInfo) Sys() any {
	return nil
}
