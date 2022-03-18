package adrive

import (
	"context"
	"fmt"
	"sync"

	"github.com/isayme/go-logger"
)

const defaultMaxWriteBytes = 5 * 1024 * 1024 * 1024

type FileWriteCloser struct {
	file   *File
	client *AdriveClient

	uploadId string

	currentPartNum int
	uploader       *Uploader

	uploadEnd chan error
	lock      sync.Mutex
}

func NewFileWriteCloser(client *AdriveClient, file *File) (*FileWriteCloser, error) {
	ctx := context.Background()
	fwc := &FileWriteCloser{
		client: client,
		file:   file,
	}

	reqBody := CreateFileReq{
		Name:          fwc.file.FileName,
		CheckNameMode: CHECK_NAME_MODE_REFUSE,
		DriveId:       fwc.client.fileDriveId,
		ParentFileId:  fwc.file.ParentFileId,
		Type:          FILE_TYPE_FILE,
		Size:          0,
	}
	respBody, err := fwc.client.createFile(ctx, &reqBody)
	if err != nil {
		logger.Errorf("创建文件 '%s' 失败: %v", fwc.file.FileName, err)
		return nil, err
	}
	logger.Infof("创建文件 '%s' 成功", fwc.file.FileName)

	fwc.file.FileId = respBody.FileId
	fwc.uploadId = respBody.UploadId

	if len(respBody.PartInfoList) < 1 {
		logger.Errorf("创建文件 '%s' 失败: part_info_list 空: %v", fwc.file.FileName, respBody)
		return nil, fmt.Errorf("no part info get")
	}

	uploadUrl := respBody.PartInfoList[0].UploadUrl
	uploader, err := NewUploader(uploadUrl, defaultMaxWriteBytes)
	if err != nil {
		fwc.tryDeleteFile()
		return nil, err
	}

	fwc.uploader = uploader

	return fwc, nil
}

func (fwc *FileWriteCloser) tryDeleteFile() {
	err := fwc.client.deleteFile(context.Background(), fwc.file.DriveId, fwc.file.FileId)
	if err != nil {
		logger.Infof("删除文件 '%s' 失败: %v", fwc.file.FileName, err)
	} else {
		logger.Infof("删除文件 '%s' 成功", fwc.file.FileName)
	}
}

// func (fwc *FileWriteCloser) getNextUploader() (*Uploader, error) {

// }

func (fwc *FileWriteCloser) Write(p []byte) (n int, err error) {
	fwc.lock.Lock()
	defer fwc.lock.Unlock()

	return fwc.uploader.Write(p)
}

func (fwc *FileWriteCloser) Close() (err error) {
	fwc.lock.Lock()
	defer fwc.lock.Unlock()

	var result *CompleteFileResp
	defer func() {
		if err != nil {
			logger.Errorf("上传文件 '%s' 失败: %v", fwc.file.FileName, err)
			fwc.tryDeleteFile()
		} else {
			logger.Infof("上传文件 '%s' 成功, 文件哈希: %s, 文件大小: %d", fwc.file.FileName, result.ContentHash, result.Size)
		}
	}()

	// 等文件内容上传结束
	if fwc.uploader != nil {
		err = fwc.uploader.CloseAndWait()
		if err != nil {
			return err
		}
	}

	result, err = fwc.client.completeFile(context.Background(), &CompleteFileReq{
		DriveId:  fwc.client.fileDriveId,
		FileId:   fwc.file.FileId,
		UploadId: fwc.uploadId,
	})
	return err
}
