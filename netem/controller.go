package netem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/michael112233/pbft/config"
)

const (
	commandTimeout      = 3 * time.Second
	setupTimeout        = 30 * time.Second
	connectionTimeout   = 5 * time.Second
	maximumRequestBytes = 64 * 1024
)

type Controller struct {
	cfg       *config.Config
	runner    CommandRunner
	tcPath    string
	log       *log.Logger
	rulesByID map[string]config.NetemRule

	mu           sync.Mutex
	appliedRules map[string]struct{}
	generation   uint64
	resetTimer   *time.Timer
	currentDelay int64

	listener *net.UnixListener
	workers  sync.WaitGroup
}

func NewController(cfg *config.Config, runner CommandRunner, logger *log.Logger) (*Controller, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	cfg.ApplyNetemDefaults()
	if err := cfg.ValidateNetem(); err != nil {
		return nil, err
	}
	if !cfg.Netem.Enabled {
		return nil, fmt.Errorf("netem is disabled")
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	// to run tc commands
	tcPath := "tc"
	if runner == nil {
		resolvedPath, err := exec.LookPath("tc")
		if err != nil {
			return nil, fmt.Errorf("find tc executable: %w", err)
		}
		tcPath = resolvedPath
		runner = ExecCommandRunner{}
	}

	rulesByID := make(map[string]config.NetemRule, len(cfg.Netem.Rules))
	for _, rule := range cfg.Netem.Rules {
		rulesByID[rule.ID] = rule
	}

	return &Controller{
		cfg:          cfg,
		runner:       runner,
		tcPath:       tcPath,
		log:          logger,
		rulesByID:    rulesByID,
		appliedRules: make(map[string]struct{}, len(rulesByID)),
	}, nil
}

func (c *Controller) Run(ctx context.Context) error {
	// socket listener
	if err := c.writePIDFile(); err != nil {
		return err
	}
	if err := c.setupNetwork(ctx); err != nil {
		_ = c.cleanupNetwork()
		_ = c.removePIDFile()
		return err
	}

	listener, err := c.listen()
	if err != nil {
		_ = c.cleanupNetwork()
		_ = c.removePIDFile()
		return err
	}
	c.listener = listener

	c.log.Printf("ready socket=%s interface=%s pid=%d", c.cfg.Netem.SocketPath, c.cfg.Netem.Interface, os.Getpid())
	go func() {
		<-ctx.Done() // cancel by signal interrupt or stop script
		_ = listener.Close()
	}()

	for {
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			if ctx.Err() != nil || errors.Is(acceptErr, net.ErrClosed) {
				break
			}
			c.log.Printf("accept error: %v", acceptErr)
			continue
		}
		c.workers.Add(1)
		go func() {
			defer c.workers.Done()
			c.handleConnection(conn)
		}()
	}
	// handle each conn in go thread

	c.workers.Wait() // wait for all connections to finish
	// clean up
	c.stopResetTimer()
	cleanupErr := c.cleanupNetwork()
	pidErr := c.removePIDFile()
	if pidErr != nil {
		c.log.Printf("remove pid file: %v", pidErr)
	}
	socketErr := c.removeSocket()
	if cleanupErr != nil {
		return cleanupErr
	}
	return socketErr
}

func (c *Controller) HandleEvent(ctx context.Context, request ExecutionEventRequest) EventResponse {
	response := EventResponse{
		Version: ProtocolVersion,
		RuleID:  request.RuleID,
		NodeID:  request.NodeID,
		Seq:     request.Seq,
	}
	rule, err := c.validateRequest(request)
	if err != nil {
		response.Error = err.Error()
		return response
	}
	response.Accepted = true

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.appliedRules[rule.ID]; exists {
		response.Duplicate = true
		return response
	}

	commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	err = c.applyDelay(commandCtx, rule.Action.DelayMs)
	cancel()
	if err != nil {
		response.Error = err.Error()
		return response
	}
	// mutex protects
	/* The mutex protects:
	appliedRules
	generation
	resetTimer
	currentDelay
	It also ensures two tc changes are not executed concurrently. */
	c.generation++ // imp for resetAfterDuration to know if a new rule has been applied since the timer was set
	/*If an earlier rule had a scheduled reset, the controller attempts to cancel it because the new rule supersedes it.
	However, Timer.Stop() alone is not sufficient. The old timer callback may already have started or may be waiting for the mutex. That is why generation is also required.*/
	generation := c.generation
	if c.resetTimer != nil {
		c.resetTimer.Stop()
		c.resetTimer = nil
	}
	c.currentDelay = rule.Action.DelayMs
	c.appliedRules[rule.ID] = struct{}{}
	response.Applied = true
	c.log.Printf("applied rule=%s node=%d seq=%d delay_ms=%d lifetime=%s", rule.ID, request.NodeID, request.Seq, rule.Action.DelayMs, rule.Action.Lifetime)
	//lifetime event expire after certain time
	if rule.Action.Lifetime == config.NetemLifetimeDuration {
		duration := time.Duration(rule.Action.DurationMs) * time.Millisecond
		c.resetTimer = time.AfterFunc(duration, func() {
			c.resetAfterDuration(generation, rule.ID)
		})
	}
	return response
}

