package node

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"math/big"
	mrand "math/rand"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

// This file is a measurement harness for the NewView / ViewChange hot path. It
// builds synthetic messages with tunable shape, then times each stage of
//
//	NewViewToPB -> marshalDeterministic -> SignMessageEd25519 -> buildEnvelope
//	-> proto.Size -> stream.Send
//
// so you can see which stage actually costs the ~40ms observed in production
// when a NewView reaches ~4.3 MiB.
//
// Run:
//
//	go test ./node -run TestNewViewCostBreakdown   -v
//	go test ./node -run TestViewChangeCostBreakdown -v
//	go test ./node -run TestNewViewGRPCBroadcast    -v
//	go test ./node -run TestNewViewSharedEnvelopeWin -v
//
// Nothing here touches production code paths, so it is safe to iterate on.

// ---------------------------------------------------------------------------
// knobs
// ---------------------------------------------------------------------------

// dummyParams controls the shape (and therefore the size) of the synthetic
// NewView / ViewChange messages.
type dummyParams struct {
	// numPreparedCerts is the number of entries in the O-set (NewView
	// PreprepareLog) and in each ViewChange P-set. This is the knob that blew
	// up in production: 253 instead of ~3.
	numPreparedCerts int

	// txnsPerBatch is how many client transactions ride inside one batch,
	// matching config max_batch_size.
	txnsPerBatch int

	// paddingBytes is per-client-message padding, matching config
	// client_msg_padding_bytes.
	paddingBytes int

	// includeActualMsgOSet controls whether NewView.PreprepareLog entries carry
	// their full ActualMsg batch payload.
	includeActualMsgOSet bool

	// includeActualMsgVCCerts controls whether the prepared certs embedded in
	// each ViewChange carry their full ActualMsg batch payload.
	includeActualMsgVCCerts bool

	// dropIndividualDigests removes PreprepareMsgMini.DigestIndividualClientMsgs
	// (txnsPerBatch * 32 bytes per preprepare). Set true to see what a prepared
	// cert costs with nothing but the batch digest + signatures.
	dropIndividualDigests bool

	// prepareVotesPerCert is the number of Prepare signatures per prepared
	// cert (2f+1 = 3 for n=4).
	prepareVotesPerCert int

	// numViewChangeMsgs is how many ViewChange messages the NewView bundles
	// (2f+1 = 3 for n=4).
	numViewChangeMsgs int

	// checkpointAccounts is how many account balances each ViewChange carries
	// in CheckpointBalances. 0 disables it. Production config has
	// dummy_account_count: 100000 with carry_state: true.
	checkpointAccounts int
}

func defaultDummyParams() dummyParams {
	return dummyParams{
		numPreparedCerts:        253, // observed in the failing view-8 log
		txnsPerBatch:            30,  // config max_batch_size
		paddingBytes:            0,   // config client_msg_padding_bytes
		includeActualMsgOSet:    true,
		includeActualMsgVCCerts: true,
		prepareVotesPerCert:     3, // 2f+1
		numViewChangeMsgs:       3, // 2f+1
		checkpointAccounts:      0,
	}
}

func (p dummyParams) String() string {
	return fmt.Sprintf(
		"certs=%d txns=%d pad=%dB oSetActual=%t vcActual=%t dropIndivDigests=%t votes=%d vcs=%d accts=%d",
		p.numPreparedCerts, p.txnsPerBatch, p.paddingBytes,
		p.includeActualMsgOSet, p.includeActualMsgVCCerts, p.dropIndividualDigests,
		p.prepareVotesPerCert, p.numViewChangeMsgs, p.checkpointAccounts,
	)
}

// grpcTuning collects every gRPC option worth sweeping. Change these and re-run
// TestNewViewGRPCBroadcast to see the effect on send latency.
type grpcTuning struct {
	writeBufferSize       int   // gRPC default is 32 KiB
	readBufferSize        int   // gRPC default is 32 KiB
	initialWindowSize     int32 // production uses 8 MiB
	initialConnWindowSize int32 // production uses 8 MiB
	maxMsgBytes           int
	useGzip               bool

	// sharedEnvelope builds the protobuf envelope once and reuses it for every
	// peer, instead of the production behaviour of rebuilding it inside each
	// per-peer goroutine.
	sharedEnvelope bool

	// usePreparedMsg encodes the message once per stream with grpc.PreparedMsg
	// so SendMsg does not re-marshal.
	usePreparedMsg bool
}

