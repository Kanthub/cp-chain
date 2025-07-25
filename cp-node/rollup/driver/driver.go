package driver

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"

	"github.com/cpchain-network/cp-chain/cp-node/rollup"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/async"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/attributes"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/clsync"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/derive"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/engine"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/event"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/finality"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/sequencing"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/status"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/sync"
	"github.com/cpchain-network/cp-chain/cp-service/eth"
	"github.com/cpchain-network/cp-chain/cp-service/sources"
)

// aliases to not disrupt op-conductor code
var (
	ErrSequencerAlreadyStarted = sequencing.ErrSequencerAlreadyStarted
	ErrSequencerAlreadyStopped = sequencing.ErrSequencerAlreadyStopped
)

type Metrics interface {
	RecordPipelineReset()
	RecordPublishingError()
	RecordDerivationError()

	RecordReceivedUnsafePayload(payload *eth.ExecutionPayloadEnvelope)

	RecordL1Ref(name string, ref eth.L1BlockRef)
	RecordL2Ref(name string, ref eth.L2BlockRef)
	RecordChannelInputBytes(inputCompressedBytes int)
	RecordHeadChannelOpened()
	RecordChannelTimedOut()
	RecordFrame()

	RecordDerivedBatches(batchType string)

	RecordUnsafePayloadsBuffer(length uint64, memSize uint64, next eth.BlockID)

	SetDerivationIdle(idle bool)
	SetSequencerState(active bool)

	RecordL1ReorgDepth(d uint64)

	engine.Metrics
	L1FetcherMetrics
	event.Metrics
	sequencing.Metrics
}

type L1Chain interface {
	derive.L1Fetcher
	L1BlockRefByLabel(context.Context, eth.BlockLabel) (eth.L1BlockRef, error)
}

type L2Chain interface {
	engine.Engine
	L2BlockRefByLabel(ctx context.Context, label eth.BlockLabel) (eth.L2BlockRef, error)
	L2BlockRefByHash(ctx context.Context, l2Hash common.Hash) (eth.L2BlockRef, error)
	L2BlockRefByNumber(ctx context.Context, num uint64) (eth.L2BlockRef, error)
	GetLatestBlock(ctx context.Context) (eth.BlockInfo, error)
}

type DerivationPipeline interface {
	Reset()
	Step(ctx context.Context, pendingSafeHead eth.L2BlockRef) (*derive.AttributesWithParent, error)
	Origin() eth.L1BlockRef
	DerivationReady() bool
	ConfirmEngineReset()
}

type EngineController interface {
	engine.LocalEngineControl
	IsEngineSyncing() bool
	InsertUnsafePayload(ctx context.Context, payload *eth.ExecutionPayloadEnvelope, ref eth.L2BlockRef) error
	TryUpdateEngine(ctx context.Context) error
	TryBackupUnsafeReorg(ctx context.Context) (bool, error)
}

type CLSync interface {
	LowestQueuedUnsafeBlock() eth.L2BlockRef
}

type AttributesHandler interface {
	// HasAttributes returns if there are any block attributes to process.
	// HasAttributes is for EngineQueue testing only, and can be removed when attribute processing is fully independent.
	HasAttributes() bool
	// SetAttributes overwrites the set of attributes. This may be nil, to clear what may be processed next.
	SetAttributes(attributes *derive.AttributesWithParent)
	// Proceed runs one attempt of processing attributes, if any.
	// Proceed returns io.EOF if there are no attributes to process.
	Proceed(ctx context.Context) error
}

type Finalizer interface {
	FinalizedL1() eth.L1BlockRef
	event.Deriver
}

type SyncStatusTracker interface {
	event.Deriver
	SyncStatus() *eth.SyncStatus
	L1Head() eth.L1BlockRef
}

type Network interface {
	// PublishL2Payload is called by the driver whenever there is a new payload to publish, synchronously with the driver main loop.
	PublishL2Payload(ctx context.Context, payload *eth.ExecutionPayloadEnvelope) error
}

type AltSync interface {
	// RequestL2Range informs the sync source that the given range of core blocks is missing,
	// and should be retrieved from any available alternative syncing source.
	// The start and end of the range are exclusive:
	// the start is the head we already have, the end is the first thing we have queued up.
	// It's the task of the alt-sync mechanism to use this hint to fetch the right payloads.
	// Note that the end and start may not be consistent: in this case the sync method should fetch older history
	//
	// If the end value is zeroed, then the sync-method may determine the end free of choice,
	// e.g. sync till the chain head meets the wallclock time. This functionality is optional:
	// a fixed target to sync towards may be determined by picking up payloads through P2P gossip or other sources.
	//
	// The sync results should be returned back to the driver via the OnUnsafeL2Payload(ctx, payload) method.
	// The latest requested range should always take priority over previous requests.
	// There may be overlaps in requested ranges.
	// An error may be returned if the scheduling fails immediately, e.g. a context timeout.
	RequestL2Range(ctx context.Context, start, end eth.L2BlockRef) error
}

