//go:build windows

package main

import (
	"fmt"
	"os"
)

func recvTunFD(sockPath string) (*os.File, error) {
	return nil, fmt.Errorf("режим rawtun недоступен на Windows (нужен UNIX fd); используйте mode=socks")
}
