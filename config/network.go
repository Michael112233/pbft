package config

import (
	"fmt"
)

var (
	ClientAddr string
	NodeAddr   map[int]string
)

func GenerateLocalNetwork(nodeNum int) {
	ClientAddr = "127.0.0.1:1000"
	NodeAddr = make(map[int]string)
	for i := 0; i < nodeNum; i++ {
		NodeAddr[i] = fmt.Sprintf("127.0.0.1:%d", 8000+i*100)
	}
}

func GenerateRemoteNetwork(nodeNum int) {
	GenerateLocalNetwork(nodeNum)
}
