package adrive

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"
	"sync"

	"github.com/isayme/go-logger"
	"github.com/pkg/errors"
)

// 4G
const defaultMaxWriteBytes = 4 * 1024 * 1024 * 1024

type FileWriteCloser struct {
	file *File
	fs   *FileSystem

	uploadId string

	currentPartNum int
	uploader       *Uploader

	uploadEnd chan error
	lock      sync.Mutex

	hash hash.Hash
}

func NewFileWriteCloser(file *File) (*FileWriteCloser, error) {
	ctx := context.Background()
	fwc := &FileWriteCloser{
		fs:             file.fs,
		file:           file,
		currentPartNum: 0,
		hash:           sha1.New(),
	}

	reqBody := CreateFileReq{
		Name:          fwc.file.FileName,
		CheckNameMode: CHECK_NAME_MODE_REFUSE,
		DriveId:       fwc.file.DriveId,
		ParentFileId:  fwc.file.ParentFileId,
		Type:          FILE_TYPE_FILE,
		Size:          0,
	}
	respBody, err := fwc.fs.createFile(ctx, &reqBody)
	if err != nil {
		logger.Errorf("创建文件 '%s' 失败: %v", fwc.file.FileName, err)
		return nil, err
	}
	logger.Infof("创建文件 '%s' 成功", fwc.file.FileName)

	fwc.file.FileId = respBody.FileId
	fwc.uploadId = respBody.UploadId

	err = fwc.getNextUploader()
	if err != nil {
		return nil, err
	}

	return fwc, nil
}

func (fwc *FileWriteCloser) tryDeleteFile() {
	err := fwc.fs.deleteFile(context.Background(), fwc.file.DriveId, fwc.file.FileId)
	if err != nil {
		logger.Infof("删除文件 '%s' 失败: %v", fwc.file.FileName, err)
	} else {
		logger.Infof("删除文件 '%s' 成功", fwc.file.FileName)
	}
}

func (fwc *FileWriteCloser) getNextUploader() (err error) {
	defer func() {
		if err != nil {
			logger.Errorf("获取文件 '%s' 分片(%d)上传地址失败: %v", fwc.file.FileName, fwc.currentPartNum, err)
		} else {
			logger.Infof("获取文件 '%s' 分片(%d)上传地址成功", fwc.file.FileName, fwc.currentPartNum)
		}
	}()

	fwc.currentPartNum = fwc.currentPartNum + 1
	getUploadUrlResp, err := fwc.fs.getUploadUrl(fwc.file.DriveId, fwc.file.FileId, fwc.uploadId, fwc.currentPartNum)
	if err != nil {
		return err
	}

	if len(getUploadUrlResp.PartInfoList) < 1 {
		return fmt.Errorf("no part info get")
	}

	uploadUrl := getUploadUrlResp.PartInfoList[0].UploadUrl
	uploader, err := NewUploader(uploadUrl, defaultMaxWriteBytes)
	if err != nil {
		fwc.tryDeleteFile()
		return err
	}

	fwc.uploader = uploader

	return nil
}

func (fwc *FileWriteCloser) Write(p []byte) (n int, err error) {
	fwc.lock.Lock()
	defer fwc.lock.Unlock()

	defer func() {
		fwc.file.FileSize = fwc.file.FileSize + int64(n)
		fwc.hash.Write(p[:n])
	}()

	n, err = fwc.uploader.Write(p)
	if err == ErrMaxWriteByteExceed {
		err = fwc.uploader.CloseAndWait()
		if err != nil {
			err = errors.Wrap(err, "close current uploader")
			return
		}
		err = fwc.getNextUploader()
		if err != nil {
			err = errors.Wrap(err, "get next uploader")
			return
		}
	}

	return
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
			hsum := strings.ToUpper(hex.EncodeToString(fwc.hash.Sum(nil)))
			if hsum != result.ContentHash {
				logger.Warnf("上传文件 '%s' 成功, 文件大小: %d, 期望文件哈希: %s, 实际文件哈希: %s", fwc.file.FileName, result.Size, hsum, result.ContentHash)
			} else {
				logger.Infof("上传文件 '%s' 成功, 文件大小: %d, 文件哈希: %s", fwc.file.FileName, result.Size, result.ContentHash)
			}
		}
	}()

	// 等文件内容上传结束
	if fwc.uploader != nil {
		err = fwc.uploader.CloseAndWait()
		if err != nil {
			return err
		}
	}

	result, err = fwc.fs.completeFile(context.Background(), &CompleteFileReq{
		DriveId:  fwc.file.DriveId,
		FileId:   fwc.file.FileId,
		UploadId: fwc.uploadId,
	})
	return err
}
