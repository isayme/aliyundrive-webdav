package adrive

import (
	"sync"

	"github.com/isayme/go-config"
)

type AlipanConfig struct {
	RootDir string `json:"rootDir" yaml:"rootDir"` // 根目录

	Readonly bool `json:"readonly" yaml:"readonly"` // 只读模式

	ClientId     string `json:"clientId" yaml:"clientId"`
	ClientSecret string `json:"clientSecret" yaml:"clientSecret"`
}

type Config struct {
	AlipanConfig AlipanConfig `json:"alipan" yaml:"alipan"`
}

var globalConfig = Config{}
var once sync.Once

func Get() *Config {
	once.Do(func() {
		config.Parse(&globalConfig)
	})

	return &globalConfig
}
