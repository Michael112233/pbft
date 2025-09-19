package client

import "github.com/michael112233/pbft/logger"

func TestClientInit(client *Client) {
	logger := logger.NewLogger(0, "test")
	logger.Test("client init: %v", client)
}
