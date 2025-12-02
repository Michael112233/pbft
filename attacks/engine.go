package attacks

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/michael112233/pbft/logger"
)

// Engine is the orchestrator that applies attack steps.
type Engine struct {
	log      *logger.Logger
	scenario *Scenario

	rootSeed int64

	mu     sync.Mutex
	rngSel *randState // deterministic selectors
	rngTim *randState // deterministic timing

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type randState struct {
	tag string
	// could hold *rand.Rand if/when used
}

func NewEngine(nodeID int64, s *Scenario) *Engine {
	l := logger.NewLogger(nodeID, "attack_engine")
	e := &Engine{
		log:      l,
		scenario: s,
		rootSeed: 0,
	}
	if s != nil {
		e.rootSeed = s.Seed
	}
	e.rngSel = &randState{tag: "selector"}
	e.rngTim = &randState{tag: "timing"}
	return e
}

// Start begins background scheduling. No-op if scenario is nil.
func (e *Engine) Start() {
	if e.scenario == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	// Register scenario for send hooks and set the start time anchor
	registerScenario(e.scenario, time.Now())
	e.wg.Add(1)
	go e.run(ctx)
	e.log.Info("Attack Engine started: scenario=%s seed=%d steps=%d", e.scenario.Name, e.scenario.Seed, len(e.scenario.Steps))
}

// Stop terminates background scheduling.
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
		e.wg.Wait()
		e.cancel = nil
		e.log.Info("Attack Engine stopped")
	}
}

func (e *Engine) run(ctx context.Context) {
	defer e.wg.Done()
	start := time.Now()
	// For now we only log planned steps based on wall clock to validate wiring
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	logged := make(map[string]bool)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			elapsedMs := time.Since(start).Milliseconds()
			for _, step := range e.scenario.Steps {
				key := fmt.Sprintf("%s_once", step.ID)
				if logged[key] {
					continue
				}
				// Minimal evaluation: only time_since_start trigger is "logged" when elapsed passes start_ms.
				if step.When.Trigger == TriggerTimeSinceStart && elapsedMs >= step.When.StartMs {
					e.log.Info("Planned step ready (no-op): id=%s what=%s start_ms=%d elapsed_ms=%d",
						step.ID, step.What, step.When.StartMs, elapsedMs)
					logged[key] = true
				}
				// on_start logs immediately
				if step.When.Trigger == TriggerOnStart && elapsedMs >= 0 && !logged[key] {
					e.log.Info("Planned step ready (no-op): id=%s what=%s trigger=on_start", step.ID, step.What)
					logged[key] = true
				}
			}
		}
	}
}