func defaultGRPCTuning() grpcTuning {
	return grpcTuning{
		writeBufferSize:       32 * 1024, // gRPC default
		readBufferSize:        32 * 1024, // gRPC default
		initialWindowSize:     grpcFlowControlWindowSize,
		initialConnWindowSize: grpcFlowControlWindowSize,
		maxMsgBytes:           maxGRPCMsgBytes,
		useGzip:               false,
		sharedEnvelope:        false,
		usePreparedMsg:        false,
	}
}

// ---------------------------------------------------------------------------
// builders
// ---------------------------------------------------------------------------

// fakeSig returns a 64-byte blob the same size as a real Ed25519 signature.
// Inner signatures are never verified by the receiver (only the outer envelope
// signature is), so a synthetic blob is size-equivalent and keeps the builder
// fast.
func fakeSig(rng *mrand.Rand) []byte {
	sig := make([]byte, ed25519.SignatureSize)
	rng.Read(sig)
	return sig
}

func fakeDigest(rng *mrand.Rand) [32]byte {
	var d [32]byte
	rng.Read(d[:])
	return d
}

func buildDummyClientMsgs(rng *mrand.Rand, count, paddingBytes int) []core.ClientMsgSignature {
	if count <= 0 {
		return nil
	}
	padding := ""
	if paddingBytes > 0 {
		buf := make([]byte, paddingBytes)
		for i := range buf {
			buf[i] = 'x'
		}
		padding = string(buf)
	}

	msgs := make([]core.ClientMsgSignature, 0, count)
	now := time.Now()
	for i := 0; i < count; i++ {
		msgs = append(msgs, core.ClientMsgSignature{
			Data: core.ClientMsg{
				Id:        int64(i),
				Timestamp: now,
				Txn: core.Transaction{
					Sender:   fmt.Sprintf("account_%06d", rng.Intn(100000)),
					Receiver: fmt.Sprintf("account_%06d", rng.Intn(100000)),
					Amount:   big.NewInt(int64(rng.Intn(1000) + 1)),
				},
				ClientName: "client_1",
				Padding:    padding,
			},
			Signature: fakeSig(rng),
		})
	}
	return msgs
}

func buildDummyPreprepareSig(rng *mrand.Rand, view, seq int64, p dummyParams, withActual bool) core.PreprepareMsgSig {
	var digests [][32]byte
	if !p.dropIndividualDigests {
		digests = make([][32]byte, 0, p.txnsPerBatch)
		for i := 0; i < p.txnsPerBatch; i++ {
			digests = append(digests, fakeDigest(rng))
		}
	}

	sig := core.PreprepareMsgSig{
		PreprepareMsgMini: core.PreprepareMsgMini{
			View:                       view,
			SeqNum:                     seq,
			DigestClientMsg:            fakeDigest(rng),
			DigestIndividualClientMsgs: digests,
		},
		Signature: fakeSig(rng),
	}
	if withActual {
		sig.ActualMsg = buildDummyClientMsgs(rng, p.txnsPerBatch, p.paddingBytes)
	}
	return sig
}

func buildDummyPreparedCert(rng *mrand.Rand, view, seq int64, p dummyParams, withActual bool) *core.PreparedCert {
	prepareLog := make(map[int]core.PrepareMsgSig, p.prepareVotesPerCert)
	digest := fakeDigest(rng)
	for from := 1; from <= p.prepareVotesPerCert; from++ {
		prepareLog[from] = core.PrepareMsgSig{
			PrepareMsg: core.PrepareMsg{
				View:   view,
				SeqNum: seq,
				Digest: digest,
				From:   from,
			},
			Signature: fakeSig(rng),
		}
	}
	return &core.PreparedCert{
		PreprepareMsg: buildDummyPreprepareSig(rng, view, seq, p, withActual),
		PrepareLog:    prepareLog,
	}
}

func buildDummyCheckpointBalances(count int) map[string]*big.Int {
	if count <= 0 {
		return nil
	}
	balances := make(map[string]*big.Int, count)
	for i := 0; i < count; i++ {
		balances[fmt.Sprintf("account_%06d", i)] = big.NewInt(int64(1000 + i))
	}
	return balances
}

