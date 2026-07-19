package config

import (
	"fmt"
)

var (
	ClientAddr        string
	NodeAddr          map[int]string
	LearningAgentAddr map[int]string
)

func GenerateLocalNetwork(nodeNum int) {

	ClientAddr = "127.0.0.1:20000"
	NodeAddr = make(map[int]string)
	LearningAgentAddr = make(map[int]string)
	localIP := "127.0.0.1"
	for i := 1; i <= nodeNum; i++ {
		NodeAddr[i] = fmt.Sprintf("%s:%d", localIP, 28000+i*100)
		LearningAgentAddr[i] = fmt.Sprintf("%s:%d", localIP, 29000+i)
	}
}
func GenerateLoopbackIPNetwork(nodeNum int) {
	ClientAddr = "127.0.0.1:20000"
	NodeAddr = make(map[int]string)
	LearningAgentAddr = make(map[int]string)
	for i := 1; i <= nodeNum; i++ {
		NodeAddr[i] = fmt.Sprintf("127.0.0.%d:%d", i+1, 28000+i*100)
		LearningAgentAddr[i] = fmt.Sprintf("127.0.0.%d:29000", i+1)
	}
}
func GenerateRemoteNetwork(nodeNum int) {
	ClientAddr = "172.18.4.1:20000"
	NodeAddr = make(map[int]string)
	LearningAgentAddr = nil
	for i := 0; i < nodeNum; i++ {
		NodeAddr[i] = fmt.Sprintf("172.18.4.%d:28000", i+2)
	}
}
