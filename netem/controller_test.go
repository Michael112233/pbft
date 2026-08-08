package netem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
)

type recordedCommand struct {
	executable string
	args       []string
}

type fakeRunner struct {
	mu       sync.Mutex
	calls    []recordedCommand
	failWhen func([]string) bool
}

func (runner *fakeRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	copyOfArgs := append([]string(nil), args...)
	runner.calls = append(runner.calls, recordedCommand{executable: executable, args: copyOfArgs})
	if runner.failWhen != nil && runner.failWhen(copyOfArgs) {
		return []byte("simulated tc failure"), errors.New("exit status 1")
	}
	return nil, nil
}

func (runner *fakeRunner) snapshot() []recordedCommand {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	result := make([]recordedCommand, len(runner.calls))
	copy(result, runner.calls)
	return result
}

func controllerRule(id string, seq, delay int64, lifetime string, duration int64) config.NetemRule {
	return config.NetemRule{
		ID: id,
		Event: config.NetemEventConfig{
			Type:   config.NetemExecutionEvent,
			NodeID: 1,
			Seq:    seq,
		},
		Action: config.NetemAction{
			DelayMs:    delay,
			Lifetime:   lifetime,
			DurationMs: duration,
		},
	}
}

func newTestController(t *testing.T, runner CommandRunner, rules ...config.NetemRule) *Controller {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		NodeNum: 4,
		Netem: config.NetemConfig{
			Enabled:    true,
			Interface:  "lo",
			SocketPath: dir + "/netem.sock",
			PIDPath:    dir + "/netem.pid",
			Limit:      100000,
			Rules:      rules,
		},
	}
	controller, err := NewController(cfg, runner, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewController returned error: %v", err)
	}
	t.Cleanup(controller.stopResetTimer)
	return controller
}

func eventFor(rule config.NetemRule) ExecutionEventRequest {
	return ExecutionEventRequest{
		Version: ProtocolVersion,
		Type:    rule.Event.Type,
		RuleID:  rule.ID,
		NodeID:  rule.Event.NodeID,
		Seq:     rule.Event.Seq,
	}
}

func TestHandleEventBuildsDelayCommandAndIsIdempotent(t *testing.T) {
	runner := &fakeRunner{}
	rule := controllerRule("delay", 2, 250, config.NetemLifetimeUntilNextEvent, 0)
	controller := newTestController(t, runner, rule)

	response := controller.HandleEvent(context.Background(), eventFor(rule))
	if !response.Accepted || !response.Applied || response.Error != "" {
		t.Fatalf("first response = %#v", response)
	}
	duplicate := controller.HandleEvent(context.Background(), eventFor(rule))
	if !duplicate.Accepted || duplicate.Applied || !duplicate.Duplicate {
		t.Fatalf("duplicate response = %#v", duplicate)
	}

	calls := runner.snapshot()
	if len(calls) != 1 {
		t.Fatalf("tc call count = %d, want 1", len(calls))
	}
	wantArgs := []string{
		"qdisc", "change", "dev", "lo", "parent", "1:3", "handle", "30:",
		"netem", "limit", "100000", "delay", "250ms",
	}
	if !slices.Equal(calls[0].args, wantArgs) {
		t.Fatalf("tc args = %q, want %q", calls[0].args, wantArgs)
	}
}

func TestHandleEventRejectsMismatchedEventWithoutRunningTC(t *testing.T) {
	runner := &fakeRunner{}
	rule := controllerRule("delay", 2, 250, config.NetemLifetimeUntilNextEvent, 0)
	controller := newTestController(t, runner, rule)
	request := eventFor(rule)
	request.NodeID = 2

	response := controller.HandleEvent(context.Background(), request)
	if response.Accepted || response.Error == "" {
		t.Fatalf("response = %#v, want rejected event", response)
	}
	if calls := runner.snapshot(); len(calls) != 0 {
		t.Fatalf("tc call count = %d, want 0", len(calls))
	}
}

func TestDurationRuleAutomaticallyResetsDelay(t *testing.T) {
	runner := &fakeRunner{}
	rule := controllerRule("timed", 2, 250, config.NetemLifetimeDuration, 15)
	controller := newTestController(t, runner, rule)

	response := controller.HandleEvent(context.Background(), eventFor(rule))
	if !response.Applied {
		t.Fatalf("response = %#v", response)
	}
	waitForCalls(t, runner, 2, time.Second)
	calls := runner.snapshot()
	if got := calls[len(calls)-1].args[len(calls[len(calls)-1].args)-1]; got != "0ms" {
		t.Fatalf("last delay = %q, want 0ms", got)
	}
}