// buildDummyViewChange builds one ViewChange whose P-set has p.numPreparedCerts
// entries, mirroring createVCContent when the stable checkpoint is a full
// interval behind lastExecuted.
func buildDummyViewChange(rng *mrand.Rand, from int, view, checkpointSeq int64, p dummyParams) core.ViewChangeMsg {
	preparedCerts := make(map[int64]*core.PreparedCert, p.numPreparedCerts)
	for i := 0; i < p.numPreparedCerts; i++ {
		seq := checkpointSeq + int64(i) + 1
		preparedCerts[seq] = buildDummyPreparedCert(rng, view-1, seq, p, p.includeActualMsgVCCerts)
	}

	checkpointProof := make([]core.CheckpointMsgSig, 0, p.prepareVotesPerCert)
	checkpointDigest := fakeDigest(rng)
	for node := 1; node <= p.prepareVotesPerCert; node++ {
		checkpointProof = append(checkpointProof, core.CheckpointMsgSig{
			CheckpointMsg: core.CheckpointMsg{
				SeqNum: checkpointSeq,
				Digest: checkpointDigest,
				From:   node,
			},
			Signature: fakeSig(rng),
		})
	}

	return core.ViewChangeMsg{
		ViewNumber:          view,
		CheckpointSeqNumber: checkpointSeq,
		CheckpointDigest:    checkpointDigest,
		CheckpointProof:     checkpointProof,
		CheckpointBalances:  buildDummyCheckpointBalances(p.checkpointAccounts),
		From:                from,
		PreparedCerts:       preparedCerts,
		Type:                core.VCTypeRoundRobin,
		RoundRobinData:      &core.RoundRobinVCData{GrantVote: false},
	}
}

// buildDummyNewView mirrors the message assembled in newview(): an O-set of
// re-proposed preprepares plus the full 2f+1 ViewChangeLog.
func buildDummyNewView(rng *mrand.Rand, from int, view, checkpointSeq int64, p dummyParams) core.NewViewMsg {
	oSet := make([]core.PreprepareMsgSig, 0, p.numPreparedCerts)
	for i := 0; i < p.numPreparedCerts; i++ {
		seq := checkpointSeq + int64(i) + 1
		oSet = append(oSet, buildDummyPreprepareSig(rng, view, seq, p, p.includeActualMsgOSet))
	}

	vcLog := make([]*core.ViewChangeMsgSig, 0, p.numViewChangeMsgs)
	for i := 0; i < p.numViewChangeMsgs; i++ {
		vc := buildDummyViewChange(rng, i+1, view, checkpointSeq, p)
		vcLog = append(vcLog, &core.ViewChangeMsgSig{
			ViewChangeMsg: vc,
			Signature:     fakeSig(rng),
		})
	}

	return core.NewViewMsg{
		NewViewNumber: view,
		From:          from,
		PreprepareLog: oSet,
		ViewChangeLog: vcLog,
		Throughput:    270.99,
	}
}

// ---------------------------------------------------------------------------
// timing helpers
// ---------------------------------------------------------------------------

type stat struct {
	name string
	mean time.Duration
	best time.Duration
}

func timeIt(name string, iters int, fn func()) stat {
	if iters < 1 {
		iters = 1
	}
	fn() // warm up allocator / caches, not counted
	var total time.Duration
	best := time.Duration(1<<63 - 1)
	for i := 0; i < iters; i++ {
		start := time.Now()
		fn()
		elapsed := time.Since(start)
		total += elapsed
		if elapsed < best {
			best = elapsed
		}
	}
	return stat{name: name, mean: total / time.Duration(iters), best: best}
}

func reportStats(t *testing.T, header string, sizeBytes int, stats []stat) {
	t.Helper()
	var total time.Duration
	for _, s := range stats {
		total += s.mean
	}
	t.Logf("--- %s", header)
	t.Logf("    wire size: %d bytes (%.3f MiB)", sizeBytes, float64(sizeBytes)/(1024*1024))
	for _, s := range stats {
		pct := 0.0
		if total > 0 {
			pct = 100 * float64(s.mean) / float64(total)
		}
		t.Logf("    %-28s mean=%-12s best=%-12s (%5.1f%%)", s.name, s.mean, s.best, pct)
	}
	t.Logf("    %-28s %s", "TOTAL (single peer)", total)
}

// ---------------------------------------------------------------------------
// stage-by-stage cost breakdown
// ---------------------------------------------------------------------------

