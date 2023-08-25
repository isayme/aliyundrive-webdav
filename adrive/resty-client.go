package adrive

import (
	"net/http"
	"net/url"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/isayme/go-logger"
)

var restyClient *resty.Client

func init() {
	c := resty.New()
	c.SetRetryCount(3)
	c.SetRetryAfter(func(c *resty.Client, resp *resty.Response) (time.Duration, error) {
		URL, _ := url.Parse(resp.Request.URL)

		statusCode := resp.StatusCode()
		if statusCode == 429 {
			logger.Warnf("请求接口 '%s' 遇到限流, 1秒后重试", URL.Path)
			return time.Second, nil
		}

		logger.Warnf("请求接口 '%s' 遇到限流, 100毫秒后重试", URL.Path)
		return time.Millisecond * 100, nil
	})

	c.AddRetryCondition(func(resp *resty.Response, err error) bool {
		statusCode := resp.StatusCode()
		if statusCode == 429 || statusCode >= 500 {
			return true
		}

		return false
	})

	c.SetPreRequestHook(func(client *resty.Client, request *http.Request) error {
		if request.Header.Get("Content-Type") == "[ignore]" {
			request.Header.Del("Content-Type")
		}

		if request.Header.Get("Host") == "" {
			request.Header.Add("Host", request.URL.Host)
		}

		if request.Header.Get("Referer") == "" {
			request.Header.Add("Referer", "https://www.aliyundrive.com/")
		}

		return nil
	})

	restyClient = c
}
