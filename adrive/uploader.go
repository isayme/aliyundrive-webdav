package adrive

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	"github.com/isayme/aliyundrive-webdav/util"
	"github.com/isayme/go-alipanopen"
	"github.com/isayme/go-logger"
)

var ErrMaxWriteByteExceed = fmt.Errorf("exceed max write byte")

type Uploader struct {
	nw int64

	// 当前分片已写入字节, 单分片写入限制最大5G
	maxWriteBytes int64

	wc io.WriteCloser

	uploadEnd chan error
	lock      sync.Mutex
}

func NewUploader(uploadUrl string, maxWriteBytes int64) (*Uploader, error) {
	u := &Uploader{
		maxWriteBytes: maxWriteBytes,
		uploadEnd:     make(chan error, 1),
	}

	// 不可用 resty, resty 会 ReadAll request body
	URL, err := url.Parse(uploadUrl)
	if err != nil {
		logger.Errorf("解析上传链接 '%s' 失败: %v", uploadUrl, err)
		return nil, err
	}

	rc, wc := io.Pipe()
	req, err := http.NewRequest("PUT", uploadUrl, rc)
	if err != nil {
		logger.Errorf("打开上传链接 '%s' 失败: %v", uploadUrl, err)
		return nil, err
	}

	go func() {
		var err error
		defer func() {
			u.uploadEnd <- err
		}()
		defer rc.Close()

		headers := http.Header{}
		headers.Set(alipanopen.HEADER_USER_AGENT, util.UserAgent)
		headers.Set(alipanopen.HEADER_HOST, URL.Host)
		headers.Set(alipanopen.HEADER_REFERER, ALIYUNDRIVE_HOST)
		req.Header = headers

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			logger.Errorf("打开上传链接 '%s' 失败: %v", uploadUrl, err)
			return
		}

		rawBody := resp.Body
		defer rawBody.Close()

		bs, err := io.ReadAll(rawBody)
		if err != nil {
			return
		}

		if resp.StatusCode >= 300 {
			logger.Errorf("上传文件内容 '%s' 失败: %v, %s", uploadUrl, err, string(bs))
			err = fmt.Errorf(string(bs))
		} else {
			logger.Infof("上传文件内容 '%s' 结束", uploadUrl)
		}
	}()

	u.wc = wc

	return u, nil
}

func (u *Uploader) Write(p []byte) (n int, err error) {
	u.lock.Lock()
	defer u.lock.Unlock()

	defer func() {
		u.nw = u.nw + int64(n)

		if err == nil && u.nw >= u.maxWriteBytes {
			err = ErrMaxWriteByteExceed
		}
	}()

	remainBytes := u.maxWriteBytes - u.nw
	if int64(len(p)) > remainBytes {
		p = p[:remainBytes]
	}
	return u.wc.Write(p)
}

func (u *Uploader) CloseAndWait() error {
	u.lock.Lock()
	defer u.lock.Unlock()

	err := u.wc.Close()
	if err != nil {
		return err
	}

	if u.uploadEnd != nil {
		return <-u.uploadEnd
	}

	return nil
}
