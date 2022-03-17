package adrive

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	"github.com/isayme/aliyundrive-webdav/util"
	"github.com/isayme/go-logger"
)

type FileWriteCloser struct {
	file   *File
	client *AdriveClient

	uploadId string
	wc       io.WriteCloser

	uploadEnd chan error
	lock      sync.Mutex
}

func NewFileWriteCloser(client *AdriveClient, file *File) *FileWriteCloser {
	return &FileWriteCloser{
		client: client,
		file:   file,
	}
}

func (fwc *FileWriteCloser) Write(p []byte) (n int, err error) {
	fwc.lock.Lock()
	defer fwc.lock.Unlock()

	if fwc.wc == nil {
		reqBody := CreateFileReq{
			Name:          fwc.file.FileName,
			CheckNameMode: CHECK_NAME_MODE_REFUSE,
			DriveId:       fwc.client.fileDriveId,
			ParentFileId:  fwc.file.ParentFileId,
			Type:          FILE_TYPE_FILE,
			Size:          0,
		}
		respBody, err := fwc.client.createFile(context.Background(), &reqBody)
		if err != nil {
			logger.Errorf("创建文件 '%s' 失败: %v", fwc.file.FileName, err)
			return 0, err
		}
		logger.Infof("创建文件 '%s' 成功", fwc.file.FileName)

		fwc.file.FileId = respBody.FileId
		fwc.uploadId = respBody.UploadId

		if len(respBody.PartInfoList) < 1 {
			logger.Errorf("创建文件 '%s' 失败: part_info_list 空: %v", fwc.file.FileName, respBody)
			return 0, fmt.Errorf("no part info get")
		}

		uploadUrl := respBody.PartInfoList[0].UploadUrl
		rc, wc := io.Pipe()

		// 不可用 resty, resty 会 ReadAll request body
		URL, err := url.Parse(uploadUrl)
		if err != nil {
			logger.Errorf("解析文件 '%s' 上传链接失败: %v", fwc.file.FileName, err)
			return 0, err
		}

		req, err := http.NewRequest("PUT", uploadUrl, rc)
		if err != nil {
			logger.Errorf("打开文件 '%s' 上传链接失败: %v", fwc.file.FileName, err)
			return 0, err
		}

		go func() {
			var err error
			defer func() {
				fwc.uploadEnd <- err
			}()
			defer rc.Close()

			fwc.uploadEnd = make(chan error, 1)

			headers := http.Header{}
			headers.Set("User-Agent", util.UserAgent)
			headers.Set("Host", URL.Host)
			headers.Set("Referer", ALIYUNDRIVE_HOST)
			req.Header = headers

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				logger.Errorf("打开文件 '%s' 上传链接失败: %v", fwc.file.FileName, err)
				return
			}
			rawBody := resp.Body
			defer rawBody.Close()

			bs, err := io.ReadAll(rawBody)
			if err != nil {
				return
			}

			if resp.StatusCode >= 300 {
				logger.Errorf("写文件 '%s' 失败: %v, %s", fwc.file.FileName, err, string(bs))
				err = fmt.Errorf(string(bs))
			} else {
				logger.Infof("写文件 '%s' 结束", fwc.file.FileName)
			}
		}()

		fwc.wc = wc
	}

	return fwc.wc.Write(p)
}

func (fwc *FileWriteCloser) Close() (err error) {
	fwc.lock.Lock()
	defer fwc.lock.Unlock()

	defer func() {
		if err != nil {
			derr := fwc.client.deleteFile(context.Background(), fwc.file.DriveId, fwc.file.FileId)
			logger.Errorf("上传文件 '%s' 失败, 需要删除此文件. 失败原因: %v, 删除结果: %v", fwc.file.FileName, err, derr)
		} else {
			logger.Infof("上传文件 '%s' 成功", fwc.file.FileName)
		}
	}()

	if fwc.wc == nil {
		return nil
	}

	err = fwc.wc.Close()
	if err != nil {
		return err
	}

	// 等文件内容上传结束
	if fwc.uploadEnd != nil {
		err = <-fwc.uploadEnd
		if err != nil {
			return err
		}
	}

	return fwc.client.completeFile(context.Background(), &CompleteFileReq{
		DriveId:  fwc.client.fileDriveId,
		FileId:   fwc.file.FileId,
		UploadId: fwc.uploadId,
	})
}
