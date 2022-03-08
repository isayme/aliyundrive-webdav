package adrive

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"time"

	"github.com/isayme/aliyundrive-webdav/util"
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
	rb           *bytes.Buffer
}

func (f *File) Close() error {
	return nil
}

func (f *File) Write(p []byte) (n int, err error) {
	return 0, fmt.Errorf("not implemented")
}

func (f *File) getDownloadUrl() (string, error) {
	if f.downloadInfo == nil || time.Now().After(f.downloadInfo.Expiration) {
		result, err := f.fs.getDownloadUrl(f.FileId)
		if err != nil || result.Url == "" {
			logger.Errorf("get download url fail, err: %v", err)
		}
		if err != nil {
			return "", err
		}
		f.downloadInfo = result
	}

	return f.downloadInfo.Url, nil
}

func (f *File) Read(p []byte) (n int, err error) {
	if f.rb == nil {
		f.rb = bytes.NewBuffer(nil)
	}

	if f.rb.Len() > 0 {
		return f.rb.Read(p)
	}

	if f.pos >= f.FileSize {
		return 0, io.EOF
	}

	url, err := f.getDownloadUrl()
	if err != nil {
		return 0, err
	}

	readLen := util.Max(len(p), 1024)
	headerRange := fmt.Sprintf("bytes=%d-%d", f.pos, f.pos+int64(readLen)-1)
	headers := map[string]string{
		"Range":   headerRange,
		"Referer": ALIYUNDRIVE_HOST,
		"Accept":  "*/*",
	}

	resp, err := client.R().SetDoNotParseResponse(true).SetHeaders(headers).Get(url)
	if err != nil {
		logger.Warnf("read file fail: %v", err)
		return 0, err
	}

	rawBody := resp.RawBody()
	defer rawBody.Close()

	bs, err := io.ReadAll(rawBody)
	if err != nil {
		return 0, nil
	}

	statusCode := resp.StatusCode()
	if statusCode >= 300 {
		logger.Warnf("read file fail: %s", string(bs))
		return 0, fmt.Errorf("read file fail")
	}

	f.rb.Write(bs)
	f.pos += int64(len(bs))
	return f.rb.Read(p)
}

func (f *File) Seek(offset int64, whence int) (int64, error) {
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
	f.rb = nil
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
