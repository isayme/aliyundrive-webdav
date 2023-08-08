package adrive

import (
	"sync"

	"github.com/isayme/go-config"
)

type AlipanConfig struct {
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
