package main

import (
	"github.com/michael112233/pbft/controller"
	"github.com/spf13/pflag"
)

const (
	cfgPath = "config/run.json"
)

type Args struct {
	Role    string
	Mode    string
	NodeNum int64
}

var role = pflag.StringP("role", "r", "node", "role type (node or client)")
var mode = pflag.StringP("mode", "m", "local", "mode (local or remote)")
var nodeID = pflag.Int64P("node-id", "n", 0, "node id, if role is client, no need to input")

func main() {
	pflag.Parse()
	controller.Main(*nodeID, *role, *mode, cfgPath)
}
