package adrive

import (
	"context"
	"io"
	"io/fs"
	"sync"
	"time"

	"github.com/isayme/go-logger"
)

type File struct {
	FileName     string    `json:"name"`
	FileSize     int64     `json:"size"`
	UpdatedAt    time.Time `json:"updated_at"`
	Type         string    `json:"type"`
	DriveId      string    `json:"drive_id"`
	FileId       string    `json:"file_id"`
	ParentFileId string    `json:"parent_file_id"`

	path string
	fs   *FileSystem

	rsc io.ReadSeekCloser
	wc  io.WriteCloser

	lock sync.Mutex
}

func (f *File) Clone() *File {
	return &File{
		FileName:     f.FileName,
		FileSize:     f.FileSize,
		UpdatedAt:    f.UpdatedAt,
		Type:         f.Type,
		DriveId:      f.DriveId,
		FileId:       f.FileId,
		ParentFileId: f.ParentFileId,

		fs:   f.fs,
		path: f.path,
	}
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
	var mode fs.FileMode = 0660
	if f.Type == FILE_TYPE_FOLDER {
		mode = mode | fs.ModeDir
	}

	return &StatInfo{
		name:      f.FileName,
		size:      f.FileSize,
		updatedAt: f.UpdatedAt,
		mode:      mode,
	}, nil
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
		si, _ := file.Stat()
		result[idx] = si
	}

	return result, nil
}
