package node

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	mrand "math/rand"
	"net"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// TestNewViewTimingBudget times every stage of a view change with the real code
// paths, to size the new-view timer for n = 4, 7, 10. The new-view timer starts when
// a node has 2f+1 ViewChanges and stops when it installs the NewView, so it has to
// cover: the primary's newview() + NewView transfer + the replica's HandleNewView,
// plus the ViewChanges the replica still verifies after its quorum (they hold its
// event loop). ViewChange build/transfer/verify are reported too, but happen before
// the timer starts.
//
// Every node of a run shares the P-set: certs prepared in view (1,1) with real
// prepare signatures, for seqs 1..certs above stable checkpoint 0. certs is
// CHECKPOINT_INTERVAL + window (max_inflight_seq): 290 = 250+40 today, 350 = 250+100,
// 500 = the high watermark cap. Nodes are built with NewNode and config/run2new.json,
// and marked dead so their own sends return at once; transport is timed separately
// over real loopback gRPC.
//
//	go test ./node -run TestNewViewTimingBudget -v -timeout 3600s
//	NVTIMING_NODES=7 NVTIMING_CERTS=290 NVTIMING_BATCH=50 ...   restrict the sweep
//	NVTIMING_CSV=/path/out.csv ...                              also write a CSV
//	NVTIMING_COLD=1 ...   verifiers hold none of the prepares, so verifyVC checks every
//	                      signature (the miss case of its already-verified skip)
//	taskset -c 0,2,4,6,8,10,12,14,28,30,32,34,36,38,40,42 go test ...   16 CPUs
func TestNewViewTimingBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-minute benchmark")
	}
	nodesSweep := envInts("NVTIMING_NODES", []int{4, 7, 10})
	certsSweep := envInts("NVTIMING_CERTS", []int{290, 350, 500})
	batchSweep := envInts("NVTIMING_BATCH", []int{10, 50, 100})
	reps := envInts("NVTIMING_REPS", []int{3})[0]

	var csv *os.File
	if path := os.Getenv("NVTIMING_CSV"); path != "" {
		var err error
		if csv, err = os.Create(path); err != nil {
			t.Fatal(err)
		}
		defer csv.Close()
		fmt.Fprintln(csv, nvTimingCSVHeader)
	}
	t.Logf("GOMAXPROCS=%d NumCPU=%d reps=%d (medians)", runtime.GOMAXPROCS(0), runtime.NumCPU(), reps)

	for _, nodes := range nodesSweep {
		for _, certs := range certsSweep {
			for _, batch := range batchSweep {
				r := runNVTiming(t, nodes, certs, batch, reps)
				t.Log(r.String())
				if csv != nil {
					fmt.Fprintln(csv, r.csv())
				}
			}
		}
	}
}

type nvTimingResult struct {
	nodes, f, certs, batch          int
	vcBytes, nvBytes                int
	vcBytesNoLists, nvBytesNoLists  int
	vcBuild                         time.Duration // VC(): P-set, marshal, sign
	vcSendPeer, vcFanOut, vcE2E     time.Duration // one sender to n-1 peers, until every peer's Deliver queued it
	verifyOne                       time.Duration // verifyVC of one VC, node alone
	verifyQuorumSolo                time.Duration // one node verifying the 2f others its quorum needs
	verifyQuorumConc, verifyAllConc time.Duration // all n nodes verifying at once: worst node to 2f, to n-1
	verifyQuorumConcMin             time.Duration // ... and the first node to reach 2f
	nvBuild                         time.Duration // primary newview()
	nvSendPeer, nvFanOut, nvE2E     time.Duration
	nvHandleSolo                    time.Duration // one replica's HandleNewView
	nvHandleConc                    time.Duration // all n-1 replicas at once, worst one
}

// timerNeed is the time from a replica's 2f+1-th ViewChange to its NewView install
// when every node is busy at once, in the worst ordering: this replica reaches its
// quorum first and the primary last (quorum skew), the replica's loop then still
// verifies its remaining ViewChanges (backlog), then the primary's newview(), the
// transfer, and the replica's HandleNewView.
func (r nvTimingResult) timerNeed() time.Duration {
	skew := r.verifyQuorumConc - r.verifyQuorumConcMin
	backlog := r.verifyAllConc - r.verifyQuorumConc
	return skew + backlog + r.nvBuild + r.nvE2E + r.nvHandleConc
}

