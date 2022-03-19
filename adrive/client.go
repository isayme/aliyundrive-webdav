package adrive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/isayme/aliyundrive-webdav/util"
	"github.com/isayme/go-logger"
)

var client *resty.Client

func init() {
	client = resty.New()
	client.SetRetryCount(3)
	client.SetRetryAfter(func(c *resty.Client, resp *resty.Response) (time.Duration, error) {
		URL, _ := url.Parse(resp.Request.URL)

		statusCode := resp.StatusCode()
		if statusCode == 429 {
			logger.Warnf("请求接口 '%s' 遇到限流, 1秒后重试", URL.Path)
			return time.Second, nil
		}

		logger.Warnf("请求接口 '%s' 遇到限流, 100毫秒后重试", URL.Path)
		return time.Millisecond * 100, nil
	})

	client.AddRetryCondition(func(resp *resty.Response, err error) bool {
		statusCode := resp.StatusCode()
		if statusCode == 429 || statusCode >= 500 {
			return true
		}

		return false
	})

	client.SetPreRequestHook(func(client *resty.Client, request *http.Request) error {
		if request.Header.Get("Content-Type") == "[ignore]" {
			request.Header.Del("Content-Type")
		}

		if request.Header.Get("Host") == "" {
			request.Header.Add("Host", request.URL.Host)
		}

		if request.Header.Get("Referer") == "" {
			request.Header.Add("Referer", ALIYUNDRIVE_HOST)
		}

		return nil
	})
}

type EmptyStruct struct{}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (fs *FileSystem) request(path string, body, out interface{}) (errResp *ErrorResponse, err error) {
	defer func() {
		logger.Debugf("Request %s, reqBody: %v, respBody: %v, err: %v, errResp: %v", path, util.Stringify(body), util.Stringify(out), err, errResp)
	}()

	url := fmt.Sprintf("%s%s", ALIYUNDRIVE_API_HOST, path)

	var accessToken string
	if path != "/token/refresh" {
		accessToken, err = fs.getAccessToken()
		if err != nil {
			return nil, err
		}
	}

	resp, err := client.R().SetHeader(HEADER_AUTHORIZATION, accessToken).SetDoNotParseResponse(true).SetBody(body).Post(url)
	if err != nil {
		return nil, err
	}

	defer resp.RawBody().Close()

	bs, err := io.ReadAll(resp.RawBody())
	if err != nil {
		return nil, err
	}

	if resp.IsSuccess() {
		json.Unmarshal(bs, out)
		if err != nil {
			return nil, err
		}
		return nil, nil
	}

	statusCode := resp.StatusCode()
	if statusCode == 429 {
		logger.Warnf("请求接口 '%s' 遇到限流", path)
	}

	errResp = &ErrorResponse{}
	err = json.Unmarshal(bs, errResp)
	if err != nil {
		return nil, err
	}

	if errResp.Code != "NotFound.File" {
		logger.Warnw("requestFail", "err", err, "statusCode", statusCode, "reqBody", body, "respBody", errResp)
	}

	return errResp, fmt.Errorf("requestFail, code: %s/%d", errResp.Code, statusCode)
}

type RefreshTokenResp struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	FileDriveId  string    `json:"default_drive_id"`
	ExpireTime   time.Time `json:"expire_time"`
}

func (fs *FileSystem) doRefreshToken() (*RefreshTokenResp, error) {
	reqBody := map[string]string{
		"refresh_token": fs.refreshToken,
	}
	respBody := &RefreshTokenResp{}
	_, err := fs.request("/token/refresh", reqBody, respBody)
	if err != nil {
		return nil, err
	}
	return respBody, nil
}

func (fs *FileSystem) getAccessToken() (string, error) {
	if fs.accessToken == "" || time.Now().After(fs.accessTokenExpireTime) {
		refreshTokenResp, err := fs.doRefreshToken()
		if err != nil {
			logger.Errorf("刷新 token 失败: %v", err)
			return "", err
		}

		fs.accessToken = refreshTokenResp.AccessToken
		fs.refreshToken = refreshTokenResp.RefreshToken
		fs.accessTokenExpireTime = refreshTokenResp.ExpireTime
		fs.fileDriveId = refreshTokenResp.FileDriveId
		logger.Info("刷新 token 成功")
	}

	return fs.accessToken, nil
}

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

type ListFileResp struct {
	Items      []*File `json:"items"`
	NextMarker string  `json:"next_marker"`
}

func (fs *FileSystem) listDir(ctx context.Context, file *File) ([]*File, error) {
	reqBody := map[string]string{
		"drive_id":       file.DriveId,
		"parent_file_id": file.FileId,
	}

	respBody := ListFileResp{}
	_, err := fs.request("/v2/file/list", reqBody, &respBody)
	if err != nil {
		return nil, err
	}

	for _, item := range respBody.Items {
		item.path = path.Join(file.path, item.FileName)
		item.fs = fs
		fs.fileCache.Set(item.path, item, 0)
	}

	return respBody.Items, nil
}

