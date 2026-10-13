package client

import (
	"crypto/ed25519"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"

	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/utils"
)

// stallLogThreshold is the shortest delay the STALL monitoring logs.
const stallLogThreshold = 20 * time.Millisecond

type LeaderUpdate struct {
	view     core.ViewID
	leaderId int
	action   core.Action // part of the quorum key: 2f updates must agree on it too
}

type Client struct {
	addr   string
	name   string
	id     int // 1..client_num; client i is colocated with node i
	config *config.Config
	// injectSpeed is this client's share of inject_speed per 24 ms tick
	// (config.ClientInjectShare); the shares of all clients sum to inject_speed.
	injectSpeed     int64
	txs             []*core.Transaction
	currentView     core.ViewID
	newLeaderQuorum map[LeaderUpdate]int
	fNodes          int

	WaitGroup sync.WaitGroup

	log                *logger.Logger
	messageHub         *ClientMessageHub
	privateKey         ed25519.PrivateKey
	TransactionManager *TransactionManager
	EventManager       *EventManager
	requestPacer       requestPacer
	leaderMu           sync.RWMutex
	leaderAddr         string
	// leaderChangedAt is when leaderAddr last changed; cleared by the first request
	// sent to the new leader (STALL monitoring). Guarded by leaderMu.
	leaderChangedAt time.Time

	vcrunChan chan core.VCRunningStatus

	memoryLoggerStop     chan struct{}
	memoryLoggerDone     chan struct{}
	memoryLoggerStarted  atomic.Bool
	memoryLoggerStopOnce sync.Once

	requestSentTxs          atomic.Int64
	requestSendRateStop     chan struct{}
	requestSendRateDone     chan struct{}
	requestSendRateStarted  atomic.Bool
	requestSendRateStopOnce sync.Once

	// Targeted proposal throttling (client/throttlemanager.go). The timeline
	// writer runs whenever logging is on; throttle is non-nil only when the
	// controller is enabled. Both are fed from HandleLeaderUpdate, off leaderMu.
	throttle               *throttleManager
	leaderTimelineCh       chan leaderNotice
	leaderTimelineStop     chan struct{}
	leaderTimelineDone     chan struct{}
	leaderTimelineOn       atomic.Bool
	leaderTimelineStopOnce sync.Once
}

// NewClient builds client id (1..client_num). Every client signs with the shared
// client key; nodes tell clients apart by name.
func NewClient(addr string, name string, id int, config *config.Config, leaderAddr string) *Client {
	privKey, err := crypto.ReadEd25519PrivateKey("keys/client_priv.pem")
	if err != nil {
		panic("Error reading client private key: " + err.Error())
	}
	// leaderid := config.NodeAddr[1]
	logID := 0 // the single client keeps logs/client.log
	if config.ClientCount() > 1 {
		logID = id
	}
	log := logger.NewLogger(logID, "client")
	c := &Client{
		addr:            addr,
		name:            name,
		id:              id,
		injectSpeed:     config.ClientInjectShare(id),
		currentView:     core.ViewID{Generation: 1, Counter: 1},
		config:          config,
		newLeaderQuorum: make(map[LeaderUpdate]int),
		fNodes:          (int(config.NodeNum) - 1) / 3,

		WaitGroup:  sync.WaitGroup{},
		leaderAddr: leaderAddr,

		// leaderElection:     leader_election.NewLeaderElection(config),
		log:        log,
		messageHub: NewClientMessageHub(),
		privateKey: privKey,

		vcrunChan:           make(chan core.VCRunningStatus, 1),
		memoryLoggerStop:    make(chan struct{}),
		memoryLoggerDone:    make(chan struct{}),
		requestSendRateStop: make(chan struct{}),
		requestSendRateDone: make(chan struct{}),
		leaderTimelineCh:    make(chan leaderNotice, throttleNoticeBuffer),
		leaderTimelineStop:  make(chan struct{}),
		leaderTimelineDone:  make(chan struct{}),
	}
	txnManager := NewTransactionManager(c, log)
	txnManager.SetRetryPolicy(newRetryPolicy(config))
	// Only a retry path resends a request; without one, keeping every body would
	// grow the client by ~0.5 KB per request that never commits.
	txnManager.SetKeepRequestBody(config.ClientRetry || config.CompleteSuite)
	c.TransactionManager = txnManager
	c.EventManager = NewEventManager(c, log, defaultEventLowerBound, defaultEventUpperBound)
	// Static throttle runs (throttle.enabled, no scenario mode), or scenario runs
	// with a Throttle scenario, where the manager is active only in its generations.
	// Only client 1 runs it: a node's gate is one setting, so several managers
	// would overwrite each other's commands.
	if id == 1 && (config.Throttle.Enabled || config.ThrottleScenarioMode()) {
		c.throttle = newThrottleManager(c)
	}
	return c
}

