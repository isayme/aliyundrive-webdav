package adrive

import (
	"context"
	"fmt"
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
	FileId       string    `json:"file_id"`
	ParentFileId string    `json:"parent_file_id"`

	fs           *FileSystem
	downloadInfo *GetFileDownloadUrlResp
	pos          int64

	lock   sync.Mutex
	reader io.ReadCloser
}

func (f *File) Clone() *File {
	return &File{
		FileName:     f.FileName,
		FileSize:     f.FileSize,
		UpdatedAt:    f.UpdatedAt,
		Type:         f.Type,
		FileId:       f.FileId,
		ParentFileId: f.ParentFileId,
		fs:           f.fs,
	}
}

func (f *File) Close() error {
	f.lock.Lock()
	defer f.lock.Unlock()

	if f.reader != nil {
		f.reader.Close()
		f.reader = nil
	}

	return nil
}

func (f *File) Write(p []byte) (n int, err error) {
	return 0, fmt.Errorf("not implemented")
}

func (f *File) getDownloadUrl() (string, error) {
	if f.downloadInfo == nil || time.Now().After(f.downloadInfo.Expiration) {
		result, err := f.fs.getDownloadUrl(f.FileId)
		if err != nil || result.Url == "" {
			logger.Errorf("获取下载地址失败, err: %v", err)
			return "", err
		}
		f.downloadInfo = result
	}

	return f.downloadInfo.Url, nil
}

func (f *File) Read(p []byte) (n int, err error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	if f.reader != nil {
		return f.reader.Read(p)
	}

	url, err := f.getDownloadUrl()
	if err != nil {
		return 0, err
	}

	headerRange := fmt.Sprintf("bytes=%d-", f.pos)
	headers := map[string]string{
		"Range":   headerRange,
		"Referer": ALIYUNDRIVE_HOST,
		"Accept":  "*/*",
	}

	resp, err := client.R().SetDoNotParseResponse(true).SetHeaders(headers).Get(url)
	if err != nil {
		logger.Warnf("下载文件失败: %v", err)
		return 0, err
	}
	rawBody := resp.RawBody()

	if resp.StatusCode() >= 300 {
		bs, err := io.ReadAll(rawBody)
		logger.Warnf("下载文件失败, err: %v, body: %s", err, string(bs))
		return 0, err
	}

	f.reader = rawBody

	return f.reader.Read(p)
}

func (f *File) Seek(offset int64, whence int) (int64, error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	pos := f.pos

	switch whence {
	case io.SeekStart:
		pos = offset
	case io.SeekCurrent:
		pos += offset
	case io.SeekEnd:
		pos = f.FileSize + offset
	default:
		return 0, fmt.Errorf("not support")
	}

	f.pos = pos
	if f.reader != nil {
		f.reader.Close()
		f.reader = nil
	}
	return f.pos, nil
}

func (f *File) Stat() (fs.FileInfo, error) {
	return &StatInfo{
		name:      f.FileName,
		size:      f.FileSize,
		updatedAt: f.UpdatedAt,
		typ:       f.Type,
	}, nil
}

func (f *File) Readdir(count int) ([]fs.FileInfo, error) {
	return f.fs.listDir(context.Background(), f)
}
