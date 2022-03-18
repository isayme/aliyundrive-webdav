package util

import "encoding/json"

func Stringify(v interface{}) string {
	bs, _ := json.Marshal(v)
	return string(bs)
}
