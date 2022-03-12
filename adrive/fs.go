package adrive

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/isayme/go-logger"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"golang.org/x/net/webdav"
	"golang.org/x/sync/singleflight"
)

type FileSystem struct {
	client *AdriveClient

	sg *singleflight.Group

	fileCache *cache.Cache
}

func NewFileSystem(refreshToken string) (*FileSystem, error) {
	client, err := NewAdriveClient(refreshToken)
	if err != nil {
		return nil, err
	}

	fs := &FileSystem{
		client:    client,
		sg:        &singleflight.Group{},
		fileCache: cache.New(time.Second*10, time.Second),
	}

	return fs, nil
}

func (fs *FileSystem) resolve(name string) string {
	return strings.TrimRight(name, "/")
}

func (fs *FileSystem) rootFolder() *File {
	return &File{
		client:    fs.client,
		FileName:  "/",
		FileId:    "root",
		FileSize:  0,
		UpdatedAt: time.Now(),
		Type:      "folder",
	}
}

func (fs *FileSystem) isInvalidFileName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}

	return false
}

func (fs *FileSystem) getFile(ctx context.Context, name string) (*File, error) {
	name = fs.resolve(name)

	if v, ok := fs.fileCache.Get(name); ok {
		file := v.(*File)
		return file.Clone(), nil
	}

	if name == "" || name == "/" {
		root := fs.rootFolder()
		fs.fileCache.Set(name, root, -1)
		return root, nil
	}

	if fs.isInvalidFileName(path.Base(name)) {
		return nil, os.ErrInvalid
	}

	result, err, _ := fs.sg.Do(name, func() (interface{}, error) {
		return fs.client.getFileByPath(ctx, name)
	})
	if err != nil {
		logger.Infof("获取文件 '%s' 失败: %v", name, err)
		return nil, err
	}

	file := result.(*File)
	fs.fileCache.Set(name, file, 0)
	return file, nil
}

func (fs *FileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) (err error) {
	defer func() {
		if err != nil {
			logger.Errorf("新建文件夹 '%s' 失败: %v", name, err)
		} else {
			logger.Infof("新建文件夹 '%s' 成功", name)
		}
	}()
	name = fs.resolve(name)

	parentFolderName := path.Dir(name)
	parentFolder, err := fs.getFile(ctx, parentFolderName)
	if err != nil {
		return err
	}

	file, err := fs.client.createFolder(ctx, path.Base(name), parentFolder.FileId)
	if err != nil {
		return err
	}

	fs.fileCache.Add(name, &file, 0)
	return nil
}

func (fs *FileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	// 调用量太大, 减少日志打印
	if flag != 0 {
		logger.Infof("打开文件 '%s', flag: %x, perm: %s", name, flag, perm.String())
	}

	name = fs.resolve(name)

	if flag&os.O_TRUNC > 0 {
		err := fs.RemoveAll(ctx, name)
		if err != nil && err != os.ErrNotExist {
			logger.Infof("打开文件 '%s' 失败: 删除原文件失败: %v", name, err)
			return nil, err
		}
	}

	if flag&os.O_CREATE > 0 {
		fileName := path.Base(name)

		if fs.isInvalidFileName(fileName) {
			return nil, os.ErrPermission
		}

		parentFolder, err := fs.getFile(ctx, path.Dir(name))
		if err != nil {
			logger.Errorf("获取父文件夹失败, err: %v", err)
			return nil, err
		}

		file := &File{
			FileName:     fileName,
			ParentFileId: parentFolder.FileId,
			Type:         "file",
			UpdatedAt:    time.Now(),
			client:       fs.client,
		}

		fs.fileCache.Set(name, file, 0)
		return file, nil
	}

	file, err := fs.getFile(ctx, name)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (fs *FileSystem) RemoveAll(ctx context.Context, name string) (err error) {
	defer func() {
		if err != nil {
			logger.Errorf("删除文件 '%s' 失败: %v", name, err)
		} else {
			logger.Infof("删除文件成功: %s", name)
		}
	}()

	fs.fileCache.Delete(name)

	file, err := fs.getFile(ctx, name)
	if err != nil {
		if err == os.ErrNotExist || err == os.ErrInvalid {
			return nil
		}
		return err
	}

	// 新文件, 还没有文件ID
	if file.FileId == "" {
		// logger.Errorf("删除文件 '%s' 失败: 文件还未创建完成", name)
		return nil
	}

	if file.FileId == "root" {
		logger.Error("根目录不允许删除")
		return fmt.Errorf("cannot remove root folder")
	}

	return fs.client.trashFile(ctx, file.FileId)
}

func (fs *FileSystem) Rename(ctx context.Context, oldName, newName string) (err error) {
	defer func() {
		if err != nil {
			logger.Errorf("移动文件 '%s' 到 '%s' 失败: %v", oldName, newName, err)
		} else {
			logger.Infof("移动文件 '%s' 到 '%s' 成功", oldName, newName)
		}
	}()

	oldName = fs.resolve(oldName)
	newName = fs.resolve(newName)

	sourceFile, err := fs.getFile(ctx, oldName)
	if err != nil {
		return errors.Wrapf(err, "获取源文件失败")
	}

	newFolder := path.Dir(newName)
	newFileName := path.Base(newName)

	if path.Dir(oldName) == newFolder {
		err := fs.client.updateFileName(sourceFile.FileId, newFileName)
		if err != nil {
			return errors.Wrapf(err, "重命名")
		}
	} else {
		newParentFolder, err := fs.getFile(ctx, newFolder)
		if err != nil {
			return errors.Wrapf(err, "获取目的父文件夹失败")
		}

		err = fs.client.moveFile(sourceFile.FileId, newParentFolder.FileId, newFileName)
		if err != nil {
			return errors.Wrapf(err, "移动失败")
		}
	}

	return nil
}

func (fs *FileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	file, err := fs.getFile(ctx, name)
	if err != nil {
		return nil, err
	}

	return file.Stat()
}
