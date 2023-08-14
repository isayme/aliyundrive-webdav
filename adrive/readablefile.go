package adrive

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"sync"

	"github.com/isayme/go-logger"
	"golang.org/x/net/webdav"
)

var _ webdav.File = &ReadableFile{}

type ReadableFile struct {
	fi *FileInfo
	fs *FileSystem

	pos  int64
	rc   io.ReadCloser
	lock sync.Mutex
}

func NewReadableFile(fi *FileInfo, fs *FileSystem) *ReadableFile {
	return &ReadableFile{
		fi: fi,
		fs: fs,
	}
}

func (readableFile *ReadableFile) Read(p []byte) (n int, err error) {
	readableFile.lock.Lock()
	defer readableFile.lock.Unlock()

	defer func() {
		// 断点续传
		readableFile.pos = readableFile.pos + int64(n)
		if err == io.EOF {
			logger.Infof("读文件 '%s' 结束", readableFile.fi.Name())
		}
	}()

	if readableFile.rc == nil {
		downloadUrl, err := readableFile.fs.getDownloadUrl(readableFile.fi.FileId, readableFile.fi.ContentHash)
		if err != nil {
			logger.Errorf("获取文件 '%s' 下载链接失败: %v", readableFile.fi.Name(), err)
			return 0, err
		}

		if downloadUrl == "" {
			return 0, fmt.Errorf("download url not return")
		}

		headers := map[string]string{
			HEADER_ACCEPT: "*/*",
		}
		if readableFile.pos > 0 {
			headerRange := fmt.Sprintf("bytes=%d-", readableFile.pos)
			headers[HEADER_RANGE] = headerRange
		}

		logger.Debugf("Read(%s/%s) Pos %d", readableFile.fi.Name(), readableFile.fi.FileId, readableFile.pos)
		resp, err := client.R().SetDoNotParseResponse(true).SetHeaders(headers).Get(downloadUrl)
		if err != nil {
			logger.Warnf("打开文件 '%s' 下载链接失败: %v", readableFile.fi.Name(), err)
			return 0, err
		}

		rawBody := resp.RawBody()

		if resp.StatusCode() >= 300 {
			bs, err := io.ReadAll(rawBody)
			logger.Warnf("打开文件 '%s' 下载链接失败, err: %v, body: %s", readableFile.fi.Name(), err, string(bs))
			return 0, fmt.Errorf("open download url fail")
		}

		readableFile.rc = rawBody
	}

	return readableFile.rc.Read(p)
}

func (readableFile *ReadableFile) Close() error {
	readableFile.lock.Lock()
	defer readableFile.lock.Unlock()

	if readableFile.rc == nil {
		return nil
	}

	readableFile.pos = 0

	err := readableFile.rc.Close()
	readableFile.rc = nil
	return err
}

func (readableFile *ReadableFile) Seek(offset int64, whence int) (int64, error) {
	logger.Debugf("Seek(%s/%s) offset %d whence %d", readableFile.fi.Name(), readableFile.fi.FileId, offset, whence)
	readableFile.lock.Lock()
	defer readableFile.lock.Unlock()

	pos := readableFile.pos

	switch whence {
	case io.SeekStart:
		pos = offset
	case io.SeekCurrent:
		pos += offset
	case io.SeekEnd:
		pos = readableFile.fi.FileSize + offset
	default:
		return 0, fmt.Errorf("not support")
	}

	readableFile.pos = pos

	if readableFile.rc != nil {
		readableFile.rc.Close()
		readableFile.rc = nil
	}

	return readableFile.pos, nil
}

func (readableFile *ReadableFile) Readdir(count int) (result []fs.FileInfo, err error) {
	defer func() {
		if err != nil {
			logger.Infof("列举目录 '%s' 失败: %v", readableFile.fi.Name(), err)
		} else {
			logger.Infof("列举目录 '%s' 成功, 共有子文件 %d 个", readableFile.fi.Name(), len(result))
		}
	}()

	files, err := readableFile.fs.listDir(context.Background(), readableFile.fi)
	if err != nil {
		return nil, err
	}

	result = make([]fs.FileInfo, len(files))
	for idx, file := range files {
		result[idx] = file
	}

	return result, nil
}

func (readableFile *ReadableFile) Write(p []byte) (n int, err error) {
	return 0, fmt.Errorf("not support")
}

func (readableFile *ReadableFile) Stat() (fi fs.FileInfo, err error) {
	return readableFile.fi, nil
}