func (fs *FileSystem) getFileByPath(ctx context.Context, name string) (*File, error) {
	reqBody := map[string]string{
		"drive_id":  fs.fileDriveId,
		"file_path": name,
	}

	file := &File{}
	errResp, err := fs.request("/v2/file/get_by_path", reqBody, file)
	if err != nil && errResp != nil && errResp.Code == "NotFound.File" {
		return nil, os.ErrNotExist
	}

	if err != nil {
		return nil, err
	}

	file.fs = fs
	file.path = name

	return file, nil
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

func (fs *FileSystem) createFolder(ctx context.Context, name string, parentFileId string) (*File, error) {
	reqBody := map[string]string{
		"drive_id":        fs.fileDriveId,
		"name":            name,
		"parent_file_id":  parentFileId,
		"type":            FILE_TYPE_FOLDER,
		"check_name_mode": CHECK_NAME_MODE_REFUSE,
	}

	file := &File{}
	_, err := fs.request("/v2/file/create", reqBody, file)
	if err != nil {
		return nil, err
	}

	file.fs = fs
	file.path = name
	return file, nil
}

type CreateFileReq struct {
	ContentHash     string `json:"content_hash"`
	ContentHashName string `json:"content_hash_name"`
	CheckNameMode   string `json:"check_name_mode"`
	DriveId         string `json:"drive_id"`
	Name            string `json:"name"`
	ParentFileId    string `json:"parent_file_id"`
	Size            int64  `json:"size"`
	Type            string `json:"type"`
}

type UploadPartInfo struct {
	PartNumber int    `json:"part_number"`
	PartSize   int64  `json:"part_size"`
	UploadUrl  string `json:"upload_url"`
}

type CreateFileResp struct {
	DriveId      string           `json:"drive_id"`
	FileId       string           `json:"file_id"`
	UploadId     string           `json:"upload_id"`
	PartInfoList []UploadPartInfo `json:"part_info_list"`
}

func (fs *FileSystem) createFile(ctx context.Context, reqBody *CreateFileReq) (*CreateFileResp, error) {
	respBody := CreateFileResp{}
	_, err := fs.request("/adrive/v2/file/createWithFolders", reqBody, &respBody)
	if err != nil {
		return nil, err
	}

	return &respBody, nil
}

func (fs *FileSystem) deleteFile(ctx context.Context, driveId, fileId string) error {
	reqBody := map[string]string{
		"drive_id": driveId,
		"file_id":  fileId,
	}
	respBody := EmptyStruct{}
	_, err := fs.request("/v2/file/delete", reqBody, &respBody)
	if err != nil {
		return err
	}

	return nil
}

type CompleteFileReq struct {
	DriveId  string `json:"drive_id"`
	FileId   string `json:"file_id"`
	UploadId string `json:"upload_id"`
}

type CompleteFileResp struct {
	ContentHash string `json:"content_hash"`
	Size        int64  `json:"size"`
}

func (fs *FileSystem) completeFile(ctx context.Context, reqBody *CompleteFileReq) (*CompleteFileResp, error) {
	respBody := &CompleteFileResp{}
	_, err := fs.request("/v2/file/complete", reqBody, respBody)
	if err != nil {
		return nil, err
	}

	return respBody, nil
}

func (fs *FileSystem) trashFile(ctx context.Context, fileId string) error {
	reqBody := map[string]string{
		"drive_id": fs.fileDriveId,
		"file_id":  fileId,
	}

	respBody := EmptyStruct{}
	_, err := fs.request("/v2/recyclebin/trash", reqBody, &respBody)
	return err
}

func (fs *FileSystem) moveFile(fileId string, newParentFolderId string, newFileName string) error {
	reqBody := map[string]interface{}{
		"drive_id":          fs.fileDriveId,
		"file_id":           fileId,
		"check_name_mode":   CHECK_NAME_MODE_REFUSE,
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

type GetUploadUrlResp struct {
	DriveId      string           `json:"drive_id"`
	FileId       string           `json:"file_id"`
	UploadId     string           `json:"upload_id"`
	PartInfoList []UploadPartInfo `json:"part_info_list"`
}

func (fs *FileSystem) getUploadUrl(driveId, fileId, uploadId string, partNum int) (*GetUploadUrlResp, error) {
	reqBody := map[string]interface{}{
		"drive_id":  driveId,
		"file_id":   fileId,
		"upload_Id": uploadId,
		"part_info_list": []UploadPartInfo{
			{
				PartNumber: partNum,
			},
		},
	}

	respBody := &GetUploadUrlResp{}
	_, err := fs.request("/v2/file/get_upload_url", reqBody, respBody)
	if err != nil {
		return nil, err
	}
	return respBody, nil
}