func TestNewerEventPreventsOldTimerFromResettingDelay(t *testing.T) {
	runner := &fakeRunner{}
	timed := controllerRule("timed", 2, 100, config.NetemLifetimeDuration, 25)
	persistent := controllerRule("persistent", 3, 250, config.NetemLifetimeUntilNextEvent, 0)
	controller := newTestController(t, runner, timed, persistent)

	if response := controller.HandleEvent(context.Background(), eventFor(timed)); !response.Applied {
		t.Fatalf("timed response = %#v", response)
	}
	if response := controller.HandleEvent(context.Background(), eventFor(persistent)); !response.Applied {
		t.Fatalf("persistent response = %#v", response)
	}
	time.Sleep(60 * time.Millisecond)

	calls := runner.snapshot()
	if len(calls) != 2 {
		t.Fatalf("tc call count = %d, want 2; calls=%#v", len(calls), calls)
	}
	if got := calls[1].args[len(calls[1].args)-1]; got != "250ms" {
		t.Fatalf("last delay = %q, want 250ms", got)
	}
}

func TestZeroDelayRuleExplicitlyResetsPersistentDelay(t *testing.T) {
	runner := &fakeRunner{}
	delay := controllerRule("delay", 2, 250, config.NetemLifetimeUntilNextEvent, 0)
	reset := controllerRule("reset", 3, 0, config.NetemLifetimeUntilNextEvent, 0)
	controller := newTestController(t, runner, delay, reset)

	if response := controller.HandleEvent(context.Background(), eventFor(delay)); !response.Applied {
		t.Fatalf("delay response = %#v", response)
	}
	if response := controller.HandleEvent(context.Background(), eventFor(reset)); !response.Applied {
		t.Fatalf("reset response = %#v", response)
	}
	calls := runner.snapshot()
	if got := calls[len(calls)-1].args[len(calls[len(calls)-1].args)-1]; got != "0ms" {
		t.Fatalf("last delay = %q, want 0ms", got)
	}
}

func TestTCFailureIsReportedAndCanBeRetried(t *testing.T) {
	runner := &fakeRunner{failWhen: func(args []string) bool { return slices.Contains(args, "250ms") }}
	rule := controllerRule("delay", 2, 250, config.NetemLifetimeUntilNextEvent, 0)
	controller := newTestController(t, runner, rule)

	for attempt := 0; attempt < 2; attempt++ {
		response := controller.HandleEvent(context.Background(), eventFor(rule))
		if !response.Accepted || response.Applied || response.Duplicate || !strings.Contains(response.Error, "simulated tc failure") {
			t.Fatalf("attempt %d response = %#v", attempt, response)
		}
	}
	if calls := runner.snapshot(); len(calls) != 2 {
		t.Fatalf("tc call count = %d, want 2", len(calls))
	}
}

func TestSetupNetworkBuildsSharedLoopbackQdiscAndFilters(t *testing.T) {
	runner := &fakeRunner{failWhen: func(args []string) bool {
		return slices.Equal(args, []string{"qdisc", "del", "dev", "lo", "root"})
	}}
	rule := controllerRule("delay", 2, 250, config.NetemLifetimeUntilNextEvent, 0)
	controller := newTestController(t, runner, rule)
	if err := controller.setupNetwork(context.Background()); err != nil {
		t.Fatalf("setupNetwork returned error: %v", err)
	}

	calls := runner.snapshot()
	if len(calls) != 19 {
		t.Fatalf("tc call count = %d, want 19", len(calls))
	}
	if !slices.Contains(calls[1].args, "prio") || !slices.Contains(calls[2].args, "netem") {
		t.Fatalf("qdisc setup calls = %#v", calls[:3])
	}
	last := strings.Join(calls[len(calls)-1].args, " ")
	if !strings.Contains(last, "src_ip 127.0.0.5/32 dst_ip 127.0.0.5/32 classid 1:3") {
		t.Fatalf("last filter = %q", last)
	}
}

func TestCreatePIDFileRejectsRunningProcess(t *testing.T) {
	rule := controllerRule("delay", 2, 250, config.NetemLifetimeUntilNextEvent, 0)
	controller := newTestController(t, &fakeRunner{}, rule)
	if err := os.WriteFile(controller.cfg.Netem.PIDPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if _, err := controller.createPIDFile(); err == nil || !strings.Contains(err.Error(), "may be running") {
		t.Fatalf("createPIDFile error = %v, want running-process error", err)
	}
}

func TestRemoveStaleSocketRefusesRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-socket")
	if err := os.WriteFile(path, []byte("do not remove"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := removeStaleSocket(path); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("removeStaleSocket error = %v, want refusal", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("regular file was removed: %v", err)
	}
}

func waitForCalls(t *testing.T, runner *fakeRunner, count int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(runner.snapshot()) >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d calls; got %d", count, len(runner.snapshot()))
}

func (command recordedCommand) String() string {
	return fmt.Sprintf("%s %s", command.executable, strings.Join(command.args, " "))
}
