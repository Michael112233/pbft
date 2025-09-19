package controller

import (
	"github.com/michael112233/pbft/config"
)

func runNode(mode string) {

}

func runClient(mode string) {

}

func Main(role, mode, cfgPath string) {
	config := config.ReadCfg(cfgPath)

	// mode -> network structure
	switch mode {
	case "local":
		config.GenerateLocalNetwork()
	case "remote":
		config.GenerateRemoteNetwork()
	}

	// if mode == "local", then all nodes are running on the same machin
	// role -> system role
	switch role {
	case "node":
		runNode(mode)
	case "client":
		runClient(mode)
	}
}
