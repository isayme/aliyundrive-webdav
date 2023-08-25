package adrive

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"hash"
	"io/fs"
	"strings"
	"sync"
	"time"

	"github.com/isayme/go-alipanopen"
	"github.com/isayme/go-logger"
	"github.com/pkg/errors"
	"golang.org/x/net/webdav"
)

var _ webdav.File = &WritableFile{}

// 4G
const defaultMaxWriteBytes = 4 * 1024 * 1024 * 1024

type WritableFile struct {
	fi *FileInfo
	fs *FileSystem

	size    int64
	modTime time.Time

	uploadId string

	currentPartNum int
	uploader       *Uploader

	uploadEnd chan error
	lock      sync.Mutex

	hash hash.Hash
}

func NewWritableFile(fi *FileInfo, fs *FileSystem) (*WritableFile, error) {
	ctx := context.Background()
	writableFile := &WritableFile{
		fi: fi,
		fs: fs,

		currentPartNum: 0,
		hash:           sha1.New(),
	}

	reqBody := alipanopen.CreateFileReq{
		Name:          writableFile.fi.FileName,
		CheckNameMode: alipanopen.CHECK_NAME_MODE_REFUSE,
		DriveId:       writableFile.fi.DriveId,
		ParentFileId:  writableFile.fi.ParentFileId,
		Type:          alipanopen.FILE_TYPE_FILE,
		Size:          0,
	}
	respBody, err := writableFile.fs.client.CreateFile(ctx, &reqBody)
	if err != nil {
		logger.Errorf("创建文件 '%s' 失败: %v", writableFile.fi.FileName, err)
		return nil, err
	}
	logger.Infof("创建文件 '%s' 成功", writableFile.fi.FileName)

	writableFile.fi.FileId = respBody.FileId
	writableFile.uploadId = respBody.UploadId

	err = writableFile.getNextUploader()
	if err != nil {
		return nil, err
	}

	return writableFile, nil
}

func (writableFile *WritableFile) tryDeleteFile() {
	err := writableFile.fs.client.DeleteFile(context.Background(), writableFile.fi.DriveId, writableFile.fi.FileId)
	if err != nil {
		logger.Infof("删除文件 '%s' 失败: %v", writableFile.fi.FileName, err)
	} else {
		logger.Infof("删除文件 '%s' 成功", writableFile.fi.FileName)
	}
}

func (writableFile *WritableFile) getNextUploader() (err error) {
	defer func() {
		if err != nil {
			logger.Errorf("获取文件 '%s' 分片(%d)上传地址失败: %v", writableFile.fi.FileName, writableFile.currentPartNum, err)
		} else {
			logger.Infof("获取文件 '%s' 分片(%d)上传地址成功", writableFile.fi.FileName, writableFile.currentPartNum)
		}
	}()

	writableFile.currentPartNum = writableFile.currentPartNum + 1

	reqBody := &alipanopen.GetUploadUrlReq{
		DriveId:  writableFile.fi.DriveId,
		FileId:   writableFile.fi.FileId,
		UploadId: writableFile.uploadId,
		PartInfoList: []alipanopen.GetUploadPartInfoReq{
			{
				PartNumber: writableFile.currentPartNum,
			},
		},
	}
	getUploadUrlResp, err := writableFile.fs.client.GetUploadUrl(context.Background(), reqBody)
	if err != nil {
		return err
	}

	if len(getUploadUrlResp.PartInfoList) < 1 {
		return fmt.Errorf("no part info get")
	}

	uploadUrl := getUploadUrlResp.PartInfoList[0].UploadUrl
	uploader, err := NewUploader(uploadUrl, defaultMaxWriteBytes)
	if err != nil {
		writableFile.tryDeleteFile()
		return err
	}

	writableFile.uploader = uploader

	return nil
}

func (writableFile *WritableFile) Write(p []byte) (n int, err error) {
	writableFile.lock.Lock()
	defer writableFile.lock.Unlock()

	defer func() {
		writableFile.fi.FileSize = writableFile.fi.FileSize + int64(n)
		writableFile.hash.Write(p[:n])
	}()

	n, err = writableFile.uploader.Write(p)
	if err == ErrMaxWriteByteExceed {
		err = writableFile.uploader.CloseAndWait()
		if err != nil {
			err = errors.Wrap(err, "close current uploader")
			return
		}
		err = writableFile.getNextUploader()
		if err != nil {
			err = errors.Wrap(err, "get next uploader")
			return
		}
	}

	return
}

func (writableFile *WritableFile) Close() (err error) {
	writableFile.lock.Lock()
	defer writableFile.lock.Unlock()

	var result *alipanopen.CompleteFileResp
	defer func() {
		if err != nil {
			logger.Errorf("上传文件 '%s' 失败: %v", writableFile.fi.FileName, err)
			writableFile.tryDeleteFile()
		} else {
			hsum := strings.ToUpper(hex.EncodeToString(writableFile.hash.Sum(nil)))
			if hsum != result.ContentHash {
				logger.Warnf("上传文件 '%s' 成功, 文件大小: %d, 期望文件哈希: %s, 实际文件哈希: %s", writableFile.fi.FileName, result.Size, hsum, result.ContentHash)
			} else {
				logger.Infof("上传文件 '%s' 成功, 文件大小: %d, 文件哈希: %s", writableFile.fi.FileName, result.Size, result.ContentHash)
			}
		}
	}()

	// 等文件内容上传结束
	if writableFile.uploader != nil {
		err = writableFile.uploader.CloseAndWait()
		if err != nil {
			return err
		}
	}

	result, err = writableFile.fs.client.CompleteFile(context.Background(), &alipanopen.CompleteFileReq{
		DriveId:  writableFile.fi.DriveId,
		FileId:   writableFile.fi.FileId,
		UploadId: writableFile.uploadId,
	})
	return err
}

func (writableFile *WritableFile) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("not support")
}

func (writableFile *WritableFile) Seek(offset int64, whence int) (int64, error) {
	return 0, fmt.Errorf("not support")
}

func (writableFile *WritableFile) Readdir(count int) ([]fs.FileInfo, error) {
	return nil, fmt.Errorf("not support")
}

func (writableFile *WritableFile) Stat() (fs.FileInfo, error) {
	return writableFile.fi, nil
}
