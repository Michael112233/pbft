package client

import (
	"sync"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/leader_election"
	"github.com/michael112233/pbft/logger"
)

type Client struct {
	addr        string
	config      *config.Config
	injectSpeed int64
	txs         []*core.Transaction
	currentView int64

	wait sync.WaitGroup

	leaderElection *leader_election.LeaderElection
	log            *logger.Logger
	messageHub     *ClientMessageHub
}

func NewClient(addr string, config *config.Config) *Client {
	return &Client{
		addr:        addr,
		currentView: 0,
		config:      config,

		wait: sync.WaitGroup{},

		leaderElection: leader_election.NewLeaderElection(config),
		log:            logger.NewLogger(0, "client"),
		messageHub:     NewClientMessageHub(),
	}
}

func (c *Client) Start() {
	c.messageHub.Start(c, &sync.WaitGroup{})

	c.injectSpeed = c.config.InjectSpeed
	c.InjectTxs()
}

func (c *Client) Stop() {
	// TODO: implement
	c.wait.Wait()
	c.log.Debug("client stopped")
}

func (c *Client) AddTxs(txs []*core.Transaction) {
	c.txs = txs
}

func (c *Client) GetAddr() string {
	return c.addr
}