func newViewCostCases() []struct {
	name   string
	params dummyParams
} {
	full := defaultDummyParams()

	noActual := defaultDummyParams()
	noActual.includeActualMsgOSet = false
	noActual.includeActualMsgVCCerts = false

	// no ActualMsg AND no per-txn digest list: a prepared cert stripped to just
	// {preprepare mini header + sig} + {2f+1 prepare {header + sig}}. This is
	// close to the textbook-PBFT P-set entry, which carries digests only.
	bareCerts := noActual
	bareCerts.dropIndividualDigests = true

	// same, but with only one ViewChange in the log instead of 2f+1. Not
	// protocol-legal (PBFT needs 2f+1), included only to show how much of the
	// no-actual message is the 3x-replicated ViewChangeLog vs the O-set.
	bareCertsOneVC := bareCerts
	bareCertsOneVC.numViewChangeMsgs = 1

	small := defaultDummyParams()
	small.numPreparedCerts = 3 // what a healthy view change carries

	padded := defaultDummyParams()
	padded.paddingBytes = 256

	balances := defaultDummyParams()
	balances.numPreparedCerts = 3
	balances.checkpointAccounts = 100000 // config dummy_account_count

	// A crash-triggered view change lands at an arbitrary point between two
	// stable checkpoints, so a P-set of a full CHECKPOINT_INTERVAL is ordinary,
	// not pathological. The hard ceiling is the high watermark:
	// GCLog sets high = low + 2*CHECKPOINT_INTERVAL - 1, so up to 500 prepared
	// certs is protocol-legal and MUST be survivable.
	worstCase := defaultDummyParams()
	worstCase.numPreparedCerts = 2 * CHECKPOINT_INTERVAL

	return []struct {
		name   string
		params dummyParams
	}{
		{"best-case-3-certs", small},
		{"typical-253-certs", full},
		{"worst-case-500-certs", worstCase},
		{"253-no-actualmsg", noActual},
		{"253-bare-certs", bareCerts},
		{"253-bare-certs-1vc", bareCertsOneVC},
		{"253-padding-256B", padded},
		{"3-certs-100k-balances", balances},
	}
}

// TestNewViewCostBreakdown times every stage newview() runs between building
// the message and handing it to the transport.
func TestNewViewCostBreakdown(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	hub := &NodeMessageHub{node_ref: &Node{NodeID: 4}}

	const iters = 5

	for _, tc := range newViewCostCases() {
		t.Run(tc.name, func(t *testing.T) {
			rng := mrand.New(mrand.NewSource(1))
			msg := buildDummyNewView(rng, 4, 8, 13500, tc.params)

			var (
				pbMsg        *transportpb.NewViewMsg
				payloadBytes []byte
				signature    []byte
				env          *transportpb.Envelope
				envBytes     []byte
				sizeBytes    int
			)

			stats := []stat{
				timeIt("NewViewToPB", iters, func() {
					pbMsg = transportpb.NewViewToPB(msg)
				}),
				timeIt("marshalDeterministic(sign)", iters, func() {
					payloadBytes, err = marshalDeterministic(pbMsg)
					if err != nil {
						t.Fatalf("marshal: %v", err)
					}
				}),
				timeIt("SignMessageEd25519", iters, func() {
					signature = crypto.SignMessageEd25519(payloadBytes, privateKey)
				}),
				timeIt("buildEnvelope (per peer)", iters, func() {
					env, err = hub.buildEnvelope(core.MsgNewViewMessage, msg, signature)
					if err != nil {
						t.Fatalf("buildEnvelope: %v", err)
					}
				}),
				timeIt("proto.Size (logging only)", iters, func() {
					sizeBytes = proto.Size(env)
				}),
				timeIt("proto.Marshal (stream.Send)", iters, func() {
					envBytes, err = proto.Marshal(env)
					if err != nil {
						t.Fatalf("marshal envelope: %v", err)
					}
				}),
			}

			t.Logf("params: %s", tc.params)
			reportStats(t, "NewView pipeline", sizeBytes, stats)
			t.Logf("    signing payload: %d bytes, envelope: %d bytes", len(payloadBytes), len(envBytes))
			t.Logf("    NOTE: buildEnvelope + proto.Size + proto.Marshal run ONCE PER PEER (x3 in a 4-node run)")
		})
	}
}

