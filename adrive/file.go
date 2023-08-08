package adrive

import (
	"context"
	"io"
	"io/fs"
	"sync"
	"time"

	"github.com/isayme/go-logger"
	"golang.org/x/net/webdav"
)

var _ fs.FileInfo = &File{}
var _ webdav.File = &File{}

type File struct {
	FileName     string    `json:"name"`
	FileSize     int64     `json:"size"`
	UpdatedAt    time.Time `json:"updated_at"`
	Type         string    `json:"type"`
	DriveId      string    `json:"drive_id"`
	FileId       string    `json:"file_id"`
	ParentFileId string    `json:"parent_file_id"`

	fs *FileSystem

	rsc io.ReadSeekCloser
	wc  io.WriteCloser

	lock sync.Mutex
}

func (f *File) Close() error {
	f.lock.Lock()
	defer f.lock.Unlock()

	if f.rsc != nil {
		f.rsc.Close()
		f.rsc = nil
	}

	if f.wc != nil {
		f.wc.Close()
		f.wc = nil
	}

	return nil
}

func (f *File) Write(p []byte) (n int, err error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	if f.wc == nil {
		f.wc, err = NewFileWriteCloser(f)
		if err != nil {
			return 0, err
		}
	}

	return f.wc.Write(p)
}

func (f *File) getFileReadSeekCloser() io.ReadSeekCloser {
	if f.rsc == nil {
		f.rsc = NewFileReadCloser(f)
	}

	return f.rsc
}

func (f *File) Read(p []byte) (n int, err error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	return f.getFileReadSeekCloser().Read(p)
}

func (f *File) Seek(offset int64, whence int) (int64, error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	return f.getFileReadSeekCloser().Seek(offset, whence)
}

func (f *File) Stat() (fs.FileInfo, error) {
	return f, nil
}

func (f *File) Readdir(count int) (result []fs.FileInfo, err error) {
	defer func() {
		if err != nil {
			logger.Infof("列举目录 '%s' 失败: %v", f.FileName, err)
		} else {
			logger.Infof("列举目录 '%s' 成功, 共有子文件 %d 个", f.FileName, len(result))
		}
	}()

	files, err := f.fs.listDir(context.Background(), f)
	if err != nil {
		return nil, err
	}

	result = make([]fs.FileInfo, len(files))
	for idx, file := range files {
		result[idx] = file
	}

	return result, nil
}

func (f *File) Name() string {
	return f.FileName
}

func (f *File) Size() int64 {
	return f.FileSize
}

func (f *File) Mode() fs.FileMode {
	var mode fs.FileMode = 0660
	if f.IsDir() {
		mode = mode | fs.ModeDir
	}

	return mode
}

func (f *File) ModTime() time.Time {
	return f.UpdatedAt
}

func (f *File) IsDir() bool {
	return f.Type == FILE_TYPE_FOLDER
}

func (f *File) Sys() any {
	return nil
}
