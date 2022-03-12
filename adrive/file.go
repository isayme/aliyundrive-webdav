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

	client *AdriveClient

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
		FileId:       f.FileId,
		DriveId:      f.DriveId,
		ParentFileId: f.ParentFileId,
		client:       f.client,
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

func (f *File) getFilWriteCloser() io.WriteCloser {
	if f.wc == nil {
		f.wc = NewFileWriteCloser(f.client, f)
	}

	return f.wc
}

func (f *File) Write(p []byte) (n int, err error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	return f.getFilWriteCloser().Write(p)
}

func (f *File) getFileReadSeekCloser() io.ReadSeekCloser {
	if f.rsc == nil {
		f.rsc = NewFileReadCloser(f.client, f)
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
	return &StatInfo{
		name:      f.FileName,
		size:      f.FileSize,
		updatedAt: f.UpdatedAt,
		typ:       f.Type,
	}, nil
}

func (f *File) Readdir(count int) (result []fs.FileInfo, err error) {
	defer func() {
		if err != nil {
			logger.Infof("列举目录 '%s' 下文件失败: %v", f.FileName, err)
		} else {
			logger.Infof("列举目录 '%s' 下文件成功, 共有子文件 %d 个", f.FileName, len(result))
		}
	}()
	result, err = f.client.listDir(context.Background(), f)
	return
}
