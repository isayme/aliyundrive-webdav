package adrive

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/dghubble/trie"
	"github.com/isayme/go-logger"
	"github.com/mdp/qrterminal/v3"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"golang.org/x/net/webdav"
	"golang.org/x/sync/singleflight"
)

var _ webdav.FileSystem = &FileSystem{}

type FileSystem struct {
	clientId     string
	clientSecret string

	refreshToken          string
	accessToken           string
	accessTokenExpireTime time.Time
	fileDriveId           string

	sg   *singleflight.Group
	root *trie.PathTrie

	c *cache.Cache
}

func NewFileSystem(clientId, clientSecret string) (*FileSystem, error) {
	fs := &FileSystem{
		clientId:     clientId,
		clientSecret: clientSecret,
		sg:           &singleflight.Group{},
		root:         trie.NewPathTrie(),
		c:            cache.New(5*time.Minute, 10*time.Minute),
	}

	refreshToken, err := readRefreshToken()
	if err != nil {
		return nil, err
	}

	if refreshToken != "" {
		fs.refreshToken = refreshToken
		refreshTokenResp, err := fs.doRefreshToken()
		if err != nil {
			logger.Warnf("使用 refreshToken 刷新 token 失败: %v", err)
		} else {
			fs.accessToken = refreshTokenResp.AccessToken
			fs.refreshToken = refreshTokenResp.RefreshToken
			fs.accessTokenExpireTime = time.Now().Add(time.Second * time.Duration(refreshTokenResp.ExpiresIn))
			fs.writeRefreshToken()
		}
	}

	if fs.accessToken == "" {
		qrCodeResp, err := fs.getQrCode()
		if err != nil {
			return nil, err
		}

		qrCodeText := "https://www.aliyundrive.com/o/oauth/authorize?sid=" + qrCodeResp.Sid
		qrterminal.GenerateWithConfig(qrCodeText, qrterminal.Config{
			Level:          qrterminal.L,
			Writer:         os.Stdout,
			HalfBlocks:     true,
			BlackChar:      qrterminal.BLACK_BLACK,
			WhiteChar:      qrterminal.WHITE_WHITE,
			WhiteBlackChar: qrterminal.WHITE_BLACK,
			BlackWhiteChar: qrterminal.BLACK_WHITE,
			QuietZone:      2,
		})

		done := false

		for {
			if done {
				break
			}

			qrCodeStatusResp, err := fs.getQrCodeStatus(qrCodeResp.Sid)
			if err != nil {
				return nil, err
			}

			switch qrCodeStatusResp.Status {
			case qrCodeStatusWaitLogin:
				logger.Info("等待扫码...")
				time.Sleep(time.Second)
			case qrCodeStatusScanSuccess:
				logger.Info("已扫码成功")
				time.Sleep(time.Second)
			case qrCodeStatusLoginSuccess:
				logger.Info("已登录成功")

				refreshTokenResp, err := fs.doRefreshTokenByAuthCode(qrCodeStatusResp.AuthCode)
				if err != nil {
					return nil, err
				}
				fs.accessToken = refreshTokenResp.AccessToken
				fs.refreshToken = refreshTokenResp.RefreshToken
				fs.accessTokenExpireTime = time.Now().Add(time.Second * time.Duration(refreshTokenResp.ExpiresIn))
				fs.writeRefreshToken()
				done = true
			case qrCodeStatusQRCodeExpired:
				return nil, fmt.Errorf("二维码过期")
			}
		}
	}

	user, err := fs.getCurrentUser()
	if err != nil {
		return nil, err
	}

	logger.Infof("认证成功, 当前账号昵称: %s, ID: %s", user.Name, user.Id)

	driveInfo, err := fs.getDriveInfo()
	if err != nil {
		return nil, err
	}
	fs.fileDriveId = driveInfo.BackupDriveId

	fs.root.Put("/", fs.rootFolder())

	go func() {
		// 每小时获取一次个人信息, 以避免长时间无使用导致 refresh_token 无法刷新失效.
		for {
			time.Sleep(time.Hour)

			user, err := fs.getCurrentUser()
			if err != nil {
				logger.Warnf("自动保活失败: %v", err)
			} else {
				logger.Infof("自动保活成功, 当前账号昵称: %s, ID: %s", user.Name, user.Id)
			}
		}
	}()

	return fs, nil
}

func (fs *FileSystem) writeRefreshToken() {
	err := writeRefreshToken(fs.refreshToken)
	if err != nil {
		logger.Warnf("写 refreshToken 失败: %v", err)
	}
}

func (fs *FileSystem) cleanTrie(prefix string) {
	fs.root.Walk(func(key string, value interface{}) error {
		if strings.HasPrefix(key, prefix) {
			fs.root.Delete(key)
		}

		return nil
	})
}

func (fs *FileSystem) resolve(name string) string {
	return strings.TrimRight(name, "/")
}

