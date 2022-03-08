package util

import "fmt"

var Name string = "aliyundrive-webdav"
var Version string = "unkown"

func ShowVersion() {
	fmt.Printf("%s/%s\n", Name, Version)
}
