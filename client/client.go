package client

import (
	"crypto/ed25519"
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"

	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/utils"
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

	cchan     chan struct{}
	vcrunChan chan core.VCRunningStatus

	memoryLoggerStop     chan struct{}
	memoryLoggerDone     chan struct{}
	memoryLoggerStarted  atomic.Bool
	memoryLoggerStopOnce sync.Once
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
		currentView: 1,
		config:      config,

		WaitGroup:  sync.WaitGroup{},
		leaderAddr: leaderAddr,

		// leaderElection:     leader_election.NewLeaderElection(config),
		log:                logger.NewLogger(0, "client"),
		messageHub:         NewClientMessageHub(),
		privateKey:         privKey,
		TransactionManager: NewTransactionManager(),
		cchan:              make(chan struct{}, 4), // buffer to number of nodes
		vcrunChan:          make(chan core.VCRunningStatus, 1),
		memoryLoggerStop:   make(chan struct{}),
		memoryLoggerDone:   make(chan struct{}),
	}
}

func (c *Client) Start() {
	c.messageHub.Start(c, &sync.WaitGroup{})
	if c.config.Logging && c.memoryLoggerStarted.CompareAndSwap(false, true) {
		go utils.StartMemoryLogger("logs/client_mem.log", "client", 10*time.Second, c.memoryLoggerStop, c.memoryLoggerDone)
	}
	go c.TransactionManager.TransactionTimerWorker(c)
	c.injectSpeed = c.config.InjectSpeed
	c.InjectTxs()
}

func (c *Client) Stop() {
	c.WaitGroup.Wait()
	c.messageHub.Close()
	c.TransactionManager.StopTimer()
	c.memoryLoggerStopOnce.Do(func() {
		close(c.memoryLoggerStop)
	})
	if c.memoryLoggerStarted.Load() {
		<-c.memoryLoggerDone
	}
	c.log.Debug("client stopped")
}

func (c *Client) AddTxs(txs []*core.Transaction) {
	c.txs = txs
}

func (c *Client) GetAddr() string {
	return c.addr
}

func (c *Client) ExportTPSSeries(path string) error {
	err := c.TransactionManager.ExportTPSSeries(path)
	if err != nil {

		return err
	}
	latencyPath := "latency" + path
	err = c.TransactionManager.LatencySummary(latencyPath)
	if err != nil {
		return err
	}
	return nil
}
