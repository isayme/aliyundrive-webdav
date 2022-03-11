package adrive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/isayme/go-logger"
	"github.com/patrickmn/go-cache"
	"golang.org/x/net/webdav"
)

const ALIYUNDRIVE_API_HOST = "https://api.aliyundrive.com"
const ALIYUNDRIVE_HOST = "https://www.aliyundrive.com/"

var client = resty.New().SetPreRequestHook(func(client *resty.Client, request *http.Request) error {
	if request.Header.Get("Content-Type") == "[ignore]" {
		request.Header.Del("Content-Type")
	}

	if request.Header.Get("Host") == "" {
		request.Header.Add("Host", request.URL.Host)
	}

	return nil
})

type FileSystem struct {
	accessToken  string
	refreshToken string

	fileDriveId string
	userId      string

	fileCache *cache.Cache
}

func NewFileSystem(refreshToken string) (*FileSystem, error) {
	fs := &FileSystem{
		refreshToken: refreshToken,
		fileCache:    cache.New(time.Second*10, time.Second),
	}

	err := fs.doRefreshToken()
	if err != nil {
		return nil, err
	}

	user, err := fs.getLoginUser()
	if err != nil {
		return nil, err
	}

	fs.userId = user.UserId
	logger.Infof("welcome %s", user.NickName)

	return fs, nil
}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (fs *FileSystem) request(url string, body, out interface{}) (*ErrorResponse, error) {
	url = fmt.Sprintf("%s%s", ALIYUNDRIVE_API_HOST, url)

	resp, err := client.R().SetHeader("Authorization", fs.accessToken).SetDoNotParseResponse(true).SetBody(body).Post(url)
	if err != nil {
		return nil, err
	}

	defer resp.RawBody().Close()

	bs, err := io.ReadAll(resp.RawBody())
	if err != nil {
		return nil, err
	}

	statusCode := resp.StatusCode()
	if statusCode >= 200 && statusCode < 300 {
		json.Unmarshal(bs, out)
		if err != nil {
			return nil, err
		}
		return nil, nil
	}

	if statusCode == 429 {
		logger.Warn("got 429, sleep 1s ...")
		time.Sleep(time.Second)
	}

	errResp := ErrorResponse{}
	err = json.Unmarshal(bs, &errResp)
	if err != nil {
		return nil, err
	}

	if statusCode != 404 {
		logger.Warnw("requestFail", "err", err, "statusCode", statusCode, "reqBody", body, "respBody", errResp)
	}

	return &errResp, fmt.Errorf("requestFail, code: %d", statusCode)
}

type RefreshTokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	FileDriveId  string `json:"default_drive_id"`
}

func (fs *FileSystem) doRefreshToken() error {
	reqBody := map[string]string{
		"refresh_token": fs.refreshToken,
	}
	respBody := RefreshTokenResp{}

	_, err := fs.request("/token/refresh", reqBody, &respBody)
	if err != nil {
		return err
	}

	fs.accessToken = respBody.AccessToken
	fs.refreshToken = respBody.RefreshToken
	fs.fileDriveId = respBody.FileDriveId

	return nil
}

type EmptyStruct struct{}

type User struct {
	UserId   string `json:"user_id"`
	NickName string `json:"nick_name"`
}

func (fs *FileSystem) getLoginUser() (*User, error) {
	reqBody := EmptyStruct{}

	respBody := User{}
	_, err := fs.request("/v2/user/get", reqBody, &respBody)
	if err != nil {
		return nil, err
	}

	return &respBody, nil
}

func (fs *FileSystem) resolve(name string) string {
	return strings.TrimRight(name, "/")
}

func (fs *FileSystem) rootFolder() *File {
	return &File{
		fs:        fs,
		FileName:  "/",
		FileId:    "root",
		FileSize:  0,
		UpdatedAt: time.Now(),
		Type:      "folder",
	}
}

type ListFileResp struct {
	Items      []File `json:"items"`
	NextMarker string `json:"next_marker"`
}

func (afs *FileSystem) listDir(ctx context.Context, file *File) ([]fs.FileInfo, error) {
	reqBody := map[string]string{
		"drive_id":       afs.fileDriveId,
		"parent_file_id": file.FileId,
	}

	respBody := ListFileResp{}
	_, err := afs.request("/v2/file/list", reqBody, &respBody)
	if err != nil {
		return nil, err
	}

	items := make([]fs.FileInfo, len(respBody.Items))
	for idx, item := range respBody.Items {
		si, _ := item.Stat()
		items[idx] = si
	}

	return items, nil
}

func (fs *FileSystem) getFile(ctx context.Context, name string) (*File, error) {
	name = fs.resolve(name)

	if v, ok := fs.fileCache.Get(name); ok {
		file := v.(*File)
		return file.Clone(), nil
	}

	if name == "" || name == "/" {
		return fs.rootFolder(), nil
	}

	if strings.HasPrefix(path.Base(name), ".") {
		return nil, syscall.ENOENT
	}

	reqBody := map[string]string{
		"drive_id":  fs.fileDriveId,
		"file_path": name,
	}

	respBody := File{}
	errResp, err := fs.request("/v2/file/get_by_path", reqBody, &respBody)
	if err != nil && errResp != nil && errResp.Code == "NotFound.File" {
		return nil, syscall.ENOENT
	}

	if err != nil {
		return nil, err
	}

	respBody.fs = fs
	fs.fileCache.Set(name, &respBody, 0)
	return &respBody, nil
}

