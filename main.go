package main

import (
	"github.com/michael112233/pbft/controller"
	"github.com/spf13/pflag"
)

const (
	cfgPath = "config/run.json"
)

type Args struct {
	Role string
	Mode string
}

var role = pflag.StringP("role", "r", "node", "role type (node or client)")
var mode = pflag.StringP("mode", "m", "local", "mode (local or remote)")

func main() {
	pflag.Parse()
	controller.Main(*role, *mode, cfgPath)
}
