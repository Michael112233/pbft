package node

import (
	"encoding/binary"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
)

// fillPool adds `slots` batches of `batch` requests at seqs 1..slots, like a pool that
// has accumulated a full watermark window before the checkpoint at seq 250 goes stable.
func fillPool(slots, batch int) *Pool {
	p := NewPool(nil)
	view := core.ViewID{Generation: 1, Counter: 1}
	padding := strings.Repeat("x", 100)
	var id uint64
	for seq := 1; seq <= slots; seq++ {
		reqs := make([]core.ClientMsgSignature, batch)
		digests := make([][32]byte, batch)
		for i := range reqs {
			reqs[i] = core.ClientMsgSignature{
				Data:      core.ClientMsg{Id: int64(id), ClientName: "client", Padding: padding},
				Signature: make([]byte, 64),
			}
			binary.BigEndian.PutUint64(digests[i][:], id)
			id++
		}
		p.AddBatch(reqs, digests, int64(seq), view)
	}
	return p
}

// TestPoolGCMeasure times one GCUpTo(CHECKPOINT_INTERVAL) call over a pool holding 250 or
// 500 slots (the low end and the full 2*CHECKPOINT_INTERVAL watermark window) at several
// batch sizes. Run: go test ./node -run TestPoolGCMeasure -v -count=1
func TestPoolGCMeasure(t *testing.T) {
	const runs = 30
	t.Logf("%-6s %-6s %10s %10s %10s %12s %10s", "slots", "batch", "scanned", "removed", "remain", "median", "ns/entry")
	for _, slots := range []int{250, 500} {
		for _, batch := range []int{30, 50, 100} {
			times := make([]time.Duration, 0, runs)
			var scanned, removed, remain int
			for r := 0; r < runs; r++ {
				p := fillPool(slots, batch)
				scanned = p.Len()
				start := time.Now()
				removed = p.GCUpTo(CHECKPOINT_INTERVAL)
				times = append(times, time.Since(start))
				remain = p.Len()
			}
			sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
			med := times[runs/2]
			t.Logf("%-6d %-6d %10d %10d %10d %12v %10.1f", slots, batch, scanned, removed, remain, med, float64(med.Nanoseconds())/float64(scanned))
		}
	}
}

func BenchmarkPoolGCUpTo(b *testing.B) {
	for _, slots := range []int{250, 500} {
		for _, batch := range []int{30, 50, 100} {
			b.Run(strings.Join([]string{"slots", itoa(slots), "batch", itoa(batch)}, "_"), func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					p := fillPool(slots, batch)
					b.StartTimer()
					p.GCUpTo(CHECKPOINT_INTERVAL)
				}
			})
		}
	}
}

func itoa(n int) string {
	var buf [20]byte
	i := len(buf)
	for {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return string(buf[i:])
}
