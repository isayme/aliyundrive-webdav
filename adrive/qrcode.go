package adrive

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/isayme/go-alipanopen"
	"github.com/isayme/go-ora"
	"github.com/mdp/qrterminal/v3"
)

func (fs *FileSystem) authIfRequired(ctx context.Context) error {
	if fs.accessToken != "" {
		return nil
	}

	reqBody := &alipanopen.GetQrCodeReq{
		ClientId:     fs.clientId,
		ClientSecret: fs.clientSecret,
	}
	qrCodeResp, err := fs.client.GetQrCode(ctx, reqBody)
	if err != nil {
		return err
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

	ora := ora.New()
	defer ora.Stop()
	ora.Start()
	ora.Text("等待扫码...")

	done := false

	for {
		if done {
			break
		}

		qrCodeStatusResp, err := fs.client.GetQrCodeStatus(ctx, qrCodeResp.Sid)
		if err != nil {
			return err
		}

		switch qrCodeStatusResp.Status {
		case alipanopen.QRCODE_STATUS_WAITLOGIN:
			ora.Text("等待扫码...")
			time.Sleep(time.Second)
		case alipanopen.QRCODE_STATUS_SCANSUCCESS:
			ora.Text("已扫码成功")
			time.Sleep(time.Second)
		case alipanopen.QRCODE_STATUS_LOGINSUCCESS:
			ora.Succeed("已登录成功")

			reqBody := &alipanopen.RefreshTokenReq{
				ClientId:     fs.clientId,
				ClientSecret: fs.clientSecret,
				GrantType:    alipanopen.GRANT_TYPE_AUTHORIZATION_CODE,
				Code:         qrCodeStatusResp.AuthCode,
			}
			refreshTokenResp, err := fs.client.RefreshToken(ctx, reqBody)
			if err != nil {
				return err
			}
			fs.saveToken(refreshTokenResp)
			done = true
		case alipanopen.QRCODE_STATUS_QRCODEEXPIRED:
			ora.Fail("二维码过期")
			return fmt.Errorf("二维码过期")
		}
	}

	return nil
}
