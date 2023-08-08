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
	"strings"
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

func (fs *FileSystem) requestWithAccessToken(method, path string, body, out interface{}) (errResp *ErrorResponse, err error) {
	accessToken, err := fs.getAccessToken()
	if err != nil {
		return
	}

	headers := make(map[string]string)
	headers["Authorization"] = fmt.Sprintf("Bearer %s", accessToken)

	return fs.request(method, path, headers, body, out)
}

func (fs *FileSystem) request(method, path string, headers map[string]string, body, out interface{}) (errResp *ErrorResponse, err error) {
	defer func() {
		logger.Debugf("Request %s, reqBody: %v, respBody: %v, err: %v, errResp: %v", path, util.Stringify(body), util.Stringify(out), err, errResp)
	}()

	url := fmt.Sprintf("%s%s", ALIYUNDRIVE_API_HOST, path)

	req := client.R()
	if headers != nil {
		req = req.SetHeaders(headers)
	}

	resp, err := req.SetDoNotParseResponse(true).SetBody(body).Execute(method, url)
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
		logger.Warnw("requestFail", "err", err, "path", path, "statusCode", statusCode, "reqBody", body, "respBody", errResp)
	}

	return errResp, fmt.Errorf("requestFail, code: %s/%d", errResp.Code, statusCode)
}

type RefreshTokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // 单位秒
}

func (fs *FileSystem) doRefreshToken() (*RefreshTokenResp, error) {
	reqBody := map[string]string{
		"client_id":     fs.clientId,
		"client_secret": fs.clientSecret,
		"grant_type":    "refresh_token",
		"refresh_token": fs.refreshToken,
	}
	respBody := &RefreshTokenResp{}
	_, err := fs.request(METHOD_POST, API_OAUTH_ACCESS_TOKEN, nil, reqBody, respBody)
	if err != nil {
		return nil, err
	}

	return respBody, nil
}

func (fs *FileSystem) doRefreshTokenByAuthCode(authCode string) (*RefreshTokenResp, error) {
	reqBody := map[string]string{
		"client_id":     fs.clientId,
		"client_secret": fs.clientSecret,
		"grant_type":    "authorization_code",
		"code":          authCode,
	}
	respBody := &RefreshTokenResp{}
	_, err := fs.request(METHOD_POST, API_OAUTH_ACCESS_TOKEN, nil, reqBody, respBody)
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
		fs.accessTokenExpireTime = time.Now().Add(time.Second * time.Duration(refreshTokenResp.ExpiresIn))
		writeRefreshToken(fs.refreshToken)

		logger.Info("刷新 token 成功")
	}

	return fs.accessToken, nil
}

type GetDriveInfoResp struct {
	DefaultDriveId  string `json:"default_drive_id"`
	ResourceDriveId string `json:"resource_drive_id"`
	BackupDriveId   string `json:"backup_drive_id"`
}

func (fs *FileSystem) getDriveInfo() (*GetDriveInfoResp, error) {
	reqBody := EmptyStruct{}

	respBody := GetDriveInfoResp{}
	_, err := fs.requestWithAccessToken(METHOD_POST, API_GET_DRIVE_INFO, reqBody, &respBody)
	if err != nil {
		return nil, err
	}

	return &respBody, nil
}

type User struct {
	Id   string `json:"id"`   // 用户ID
	Name string `json:"name"` // 用户昵称
}

func (fs *FileSystem) getCurrentUser() (*User, error) {
	reqBody := EmptyStruct{}

	respBody := User{}
	_, err := fs.requestWithAccessToken(METHOD_GET, API_OAUTH_USER_INFO, reqBody, &respBody)
	if err != nil {
		return nil, err
	}

	return &respBody, nil
}

type ListFileResp struct {
	Items      []*File `json:"items"`
	NextMarker string  `json:"next_marker"`
}

func (fs *FileSystem) listFolder(ctx context.Context, parentFileId string) ([]*File, error) {
	result, err, _ := fs.sg.Do(fmt.Sprintf("listFolder-%s", parentFileId), func() (interface{}, error) {
		logger.Infof("listFolder %s", parentFileId)

		reqBody := map[string]interface{}{
			"drive_id":       fs.fileDriveId,
			"parent_file_id": parentFileId,
			"limit":          100,
		}
		respBody := ListFileResp{}
		_, err := fs.requestWithAccessToken(METHOD_POST, API_FILE_LIST, reqBody, &respBody)
		if err != nil {
			return nil, err
		}

		for _, item := range respBody.Items {
			item.fs = fs
		}

		return respBody.Items, nil
	})

	return result.([]*File), err
}