type SequencerStateListener interface {
	SequencerStarted() error
	SequencerStopped() error
}

type Drain interface {
	Drain() error
}

// NewDriver composes an events handler that tracks L1 state, triggers core Derivation, and optionally sequences new core blocks.
func NewDriver(
	sys event.Registry,
	drain Drain,
	driverCfg *Config,
	cfg *rollup.Config,
	l2 L2Chain,
	elClient *sources.EthClient,
	altSync AltSync,
	network Network,
	log log.Logger,
	metrics Metrics,
	sequencerStateListener sequencing.SequencerStateListener,
	safeHeadListener rollup.SafeHeadListener,
	syncCfg *sync.Config,
	managedMode bool,
	batchSize int,
) *Driver {
	driverCtx, driverCancel := context.WithCancel(context.Background())

	opts := event.DefaultRegisterOpts()

	statusTracker := status.NewStatusTracker(log, metrics)
	sys.Register("status", statusTracker, opts)

	ec := engine.NewEngineController(l2, log, metrics, cfg, syncCfg,
		sys.Register("engine-controller", nil, opts))

	sys.Register("engine-reset",
		engine.NewEngineResetDeriver(driverCtx, log, cfg, l2, syncCfg), opts)

	clSync := clsync.NewCLSync(log, cfg, metrics) // alt-sync still uses cl-sync state to determine what to sync to
	sys.Register("cl-sync", clSync, opts)

	var finalizer Finalizer

	finalizer = finality.NewFinalizer(driverCtx, log, cfg)

	sys.Register("finalizer", finalizer, opts)

	sys.Register("attributes-handler",
		attributes.NewAttributesHandler(log, cfg, driverCtx, l2), opts)

	derivationPipeline := derive.NewDerivationPipeline(log, cfg, l2, metrics, managedMode)

	sys.Register("pipeline",
		derive.NewPipelineDeriver(driverCtx, derivationPipeline), opts)

	syncDeriver := &SyncDeriver{
		Derivation:     derivationPipeline,
		SafeHeadNotifs: safeHeadListener,
		CLSync:         clSync,
		Engine:         ec,
		SyncCfg:        syncCfg,
		Config:         cfg,
		L2:             l2,
		ELClient:       elClient,
		Log:            log,
		Ctx:            driverCtx,
		Drain:          drain.Drain,
		ManagedMode:    managedMode,
	}
	sys.Register("sync", syncDeriver, opts)

	sys.Register("engine", engine.NewEngDeriver(log, driverCtx, cfg, metrics, ec), opts)

	schedDeriv := NewStepSchedulingDeriver(log)
	sys.Register("step-scheduler", schedDeriv, opts)

	var sequencer sequencing.SequencerIface
	if driverCfg.SequencerEnabled {
		asyncGossiper := async.NewAsyncGossiper(driverCtx, network, log, metrics)
		attrBuilder := derive.NewFetchingAttributesBuilder(cfg, l2)
		sequencer = sequencing.NewSequencer(driverCtx, log, cfg, attrBuilder,
			sequencerStateListener, asyncGossiper, metrics)
		sys.Register("sequencer", sequencer, opts)
	} else {
		sequencer = sequencing.DisabledSequencer{}
	}

	driverEmitter := sys.Register("driver", nil, opts)
	driver := &Driver{
		statusTracker:    statusTracker,
		SyncDeriver:      syncDeriver,
		sched:            schedDeriv,
		emitter:          driverEmitter,
		drain:            drain.Drain,
		stateReq:         make(chan chan struct{}),
		forceReset:       make(chan chan struct{}, 10),
		driverConfig:     driverCfg,
		driverCtx:        driverCtx,
		driverCancel:     driverCancel,
		log:              log,
		maxBatchSize:     uint(batchSize),
		sequencer:        sequencer,
		network:          network,
		metrics:          metrics,
		l1HeadSig:        make(chan eth.L1BlockRef, 10),
		l1SafeSig:        make(chan eth.L1BlockRef, 10),
		l1FinalizedSig:   make(chan eth.L1BlockRef, 10),
		unsafeL2Payloads: make(chan *eth.ExecutionPayloadEnvelope, 10),
		altSync:          altSync,
	}

	return driver
}
