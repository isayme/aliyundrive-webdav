package adrive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/isayme/go-logger"
)

type AdriveClient struct {
	refreshToken          string
	accessToken           string
	accessTokenExpireTime time.Time
	fileDriveId           string
}

const ALIYUNDRIVE_API_HOST = "https://api.aliyundrive.com"
const ALIYUNDRIVE_HOST = "https://www.aliyundrive.com/"

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

func NewAdriveClient(refreshToken string) (*AdriveClient, error) {
	c := &AdriveClient{
		refreshToken: refreshToken,
	}

	user, err := c.getLoginUser()
	if err != nil {
		return nil, err
	}

	logger.Infof("认证成功, 当前账号昵称: %s, ID: %s", user.NickName, user.UserId)

	return c, nil
}

type EmptyStruct struct{}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (c *AdriveClient) request(path string, body, out interface{}) (errResp *ErrorResponse, err error) {
	defer func() {
		logger.Debugf("Request %s, reqBody: %v, respBody: %v, err: %v, errResp: %v", path, body, out, err, errResp)
	}()

	url := fmt.Sprintf("%s%s", ALIYUNDRIVE_API_HOST, path)

	var accessToken string
	if path != "/token/refresh" {
		accessToken, err = c.getAccessToken()
		if err != nil {
			return nil, err
		}
	}

	resp, err := client.R().SetHeader("Authorization", accessToken).SetDoNotParseResponse(true).SetBody(body).Post(url)
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

func (c *AdriveClient) doRefreshToken() (*RefreshTokenResp, error) {
	reqBody := map[string]string{
		"refresh_token": c.refreshToken,
	}
	respBody := &RefreshTokenResp{}
	_, err := c.request("/token/refresh", reqBody, respBody)
	if err != nil {
		return nil, err
	}
	return respBody, nil
}

func (c *AdriveClient) getAccessToken() (string, error) {
	if c.accessToken == "" || time.Now().After(c.accessTokenExpireTime) {
		refreshTokenResp, err := c.doRefreshToken()
		if err != nil {
			logger.Errorf("刷新 token 失败: %v", err)
			return "", err
		}

		c.accessToken = refreshTokenResp.AccessToken
		c.refreshToken = refreshTokenResp.RefreshToken
		c.accessTokenExpireTime = refreshTokenResp.ExpireTime
		c.fileDriveId = refreshTokenResp.FileDriveId
		logger.Info("刷新 token 成功")
	}

	return c.accessToken, nil
}

type User struct {
	UserId   string `json:"user_id"`
	NickName string `json:"nick_name"`
}

func (c *AdriveClient) getLoginUser() (*User, error) {
	reqBody := EmptyStruct{}

	respBody := User{}
	_, err := c.request("/v2/user/get", reqBody, &respBody)
	if err != nil {
		return nil, err
	}

	return &respBody, nil
}

type ListFileResp struct {
	Items      []File `json:"items"`
	NextMarker string `json:"next_marker"`
}

func (c *AdriveClient) listDir(ctx context.Context, file *File) ([]fs.FileInfo, error) {
	reqBody := map[string]string{
		"drive_id":       c.fileDriveId,
		"parent_file_id": file.FileId,
	}

	respBody := ListFileResp{}
	_, err := c.request("/v2/file/list", reqBody, &respBody)
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

func (c *AdriveClient) getFileByPath(ctx context.Context, name string) (*File, error) {
	reqBody := map[string]string{
		"drive_id":  c.fileDriveId,
		"file_path": name,
	}

	file := &File{}
	errResp, err := c.request("/v2/file/get_by_path", reqBody, file)
	if err != nil && errResp != nil && errResp.Code == "NotFound.File" {
		return nil, os.ErrNotExist
	}

	if err != nil {
		return nil, err
	}

	file.client = c
	return file, nil
}

type GetFileDownloadUrlResp struct {
	Url        string    `json:"url"`
	Size       int64     `json:"size"`
	Expiration time.Time `json:"expiration"`
}

func (c *AdriveClient) getDownloadUrl(fileId string) (*GetFileDownloadUrlResp, error) {
	reqBody := map[string]string{
		"drive_id": c.fileDriveId,
		"file_id":  fileId,
	}

	respBody := GetFileDownloadUrlResp{}
	_, err := c.request("/v2/file/get_download_url", reqBody, &respBody)
	if err != nil {
		return nil, err
	}

	return &respBody, nil
}

func (c *AdriveClient) createFolder(ctx context.Context, name string, parentFileId string) (*File, error) {
	reqBody := map[string]string{
		"drive_id":        c.fileDriveId,
		"name":            name,
		"parent_file_id":  parentFileId,
		"type":            FILE_TYPE_FOLDER,
		"check_name_mode": CHECK_NAME_MODE_REFUSE,
	}

	file := &File{}
	_, err := c.request("/v2/file/create", reqBody, file)
	if err != nil {
		return nil, err
	}

	file.client = c
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
	PartNumber int64  `json:"part_number"`
	PartSize   int64  `json:"part_size"`
	UploadUrl  string `json:"upload_url"`
}

type CreateFileResp struct {
	DriveId      string           `json:"drive_id"`
	FileId       string           `json:"file_id"`
	UploadId     string           `json:"upload_id"`
	PartInfoList []UploadPartInfo `json:"part_info_list"`
}

func (c *AdriveClient) createFile(ctx context.Context, reqBody *CreateFileReq) (*CreateFileResp, error) {
	respBody := CreateFileResp{}
	_, err := c.request("/adrive/v2/file/createWithFolders", reqBody, &respBody)
	if err != nil {
		return nil, err
	}

	return &respBody, nil
}

func (c *AdriveClient) deleteFile(ctx context.Context, driveId, fileId string) error {
	reqBody := map[string]string{
		"drive_id": driveId,
		"file_id":  fileId,
	}
	respBody := EmptyStruct{}
	_, err := c.request("/v2/file/delete", reqBody, &respBody)
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

func (c *AdriveClient) completeFile(ctx context.Context, reqBody *CompleteFileReq) error {
	respBody := EmptyStruct{}
	_, err := c.request("/v2/file/complete", reqBody, &respBody)
	return err
}

func (c *AdriveClient) trashFile(ctx context.Context, fileId string) error {
	reqBody := map[string]string{
		"drive_id": c.fileDriveId,
		"file_id":  fileId,
	}

	respBody := EmptyStruct{}
	_, err := c.request("/v2/recyclebin/trash", reqBody, &respBody)
	return err
}

func (c *AdriveClient) moveFile(fileId string, newParentFolderId string, newFileName string) error {
	reqBody := map[string]interface{}{
		"drive_id":          c.fileDriveId,
		"file_id":           fileId,
		"check_name_mode":   CHECK_NAME_MODE_REFUSE,
		"overwrite":         false,
		"to_parent_file_id": newParentFolderId,
		"new_name":          newFileName,
	}
	respBody := EmptyStruct{}
	_, err := c.request("/v2/file/move", reqBody, &respBody)
	return err
}

func (c *AdriveClient) updateFileName(fileId string, newFileName string) error {
	reqBody := map[string]interface{}{
		"drive_id": c.fileDriveId,
		"file_id":  fileId,
		"name":     newFileName,
	}
	respBody := EmptyStruct{}
	_, err := c.request("/v2/file/update", reqBody, &respBody)
	return err
}
