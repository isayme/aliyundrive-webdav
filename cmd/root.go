package cmd

import (
	"fmt"
	"net/http"
	"os"

	"github.com/isayme/aliyundrive-webdav/adrive"
	"github.com/isayme/aliyundrive-webdav/util"
	"github.com/isayme/go-logger"
	"github.com/spf13/cobra"
	"golang.org/x/net/webdav"
)

var showVersion bool
var listenPort uint16
var logLevel string

func init() {
	rootCmd.Flags().BoolVarP(&showVersion, "version", "v", false, "show version")
	rootCmd.Flags().StringVarP(&logLevel, "level", "l", "info", "log level")
	rootCmd.Flags().Uint16VarP(&listenPort, "port", "p", 8080, "listen port")
}

var rootCmd = &cobra.Command{
	Use: "aliyundrive-webdav",
	Run: func(cmd *cobra.Command, args []string) {
		if showVersion {
			util.ShowVersion()
			os.Exit(0)
		}

		logger.SetLevel(logLevel)

		fs, err := adrive.NewFileSystem("TODO: your refresh_token")
		if err != nil {
			logger.Errorf("create adrive fs faild, err: %v", err)
			return
		}

		address := fmt.Sprintf(":%d", listenPort)
		logger.Infow("start listen ...", "addr", address)

		err = http.ListenAndServe(address, &webdav.Handler{
			FileSystem: fs,
			LockSystem: webdav.NewMemLS(),
		})
		if err != nil {
			logger.Errorf("ListenAndServe fail: %v", err)
		}
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		logger.Panicf("rootCmd execute fail: %s", err.Error())
		os.Exit(1)
	}
}
