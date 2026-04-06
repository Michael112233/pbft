package client

import (
	"crypto/ed25519"
	"sync"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"

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

	log                *logger.Logger
	messageHub         *ClientMessageHub
	privateKey         ed25519.PrivateKey
	TransactionManager *TransactionManager
	leaderMu           sync.RWMutex
	leaderAddr         string
}

func NewClient(addr string, name string, config *config.Config, leaderAddr string) *Client {
	privKey, err := crypto.ReadEd25519PrivateKey("keys/client_priv.pem")
	if err != nil {
		panic("Error reading client private key: " + err.Error())
	}
	// leaderid := config.NodeAddr[1]
	return &Client{
		addr:        addr,
		name:        name,
		currentView: 0,
		config:      config,

		WaitGroup:  sync.WaitGroup{},
		leaderAddr: leaderAddr,

		// leaderElection:     leader_election.NewLeaderElection(config),
		log:                logger.NewLogger(0, "client"),
		messageHub:         NewClientMessageHub(),
		privateKey:         privKey,
		TransactionManager: NewTransactionManager(),
	}
}

func (c *Client) Start() {
	c.messageHub.Start(c, &sync.WaitGroup{})
	go c.TransactionManager.TransactionTimerWorker(c)
	c.injectSpeed = c.config.InjectSpeed
	c.InjectTxs()
}

func (c *Client) Stop() {
	c.WaitGroup.Wait()
	c.messageHub.Close()
	c.TransactionManager.StopTimer()
	c.log.Debug("client stopped")
}

func (c *Client) AddTxs(txs []*core.Transaction) {
	c.txs = txs
}

func (c *Client) GetAddr() string {
	return c.addr
}
