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
	return fmt.Errorf("not implemented")
}

func (fs *FileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	file, err := fs.getFile(ctx, name)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (fs *FileSystem) RemoveAll(ctx context.Context, name string) error {
	return fmt.Errorf("not implemented")
}

func (fs *FileSystem) Rename(ctx context.Context, oldName, newName string) error {
	return fmt.Errorf("not implemented")
}

func (fs *FileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	file, err := fs.getFile(ctx, name)
	if err != nil {
		return nil, err
	}

	return file.Stat()
}
