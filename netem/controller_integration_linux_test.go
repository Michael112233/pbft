//go:build linux

package netem

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
)

type namespaceRunner struct {
	namespace string
}

func (runner namespaceRunner) Run(ctx context.Context, executable string, args ...string) ([]byte, error) {
	commandArgs := []string{"netns", "exec", runner.namespace, executable}
	commandArgs = append(commandArgs, args...)
	return exec.CommandContext(ctx, "ip", commandArgs...).CombinedOutput()
}

func TestNetemControllerInNetworkNamespace(t *testing.T) {
	if os.Getenv("PBFT_NETEM_INTEGRATION") != "1" {
		t.Skip("set PBFT_NETEM_INTEGRATION=1 to run the privileged namespace test")
	}
	if os.Geteuid() != 0 {
		t.Skip("network namespace integration test requires root")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("ip command is unavailable")
	}
	if _, err := exec.LookPath("tc"); err != nil {
		t.Skip("tc command is unavailable")
	}

	namespace := fmt.Sprintf("pbft-netem-%d", os.Getpid())
	runHostCommand(t, "ip", "netns", "add", namespace)
	t.Cleanup(func() {
		_ = exec.Command("ip", "netns", "delete", namespace).Run()
	})
	runHostCommand(t, "ip", "-n", namespace, "link", "set", "lo", "up")

	delayRule := controllerRule("delay", 2, 250, config.NetemLifetimeUntilNextEvent, 0)
	resetRule := controllerRule("reset", 3, 0, config.NetemLifetimeUntilNextEvent, 0)
	cfg := &config.Config{
		NodeNum: 1,
		Netem: config.NetemConfig{
			Enabled:    true,
			Interface:  "lo",
			SocketPath: t.TempDir() + "/netem.sock",
			PIDPath:    t.TempDir() + "/netem.pid",
			Limit:      100000,
			Rules:      []config.NetemRule{delayRule, resetRule},
		},
	}
	controller, err := NewController(cfg, namespaceRunner{namespace: namespace}, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewController returned error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := controller.setupNetwork(ctx); err != nil {
		t.Fatalf("setupNetwork returned error: %v", err)
	}
	t.Cleanup(func() { _ = controller.cleanupNetwork() })

	if response := controller.HandleEvent(ctx, eventFor(delayRule)); !response.Applied {
		t.Fatalf("delay response = %#v", response)
	}
	show := runHostCommand(t, "ip", "netns", "exec", namespace, "tc", "qdisc", "show", "dev", "lo")
	if !strings.Contains(show, "delay 250ms") {
		t.Fatalf("qdisc output does not contain delay 250ms: %s", show)
	}

	if response := controller.HandleEvent(ctx, eventFor(resetRule)); !response.Applied {
		t.Fatalf("reset response = %#v", response)
	}
	show = runHostCommand(t, "ip", "netns", "exec", namespace, "tc", "qdisc", "show", "dev", "lo")
	if strings.Contains(show, "delay 250ms") {
		t.Fatalf("qdisc still contains delay 250ms after reset: %s", show)
	}
}

func runHostCommand(t *testing.T, executable string, args ...string) string {
	t.Helper()
	output, err := exec.Command(executable, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v: %s", executable, strings.Join(args, " "), err, output)
	}
	return string(output)
}
