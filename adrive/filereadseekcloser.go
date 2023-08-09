package adrive

import (
	"fmt"
	"io"
	"sync"

	"github.com/isayme/go-logger"
)

type FileReadSeekerCloser struct {
	file *File

	fs *FileSystem

	pos int64

	rc io.ReadCloser

	lock sync.Mutex
}

func NewFileReadCloser(file *File) *FileReadSeekerCloser {
	return &FileReadSeekerCloser{
		file: file,
		fs:   file.fs,
	}
}

func (rsc *FileReadSeekerCloser) Read(p []byte) (n int, err error) {
	rsc.lock.Lock()
	defer rsc.lock.Unlock()

	defer func() {
		// 断点续传
		rsc.pos = rsc.pos + int64(n)
		if err == io.EOF {
			logger.Infof("读文件 '%s' 结束", rsc.file.FileName)
		}
	}()

	if rsc.rc == nil {
		downloadUrl, err := rsc.fs.getDownloadUrl(rsc.file.FileId, rsc.file.ContentHash)
		if err != nil {
			logger.Errorf("获取文件 '%s' 下载链接失败: %v", rsc.file.FileName, err)
			return 0, err
		}

		if downloadUrl == "" {
			return 0, fmt.Errorf("download url not return")
		}

		headerRange := fmt.Sprintf("bytes=%d-", rsc.pos)
		headers := map[string]string{
			HEADER_RANGE:  headerRange,
			HEADER_ACCEPT: "*/*",
		}

		logger.Debugf("Read(%s/%s) Range %s", rsc.file.FileName, rsc.file.FileId, headerRange)
		resp, err := client.R().SetDoNotParseResponse(true).SetHeaders(headers).Get(downloadUrl)
		if err != nil {
			logger.Warnf("打开文件 '%s' 下载链接失败: %v", rsc.file.FileName, err)
			return 0, err
		}

		rawBody := resp.RawBody()

		if resp.StatusCode() >= 300 {
			bs, err := io.ReadAll(rawBody)
			logger.Warnf("打开文件 '%s' 下载链接失败, err: %v, body: %s", rsc.file.FileName, err, string(bs))
			return 0, fmt.Errorf("open download url fail")
		}

		rsc.rc = rawBody
	}

	return rsc.rc.Read(p)
}

func (rsc *FileReadSeekerCloser) Close() error {
	rsc.lock.Lock()
	defer rsc.lock.Unlock()

	if rsc.rc == nil {
		return nil
	}

	rsc.pos = 0

	return rsc.rc.Close()
}

func (rsc *FileReadSeekerCloser) Seek(offset int64, whence int) (int64, error) {
	logger.Debugf("Seek(%s/%s) offset %d whence %d", rsc.file.FileName, rsc.file.FileId, offset, whence)
	rsc.lock.Lock()
	defer rsc.lock.Unlock()

	pos := rsc.pos

	switch whence {
	case io.SeekStart:
		pos = offset
	case io.SeekCurrent:
		pos += offset
	case io.SeekEnd:
		pos = rsc.file.FileSize + offset
	default:
		return 0, fmt.Errorf("not support")
	}

	rsc.pos = pos

	if rsc.rc != nil {
		rsc.rc.Close()
		rsc.rc = nil
	}

	return rsc.pos, nil
}