func (c *Controller) validateRequest(request ExecutionEventRequest) (config.NetemRule, error) {
	if request.Version != ProtocolVersion {
		return config.NetemRule{}, fmt.Errorf("unsupported protocol version %d", request.Version)
	}
	if request.Type != config.NetemExecutionEvent {
		return config.NetemRule{}, fmt.Errorf("unsupported event type %q", request.Type)
	}
	rule, exists := c.rulesByID[request.RuleID]
	if !exists {
		return config.NetemRule{}, fmt.Errorf("unknown rule %q", request.RuleID)
	}
	if rule.Event.Type != request.Type || rule.Event.NodeID != request.NodeID || rule.Event.Seq != request.Seq {
		return config.NetemRule{}, fmt.Errorf("event does not match rule %q", request.RuleID)
	}
	return rule, nil
}

func (c *Controller) resetAfterDuration(generation uint64, ruleID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generation {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if err := c.applyDelay(ctx, 0); err != nil {
		c.log.Printf("timed reset failed rule=%s: %v", ruleID, err)
		return
	}
	c.currentDelay = 0
	c.resetTimer = nil
	c.log.Printf("timed reset applied rule=%s delay_ms=0", ruleID)
}

func (c *Controller) handleConnection(conn *net.UnixConn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(connectionTimeout))
	// get request
	decoder := json.NewDecoder(io.LimitReader(conn, maximumRequestBytes))
	var request ExecutionEventRequest
	if err := decoder.Decode(&request); err != nil {
		_ = json.NewEncoder(conn).Encode(EventResponse{Version: ProtocolVersion, Error: fmt.Sprintf("decode request: %v", err)})
		return
	}
	response := c.HandleEvent(context.Background(), request)
	if response.Error != "" {
		c.log.Printf("event failed rule=%s node=%d seq=%d accepted=%t error=%s", request.RuleID, request.NodeID, request.Seq, response.Accepted, response.Error)
	} else if response.Duplicate {
		c.log.Printf("duplicate event ignored rule=%s node=%d seq=%d", request.RuleID, request.NodeID, request.Seq)
	}
	if err := json.NewEncoder(conn).Encode(response); err != nil { // send response
		c.log.Printf("encode response rule=%s: %v", request.RuleID, err)
	}
}

// setup neywork delay class
func (c *Controller) setupNetwork(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, setupTimeout)
	defer cancel()
	if output, err := c.runner.Run(ctx, c.tcPath, "qdisc", "del", "dev", c.cfg.Netem.Interface, "root"); err != nil {
		c.log.Printf("initial qdisc delete ignored: %v output=%s", err, strings.TrimSpace(string(output)))
	}

	prioArgs := []string{"qdisc", "add", "dev", c.cfg.Netem.Interface, "root", "handle", "1:", "prio", "bands", "3", "priomap"}
	for i := 0; i < 16; i++ {
		prioArgs = append(prioArgs, "0")
	}
	if err := c.runTC(ctx, prioArgs...); err != nil {
		return fmt.Errorf("add root prio qdisc: %w", err)
	}
	if err := c.runTC(ctx,
		"qdisc", "add", "dev", c.cfg.Netem.Interface,
		"parent", "1:3", "handle", "30:",
		"netem", "limit", strconv.Itoa(c.cfg.Netem.Limit), "delay", "0ms",
	); err != nil {
		return fmt.Errorf("add netem qdisc: %w", err)
	}

	for sourceID := 1; sourceID <= int(c.cfg.NodeNum); sourceID++ {
		sourceIP := fmt.Sprintf("127.0.0.%d/32", sourceID+1)
		for destinationID := 1; destinationID <= int(c.cfg.NodeNum); destinationID++ {
			destinationIP := fmt.Sprintf("127.0.0.%d/32", destinationID+1)
			if err := c.runTC(ctx,
				"filter", "add", "dev", c.cfg.Netem.Interface,
				"parent", "1:", "protocol", "ip", "prio", "10", "flower",
				"src_ip", sourceIP, "dst_ip", destinationIP, "classid", "1:3",
			); err != nil {
				return fmt.Errorf("add netem filter %d->%d: %w", sourceID, destinationID, err)
			}
		}
	}
	return nil
}

