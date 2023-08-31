package adrive

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/dghubble/trie"
	"github.com/isayme/aliyundrive-webdav/util"
	"github.com/isayme/go-alipanopen"
	"github.com/isayme/go-logger"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"golang.org/x/net/webdav"
	"golang.org/x/sync/singleflight"
)

const ALIYUNDRIVE_HOST = "https://www.aliyundrive.com"

var _ webdav.FileSystem = &FileSystem{}

type FileSystem struct {
	rootDir  string
	rootFile *FileInfo

	clientId     string
	clientSecret string
	client       *alipanopen.Client
	fileDriveId  string

	cache *cache.Cache
	root  *trie.PathTrie
	sg    *singleflight.Group

	refreshToken          string
	accessToken           string
	accessTokenExpireTime time.Time
}

func NewFileSystem(config AlipanConfig) (*FileSystem, error) {
	ctx := context.Background()

	clientId := config.ClientId
	clientSecret := config.ClientSecret
	rootDir := config.RootDir
	rootDir = path.Join(rootDir, "/")

	client := alipanopen.NewClient()
	client.SetRestyClient(restyClient)

	fs := &FileSystem{
		clientId:     clientId,
		clientSecret: clientSecret,

		client: client,
		cache:  cache.New(5*time.Minute, 10*time.Minute),
		root:   trie.NewPathTrie(),
		sg:     &singleflight.Group{},
	}

	refreshToken, err := readRefreshToken()
	if err != nil {
		return nil, err
	}
	fs.refreshToken = refreshToken

	if refreshToken != "" {
		reqBody := &alipanopen.RefreshTokenReq{
			ClientId:     clientId,
			ClientSecret: clientSecret,
			RefreshToken: refreshToken,
			GrantType:    alipanopen.GRANT_TYPE_REFRESH_TOKEN,
		}
		refreshTokenResp, err := fs.client.RefreshToken(ctx, reqBody)
		if err != nil {
			logger.Warnf("使用 refreshToken 刷新 token 失败: %v", err)
		} else {
			fs.saveToken(refreshTokenResp)
		}
	}

	err = fs.authIfRequired(ctx)
	if err != nil {
		return nil, err
	}

	user, err := fs.client.GetCurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	logger.Infof("认证成功, 当前账号昵称: %s, ID: %s", user.Name, user.Id)
	driveInfo, err := fs.client.GetDriveInfo(ctx)
	if err != nil {
		return nil, err
	}
	fs.fileDriveId = driveInfo.BackupDriveId

	if rootDir != "/" {
		reqBody := &alipanopen.GetFileByPathReq{
			DriveId:  fs.fileDriveId,
			FilePath: rootDir,
		}
		rootFolder, err := fs.client.GetFileByPath(ctx, reqBody)
		if err != nil {
			return nil, err
		}
		fs.rootDir = rootDir
		fs.rootFile = NewFileInfo(rootFolder)
	} else {
		fs.rootFile = NewFileInfo(&alipanopen.File{
			FileName: util.Name,
			DriveId:  fs.fileDriveId,
			FileId:   alipanopen.ROOT_FOLDER_ID,
			Type:     alipanopen.FILE_TYPE_FOLDER,
		})
	}
	fs.root.Put(rootDir, fs.rootFile)

	fs.startKeepAlive()

	return fs, nil
}

func (fs *FileSystem) startKeepAlive() {
	go func() {
		// 每小时刷新一次 access_token , 以避免长时间无使用导致 access_token、refresh_token 过期.
		for {
			time.Sleep(time.Hour)

			reqBody := &alipanopen.RefreshTokenReq{
				ClientId:     fs.clientId,
				ClientSecret: fs.clientSecret,
				RefreshToken: fs.refreshToken,
				GrantType:    alipanopen.GRANT_TYPE_REFRESH_TOKEN,
			}
			refreshTokenResp, err := fs.client.RefreshToken(context.Background(), reqBody)
			if err != nil {
				logger.Warnf("自动保活失败: %v", err)
			} else {
				logger.Infof("自动保活成功")
				fs.saveToken(refreshTokenResp)
				fs.client.SetAccessToken(fs.accessToken)
			}
		}
	}()
}

func (fs *FileSystem) saveToken(refreshTokenResp *alipanopen.RefreshTokenResp) {
	fs.accessToken = refreshTokenResp.AccessToken
	fs.refreshToken = refreshTokenResp.RefreshToken
	fs.accessTokenExpireTime = time.Now().Add(time.Second * time.Duration(refreshTokenResp.ExpiresIn))
	fs.client.SetAccessToken(fs.accessToken)
	fs.writeRefreshToken(refreshTokenResp.RefreshToken)
}

func (fs *FileSystem) writeRefreshToken(refreshToken string) {
	err := writeRefreshToken(refreshToken)
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
	return path.Join(fs.rootDir, name)
}

