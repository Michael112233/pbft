# Event-triggered netem controller

PBFT nodes remain unprivileged. When a configured node executes a configured
sequence, it asynchronously sends a JSON event to the local Go netem
controller over `logs/netem-controller.sock`. The controller validates the
event against the same experiment configuration and is the only process that
runs `tc`.

## Configuration

Add rules under `netem` in `config/run2new.json`:

```json
"netem": {
  "enabled": true,
  "interface": "lo",
  "socket_path": "logs/netem-controller.sock",
  "pid_path": "logs/netem-controller.sock.pid",
  "limit": 100000,
  "rules": [
    {
      "id": "delay-after-seq-2",
      "event": {
        "type": "execution",
        "node_id": 1,
        "seq": 2
      },
      "action": {
        "delay_ms": 250,
        "lifetime": "until_next_event"
      }
    }
  ]
}
```

`until_next_event` leaves the delay in place until another rule changes it. A
later rule with `delay_ms: 0` resets netem. To reset automatically, use:

```json
"action": {
  "delay_ms": 250,
  "lifetime": "duration",
  "duration_ms": 2000
}
```

A newer event always supersedes an older duration timer, so an old timer
cannot reset a newer impairment.

## Generations and timer safety

The controller serializes all qdisc state changes with `c.mu`. Each
successfully applied rule increments `c.generation`. This value is a controller
state version; it is not a PBFT sequence number. A duration rule captures its
generation when it creates the reset timer:

```go
c.generation++
generation := c.generation
c.resetTimer = time.AfterFunc(duration, func() {
    c.resetAfterDuration(generation, rule.ID)
})
```

Before resetting the delay to zero, the callback locks the same mutex and
checks that its captured generation is still current:

```go
if generation != c.generation {
    return
}
```

For example:

```text
Rule A applies 250ms for 5 seconds  -> generation 1
Rule B applies 500ms after 2 seconds -> generation 2
Rule A's timer fires after 5 seconds -> 1 != 2, so it does nothing
Final active delay                    -> 500ms
```

The controller also calls `c.resetTimer.Stop()` when a newer rule succeeds,
but `Stop` alone cannot guarantee safety. The timer may already have expired,
its callback may already be running, or the callback may be waiting to acquire
`c.mu`. The generation check makes all of those stale callbacks harmless.
Ignoring the boolean returned by `Stop` is intentional because generation is
the authoritative check.

If a timer expiry and a new event happen simultaneously, either ordering is
safe:

- If the timer obtains `c.mu` first, it resets the old delay and then the new
  event installs the new delay.
- If the new event obtains `c.mu` first, it installs the new delay and advances
  the generation; the old callback later detects its stale generation and
  returns without resetting anything.

Generation changes only after the new `tc` command succeeds. If the new
command fails, the controller does not mark its rule as applied, does not
advance the generation, and does not cancel the previous rule's timer. The
previous impairment therefore retains its original expiration behavior. A
duplicate event also does not advance the generation or modify its timer.

An `until_next_event` rule does not create a timer, but it still advances the
generation and cancels any older duration timer. A rule with `delay_ms: 0` is
treated like any other successful new action, so it also invalidates older
callbacks.

If the automatic reset's own `tc` command fails, the controller logs the
failure and leaves the current delay active; it does not retry automatically.
The next configured rule can still replace or reset that state. Controller
restart clears the in-memory generations and applied-rule set because startup
recreates the qdisc with an initial `0ms` delay.

## Running and stopping

Start the experiment normally:

```bash
./run_project_linux.sh
```

The launcher authenticates `sudo`, builds and starts `netem_controller`, waits
for its Unix socket, and only then starts PBFT. The controller owns the initial
loopback qdisc, all changes, and cleanup. Stop the tmux experiment and remove
the controller-owned qdisc with:

```bash
./stop_project_linux.sh
```

Follow controller events and `tc` transitions with:

```bash
tail -f logs/netem_controller.log
```

## Manual acceptance check

After node 1 executes sequence 2, the log should contain an applied
`delay-after-seq-2` event. Confirm the active qdisc with:

```bash
tc qdisc show dev lo
```

It should report `delay 250ms`. Because every node-to-node loopback flow is
mapped to the shared `1:3` class, one controller command covers every PBFT
node-to-node path on this host. Client traffic is not included in those
filters.

The optional privileged integration test uses an isolated network namespace:

```bash
sudo env PBFT_NETEM_INTEGRATION=1 go test ./netem -run TestNetemControllerInNetworkNamespace
```
