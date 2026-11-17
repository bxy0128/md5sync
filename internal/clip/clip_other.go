//go:build !windows

package clip

import "errors"

var errUnsupported = errors.New("当前系统不支持剪贴板读取")

func Read() (string, error) { return "", errUnsupported }
func Write(string) error    { return errUnsupported }