// TestViewChangeCostBreakdown does the same for the ViewChange message built in
// VC().
func TestViewChangeCostBreakdown(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	hub := &NodeMessageHub{node_ref: &Node{NodeID: 4}}

	const iters = 5

	for _, tc := range newViewCostCases() {
		t.Run(tc.name, func(t *testing.T) {
			rng := mrand.New(mrand.NewSource(1))
			msg := buildDummyViewChange(rng, 4, 8, 13500, tc.params)

			var (
				pbMsg        *transportpb.ViewChangeMsg
				payloadBytes []byte
				signature    []byte
				env          *transportpb.Envelope
				sizeBytes    int
			)

			stats := []stat{
				timeIt("ViewChangeToPB", iters, func() {
					pbMsg = transportpb.ViewChangeToPB(msg)
				}),
				timeIt("marshalDeterministic(sign)", iters, func() {
					payloadBytes, err = marshalDeterministic(pbMsg)
					if err != nil {
						t.Fatalf("marshal: %v", err)
					}
				}),
				timeIt("SignMessageEd25519", iters, func() {
					signature = crypto.SignMessageEd25519(payloadBytes, privateKey)
				}),
				timeIt("buildEnvelope (per peer)", iters, func() {
					env, err = hub.buildEnvelope(core.MsgViewChangeMessage, msg, signature)
					if err != nil {
						t.Fatalf("buildEnvelope: %v", err)
					}
				}),
				timeIt("proto.Size (logging only)", iters, func() {
					sizeBytes = proto.Size(env)
				}),
				timeIt("proto.Marshal (stream.Send)", iters, func() {
					if _, err := proto.Marshal(env); err != nil {
						t.Fatalf("marshal envelope: %v", err)
					}
				}),
			}

			t.Logf("params: %s", tc.params)
			reportStats(t, "ViewChange pipeline", sizeBytes, stats)
		})
	}
}

// ---------------------------------------------------------------------------
// gRPC broadcast harness
// ---------------------------------------------------------------------------

type benchPeer struct {
	addr     string
	node     *Node
	hub      *NodeMessageHub
	srv      *grpc.Server
	listener net.Listener
	received chan time.Time
}

func (p *benchPeer) stop() {
	p.srv.Stop()
	_ = p.listener.Close()
}

// startBenchPeers spins up n receiver hubs on real 127.0.0.1 TCP listeners with
// the given tuning applied to the gRPC server. Each peer drains its
// newViewMsgChan and records the receipt timestamp.
func startBenchPeers(t *testing.T, n int, senderPub ed25519.PublicKey, tune grpcTuning) []*benchPeer {
	t.Helper()

	peers := make([]*benchPeer, 0, n)
	for i := 0; i < n; i++ {
		nodeID := i + 2 // sender is node 1
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}

		node := &Node{
			NodeID:             nodeID,
			log:                logger.NewLogger(nodeID, "node"),
			cfg:                &config.Config{},
			encryptionKeyStore: &KeyStore{publicKeys: map[int]ed25519.PublicKey{1: senderPub}},
			consensusMsgChan:   make(chan ConsensusMsg, 1024),
			viewChangeMsgChan:  make(chan ViewChangeMsg, 1024),
			checkpointMsgChan:  make(chan CheckpointMsg, 1024),
			newViewMsgChan:     make(chan NewViewMsg, 1024),
			electionMsgChan:    make(chan ElectionMsg, 1024),
		}
		hub := &NodeMessageHub{node_ref: node, log: node.log}

		srv := grpc.NewServer(
			grpc.InitialWindowSize(tune.initialWindowSize),
			grpc.InitialConnWindowSize(tune.initialConnWindowSize),
			grpc.MaxRecvMsgSize(tune.maxMsgBytes),
			grpc.MaxSendMsgSize(tune.maxMsgBytes),
			grpc.WriteBufferSize(tune.writeBufferSize),
			grpc.ReadBufferSize(tune.readBufferSize),
		)
		transportpb.RegisterPBFTTransportServer(srv, hub)

		peer := &benchPeer{
			addr:     lis.Addr().String(),
			node:     node,
			hub:      hub,
			srv:      srv,
			listener: lis,
			received: make(chan time.Time, 1024),
		}

		go func() { _ = srv.Serve(lis) }()
		go func() {
			for range node.newViewMsgChan {
				peer.received <- time.Now()
			}
		}()

		peers = append(peers, peer)
	}
	return peers
}

func dialBenchPeer(t *testing.T, addr string, tune grpcTuning) (*grpc.ClientConn, transportpb.PBFTTransport_ClientNodeChannelClient) {
	t.Helper()

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithInitialWindowSize(tune.initialWindowSize),
		grpc.WithInitialConnWindowSize(tune.initialConnWindowSize),
		grpc.WithWriteBufferSize(tune.writeBufferSize),
		grpc.WithReadBufferSize(tune.readBufferSize),
	}
	callOpts := []grpc.CallOption{
		grpc.MaxCallRecvMsgSize(tune.maxMsgBytes),
		grpc.MaxCallSendMsgSize(tune.maxMsgBytes),
	}
	if tune.useGzip {
		callOpts = append(callOpts, grpc.UseCompressor(gzip.Name))
	}
	opts = append(opts, grpc.WithDefaultCallOptions(callOpts...))

	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}

	ctx := metadata.NewOutgoingContext(
		context.Background(),
		metadata.Pairs(transportpb.ChannelKindMetadataKey, transportpb.ChannelKindNode),
	)
	stream, err := transportpb.NewPBFTTransportClient(conn).ClientNodeChannel(ctx)
	if err != nil {
		_ = conn.Close()
		t.Fatalf("open stream to %s: %v", addr, err)
	}
	return conn, stream
}