func (c *Controller) applyDelay(ctx context.Context, delayMs int64) error {
	return c.runTC(ctx,
		"qdisc", "change", "dev", c.cfg.Netem.Interface,
		"parent", "1:3", "handle", "30:",
		"netem", "limit", strconv.Itoa(c.cfg.Netem.Limit),
		"delay", fmt.Sprintf("%dms", delayMs),
	)
}

func (c *Controller) runTC(ctx context.Context, args ...string) error {
	output, err := c.runner.Run(ctx, c.tcPath, args...)
	if err != nil {
		return fmt.Errorf("tc %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (c *Controller) cleanupNetwork() error {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	output, err := c.runner.Run(ctx, c.tcPath, "qdisc", "del", "dev", c.cfg.Netem.Interface, "root")
	if err != nil {
		return fmt.Errorf("remove root qdisc: %w: %s", err, strings.TrimSpace(string(output)))
	}
	c.log.Printf("removed root qdisc interface=%s", c.cfg.Netem.Interface)
	return nil
}

func (c *Controller) listen() (*net.UnixListener, error) {
	if err := removeStaleSocket(c.cfg.Netem.SocketPath); err != nil {
		return nil, err
	}
	address := &net.UnixAddr{Name: c.cfg.Netem.SocketPath, Net: "unix"}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", c.cfg.Netem.SocketPath, err)
	}
	uid, gid := socketOwner()
	if err := os.Chown(c.cfg.Netem.SocketPath, uid, gid); err != nil {
		_ = listener.Close()
		_ = os.Remove(c.cfg.Netem.SocketPath)
		return nil, fmt.Errorf("set socket owner: %w", err)
	}
	if err := os.Chmod(c.cfg.Netem.SocketPath, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(c.cfg.Netem.SocketPath)
		return nil, fmt.Errorf("set socket permissions: %w", err)
	}
	return listener, nil
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect socket path: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to remove non-socket path %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return nil
}

func socketOwner() (int, int) {
	uid, gid := os.Getuid(), os.Getgid()
	if sudoUID, err := strconv.Atoi(os.Getenv("SUDO_UID")); err == nil && sudoUID >= 0 {
		uid = sudoUID
	}
	if sudoGID, err := strconv.Atoi(os.Getenv("SUDO_GID")); err == nil && sudoGID >= 0 {
		gid = sudoGID
	}
	return uid, gid
}

func (c *Controller) writePIDFile() error {
	if err := os.MkdirAll(filepath.Dir(c.cfg.Netem.PIDPath), 0o755); err != nil {
		return fmt.Errorf("create pid directory: %w", err)
	}
	file, err := c.createPIDFile()
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		_ = file.Close()
		_ = os.Remove(c.cfg.Netem.PIDPath)
		return fmt.Errorf("write pid file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(c.cfg.Netem.PIDPath)
		return fmt.Errorf("close pid file: %w", err)
	}
	return nil
}

func (c *Controller) createPIDFile() (*os.File, error) {
	file, err := os.OpenFile(c.cfg.Netem.PIDPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		return file, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create pid file: %w", err)
	}

	data, readErr := os.ReadFile(c.cfg.Netem.PIDPath)
	if readErr != nil {
		return nil, fmt.Errorf("read existing pid file: %w", readErr)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if parseErr != nil || pid <= 0 {
		return nil, fmt.Errorf("existing pid file contains invalid pid %q", strings.TrimSpace(string(data)))
	}
	if processExists(pid) {
		return nil, fmt.Errorf("another netem controller may be running with pid %d", pid)
	}
	if removeErr := os.Remove(c.cfg.Netem.PIDPath); removeErr != nil {
		return nil, fmt.Errorf("remove stale pid file: %w", removeErr)
	}
	file, err = os.OpenFile(c.cfg.Netem.PIDPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, fmt.Errorf("create pid file after stale cleanup: %w", err)
	}
	return file, nil
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (c *Controller) removeSocket() error {
	err := os.Remove(c.cfg.Netem.SocketPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove socket: %w", err)
	}
	return nil
}

func (c *Controller) removePIDFile() error {
	err := os.Remove(c.cfg.Netem.PIDPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove pid file: %w", err)
	}
	return nil
}

func (c *Controller) stopResetTimer() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	if c.resetTimer != nil {
		c.resetTimer.Stop()
		c.resetTimer = nil
	}
}