func (fs *FileSystem) getFile(ctx context.Context, name string) (*FileInfo, error) {
	if name == "" || name == "/" {
		return fs.rootFile, nil
	}

	file, err := fs.getFileByPath(ctx, fs.fileDriveId, name)
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

	reqBody := &alipanopen.CreateFolderReq{
		DriveId:       parentFolder.DriveId,
		ParentFileId:  parentFolder.FileId,
		Name:          path.Base(name),
		CheckNameMode: alipanopen.CHECK_NAME_MODE_REFUSE,
	}
	_, err = fs.client.CreateFolder(ctx, reqBody)
	if err != nil {
		return err
	}

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

	if flag&(os.O_SYNC|os.O_APPEND) > 0 {
		return nil, os.ErrInvalid
	}

	if flag&os.O_TRUNC > 0 {
		err := fs.RemoveAll(ctx, name)
		if err != nil && err != os.ErrNotExist {
			return nil, errors.Wrap(err, "删除源文件失败")
		}
	}

	name = fs.resolve(name)

	if flag&os.O_CREATE > 0 {
		fileName := path.Base(name)

		parentFolder, err := fs.getFile(ctx, path.Dir(name))
		if err != nil {
			return nil, errors.Wrap(err, "获取父文件夹失败")
		}

		file := NewFileInfo(&alipanopen.File{
			FileName:     fileName,
			ParentFileId: parentFolder.FileId,
			DriveId:      parentFolder.DriveId,
			Type:         alipanopen.FILE_TYPE_FILE,
			UpdatedAt:    time.Now(),
		})

		return NewWritableFile(file, fs)
	}

	file, err := fs.getFile(ctx, name)
	if err != nil {
		return nil, err
	}

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

	name = fs.resolve(name)

	fs.cleanTrie(name)

	file, err := fs.getFile(ctx, name)
	if err != nil {
		if err == os.ErrNotExist || err == os.ErrInvalid {
			return nil
		}
		return err
	}

	reqBody := &alipanopen.TrashFileReq{
		DriveId: file.DriveId,
		FileId:  file.FileId,
	}
	return fs.client.TrashFile(ctx, reqBody)
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
		reqBody := &alipanopen.UpdateFileNameReq{
			DriveId:       sourceFile.DriveId,
			FileId:        sourceFile.FileId,
			Name:          newFileName,
			CheckNameMode: alipanopen.CHECK_NAME_MODE_REFUSE,
		}
		err := fs.client.UpdateFileName(ctx, reqBody)
		if err != nil {
			return errors.Wrapf(err, "重命名")
		}
	} else {
		newParentFolder, err := fs.getFile(ctx, newFolder)
		if err != nil {
			return errors.Wrapf(err, "获取目的父文件夹失败")
		}

		reqBody := &alipanopen.MoveFileReq{
			DriveId:        sourceFile.DriveId,
			FileId:         sourceFile.FileId,
			NewName:        newFileName,
			ToParentFileId: newParentFolder.FileId,
			CheckNameMode:  alipanopen.CHECK_NAME_MODE_REFUSE,
		}
		err = fs.client.MoveFile(ctx, reqBody)
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

	name = fs.resolve(name)

	file, err := fs.getFile(ctx, name)
	if err != nil {
		return nil, err
	}

	return file, nil
}

func (fs *FileSystem) genDownloadUrlCacheKey(contentHash string) string {
	return fmt.Sprintf("downloadUrl-%s", contentHash)
}

func (fs *FileSystem) cacheSetDownloadUrl(contentHash string, downloadUrl string, duration time.Duration) {
	fs.cache.Set(fs.genDownloadUrlCacheKey(contentHash), downloadUrl, duration)
}

func (fs *FileSystem) cacheGetDownloadUrl(contentHash string) string {
	v, ok := fs.cache.Get(fs.genDownloadUrlCacheKey(contentHash))
	if ok {
		return v.(string)
	}

	return ""
}

func (fs *FileSystem) getDownloadUrl(driveId, fileId, contentHash string) (string, error) {
	downloadUrl := fs.cacheGetDownloadUrl(contentHash)
	if downloadUrl != "" {
		return downloadUrl, nil
	}

	reqBody := &alipanopen.GetFileDownloadUrlReq{
		DriveId: driveId,
		FileId:  fileId,
	}
	resp, err := fs.client.GetDownloadUrl(context.Background(), reqBody)
	if err != nil {
		return "", err
	}

	fs.cacheSetDownloadUrl(contentHash, resp.Url, resp.Expiration.Sub(time.Now()))
	return resp.Url, nil
}

func (fs *FileSystem) getFileByPath(ctx context.Context, driveId, name string) (*FileInfo, error) {
	if name != "/" {
		name = strings.TrimRight(name, "/")
	}

	if v := fs.root.Get(name); v != nil {
		return v.(*FileInfo), nil
	}

	dir, fileName := path.Split(name)
	parent, err := fs.getFileByPath(ctx, driveId, dir)
	if err != nil {
		return nil, err
	}

	reqBody := &alipanopen.ListFileReq{
		DriveId:      driveId,
		ParentFileId: parent.FileId,
		Limit:        100,
	}
	listFileResp, err := fs.client.ListFolder(ctx, reqBody)
	if err != nil {
		return nil, err
	}

	var fi *FileInfo = nil
	for _, item := range listFileResp.Items {
		fs.root.Put(path.Join(dir, item.FileName), NewFileInfo(item))
		if item.FileName == fileName {
			fi = NewFileInfo(item)
		}
	}

	if fi == nil {
		return nil, os.ErrNotExist
	}

	return fi, nil
}

func (fs *FileSystem) listDir(ctx context.Context, fi *FileInfo) ([]*FileInfo, error) {
	result, err, _ := fs.sg.Do(fmt.Sprintf("listDir-%s", fi.FileId), func() (interface{}, error) {
		reqBody := &alipanopen.ListFileReq{
			DriveId:      fi.DriveId,
			ParentFileId: fi.FileId,
			Limit:        100,
		}
		listFileResp, err := fs.client.ListFolder(ctx, reqBody)
		if err != nil {
			return nil, err
		}

		return listFileResp.Items, nil
	})

	if err != nil {
		return nil, err
	}

	files := result.([]*alipanopen.File)
	fis := make([]*FileInfo, len(files))
	for idx, file := range files {
		fis[idx] = NewFileInfo(file)
	}
	return fis, nil
}
