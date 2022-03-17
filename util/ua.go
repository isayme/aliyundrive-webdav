package util

import (
	"fmt"
	"runtime"
)

var UserAgent string

func init() {
	UserAgent = fmt.Sprintf("golang/%s %s/%s", runtime.Version(), Name, Version)
}