const nvTimingCSVHeader = "nodes,f,certs,batch,vc_kib,nv_kib,vc_kib_nolists,nv_kib_nolists," +
	"vc_build_ms,vc_send_peer_ms,vc_fanout_ms,vc_e2e_ms,verify_one_ms,verify_quorum_solo_ms," +
	"verify_quorum_conc_min_ms,verify_quorum_conc_ms,verify_all_conc_ms,nv_build_ms,nv_send_peer_ms,nv_fanout_ms,nv_e2e_ms," +
	"nv_handle_solo_ms,nv_handle_conc_ms,timer_need_ms"

func ms(d time.Duration) string {
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 1, 64)
}

func kib(b int) string { return strconv.FormatFloat(float64(b)/1024, 'f', 1, 64) }

func (r nvTimingResult) csv() string {
	return strings.Join([]string{
		strconv.Itoa(r.nodes), strconv.Itoa(r.f), strconv.Itoa(r.certs), strconv.Itoa(r.batch),
		kib(r.vcBytes), kib(r.nvBytes), kib(r.vcBytesNoLists), kib(r.nvBytesNoLists),
		ms(r.vcBuild), ms(r.vcSendPeer), ms(r.vcFanOut), ms(r.vcE2E), ms(r.verifyOne), ms(r.verifyQuorumSolo),
		ms(r.verifyQuorumConcMin), ms(r.verifyQuorumConc), ms(r.verifyAllConc), ms(r.nvBuild), ms(r.nvSendPeer), ms(r.nvFanOut), ms(r.nvE2E),
		ms(r.nvHandleSolo), ms(r.nvHandleConc), ms(r.timerNeed()),
	}, ",")
}

func (r nvTimingResult) String() string {
	return fmt.Sprintf("n=%d certs=%d batch=%d | VC %s KiB (no lists %s) build %sms send/peer %sms fan-out %sms e2e %sms | verify 1 VC %sms, 2f solo %sms, conc 2f %s-%sms all %sms | "+
		"NV %s KiB (no lists %s) build %sms send/peer %sms fan-out %sms e2e %sms handle solo %sms conc %sms | timer need %sms",
		r.nodes, r.certs, r.batch, kib(r.vcBytes), kib(r.vcBytesNoLists), ms(r.vcBuild), ms(r.vcSendPeer), ms(r.vcFanOut), ms(r.vcE2E),
		ms(r.verifyOne), ms(r.verifyQuorumSolo), ms(r.verifyQuorumConcMin), ms(r.verifyQuorumConc), ms(r.verifyAllConc),
		kib(r.nvBytes), kib(r.nvBytesNoLists), ms(r.nvBuild), ms(r.nvSendPeer), ms(r.nvFanOut), ms(r.nvE2E),
		ms(r.nvHandleSolo), ms(r.nvHandleConc), ms(r.timerNeed()))
}

