package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
)

// Network modes, the values of -m.
const (
	NetworkModeLocal      = "local"
	NetworkModeLoopbackIP = "loopbackip"
	NetworkModeRemote     = "remote"
)

// Remote-mode ports: the loopbackip ones, so remote differs from loopbackip only
// in the IPs and learningagent/address.py keeps one port formula for both.
const (
	nodeBasePort = 28000
	nodePortStep = 100
	agentPort    = 29000
	clientPort   = 20000 // never listened on; only the client's IP is used
)

var (
	// NetworkMode is the mode the Generate*Network call that ran last set up.
	NetworkMode string
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
	NetworkMode = NetworkModeLocal
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
	NetworkMode = NetworkModeLoopbackIP
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

// HostEntry is one machine in a hosts file. SSH is the name the cluster scripts
// log in with (scripts/cluster/); the Go side only reads IP.
type HostEntry struct {
	IP  string `json:"ip"`
	SSH string `json:"ssh"`
}

// Hosts is the hosts file -m remote takes through --hosts: which machine runs
// each node and each client. It lives outside the experiment config so one
// config runs unchanged on any cluster. Keys are ids as strings ("1".."n").
type Hosts struct {
	RunDir  string               `json:"run_dir"`
	Nodes   map[string]HostEntry `json:"nodes"`
	Clients map[string]HostEntry `json:"clients"`
}

// LoadHosts reads a hosts file.
func LoadHosts(path string) (*Hosts, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read hosts file: %w", err)
	}
	var h Hosts
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("parse hosts file %s: %w", path, err)
	}
	return &h, nil
}

// hostIPs returns ids 1..count from entries, each a valid IP. It rejects
// missing, extra and non-numeric ids so a hosts file written for another
// node_num fails at startup instead of leaving a node without an address.
func hostIPs(kind string, entries map[string]HostEntry, count int) (map[int]string, error) {
	ips := make(map[int]string, count)
	for key, e := range entries {
		id, err := strconv.Atoi(key)
		if err != nil || id < 1 || id > count {
			return nil, fmt.Errorf("hosts file: %s id %q, want 1..%d", kind, key, count)
		}
		if net.ParseIP(e.IP) == nil {
			return nil, fmt.Errorf("hosts file: %s %d ip %q is not an IP address", kind, id, e.IP)
		}
		ips[id] = e.IP
	}
	for id := 1; id <= count; id++ {
		if _, ok := ips[id]; !ok {
			return nil, fmt.Errorf("hosts file: %s %d missing, want ids 1..%d", kind, id, count)
		}
	}
	return ips, nil
}

// GenerateRemoteNetwork gives node i hosts.Nodes[i]:28000+100i, its learning
// agent the same IP on 29000, and client i hosts.Clients[i]. The ports are the
// loopbackip ones, so only the IPs differ between the modes.
//
// Every IP must be distinct, and a client may not share a node's IP: in remote
// mode each node delays its own egress to the peer IPs (scripts/netem_egress.sh),
// and a client sending from a node's IP would be delayed along with it.
func GenerateRemoteNetwork(nodeNum, clientNum int, hosts *Hosts) error {
	if hosts == nil {
		return fmt.Errorf("remote mode needs a hosts file (--hosts)")
	}
	if clientNum < 1 {
		clientNum = 1
	}
	nodeIPs, err := hostIPs("node", hosts.Nodes, nodeNum)
	if err != nil {
		return err
	}
	if len(hosts.Clients) < clientNum {
		return fmt.Errorf("hosts file: %d clients, client_num is %d", len(hosts.Clients), clientNum)
	}
	clientIPs, err := hostIPs("client", hosts.Clients, len(hosts.Clients))
	if err != nil {
		return err
	}
	owner := make(map[string]string)
	claim := func(ip, who string) error {
		if prev, ok := owner[ip]; ok {
			return fmt.Errorf("hosts file: %s and %s share ip %s; every node and client needs its own", prev, who, ip)
		}
		owner[ip] = who
		return nil
	}
	for id := 1; id <= nodeNum; id++ {
		if err := claim(nodeIPs[id], fmt.Sprintf("node %d", id)); err != nil {
			return err
		}
	}
	for id := 1; id <= clientNum; id++ {
		if err := claim(clientIPs[id], fmt.Sprintf("client %d", id)); err != nil {
			return err
		}
	}

	NetworkMode = NetworkModeRemote
	NodeAddr = make(map[int]string, nodeNum)
	LearningAgentAddr = make(map[int]string, nodeNum)
	ClientAddrs = make(map[int]string, clientNum)
	for id := 1; id <= nodeNum; id++ {
		NodeAddr[id] = net.JoinHostPort(nodeIPs[id], strconv.Itoa(nodeBasePort+id*nodePortStep))
		LearningAgentAddr[id] = net.JoinHostPort(nodeIPs[id], strconv.Itoa(agentPort))
	}
	for id := 1; id <= clientNum; id++ {
		ClientAddrs[id] = net.JoinHostPort(clientIPs[id], strconv.Itoa(clientPort))
	}
	ClientAddr = ClientAddrs[1]
	return nil
}
