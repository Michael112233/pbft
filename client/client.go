package client

import (
	"github.com/michael112233/pbft/config"
)

type Client struct {
	config *config.Config

	injectSpeed int64
}

func NewClient(config *config.Config) *Client {
	return &Client{
		config: config,
	}
}

func (c *Client) Start() {
	c.injectSpeed = c.config.InjectSpeed

	// c.StartInjectTransactions()
}
