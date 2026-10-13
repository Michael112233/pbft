package config

import (
	"fmt"
)

var (
	// ClientAddr is the single client's address (client_num 1). With more clients,
	// client i uses ClientAddrs[i]; see ClientAddrFor. Only the IP is used: the
	// client binds it as its source address and listens on nothing.
	ClientAddr        string
	ClientAddrs       map[int]string
	NodeAddr          map[int]string
	LearningAgentAddr map[int]string
)

// ClientAddrFor returns client id's address when there are clientNum clients.
func ClientAddrFor(id, clientNum int) string {
	if clientNum <= 1 {
		return ClientAddr
	}
	return ClientAddrs[id]
}

func GenerateLocalNetwork(nodeNum int) {

	ClientAddr = "127.0.0.1:20000"
	ClientAddrs = make(map[int]string)
	NodeAddr = make(map[int]string)
	LearningAgentAddr = make(map[int]string)
	localIP := "127.0.0.1"
	for i := 1; i <= nodeNum; i++ {
		NodeAddr[i] = fmt.Sprintf("%s:%d", localIP, 28000+i*100)
		LearningAgentAddr[i] = fmt.Sprintf("%s:%d", localIP, 29000+i)
		ClientAddrs[i] = ClientAddr
	}
}

// GenerateLoopbackIPNetwork gives node i 127.0.0.(i+1) and client i 127.0.1.(i+1).
// Clients get their own range so the node-pair netem filters (which also match a
// node with itself) never delay a client, not even towards its own node; delaying
// a client is left to filters written for it.
func GenerateLoopbackIPNetwork(nodeNum int) {
	ClientAddr = "127.0.0.1:20000"
	ClientAddrs = make(map[int]string)
	NodeAddr = make(map[int]string)
	LearningAgentAddr = make(map[int]string)
	for i := 1; i <= nodeNum; i++ {
		NodeAddr[i] = fmt.Sprintf("127.0.0.%d:%d", i+1, 28000+i*100)
		LearningAgentAddr[i] = fmt.Sprintf("127.0.0.%d:29000", i+1)
		ClientAddrs[i] = fmt.Sprintf("127.0.1.%d:20000", i+1)
	}
}

// GenerateRemoteNetwork puts client i on the host the NodeAddr formula gives node i.
func GenerateRemoteNetwork(nodeNum int) {
	ClientAddr = "172.18.4.1:20000"
	ClientAddrs = make(map[int]string)
	NodeAddr = make(map[int]string)
	LearningAgentAddr = nil
	for i := 0; i < nodeNum; i++ {
		NodeAddr[i] = fmt.Sprintf("172.18.4.%d:28000", i+2)
	}
	for i := 1; i <= nodeNum; i++ {
		ClientAddrs[i] = fmt.Sprintf("172.18.4.%d:20000", i+2)
	}
}
