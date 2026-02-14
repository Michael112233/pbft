package client

import (
	"crypto/ed25519"
	"sync"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/leader_election"
	"github.com/michael112233/pbft/logger"
)

type Client struct {
	addr        string
	name        string
	config      *config.Config
	injectSpeed int64
	txs         []*core.Transaction
	currentView int64

	WaitGroup sync.WaitGroup

	leaderElection *leader_election.LeaderElection
	log            *logger.Logger
	messageHub     *ClientMessageHub
	privateKey     ed25519.PrivateKey
}

func NewClient(addr string, name string, config *config.Config) *Client {
	privKey, err := crypto.ReadEd25519PrivateKey("keys/client_priv.pem")
	if err != nil {
		panic("Error reading client private key: " + err.Error())
	}

	return &Client{
		addr:        addr,
		name:        name,
		currentView: 0,
		config:      config,

		WaitGroup: sync.WaitGroup{},

		leaderElection: leader_election.NewLeaderElection(config),
		log:            logger.NewLogger(0, "client"),
		messageHub:     NewClientMessageHub(),
		privateKey:     privKey,
	}
}

func (c *Client) Start() {
	c.messageHub.Start(c, &sync.WaitGroup{})

	c.injectSpeed = c.config.InjectSpeed
	c.InjectTxs()
}

func (c *Client) Stop() {
	c.WaitGroup.Wait()
	c.log.Debug("client stopped")
}

func (c *Client) AddTxs(txs []*core.Transaction) {
	c.txs = txs
}

func (c *Client) GetAddr() string {
	return c.addr
}
