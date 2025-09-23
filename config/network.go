package config

import (
	"fmt"
)

var (
	ClientAddr string
	NodeAddr   map[int]string
)

func GenerateLocalNetwork(nodeNum int) {
	localIp := "localhost:"
	ClientAddr = localIp + "20000"
	NodeAddr = make(map[int]string)
	for i := 0; i < nodeNum; i++ {
		NodeAddr[i] = fmt.Sprintf("%s%d", localIp, 28000+i*100)
	}
}

func GenerateRemoteNetwork(nodeNum int) {
	ClientAddr = "172.17.8.1:20000"
	NodeAddr = make(map[int]string)
	for i := 0; i < nodeNum; i++ {
		NodeAddr[i] = fmt.Sprintf("172.17.8.%d:28000", i+2)
	}
}
