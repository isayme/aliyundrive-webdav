package adrive

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/isayme/go-logger"
)

type FileReadSeekerCloser struct {
	file   *File
	client *AdriveClient

	pos                   int64
	downloadUrlExpiration time.Time

	rc io.ReadCloser

	lock sync.Mutex
}

func NewFileReadCloser(client *AdriveClient, file *File) *FileReadSeekerCloser {
	return &FileReadSeekerCloser{
		file:   file,
		client: client,
	}
}

func (rsc *FileReadSeekerCloser) Read(p []byte) (n int, err error) {
	rsc.lock.Lock()
	defer rsc.lock.Unlock()

	defer func() {
		if err == io.EOF {
			logger.Infof("读文件 '%s' 结束", rsc.file.FileName)
		}
	}()

	if rsc.rc == nil || time.Now().After(rsc.downloadUrlExpiration) {
		downloadInfo, err := rsc.client.getDownloadUrl(rsc.file.FileId)
		if err != nil {
			logger.Errorf("获取文件 '%s' 下载链接失败: %v", rsc.file.FileName, err)
			return 0, err
		}

		rsc.downloadUrlExpiration = downloadInfo.Expiration

		headerRange := fmt.Sprintf("bytes=%d-", rsc.pos)
		headers := map[string]string{
			"Range":  headerRange,
			"Accept": "*/*",
		}

		resp, err := client.R().SetDoNotParseResponse(true).SetHeaders(headers).Get(downloadInfo.Url)
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

	return rsc.rc.Close()
}

func (rsc *FileReadSeekerCloser) Seek(offset int64, whence int) (int64, error) {
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
