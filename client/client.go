package client

import (
	"pbft/config"
)

type Client struct {
	injectSpeed int64
}

func NewClient() *Client {

	return &Client{}
}

func (c *Client) Start() {
	c.injectSpeed = config.InjectSpeed

	// c.StartInjectTransactions()
}
