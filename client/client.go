package client

import (
	"sync"
	"time"

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
}

func NewClient(addr string, config *config.Config) *Client {
	return &Client{
		addr:        addr,
		currentView: 0,
		config:      config,

		wait: sync.WaitGroup{},

		leaderElection: leader_election.NewLeaderElection(config),
		log:            logger.NewLogger(0, "client"),
	}
}

func (c *Client) Start() {
	c.injectSpeed = c.config.InjectSpeed
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

func (c *Client) InjectTxs() {
	c.wait.Add(1)
	go func() {
		defer c.wait.Done()
		var injectTxs []*core.Transaction
		for i := int64(0); (i+1)*c.injectSpeed <= int64(len(c.txs)); i++ {
			injectTxs = c.txs[i*c.injectSpeed : (i+1)*c.injectSpeed]
			leader := c.leaderElection.GetLeader(c.currentView)
			msg := core.RequestMsg{
				Timestamp: time.Now().Unix(),
				From:      c.addr,
				To:        leader,
				Txs:       injectTxs,
			}
			c.log.Info("client %s inject txs to %s, len: %d", c.addr, leader, len(msg.Txs))
		}
	}()
}