func (c *Client) Start() {
	c.messageHub.Start(c, &sync.WaitGroup{})
	c.log.Info("client %s (id %d of %d) from %s, inject share %d per %v", c.name, c.id, c.config.ClientCount(), c.addr, c.injectSpeed, clientSendInterval)
	if c.config.Logging && c.memoryLoggerStarted.CompareAndSwap(false, true) {
		go utils.StartMemoryLogger(c.outPath("client_mem.log"), c.name, 30*time.Second, c.memoryLoggerStop, c.memoryLoggerDone)
	}
	if c.config.Logging && c.requestSendRateStarted.CompareAndSwap(false, true) {
		go c.requestSendRateLogger()
	}
	if c.leaderTimelineOn.CompareAndSwap(false, true) {
		go c.leaderTimelineLogger()
	}
	if c.throttle != nil {
		c.throttle.start()
	}
	if c.config.ClientRetry {
		c.log.Info("client retry on: mode=%s interval=%v max=%v", c.config.ClientRetryModeOrDefault(), c.config.ClientRetryInterval(), c.config.ClientRetryMax())
		c.TransactionManager.StartRetryTimer(true)
	}

	time.Sleep(100 * time.Millisecond) // msg hub to start
	// c.EventManager.Start()
	c.InjectTxs()
}

func (c *Client) Stop() {
	c.WaitGroup.Wait()
	c.EventManager.Stop()
	c.TransactionManager.StopRetryTimer()
	c.messageHub.Close()
	c.memoryLoggerStopOnce.Do(func() {
		close(c.memoryLoggerStop)
	})
	if c.memoryLoggerStarted.Load() {
		<-c.memoryLoggerDone
	}
	c.requestSendRateStopOnce.Do(func() {
		close(c.requestSendRateStop)
	})
	if c.requestSendRateStarted.Load() {
		<-c.requestSendRateDone
	}
	if c.throttle != nil {
		c.throttle.stopAndWait()
	}
	c.leaderTimelineStopOnce.Do(func() {
		close(c.leaderTimelineStop)
	})
	if c.leaderTimelineOn.Load() {
		<-c.leaderTimelineDone
	}
	c.log.Debug("client stopped")
}

// leaderTimelineLogger serializes accepted-leader observations to
// logs/leader_timeline.jsonl, the ground truth for per-leader tenure windows in
// the throttle analysis. Runs whether or not the controller is enabled.
func (c *Client) leaderTimelineLogger() {
	defer close(c.leaderTimelineDone)
	w := newJSONLWriter(c.outPath("leader_timeline.jsonl"), c.log)
	defer w.close()
	for {
		select {
		case <-c.leaderTimelineStop:
			return
		case n := <-c.leaderTimelineCh:
			w.write(map[string]any{
				"t":           time.Now().UnixNano(),
				"gen":         n.view.Generation,
				"counter":     n.view.Counter,
				"leader":      n.leaderID,
				"action":      core.ActiontoString(n.action),
				"observed_at": n.observedAt.UnixNano(),
			})
		}
	}
}

// notifyLeaderObserved feeds a confirmed leader change to the timeline writer
// and the throttle manager. Non-blocking: a full channel drops the notice so the
// client never stalls, and it must be called off leaderMu.
func (c *Client) notifyLeaderObserved(n leaderNotice) {
	select {
	case c.leaderTimelineCh <- n:
	default:
		c.log.Info("timeline channel full, dropped leader %d view (%d,%d)", n.leaderID, n.view.Generation, n.view.Counter)
	}
	if c.throttle != nil {
		c.throttle.notify(n)
	}
}

func (c *Client) AddTxs(txs []*core.Transaction) {
	c.txs = txs
}

func (c *Client) GetAddr() string {
	return c.addr
}

func (c *Client) currentLeaderAddr() string {
	c.leaderMu.RLock()
	defer c.leaderMu.RUnlock()
	return c.leaderAddr
}

// outPath returns where this client writes the file the single client writes to
// logs/<legacy>. With several clients each gets its own copy,
// logs/client<i>_<legacy> with a leading "client_" dropped (client_mem.log ->
// client3_mem.log), which scripts/merge_client_reports.py combines.
func (c *Client) outPath(legacy string) string {
	if c.config == nil || c.config.ClientCount() <= 1 {
		return filepath.Join("logs", legacy)
	}
	return filepath.Join("logs", fmt.Sprintf("client%d_%s", c.id, strings.TrimPrefix(legacy, "client_")))
}

func (c *Client) ExportTPSSeries(path string) error {
	pathToLogs := c.outPath(path)
	err := c.TransactionManager.ExportTPSSeries(pathToLogs)
	if err != nil {

		return err
	}
	latencyPath := c.outPath("latency" + path)
	err = c.TransactionManager.LatencySummary(latencyPath)
	if err != nil {
		return err
	}
	return nil
}
