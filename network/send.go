package network

import (
	"fmt"
	"net"

	"github.com/michael112233/pbft/logger"
)

var log = logger.NewLogger(0, "network")

func Dial(addr string) (net.Conn, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		log.Debug(fmt.Sprintf("DialTCPError: target_addr=%s, err=%v", addr, err))
		// 再dial一次
		log.Debug(fmt.Sprintf("Try dial again... target_addr=%s", addr))
		conn, err = net.Dial("tcp", addr)
		if err != nil {
			log.Debug(fmt.Sprintf("DialTCPError: target_addr=%s, err=%v", addr, err))
			return nil, nil
		} else {
			log.Debug(fmt.Sprintf("dial success. target_addr=%s", addr))
		}
	}
	return conn, nil
}