func (fs *FileSystem) rootFolder() *FileInfo {
	return &FileInfo{
		FileName:  "/",
		FileId:    ROOT_FOLDER_ID,
		DriveId:   fs.fileDriveId,
		FileSize:  0,
		UpdatedAt: time.Now(),
		Type:      FILE_TYPE_FOLDER,
	}
}

func (fs *FileSystem) isInvalidFileName(name string) bool {
	// if strings.HasPrefix(name, ".") {
	// 	return true
	// }

	return false
}

func (fs *FileSystem) getFile(ctx context.Context, name string) (*FileInfo, error) {
	name = fs.resolve(name)

	if name == "" || name == "/" {
		root := fs.rootFolder()
		return root, nil
	}

	if fs.isInvalidFileName(path.Base(name)) {
		return nil, os.ErrInvalid
	}

	file, err := fs.getFileByPath(ctx, name)
	if err != nil {
		return nil, err
	}

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

	folder, err := fs.createFolder(ctx, path.Base(name), parentFolder.FileId)
	if err != nil {
		return err
	}

	fs.root.Put(name, folder)

	return nil
}

func (fs *FileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (result webdav.File, err error) {
	defer func() {
		if err != nil {
			logger.Errorf("打开文件 '%s' 失败, flag: %x, perm: %s, err: %v", name, flag, perm.String(), err)
		} else {
			// 调用量太大, 减少日志打印
			// if flag != 0 {
			logger.Infof("打开文件 '%s' 成功, flag: %x, perm: %s", name, flag, perm.String())
			// }
		}
	}()

	name = fs.resolve(name)

	if flag&(os.O_SYNC|os.O_APPEND) > 0 {
		return nil, os.ErrInvalid
	}

	if flag&os.O_TRUNC > 0 {
		err := fs.RemoveAll(ctx, name)
		if err != nil && err != os.ErrNotExist {
			return nil, errors.Wrap(err, "删除源文件失败")
		}
	}

	if flag&os.O_CREATE > 0 {
		fileName := path.Base(name)

		if fs.isInvalidFileName(fileName) {
			return nil, os.ErrPermission
		}

		parentFolder, err := fs.getFile(ctx, path.Dir(name))
		if err != nil {
			return nil, errors.Wrap(err, "获取父文件夹失败")
		}

		file := &FileInfo{
			FileName:     fileName,
			ParentFileId: parentFolder.FileId,
			DriveId:      parentFolder.DriveId,
			Type:         FILE_TYPE_FILE,
			UpdatedAt:    time.Now(),
		}

		fs.root.Put(name, file)
		return NewWritableFile(file, fs)
	}

	file, err := fs.getFile(ctx, name)
	if err != nil {
		return nil, err
	}
	fs.root.Put(name, file)
	return NewReadableFile(file, fs), nil
}

func (fs *FileSystem) RemoveAll(ctx context.Context, name string) (err error) {
	defer func() {
		if err != nil {
			logger.Errorf("删除文件 '%s' 失败: %v", name, err)
		} else {
			logger.Infof("删除文件 '%s' 成功", name)
		}
	}()

	fs.cleanTrie(name)

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

	if file.FileId == ROOT_FOLDER_ID {
		return fmt.Errorf("根目录不允许删除")
	}

	return fs.trashFile(ctx, file.FileId)
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

	fs.cleanTrie(oldName)
	fs.cleanTrie(newName)

	sourceFile, err := fs.getFile(ctx, oldName)
	if err != nil {
		return errors.Wrapf(err, "获取源文件失败")
	}

	newFolder := path.Dir(newName)
	newFileName := path.Base(newName)

	if path.Dir(oldName) == newFolder {
		err := fs.updateFileName(sourceFile.FileId, newFileName)
		if err != nil {
			return errors.Wrapf(err, "重命名")
		}
	} else {
		newParentFolder, err := fs.getFile(ctx, newFolder)
		if err != nil {
			return errors.Wrapf(err, "获取目的父文件夹失败")
		}

		err = fs.moveFile(sourceFile.FileId, newParentFolder.FileId, newFileName)
		if err != nil {
			return errors.Wrapf(err, "移动失败")
		}
	}

	return nil
}

func (fs *FileSystem) Stat(ctx context.Context, name string) (fi os.FileInfo, err error) {
	defer func() {
		if err != nil {
			logger.Errorf("查看文件 '%s' 信息失败: %v", name, err)
		} else {
			logger.Infof("查看文件 '%s' 信息成功", name)
		}
	}()

	file, err := fs.getFile(ctx, name)
	if err != nil {
		return nil, err
	}

	return file, nil
}

func (fs *FileSystem) cacheSetDownloadUrl(contentHash string, downloadUrl string, duration time.Duration) {
	fs.c.Set(fmt.Sprintf("downloadUrl-%s", contentHash), downloadUrl, duration)
}

func (fs *FileSystem) cacheGetDownloadUrl(contentHash string) string {
	v, ok := fs.c.Get(fmt.Sprintf("downloadUrl-%s", contentHash))
	if ok {
		return v.(string)
	}

	return ""
}