type GetFileDownloadUrlResp struct {
	Url        string    `json:"url"`
	Size       int64     `json:"size"`
	Expiration time.Time `json:"expiration"`
}

func (fs *FileSystem) getDownloadUrl(fileId string) (*GetFileDownloadUrlResp, error) {
	reqBody := map[string]string{
		"drive_id": fs.fileDriveId,
		"file_id":  fileId,
	}

	respBody := GetFileDownloadUrlResp{}
	_, err := fs.request("/v2/file/get_download_url", reqBody, &respBody)
	if err != nil {
		return nil, err
	}

	return &respBody, nil
}

func (fs *FileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	logger.Infof("准备新建文件夹: %s", name)
	name = fs.resolve(name)

	parentFolderName := path.Dir(name)
	parentFolder, err := fs.getFile(ctx, parentFolderName)
	if err != nil {
		logger.Errorf("新建文件夹失败, err: %v, name: %s", err, name)
		return err
	}

	reqBody := map[string]string{
		"drive_id":        fs.fileDriveId,
		"name":            path.Base(name),
		"type":            "folder",
		"parent_file_id":  parentFolder.FileId,
		"check_name_mode": "refuse",
	}

	respBody := File{}
	_, err = fs.request("/v2/file/create", reqBody, &respBody)
	if err != nil {
		logger.Errorf("新建文件夹失败, err: %v, name: %s", err, name)
		return err
	}

	fs.fileCache.Add(name, &respBody, 0)
	logger.Infof("新建文件夹成功: %s", name)
	return nil
}

func (fs *FileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	file, err := fs.getFile(ctx, name)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (fs *FileSystem) RemoveAll(ctx context.Context, name string) error {
	logger.Infof("准备删除文件: %s", name)

	file, err := fs.getFile(ctx, name)
	if err != nil {
		logger.Errorf("删除文件失败, name: %s, err: %v", name, err)
		return err
	}

	if file.FileId == "root" {
		return fmt.Errorf("cannot remove root folder")
	}

	reqBody := map[string]string{
		"drive_id": fs.fileDriveId,
		"file_id":  file.FileId,
	}

	respBody := EmptyStruct{}
	_, err = fs.request("/v2/recyclebin/trash", reqBody, &respBody)
	if err != nil {
		logger.Errorf("删除文件失败, name: %s, err: %v", name, err)
		return err
	}

	logger.Infof("删除文件成功: %s", name)
	return nil
}

func (fs *FileSystem) moveFile(fileId string, newParentFolderId string, newFileName string) error {
	reqBody := map[string]interface{}{
		"drive_id":          fs.fileDriveId,
		"file_id":           fileId,
		"check_name_mode":   "refuse",
		"overwrite":         false,
		"to_parent_file_id": newParentFolderId,
		"new_name":          newFileName,
	}
	respBody := EmptyStruct{}
	_, err := fs.request("/v2/file/move", reqBody, &respBody)
	return err
}

func (fs *FileSystem) updateFileName(fileId string, newFileName string) error {
	reqBody := map[string]interface{}{
		"drive_id": fs.fileDriveId,
		"file_id":  fileId,
		"name":     newFileName,
	}
	respBody := EmptyStruct{}
	_, err := fs.request("/v2/file/update", reqBody, &respBody)
	return err
}

func (fs *FileSystem) Rename(ctx context.Context, oldName, newName string) error {
	logger.Infof("准备移动文件(夹) '%s' 到 '%s'", oldName, newName)
	oldName = fs.resolve(oldName)
	newName = fs.resolve(newName)

	sourceFile, err := fs.getFile(ctx, oldName)
	if err != nil {
		logger.Errorf("移动文件(夹) '%s' 到 '%s' 失败: 获取原文件失败 %v", oldName, newName, err)
		return err
	}

	newFolder := path.Dir(newName)
	newFileName := path.Base(newName)

	if path.Dir(oldName) == newFolder {
		err := fs.updateFileName(sourceFile.FileId, newFileName)
		if err != nil {
			logger.Errorf("移动文件(夹) '%s' 到 '%s' 失败: 移动失败 %v", oldName, newName, err)
			return err
		}
	} else {
		newParentFolder, err := fs.getFile(ctx, newFolder)
		if err != nil {
			logger.Errorf("移动文件(夹) '%s' 到 '%s' 失败: 获取目的父文件夹失败 %v", oldName, newName, err)
			return err
		}

		err = fs.moveFile(sourceFile.FileId, newParentFolder.FileId, newFileName)
		if err != nil {
			logger.Errorf("移动文件(夹) '%s' 到 '%s' 失败: 移动失败 %v", oldName, newName, err)
			return err
		}
	}

	logger.Infof("移动文件(夹) '%s' 到 '%s' 成功", oldName, newName)
	return nil
}

func (fs *FileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	file, err := fs.getFile(ctx, name)
	if err != nil {
		return nil, err
	}

	return file.Stat()
}