func (fs *FileSystem) listDir(ctx context.Context, file *File) ([]*File, error) {
	items, err := fs.listFolder(ctx, file.FileId)
	if err != nil {
		return nil, err
	}

	return items, nil
}

func (fs *FileSystem) getFileByPath(ctx context.Context, name string) (*File, error) {
	if name != "/" {
		name = strings.TrimRight(name, "/")
	}

	if v := fs.root.Get(name); v != nil {
		return v.(*File), nil
	}

	dir, fileName := path.Split(name)
	parent, err := fs.getFileByPath(ctx, dir)
	if err != nil {
		return nil, err
	}

	files, err := fs.listFolder(ctx, parent.FileId)
	if err != nil {
		return nil, err
	}

	var file *File = nil
	for _, item := range files {
		if item.IsDir() {
			fs.root.Put(path.Join(dir, item.Name()), item)
		}
		if item.Name() == fileName {
			file = item
		}
	}

	if file == nil {
		return nil, os.ErrNotExist
	}

	return file, nil
}

type GetFileDownloadUrlResp struct {
	Url        string    `json:"url"`
	Expiration time.Time `json:"expiration"`
}

func (fs *FileSystem) getDownloadUrl(fileId string) (*GetFileDownloadUrlResp, error) {
	reqBody := map[string]string{
		"drive_id": fs.fileDriveId,
		"file_id":  fileId,
	}

	logger.Infof("getDownloadUrl %s", fileId)

	respBody := GetFileDownloadUrlResp{}
	_, err := fs.requestWithAccessToken(METHOD_POST, API_FILE_GET_DOWNLOAD_URL, reqBody, &respBody)
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
	_, err := fs.requestWithAccessToken(METHOD_POST, API_FILE_CREATE, reqBody, file)
	if err != nil {
		return nil, err
	}

	file.fs = fs
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
	_, err := fs.requestWithAccessToken(METHOD_POST, API_FILE_CREATE, reqBody, &respBody)
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
	_, err := fs.requestWithAccessToken(METHOD_POST, API_FILE_DELETE, reqBody, &respBody)
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
	_, err := fs.requestWithAccessToken(METHOD_POST, API_FILE_COMPLETE, reqBody, respBody)
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
	_, err := fs.requestWithAccessToken(METHOD_POST, API_FILE_TRASH, reqBody, &respBody)
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
	_, err := fs.requestWithAccessToken(METHOD_POST, API_FILE_MOVE, reqBody, &respBody)
	return err
}

func (fs *FileSystem) updateFileName(fileId string, newFileName string) error {
	reqBody := map[string]interface{}{
		"drive_id":        fs.fileDriveId,
		"file_id":         fileId,
		"name":            newFileName,
		"check_name_mode": CHECK_NAME_MODE_REFUSE,
	}
	respBody := EmptyStruct{}
	_, err := fs.requestWithAccessToken(METHOD_POST, API_FILE_UPDATE, reqBody, &respBody)
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
		"upload_id": uploadId,
		"part_info_list": []UploadPartInfo{
			{
				PartNumber: partNum,
			},
		},
	}

	respBody := &GetUploadUrlResp{}
	_, err := fs.requestWithAccessToken(METHOD_POST, API_FILE_GET_UPLOAD_URL, reqBody, respBody)
	if err != nil {
		return nil, err
	}
	return respBody, nil
}

type GetQrCodeResp struct {
	QrCodeUrl string `json:"qrCodeUrl"`
	Sid       string `json:"sid"`
}

func (fs *FileSystem) getQrCode() (*GetQrCodeResp, error) {
	reqBody := map[string]interface{}{
		"client_id":     fs.clientId,
		"client_secret": fs.clientSecret,
		"scopes":        []string{"user:base", "file:all:read", "file:all:write"},
	}
	respBody := &GetQrCodeResp{}
	_, err := fs.request(METHOD_POST, API_OAUTH_AUTHORIZE_QRCODE, nil, reqBody, respBody)
	if err != nil {
		return nil, err
	}
	return respBody, nil
}

type GetQrCodeStatusResp struct {
	Status   string `json:"status"`
	AuthCode string `json:"authCode"`
}

func (fs *FileSystem) getQrCodeStatus(sid string) (*GetQrCodeStatusResp, error) {
	respBody := &GetQrCodeStatusResp{}
	_, err := fs.request(METHOD_GET, fmt.Sprintf("/oauth/qrcode/%s/status", sid), nil, nil, respBody)
	if err != nil {
		return nil, err
	}
	return respBody, nil
}
