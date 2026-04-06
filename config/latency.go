package config

import "time"

func (cfg *Config) ArtificialLatency(fromAddr, toAddr string) time.Duration {
	if cfg == nil || cfg.FarNodeID == 0 || cfg.FarNodeDelayMs <= 0 {
		return 0
	}
	if cfg.addrMatchesFarNode(fromAddr) || cfg.addrMatchesFarNode(toAddr) {
		return time.Duration(cfg.FarNodeDelayMs) * time.Millisecond
	}
	return 0
}

func (cfg *Config) addrMatchesFarNode(addr string) bool {
	if cfg == nil || cfg.FarNodeID == 0 || addr == "" {
		return false
	}
	farAddr, ok := NodeAddr[cfg.FarNodeID]
	if !ok {
		return false
	}
	return farAddr == addr
}
