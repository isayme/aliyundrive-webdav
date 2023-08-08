package adrive

var (
	ROOT_FOLDER_ID = "root"

	FILE_TYPE_FILE   = "file"
	FILE_TYPE_FOLDER = "folder"

	CHECK_NAME_MODE_REFUSE = "refuse"
)

var (
	ALIYUNDRIVE_API_HOST = "https://openapi.aliyundrive.com"
	ALIYUNDRIVE_HOST     = "https://www.aliyundrive.com/"

	HEADER_HOST          = "Host"
	HEADER_REFERER       = "Referer"
	HEADER_USER_AGENT    = "User-Agent"
	HEADER_RANGE         = "Range"
	HEADER_ACCEPT        = "Accept"
	HEADER_AUTHORIZATION = "Authorization"

	METHOD_GET  = "GET"
	METHOD_POST = "POST"

	API_OAUTH_USER_INFO        = "/oauth/users/info"
	API_GET_DRIVE_INFO         = "/adrive/v1.0/user/getDriveInfo"
	API_OAUTH_ACCESS_TOKEN     = "/oauth/access_token"
	API_OAUTH_AUTHORIZE_QRCODE = "/oauth/authorize/qrcode"
	API_FILE_LIST              = "/adrive/v1.0/openFile/list"
	API_FILE_CREATE            = "/adrive/v1.0/openFile/create"
	API_FILE_DELETE            = "/adrive/v1.0/openFile/delete"
	API_FILE_TRASH             = "/adrive/v1.0/openFile/recyclebin/trash"
	API_FILE_COMPLETE          = "/adrive/v1.0/openFile/complete"
	API_FILE_MOVE              = "/adrive/v1.0/openFile/move"
	API_FILE_UPDATE            = "/adrive/v1.0/openFile/update"
	API_FILE_GET_UPLOAD_URL    = "/adrive/v1.0/openFile/getUploadUrl"
	API_FILE_GET_DOWNLOAD_URL  = "/adrive/v1.0/openFile/getDownloadUrl"
)

const (
	qrCodeStatusWaitLogin     = "WaitLogin"
	qrCodeStatusScanSuccess   = "ScanSuccess"
	qrCodeStatusLoginSuccess  = "LoginSuccess"
	qrCodeStatusQRCodeExpired = "QRCodeExpired"
)