// broadcastResult captures one broadcast round.
type broadcastResult struct {
	perPeerSend []time.Duration // what the "HUB: node stream send took" log measures
	totalSend   time.Duration   // wall time for the whole fan-out
	endToEnd    time.Duration   // until every peer's event loop channel had it
	sizeBytes   int
}

// broadcastOnce mirrors asyncBroadCast: one goroutine per peer, each building
// its own envelope and calling stream.Send. Set tune.sharedEnvelope to build
// the envelope once instead.
func broadcastOnce(
	t *testing.T,
	hub *NodeMessageHub,
	msg core.NewViewMsg,
	signature []byte,
	peers []*benchPeer,
	streams []transportpb.PBFTTransport_ClientNodeChannelClient,
	tune grpcTuning,
) broadcastResult {
	t.Helper()

	// Drain any stale receipts so endToEnd measures this round only.
	for _, p := range peers {
		for {
			select {
			case <-p.received:
				continue
			default:
			}
			break
		}
	}

	perPeer := make([]time.Duration, len(peers))
	sizeBytes := 0

	var wg sync.WaitGroup
	start := time.Now()

	// Built INSIDE the timed region so the shared-vs-per-peer comparison is
	// honest: sharing removes 2 of the 3 encodes, it does not remove the work.
	var sharedEnv *transportpb.Envelope
	if tune.sharedEnvelope {
		var err error
		sharedEnv, err = hub.buildEnvelope(core.MsgNewViewMessage, msg, signature)
		if err != nil {
			t.Fatalf("buildEnvelope: %v", err)
		}
	}

	for i := range peers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			env := sharedEnv
			if env == nil {
				var err error
				env, err = hub.buildEnvelope(core.MsgNewViewMessage, msg, signature)
				if err != nil {
					t.Errorf("buildEnvelope: %v", err)
					return
				}
			}

			sendStart := time.Now()
			// Production computes this inside the timed region purely to log
			// size_mib; keep it here so the number is comparable.
			if idx == 0 {
				sizeBytes = proto.Size(env)
			} else {
				_ = proto.Size(env)
			}

			var err error
			if tune.usePreparedMsg {
				prepared := &grpc.PreparedMsg{}
				if err = prepared.Encode(streams[idx], env); err == nil {
					err = streams[idx].SendMsg(prepared)
				}
			} else {
				err = streams[idx].Send(env)
			}
			if err != nil {
				t.Errorf("send to peer %d: %v", idx, err)
				return
			}
			perPeer[idx] = time.Since(sendStart)
		}(i)
	}
	wg.Wait()
	totalSend := time.Since(start)

	for _, p := range peers {
		select {
		case <-p.received:
		case <-time.After(30 * time.Second):
			t.Fatalf("timed out waiting for peer %s to deliver NewView", p.addr)
		}
	}
	endToEnd := time.Since(start)

	return broadcastResult{
		perPeerSend: perPeer,
		totalSend:   totalSend,
		endToEnd:    endToEnd,
		sizeBytes:   sizeBytes,
	}
}

func reportBroadcast(t *testing.T, label string, results []broadcastResult) {
	t.Helper()
	if len(results) == 0 {
		return
	}

	var totalSend, endToEnd time.Duration
	var maxPeer time.Duration
	var sumPeer time.Duration
	peerCount := 0
	for _, r := range results {
		totalSend += r.totalSend
		endToEnd += r.endToEnd
		for _, d := range r.perPeerSend {
			sumPeer += d
			peerCount++
			if d > maxPeer {
				maxPeer = d
			}
		}
	}
	n := time.Duration(len(results))

	t.Logf("--- %s", label)
	t.Logf("    wire size:            %d bytes (%.3f MiB)", results[0].sizeBytes, float64(results[0].sizeBytes)/(1024*1024))
	if peerCount > 0 {
		t.Logf("    per-peer Send mean:   %s", sumPeer/time.Duration(peerCount))
		t.Logf("    per-peer Send worst:  %s", maxPeer)
	}
	t.Logf("    fan-out wall time:    %s", totalSend/n)
	t.Logf("    end-to-end (peer rx): %s", endToEnd/n)
}