func envInts(name string, def []int) []int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	var out []int
	for _, s := range strings.Split(v, ",") {
		i, err := strconv.Atoi(strings.TrimSpace(s))
		if err == nil {
			out = append(out, i)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

func median(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

func minDur(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	m := ds[0]
	for _, d := range ds[1:] {
		if d < m {
			m = d
		}
	}
	return m
}

func maxDur(ds []time.Duration) time.Duration {
	var m time.Duration
	for _, d := range ds {
		if d > m {
			m = d
		}
	}
	return m
}

// ---------------------------------------------------------------------------
// cluster: keys, the shared P-set, and real nodes in the view-change state
// ---------------------------------------------------------------------------

type nvTimingCluster struct {
	nodes, f, certs, batch int
	cfg                    *config.Config
	privs                  map[int]ed25519.PrivateKey
	pubs                   map[int]ed25519.PublicKey
	pset                   map[int64]*core.PreparedCert
	oldView, newView       core.ViewID
}

func newNVTimingCluster(t *testing.T, nodes, certs, batch int) *nvTimingCluster {
	t.Helper()
	cfg := config.ReadCfg("../config/run2new.json")
	cfg.NodeNum = int64(nodes)
	cfg.NodesDead = nil
	cfg.ScenarioMode = false
	cfg.EpochMode = false

	c := &nvTimingCluster{
		nodes: nodes, f: (nodes - 1) / 3, certs: certs, batch: batch, cfg: cfg,
		privs: make(map[int]ed25519.PrivateKey), pubs: make(map[int]ed25519.PublicKey),
		pset:    make(map[int64]*core.PreparedCert, certs),
		oldView: core.ViewID{Generation: 1, Counter: 1},
		// primaryForView(nodes+1) == 1, so node 1 is the new primary
		newView: core.ViewID{Generation: 1, Counter: uint64(nodes + 1)},
	}
	for id := 1; id <= nodes; id++ {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		c.pubs[id], c.privs[id] = pub, priv
	}
	rng := mrand.New(mrand.NewSource(int64(nodes*100000 + certs*100 + batch)))
	for seq := int64(1); seq <= int64(certs); seq++ {
		mini := core.PreprepareMsgMini{View: c.oldView, SeqNum: seq, DigestClientMsg: fakeDigest(rng)}
		for i := 0; i < batch; i++ {
			mini.DigestIndividualClientMsgs = append(mini.DigestIndividualClientMsgs, fakeDigest(rng))
		}
		ppBytes, err := marshalDeterministic(preprepareSignPayload(mini.View, seq, mini.DigestClientMsg[:]))
		if err != nil {
			t.Fatal(err)
		}
		prepares := make(map[int]core.PrepareMsgSig, 2*c.f)
		for from := 2; from < 2+2*c.f; from++ { // the old view's primary (node 1) sends no prepare
			pm := core.PrepareMsg{View: c.oldView, SeqNum: seq, Digest: mini.DigestClientMsg, From: from}
			b, err := marshalDeterministic(transportpb.PrepareToPB(pm))
			if err != nil {
				t.Fatal(err)
			}
			prepares[from] = core.PrepareMsgSig{PrepareMsg: pm, Signature: crypto.SignMessageEd25519(b, c.privs[from])}
		}
		c.pset[seq] = &core.PreparedCert{
			PreprepareMsg: core.PreprepareMsgSig{PreprepareMsgMini: mini, Signature: crypto.SignMessageEd25519(ppBytes, c.privs[1])},
			PrepareLog:    prepares,
		}
	}
	return c
}

// node builds node id with NewNode, prepared at every seq of the P-set, in a view
// change from oldView to newView.
func (c *nvTimingCluster) node(t *testing.T, id int) *Node {
	t.Helper()
	// NewNode's key store looks for keys/*.pem and prints a line per missing file.
	stdout := os.Stdout
	if devnull, err := os.Open(os.DevNull); err == nil {
		os.Stdout = devnull
		defer func() { os.Stdout = stdout; devnull.Close() }()
	}
	n, err := NewNode(id, c.cfg)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = stdout
	n.encryptionKeyStore = &KeyStore{privateKey: c.privs[id], publicKeys: c.pubs}
	n.dead.Store(true)                                 // its own sends return at once; transport is timed separately
	n.messageHub.node_ref, n.messageHub.log = n, n.log // Start() would set these
	for seq, cert := range c.pset {
		slot, _ := n.consensusLog.GetorCreateEntry(seq)
		mini := cert.PreprepareMsg.PreprepareMsgMini
		slot.view = c.oldView
		slot.preprepare = &mini
		slot.preprepareSignature = cert.PreprepareMsg.Signature
		for from, p := range cert.PrepareLog {
			slot.prepares[from] = p
		}
		slot.prepareSent, slot.commitSent = true, true
		slot.recordPreparedIfHigher(c.oldView, cert)
	}
	n.viewID = c.oldView
	n.forViewID = c.newView
	n.viewChangeRunning = true
	return n
}

// withNodeAddrs gives config.NodeAddr one entry per node, so asyncBroadCast spawns
// its per-peer goroutines as in production (they return at once: nodes are dead).
func withNodeAddrs(t *testing.T, nodes int) {
	old := config.NodeAddr
	addrs := make(map[int]string, nodes)
	for i := 1; i <= nodes; i++ {
		addrs[i] = fmt.Sprintf("127.0.0.%d:1", i+1)
	}
	config.NodeAddr = addrs
	t.Cleanup(func() { config.NodeAddr = old })
}

func runNVTiming(t *testing.T, nodes, certs, batch, reps int) nvTimingResult {
	t.Helper()
	withNodeAddrs(t, nodes)
	c := newNVTimingCluster(t, nodes, certs, batch)
	r := nvTimingResult{nodes: nodes, f: c.f, certs: certs, batch: batch}
	quorumOthers := 2 * c.f // the node's own VC is the +1

	// --- ViewChanges: every node builds its own with VC() ---
	vcs := make(map[int]*core.ViewChangeMsgSig, nodes)
	var vcBuild []time.Duration
	for id := 1; id <= nodes; id++ {
		n := c.node(t, id)
		runtime.GC()
		start := time.Now()
		n.VC(c.newView, c.oldView, n.GetCurrAction())
		vcBuild = append(vcBuild, time.Since(start))
		vcs[id] = n.viewChangeMsgsLog[c.newView][0]
	}
	r.vcBuild = median(vcBuild)

	// --- ViewChange verification (verifyVC, as HandleViewChangeRoundRobin runs it) ---
	others := func(id int) []*core.ViewChangeMsgSig {
		out := make([]*core.ViewChangeMsgSig, 0, nodes-1)
		for j := 1; j <= nodes; j++ {
			if j != id {
				out = append(out, vcs[j])
			}
		}
		return out
	}
	verifiers := make([]*Node, nodes+1)
	for id := 1; id <= nodes; id++ {
		verifiers[id] = c.node(t, id)
		if os.Getenv("NVTIMING_COLD") == "1" {
			for seq := range c.pset {
				slot, _ := verifiers[id].consensusLog.GetLogEntry(seq)
				slot.prepares = make(map[int]core.PrepareMsgSig)
				slot.preparedProof, slot.preparedView = nil, core.ViewID{}
			}
		}
	}
	var verifyOne, verifyQuorumSolo, verifyQuorumConc, verifyQuorumConcMin, verifyAllConc []time.Duration
	for rep := 0; rep < reps; rep++ {
		v := verifiers[2]
		runtime.GC()
		start := time.Now()
		for i, vc := range others(2) {
			t0 := time.Now()
			if !v.verifyVC(vc.ViewChangeMsg) {
				t.Fatalf("view change from %d failed verification", vc.ViewChangeMsg.From)
			}
			if i == 0 {
				verifyOne = append(verifyOne, time.Since(t0))
			}
			if i == quorumOthers-1 {
				verifyQuorumSolo = append(verifyQuorumSolo, time.Since(start))
			}
		}

		toQuorum := make([]time.Duration, nodes+1)
		toAll := make([]time.Duration, nodes+1)
		var wg sync.WaitGroup
		runtime.GC()
		begin := make(chan struct{})
		for id := 1; id <= nodes; id++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				<-begin
				start := time.Now()
				for i, vc := range others(id) {
					verifiers[id].verifyVC(vc.ViewChangeMsg)
					if i == quorumOthers-1 {
						toQuorum[id] = time.Since(start)
					}
				}
				toAll[id] = time.Since(start)
			}(id)
		}
		close(begin)
		wg.Wait()
		verifyQuorumConc = append(verifyQuorumConc, maxDur(toQuorum))
		verifyQuorumConcMin = append(verifyQuorumConcMin, minDur(toQuorum[1:]))
		verifyAllConc = append(verifyAllConc, maxDur(toAll))
	}
	r.verifyOne, r.verifyQuorumSolo = median(verifyOne), median(verifyQuorumSolo)
	r.verifyQuorumConc, r.verifyAllConc = median(verifyQuorumConc), median(verifyAllConc)
	r.verifyQuorumConcMin = median(verifyQuorumConcMin)

	// The primary's 2f+1: its own VC first (VC() appends it), then 2f others.
	primaryLog := []*core.ViewChangeMsgSig{vcs[1]}
	for id := 2; len(primaryLog) < 2*c.f+1; id++ {
		primaryLog = append(primaryLog, vcs[id])
	}

	// --- primary: newview() ---
	var nvBuild []time.Duration
	for rep := 0; rep < reps; rep++ {
		p := c.node(t, 1)
		p.viewChangeMsgsLog[c.newView] = append([]*core.ViewChangeMsgSig(nil), primaryLog...)
		runtime.GC()
		start := time.Now()
		p.newview()
		nvBuild = append(nvBuild, time.Since(start))
		if !p.GetViewID().Equal(c.newView) {
			t.Fatalf("primary did not install %v", c.newView)
		}
	}
	r.nvBuild = median(nvBuild)

	// The NewView newview() broadcasts, rebuilt the same way for transfer and replicas.
	q := c.node(t, 1)
	O, _, _, _, _ := q.createO(primaryLog, c.newView, c.oldView)
	nv := core.NewViewMsg{
		NewViewNumber:  c.newView,
		Action:         q.GetCurrAction(),
		From:           1,
		PreprepareLog:  O,
		ViewChangeRefs: q.recordNewViewSent(c.newView, primaryLog),
		Throughput:     c.cfg.PerfDefaultMaxThroughput(),
	}
	nvBytes, err := marshalDeterministic(transportpb.NewViewToPB(nv))
	if err != nil {
		t.Fatal(err)
	}
	nvSig := crypto.SignMessageEd25519(nvBytes, c.privs[1])

	// --- sizes, now and with the per-txn lists dropped (rest of Phase 1) ---
	vcEnv := &transportpb.Envelope{MsgType: core.MsgViewChangeMessage, From: 1, Signature: vcs[1].Signature,
		Body: &transportpb.Envelope_ViewChange{ViewChange: transportpb.ViewChangeToPB(vcs[1].ViewChangeMsg)}}
	nvEnv := &transportpb.Envelope{MsgType: core.MsgNewViewMessage, From: 1, Signature: nvSig,
		Body: &transportpb.Envelope_NewView{NewView: transportpb.NewViewToPB(nv)}}
	r.vcBytes, r.nvBytes = proto.Size(vcEnv), proto.Size(nvEnv)
	for _, pc := range vcEnv.GetViewChange().PreparedCerts {
		pc.PreprepareMsg.PreprepareMsgMini.DigestIndividualClientMsgs = nil
	}
	for _, pp := range nvEnv.GetNewView().PreprepareLog {
		pp.PreprepareMsgMini.DigestIndividualClientMsgs = nil
	}
	r.vcBytesNoLists, r.nvBytesNoLists = proto.Size(vcEnv), proto.Size(nvEnv)

	// --- transfer over loopback gRPC: one sender to n-1 receivers running Deliver ---
	peers := startTimingPeers(t, c, nodes-1)
	defer func() {
		for _, p := range peers {
			p.stop()
		}
	}()
	tune := defaultGRPCTuning()
	streams := make([]transportpb.PBFTTransport_ClientNodeChannelClient, len(peers))
	for i, p := range peers {
		conn, stream := dialBenchPeer(t, p.addr, tune)
		defer conn.Close()
		streams[i] = stream
	}
	senderHub := &NodeMessageHub{node_ref: &Node{NodeID: 1}}
	var vcPeer, vcFan, vcE2E, nvPeer, nvFan, nvE2E []time.Duration
	for rep := 0; rep < reps; rep++ {
		runtime.GC()
		p, f, e := timedFanOut(t, senderHub, core.MsgViewChangeMessage, vcs[1].ViewChangeMsg, vcs[1].Signature, peers, streams)
		vcPeer, vcFan, vcE2E = append(vcPeer, p), append(vcFan, f), append(vcE2E, e)
		runtime.GC()
		p, f, e = timedFanOut(t, senderHub, core.MsgNewViewMessage, nv, nvSig, peers, streams)
		nvPeer, nvFan, nvE2E = append(nvPeer, p), append(nvFan, f), append(nvE2E, e)
	}
	r.vcSendPeer, r.vcFanOut, r.vcE2E = median(vcPeer), median(vcFan), median(vcE2E)
	r.nvSendPeer, r.nvFanOut, r.nvE2E = median(nvPeer), median(nvFan), median(nvE2E)

	// --- replicas: HandleNewView, one alone and all n-1 at once ---
	replica := func(id int) *Node {
		n := c.node(t, id)
		log := []*core.ViewChangeMsgSig{vcs[id]}
		for j := 1; j <= nodes; j++ {
			if j != id {
				log = append(log, vcs[j])
			}
		}
		n.viewChangeMsgsLog[c.newView] = log
		return n
	}
	var handleSolo, handleConc []time.Duration
	for rep := 0; rep < reps; rep++ {
		n := replica(2)
		runtime.GC()
		start := time.Now()
		n.HandleNewView(nv, nvSig)
		handleSolo = append(handleSolo, time.Since(start))
		if !n.GetViewID().Equal(c.newView) {
			t.Fatalf("replica did not install %v", c.newView)
		}

		rs := make([]*Node, 0, nodes-1)
		for id := 2; id <= nodes; id++ {
			rs = append(rs, replica(id))
		}
		took := make([]time.Duration, len(rs))
		var wg sync.WaitGroup
		begin := make(chan struct{})
		runtime.GC()
		for i, rn := range rs {
			wg.Add(1)
			go func(i int, rn *Node) {
				defer wg.Done()
				<-begin
				start := time.Now()
				rn.HandleNewView(nv, nvSig)
				took[i] = time.Since(start)
			}(i, rn)
		}
		close(begin)
		wg.Wait()
		for _, rn := range rs {
			if !rn.GetViewID().Equal(c.newView) {
				t.Fatalf("replica %d did not install %v", rn.NodeID, c.newView)
			}
		}
		handleConc = append(handleConc, maxDur(took))
	}
	r.nvHandleSolo, r.nvHandleConc = median(handleSolo), median(handleConc)
	return r
}

// ---------------------------------------------------------------------------
// loopback gRPC receivers running the real Deliver
// ---------------------------------------------------------------------------

type timingPeer struct {
	addr     string
	srv      *grpc.Server
	listener net.Listener
	received chan time.Time
}

func (p *timingPeer) stop() {
	p.srv.Stop()
	_ = p.listener.Close()
}

// startTimingPeers starts receivers 2..count+1 with production gRPC options. Each
// records when Deliver has verified, decoded and queued a ViewChange or NewView.
func startTimingPeers(t *testing.T, c *nvTimingCluster, count int) []*timingPeer {
	t.Helper()
	peers := make([]*timingPeer, 0, count)
	for i := 0; i < count; i++ {
		id := i + 2
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		node := &Node{
			NodeID:             id,
			log:                logger.NewLogger(id, "node"),
			cfg:                c.cfg,
			encryptionKeyStore: &KeyStore{publicKeys: c.pubs},
			viewChangeMsgChan:  make(chan ViewChangeMsg, 64),
			newViewMsgChan:     make(chan NewViewMsg, 64),
		}
		hub := &NodeMessageHub{node_ref: node, log: node.log}
		srv := grpc.NewServer(
			grpc.InitialWindowSize(grpcFlowControlWindowSize),
			grpc.InitialConnWindowSize(grpcFlowControlWindowSize),
			grpc.MaxRecvMsgSize(maxGRPCMsgBytes),
			grpc.MaxSendMsgSize(maxGRPCMsgBytes),
		)
		transportpb.RegisterPBFTTransportServer(srv, hub)
		p := &timingPeer{addr: lis.Addr().String(), srv: srv, listener: lis, received: make(chan time.Time, 64)}
		go func() { _ = srv.Serve(lis) }()
		go func() {
			for {
				select {
				case <-node.viewChangeMsgChan:
				case <-node.newViewMsgChan:
				}
				p.received <- time.Now()
			}
		}()
		peers = append(peers, p)
	}
	return peers
}

// timedFanOut sends msg to every peer the way asyncBroadCast does (a goroutine per
// peer, each building its own envelope) and returns the mean per-peer stream.Send,
// the fan-out wall time, and the time until every peer's Deliver queued it.
func timedFanOut(t *testing.T, hub *NodeMessageHub, msgType string, msg interface{}, sig []byte,
	peers []*timingPeer, streams []transportpb.PBFTTransport_ClientNodeChannelClient) (time.Duration, time.Duration, time.Duration) {
	t.Helper()
	perPeer := make([]time.Duration, len(peers))
	var wg sync.WaitGroup
	start := time.Now()
	for i := range peers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			env, err := hub.buildEnvelope(msgType, msg, sig)
			if err != nil {
				t.Errorf("buildEnvelope: %v", err)
				return
			}
			t0 := time.Now()
			if err := streams[i].Send(env); err != nil {
				t.Errorf("send: %v", err)
				return
			}
			perPeer[i] = time.Since(t0)
		}(i)
	}
	wg.Wait()
	fanOut := time.Since(start)
	for _, p := range peers {
		select {
		case <-p.received:
		case <-time.After(30 * time.Second):
			t.Fatalf("peer %s never delivered %s", p.addr, msgType)
		}
	}
	e2e := time.Since(start)
	var sum time.Duration
	for _, d := range perPeer {
		sum += d
	}
	return sum / time.Duration(len(perPeer)), fanOut, e2e
}
