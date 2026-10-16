package main

import (
	"github.com/michael112233/pbft/controller"
	"github.com/spf13/pflag"
)

type Args struct {
	Role    string
	Mode    string
	NodeNum int64
}

var role = pflag.StringP("role", "r", "node", "role type (node or client)")
var mode = pflag.StringP("mode", "m", "local", "network mode (local, loopbackip or remote)")
var cfgPath = pflag.StringP("config", "c", "config/run2new.json", "path to the experiment config json")
var nodeID = pflag.Int64P("node-id", "n", 0, "node id; for a client, its id 1..client_num (default 1)")
var hostsPath = pflag.String("hosts", "", "hosts file giving every node's and client's IP; required with -m remote")

func main() {
	pflag.Parse()
	controller.Main(*nodeID, *role, *mode, *cfgPath, *hostsPath)
}