// TestNewViewGRPCBroadcast measures the real gRPC cost of broadcasting a
// synthetic NewView over loopback TCP. Sweep grpcTuning to see which options
// matter.
func TestNewViewGRPCBroadcast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping gRPC broadcast benchmark in -short mode")
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}

	tunings := []struct {
		name string
		tune grpcTuning
	}{
		{"baseline-production-opts", defaultGRPCTuning()},
		{"1MiB-rw-buffers", func() grpcTuning {
			g := defaultGRPCTuning()
			g.writeBufferSize = 1 << 20
			g.readBufferSize = 1 << 20
			return g
		}()},
		{"1MiB-buffers+64MiB-windows", func() grpcTuning {
			g := defaultGRPCTuning()
			g.writeBufferSize = 1 << 20
			g.readBufferSize = 1 << 20
			g.initialWindowSize = 64 << 20
			g.initialConnWindowSize = 64 << 20
			return g
		}()},
		{"shared-envelope", func() grpcTuning {
			g := defaultGRPCTuning()
			g.sharedEnvelope = true
			return g
		}()},
		{"shared-envelope+prepared-msg", func() grpcTuning {
			g := defaultGRPCTuning()
			g.sharedEnvelope = true
			g.usePreparedMsg = true
			return g
		}()},
		{"gzip", func() grpcTuning {
			g := defaultGRPCTuning()
			g.useGzip = true
			return g
		}()},
		{"all-optimisations", func() grpcTuning {
			g := defaultGRPCTuning()
			g.writeBufferSize = 1 << 20
			g.readBufferSize = 1 << 20
			g.initialWindowSize = 64 << 20
			g.initialConnWindowSize = 64 << 20
			g.sharedEnvelope = true
			g.usePreparedMsg = true
			return g
		}()},
	}

	shapes := []struct {
		name   string
		params dummyParams
	}{
		{"best-case-3-certs", func() dummyParams {
			p := defaultDummyParams()
			p.numPreparedCerts = 3
			return p
		}()},
		{"typical-253-certs", defaultDummyParams()},
		{"worst-case-500-certs", func() dummyParams {
			p := defaultDummyParams()
			p.numPreparedCerts = 2 * CHECKPOINT_INTERVAL
			return p
		}()},
	}

	const rounds = 5

	for _, shape := range shapes {
		for _, tuning := range tunings {
			t.Run(shape.name+"/"+tuning.name, func(t *testing.T) {
				rng := mrand.New(mrand.NewSource(1))
				msg := buildDummyNewView(rng, 1, 8, 13500, shape.params)

				pbMsg := transportpb.NewViewToPB(msg)
				payloadBytes, err := marshalDeterministic(pbMsg)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				signature := crypto.SignMessageEd25519(payloadBytes, privateKey)

				peers := startBenchPeers(t, 3, publicKey, tuning.tune)
				defer func() {
					for _, p := range peers {
						p.stop()
					}
				}()

				senderHub := &NodeMessageHub{
					node_ref: &Node{NodeID: 1, cfg: &config.Config{}, log: logger.NewLogger(1, "node")},
					log:      logger.NewLogger(1, "node"),
				}

				conns := make([]*grpc.ClientConn, 0, len(peers))
				streams := make([]transportpb.PBFTTransport_ClientNodeChannelClient, 0, len(peers))
				for _, p := range peers {
					conn, stream := dialBenchPeer(t, p.addr, tuning.tune)
					conns = append(conns, conn)
					streams = append(streams, stream)
				}
				defer func() {
					for _, c := range conns {
						_ = c.Close()
					}
				}()

				// Warm up: establishes the HTTP/2 connection and grows windows
				// so the measured rounds are steady state.
				broadcastOnce(t, senderHub, msg, signature, peers, streams, tuning.tune)

				results := make([]broadcastResult, 0, rounds)
				for i := 0; i < rounds; i++ {
					results = append(results, broadcastOnce(t, senderHub, msg, signature, peers, streams, tuning.tune))
				}

				t.Logf("params: %s", shape.params)
				reportBroadcast(t, shape.name+" / "+tuning.name, results)
			})
		}
	}
}

