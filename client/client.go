package client

import (
	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
)

type Client struct {
	addr   string
	config *config.Config

	injectSpeed int64
	txs         []*core.Transaction
}

func NewClient(addr string, config *config.Config) *Client {
	return &Client{
		addr:   addr,
		config: config,
	}
}

func (c *Client) Start() {
	c.injectSpeed = c.config.InjectSpeed
}