// TestNewViewEnvelopeParallelScaling answers: asyncBroadCast fans out one
// goroutine per peer, so the per-peer buildEnvelope/Size/Marshal work runs
// concurrently. Does that make it free?
//
// It measures wall time for N concurrent copies of the per-peer encode work.
// Perfect scaling would keep wall time flat as N grows. Any slope is
// contention (allocator, memory bandwidth, GC) that asyncBroadCast pays on the
// critical path even with idle cores.
func TestNewViewEnvelopeParallelScaling(t *testing.T) {
	hub := &NodeMessageHub{node_ref: &Node{NodeID: 4}}
	signature := make([]byte, ed25519.SignatureSize)

	const iters = 5

	for _, tc := range []struct {
		name   string
		params dummyParams
	}{
		{"healthy-3-certs", func() dummyParams {
			p := defaultDummyParams()
			p.numPreparedCerts = 3
			return p
		}()},
		{"broken-253-certs", defaultDummyParams()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rng := mrand.New(mrand.NewSource(1))
			msg := buildDummyNewView(rng, 4, 8, 13500, tc.params)

			t.Logf("params: %s  (GOMAXPROCS=%d)", tc.params, runtime.GOMAXPROCS(0))

			var baseline time.Duration
			for _, workers := range []int{1, 2, 3, 4, 8} {
				var memBefore, memAfter runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&memBefore)

				s := timeIt(fmt.Sprintf("%d parallel", workers), iters, func() {
					var wg sync.WaitGroup
					for i := 0; i < workers; i++ {
						wg.Add(1)
						go func() {
							defer wg.Done()
							env, err := hub.buildEnvelope(core.MsgNewViewMessage, msg, signature)
							if err != nil {
								t.Errorf("buildEnvelope: %v", err)
								return
							}
							_ = proto.Size(env)
							if _, err := proto.Marshal(env); err != nil {
								t.Errorf("marshal: %v", err)
							}
						}()
					}
					wg.Wait()
				})

				runtime.ReadMemStats(&memAfter)
				allocPerRound := (memAfter.TotalAlloc - memBefore.TotalAlloc) / uint64(iters+1)

				if workers == 1 {
					baseline = s.mean
				}
				slowdown := 1.0
				if baseline > 0 {
					slowdown = float64(s.mean) / float64(baseline)
				}
				t.Logf("    %-14s wall mean=%-13s best=%-13s vs 1-worker=%.2fx  alloc/round=%.1f MiB",
					s.name, s.mean, s.best, slowdown, float64(allocPerRound)/(1024*1024))
			}
			t.Logf("    (flat = perfectly parallel; rising = contention on the critical path)")
		})
	}
}

// TestNewViewSharedEnvelopeWin isolates the cost of rebuilding the protobuf
// envelope inside every per-peer goroutine (what asyncBroadCast does today)
// versus building it once.
func TestNewViewSharedEnvelopeWin(t *testing.T) {
	hub := &NodeMessageHub{node_ref: &Node{NodeID: 4}}
	rng := mrand.New(mrand.NewSource(1))
	params := defaultDummyParams()
	msg := buildDummyNewView(rng, 4, 8, 13500, params)
	signature := make([]byte, ed25519.SignatureSize)

	const peers = 3
	const iters = 5

	perPeer := timeIt("buildEnvelope x3 (today)", iters, func() {
		var wg sync.WaitGroup
		for i := 0; i < peers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				env, err := hub.buildEnvelope(core.MsgNewViewMessage, msg, signature)
				if err != nil {
					t.Errorf("buildEnvelope: %v", err)
					return
				}
				_ = proto.Size(env)
				if _, err := proto.Marshal(env); err != nil {
					t.Errorf("marshal: %v", err)
				}
			}()
		}
		wg.Wait()
	})

	shared := timeIt("buildEnvelope x1 (shared)", iters, func() {
		env, err := hub.buildEnvelope(core.MsgNewViewMessage, msg, signature)
		if err != nil {
			t.Fatalf("buildEnvelope: %v", err)
		}
		bytes, err := proto.Marshal(env)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var wg sync.WaitGroup
		for i := 0; i < peers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = len(bytes) // stand-in for handing the same bytes to each stream
			}()
		}
		wg.Wait()
	})

	t.Logf("params: %s", params)
	t.Logf("    %-28s mean=%-12s best=%s", perPeer.name, perPeer.mean, perPeer.best)
	t.Logf("    %-28s mean=%-12s best=%s", shared.name, shared.mean, shared.best)
	if shared.mean > 0 {
		t.Logf("    speedup: %.2fx", float64(perPeer.mean)/float64(shared.mean))
	}
}
