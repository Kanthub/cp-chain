package driver

import (
	"context"
	"errors"
	"fmt"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/finality"
	"math/big"
	gosync "sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"

	"github.com/cpchain-network/cp-chain/cp-node/rollup"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/clsync"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/derive"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/engine"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/event"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/sequencing"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/sync"
	"github.com/cpchain-network/cp-chain/cp-service/eth"
	"github.com/cpchain-network/cp-chain/cp-service/sources"
)

// Deprecated: use eth.SyncStatus instead.
type SyncStatus = eth.SyncStatus

type Driver struct {
	statusTracker SyncStatusTracker

	*SyncDeriver

	sched *StepSchedulingDeriver

	emitter event.Emitter
	drain   func() error

	// Requests to block the event loop for synchronous execution to avoid reading an inconsistent state
	stateReq chan chan struct{}

	// Upon receiving a channel in this channel, the derivation pipeline is forced to be reset.
	// It tells the caller that the reset occurred by closing the passed in channel.
	forceReset chan chan struct{}

	// Driver config: verifier and sequencer settings.
	// May not be modified after starting the Driver.
	driverConfig *Config

	// L1 Signals:
	//
	// Not all L1 blocks, or all changes, have to be signalled:
	// the derivation process traverses the chain and handles reorgs as necessary,
	// the driver just needs to be aware of the *latest* signals enough so to not
	// lag behind actionable data.
	l1HeadSig      chan eth.L1BlockRef
	l1SafeSig      chan eth.L1BlockRef
	l1FinalizedSig chan eth.L1BlockRef

	l2Payloads   []eth.ExecutionPayloadEnvelope
	maxBatchSize uint

	// Interface to signal the core block range to sync.
	altSync AltSync

	// core Signals:

	unsafeL2Payloads chan *eth.ExecutionPayloadEnvelope

	sequencer sequencing.SequencerIface
	network   Network // may be nil, network for is optional

	metrics Metrics
	log     log.Logger

	wg gosync.WaitGroup

	driverCtx    context.Context
	driverCancel context.CancelFunc
}

// Start starts up the state loop.
// The loop will have been started iff err is not nil.
func (s *Driver) Start() error {
	log.Info("Starting driver", "sequencerEnabled", s.driverConfig.SequencerEnabled,
		"sequencerStopped", s.driverConfig.SequencerStopped, "recoverMode", s.driverConfig.RecoverMode)
	if s.driverConfig.SequencerEnabled {
		if s.driverConfig.RecoverMode {
			log.Warn("sequencer is in recover mode")
			s.sequencer.SetRecoverMode(true)
		}
		if err := s.sequencer.SetMaxSafeLag(s.driverCtx, s.driverConfig.SequencerMaxSafeLag); err != nil {
			return fmt.Errorf("failed to set sequencer max safe lag: %w", err)
		}
		if err := s.sequencer.Init(s.driverCtx, !s.driverConfig.SequencerStopped); err != nil {
			return fmt.Errorf("persist initial sequencer state: %w", err)
		}
	}

	if s.SyncCfg.SyncMode == sync.ELSync {
		s.syncUnsafeBlocks(s.driverCtx)
	}

	s.wg.Add(1)
	go s.eventLoop()

	return nil
}

func (s *Driver) Close() error {
	s.driverCancel()
	s.wg.Wait()
	s.sequencer.Close()
	return nil
}

// OnL1Head signals the driver that the L1 chain changed the "unsafe" block,
// also known as head of the chain, or "latest".
func (s *Driver) OnL1Head(ctx context.Context, unsafe eth.L1BlockRef) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.l1HeadSig <- unsafe:
		return nil
	}
}

// OnL1Safe signals the driver that the L1 chain changed the "safe",
// also known as the justified checkpoint (as seen on L1 beacon-chain).
func (s *Driver) OnL1Safe(ctx context.Context, safe eth.L1BlockRef) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.l1SafeSig <- safe:
		return nil
	}
}

func (s *Driver) OnL1Finalized(ctx context.Context, finalized eth.L1BlockRef) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.l1FinalizedSig <- finalized:
		return nil
	}
}

func (s *Driver) OnUnsafeL2Payload(ctx context.Context, envelope *eth.ExecutionPayloadEnvelope) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.unsafeL2Payloads <- envelope:
		return nil
	}
}

// the eventLoop responds to L1 changes and internal timers to produce core blocks.
func (s *Driver) eventLoop() {
	defer s.wg.Done()
	s.log.Info("State loop started")
	defer s.log.Info("State loop returned")

	defer s.driverCancel()

	// reqStep requests a derivation step nicely, with a delay if this is a reattempt, or not at all if we already scheduled a reattempt.
	reqStep := func() {
		s.emitter.Emit(StepReqEvent{})
	}

	// We call reqStep right away to finish syncing to the tip of the chain if we're behind.
	// reqStep will also be triggered when the L1 head moves forward or if there was a reorg on the
	// L1 chain that we need to handle.
	reqStep()

	sequencerTimer := time.NewTimer(0)
	var sequencerCh <-chan time.Time
	var prevTime time.Time
	// planSequencerAction updates the sequencerTimer with the next action, if any.
	// The sequencerCh is nil (indefinitely blocks on read) if no action needs to be performed,
	// or set to the timer channel if there is an action scheduled.
	planSequencerAction := func() {
		nextAction, ok := s.sequencer.NextAction()
		if !ok {
			if sequencerCh != nil {
				s.log.Info("Sequencer paused until new events")
			}
			sequencerCh = nil
			return
		}
		// avoid unnecessary timer resets
		if nextAction == prevTime {
			return
		}
		prevTime = nextAction
		sequencerCh = sequencerTimer.C
		if len(sequencerCh) > 0 { // empty if not already drained before resetting
			<-sequencerCh
		}
		delta := time.Until(nextAction)
		s.log.Info("Scheduled sequencer action", "delta", delta.Seconds())
		sequencerTimer.Reset(delta)
	}

	// Create a ticker to check if there is a gap in the engine queue. Whenever
	// there is, we send requests to sync source to retrieve the missing payloads.
	syncCheckInterval := time.Duration(s.Config.BlockTime) * time.Second / 2
	altSyncTicker := time.NewTicker(syncCheckInterval)
	defer altSyncTicker.Stop()
	lastUnsafeL2 := s.Engine.UnsafeL2Head()

	for {
		if s.driverCtx.Err() != nil { // don't try to schedule/handle more work when we are closing.
			return
		}

		if s.drain != nil {
			// While event-processing is synchronous we have to drain
			// (i.e. process all queued-up events) before creating any new events.
			if err := s.drain(); err != nil {
				if s.driverCtx.Err() != nil {
					return
				}
				s.log.Error("unexpected error from event-draining", "err", err)
			}
		}

		planSequencerAction()

		// If the engine is not ready, or if the core head is actively changing, then reset the alt-sync:
		// there is no need to request core blocks when we are syncing already.
		if head := s.Engine.UnsafeL2Head(); head != lastUnsafeL2 || !s.Derivation.DerivationReady() {
			lastUnsafeL2 = head
			altSyncTicker.Reset(syncCheckInterval)
		}

		if s.SyncCfg.SyncMode == sync.CLSync {
			s.emitter.Emit(finality.FinalizeL1Event{}) // todo: if all node vote change block to finalized
			reqStep()                                  // we may be able to mark more core data as finalized now
		}

		select {
		case <-sequencerCh:
			s.Emitter.Emit(sequencing.SequencerActionEvent{})
		case <-altSyncTicker.C:
			if s.SyncCfg.SyncMode == sync.CLSync {
				// Check if there is a gap in the current unsafe payload queue.
				ctx, cancel := context.WithTimeout(s.driverCtx, time.Second*2)
				err := s.checkForGapInUnsafeQueue(ctx)
				cancel()
				if err != nil {
					s.log.Warn("failed to check for unsafe core blocks to sync", "err", err)
				}
			} else {
				// Check if there is a gap in the current unsafe payload queue.
				ctx, cancel := context.WithTimeout(s.driverCtx, time.Second*2)
				err := s.checkSyncUnsafeBlocks(ctx)
				cancel()
				if err != nil {
					s.log.Warn("failed to check sync unsafe blocks", "err", err)
				}
			}
		case envelope := <-s.unsafeL2Payloads:
			// If we are doing CL sync or done with engine syncing, fallback to the unsafe payload queue & CL P2P sync.
			if s.SyncCfg.SyncMode == sync.CLSync || !s.Engine.IsEngineSyncing() {
				s.log.Info("Optimistically queueing unsafe core execution payload", "id", envelope.ExecutionPayload.ID())
				s.Emitter.Emit(clsync.ReceivedUnsafePayloadEvent{Envelope: envelope})
				s.metrics.RecordReceivedUnsafePayload(envelope)
				reqStep()
			} else if s.SyncCfg.SyncMode == sync.ELSync {
				ref, err := derive.PayloadToBlockRef(s.Config, envelope.ExecutionPayload)
				if err != nil {
					s.log.Info("Failed to turn execution payload into a block ref", "id", envelope.ExecutionPayload.ID(), "err", err)
					continue
				}
				if ref.Number <= s.Engine.UnsafeL2Head().Number {
					continue
				}
				s.log.Info("Optimistically inserting unsafe core execution payload to drive EL sync", "id", envelope.ExecutionPayload.ID())
				if err := s.Engine.InsertUnsafePayload(s.driverCtx, envelope, ref); err != nil {
					s.log.Warn("Failed to insert unsafe payload for EL sync", "id", envelope.ExecutionPayload.ID(), "err", err)
				}
			}
		case <-s.sched.NextDelayedStep():
			s.emitter.Emit(StepAttemptEvent{})
		case <-s.sched.NextStep():
			s.emitter.Emit(StepAttemptEvent{})
		case respCh := <-s.stateReq:
			respCh <- struct{}{}
		case respCh := <-s.forceReset:
			s.log.Warn("Derivation pipeline is manually reset")
			s.Derivation.Reset()
			s.metrics.RecordPipelineReset()
			close(respCh)
		case <-s.driverCtx.Done():
			return
		}
	}
}

type SyncDeriver struct {
	// The derivation pipeline is reset whenever we reorg.
	// The derivation pipeline determines the new l2Safe.
	Derivation DerivationPipeline

	SafeHeadNotifs rollup.SafeHeadListener // notified when safe head is updated

	CLSync CLSync

	// The engine controller is used by the sequencer & Derivation components.
	// We will also use it for EL sync in a future PR.
	Engine EngineController

	// Sync Mod Config
	SyncCfg *sync.Config

	Config *rollup.Config

	L1       L1Chain
	L2       L2Chain
	ELClient *sources.EthClient

	Emitter event.Emitter

	Log log.Logger

	Ctx context.Context

	Drain func() error

	// When in interop, and managed by an cp-supervisor,
	// the node performs a reset based on the instructions of the cp-supervisor.
	ManagedMode bool
}

func (s *SyncDeriver) AttachEmitter(em event.Emitter) {
	s.Emitter = em
}

func (s *SyncDeriver) OnEvent(ev event.Event) bool {
	switch x := ev.(type) {
	case StepEvent:
		s.SyncStep()
	case rollup.ResetEvent:
		s.onResetEvent(x)
	case rollup.L1TemporaryErrorEvent:
		s.Log.Warn("L1 temporary error", "err", x.Err)
		s.Emitter.Emit(StepReqEvent{})
	case rollup.EngineTemporaryErrorEvent:
		s.Log.Warn("Engine temporary error", "err", x.Err)
		// Make sure that for any temporarily failed attributes we retry processing.
		// This will be triggered by a step. After appropriate backoff.
		s.Emitter.Emit(StepReqEvent{})
	case engine.EngineResetConfirmedEvent:
		s.onEngineConfirmedReset(x)
	case derive.DeriverIdleEvent:
		// Once derivation is idle the system is healthy
		// and we can wait for new inputs. No backoff necessary.
		s.Emitter.Emit(ResetStepBackoffEvent{})
	case derive.DeriverMoreEvent:
		// If there is more data to process,
		// continue derivation quickly
		//s.Emitter.Emit(StepReqEvent{ResetBackoff: true})
	case engine.SafeDerivedEvent:
		s.onSafeDerivedBlock(x)
	case derive.ProvideL1Traversal:
		s.Emitter.Emit(StepReqEvent{})
	default:
		return false
	}
	return true
}

func (s *SyncDeriver) onSafeDerivedBlock(x engine.SafeDerivedEvent) {
	if s.SafeHeadNotifs != nil && s.SafeHeadNotifs.Enabled() {
		if err := s.SafeHeadNotifs.SafeHeadUpdated(x.Safe); err != nil {
			// At this point our state is in a potentially inconsistent state as we've updated the safe head
			// in the execution client but failed to post process it. Reset the pipeline so the safe head rolls back
			// a little (it always rolls back at least 1 block) and then it will retry storing the entry
			s.Emitter.Emit(rollup.ResetEvent{Err: fmt.Errorf("safe head notifications failed: %w", err)})
		}
	}
}

func (s *SyncDeriver) onEngineConfirmedReset(x engine.EngineResetConfirmedEvent) {
	// If the listener update fails, we return,
	// and don't confirm the engine-reset with the derivation pipeline.
	// The pipeline will re-trigger a reset as necessary.
	if s.SafeHeadNotifs != nil {
		if err := s.SafeHeadNotifs.SafeHeadReset(x.CrossSafe); err != nil {
			s.Log.Error("Failed to warn safe-head notifier of safe-head reset", "safe", x.CrossSafe)
			return
		}
		if s.SafeHeadNotifs.Enabled() && x.CrossSafe.ID() == s.Config.Genesis.L2 {
			// The rollup genesis block is always safe by definition. So if the pipeline resets this far back we know
			// we will process all safe head updates and can record genesis as always safe from L1 genesis.
			// Note that it is not safe to use cfg.Genesis.L1 here as it is the block immediately before the core genesis
			// but the contracts may have been deployed earlier than that, allowing creating a dispute game
			// with a L1 head prior to cfg.Genesis.L1
			if err := s.SafeHeadNotifs.SafeHeadUpdated(x.CrossSafe); err != nil {
				s.Log.Error("Failed to notify safe-head listener of safe-head", "err", err)
				return
			}
		}
	}
	s.Log.Info("Confirming pipeline reset")
	s.Emitter.Emit(derive.ConfirmPipelineResetEvent{})
}

func (s *SyncDeriver) onResetEvent(x rollup.ResetEvent) {
	if s.ManagedMode {
		s.Log.Warn("Encountered reset in Managed Mode, waiting for cp-supervisor", "err", x.Err)
		// ManagedMode will pick up the ResetEvent
		return
	}
	// If the system corrupts, e.g. due to a reorg, simply reset it
	s.Log.Warn("Deriver system is resetting", "err", x.Err)
	s.Emitter.Emit(StepReqEvent{})
	s.Emitter.Emit(engine.ResetEngineRequestEvent{})
}

// SyncStep performs the sequence of encapsulated syncing steps.
// Warning: this sequence will be broken apart as outlined in cp-node derivers design doc.
func (s *SyncDeriver) SyncStep() {
	s.Log.Debug("Sync process step")

	drain := func() (ok bool) {
		if err := s.Drain(); err != nil {
			if errors.Is(err, context.Canceled) {
				return false
			} else {
				s.Emitter.Emit(rollup.CriticalErrorEvent{
					Err: fmt.Errorf("unexpected error on SyncStep event Drain: %w", err)})
				return false
			}
		}
		return true
	}

	if !drain() {
		return
	}

	s.Emitter.Emit(engine.TryBackupUnsafeReorgEvent{})
	if !drain() {
		return
	}

	s.Emitter.Emit(engine.TryUpdateEngineEvent{})
	if !drain() {
		return
	}

	if s.Engine.IsEngineSyncing() {
		// The pipeline cannot move forwards if doing EL sync.
		s.Log.Debug("Rollup driver is backing off because execution engine is syncing.",
			"unsafe_head", s.Engine.UnsafeL2Head())
		s.Emitter.Emit(ResetStepBackoffEvent{})
		return
	}

	// Any now processed forkchoice updates will trigger CL-sync payload processing, if any payload is queued up.

	// Since we don't force attributes to be processed at this point,
	// we cannot safely directly trigger the derivation, as that may generate new attributes that
	// conflict with what attributes have not been applied yet.
	// Instead, we request the engine to repeat where its pending-safe head is at.
	// Upon the pending-safe signal the attributes deriver can then ask the pipeline
	// to generate new attributes, if no attributes are known already.
	s.Emitter.Emit(engine.PendingSafeRequestEvent{})

	// If interop is configured, we have to run the engine events,
	// to ensure cross-core safety is continuously verified against the interop-backend.
	if s.Config.InteropTime != nil && !s.ManagedMode {
		s.Emitter.Emit(engine.CrossUpdateRequestEvent{})
	}
}

// ResetDerivationPipeline forces a reset of the derivation pipeline.
// It waits for the reset to occur. It simply unblocks the caller rather
// than fully cancelling the reset request upon a context cancellation.
func (s *Driver) ResetDerivationPipeline(ctx context.Context) error {
	respCh := make(chan struct{}, 1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.forceReset <- respCh:
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-respCh:
			return nil
		}
	}
}

func (s *Driver) StartSequencer(ctx context.Context, blockHash common.Hash) error {
	return s.sequencer.Start(ctx, blockHash)
}

func (s *Driver) StopSequencer(ctx context.Context) (common.Hash, error) {
	return s.sequencer.Stop(ctx)
}

func (s *Driver) SequencerActive(ctx context.Context) (bool, error) {
	return s.sequencer.Active(), nil
}

func (s *Driver) OverrideLeader(ctx context.Context) error {
	return s.sequencer.OverrideLeader(ctx)
}

func (s *Driver) ConductorEnabled(ctx context.Context) (bool, error) {
	return s.sequencer.ConductorEnabled(ctx), nil
}

func (s *Driver) SetRecoverMode(ctx context.Context, mode bool) error {
	s.sequencer.SetRecoverMode(mode)
	return nil
}

// SyncStatus blocks the driver event loop and captures the syncing status.
func (s *Driver) SyncStatus(ctx context.Context) (*eth.SyncStatus, error) {
	return s.statusTracker.SyncStatus(), nil
}

// BlockRefWithStatus blocks the driver event loop and captures the syncing status,
// along with an core block reference by number consistent with that same status.
// If the event loop is too busy and the context expires, a context error is returned.
func (s *Driver) BlockRefWithStatus(ctx context.Context, num uint64) (eth.L2BlockRef, *eth.SyncStatus, error) {
	resp := s.statusTracker.SyncStatus()
	if resp.FinalizedL2.Number >= num { // If finalized, we are certain it does not reorg, and don't have to lock.
		ref, err := s.L2.L2BlockRefByNumber(ctx, num)
		return ref, resp, err
	}
	wait := make(chan struct{})
	select {
	case s.stateReq <- wait:
		resp := s.statusTracker.SyncStatus()
		ref, err := s.L2.L2BlockRefByNumber(ctx, num)
		<-wait
		return ref, resp, err
	case <-ctx.Done():
		return eth.L2BlockRef{}, nil, ctx.Err()
	}
}

// checkForGapInUnsafeQueue checks if there is a gap in the unsafe queue and attempts to retrieve the missing payloads from an alt-sync method.
// WARNING: This is only an outgoing signal, the blocks are not guaranteed to be retrieved.
// Results are received through OnUnsafeL2Payload.
func (s *Driver) checkForGapInUnsafeQueue(ctx context.Context) error {
	start := s.Engine.UnsafeL2Head()
	end := s.CLSync.LowestQueuedUnsafeBlock()
	// Check if we have missing blocks between the start and end. Request them if we do.
	if end == (eth.L2BlockRef{}) {
		s.log.Debug("requesting sync with open-end range", "start", start)
		return s.altSync.RequestL2Range(ctx, start, eth.L2BlockRef{})
	} else if end.Number > start.Number+1 {
		s.log.Debug("requesting missing unsafe core block range", "start", start, "end", end, "size", end.Number-start.Number)
		return s.altSync.RequestL2Range(ctx, start, end)
	}
	return nil
}

func (s *Driver) syncUnsafeBlocks(ctx context.Context) {
	tickerSyncer := time.NewTicker(time.Second * 1)
	for range tickerSyncer.C {
		startBlock, err := s.L2.GetLatestBlock(ctx)
		if err != nil && errors.Is(err, ethereum.NotFound) {
			s.log.Error("failed to get latest block by l2 geth", "err", err)
			continue
		}
		endBlock, err := s.ELClient.GetLatestBlock(ctx)
		if err != nil && errors.Is(err, ethereum.NotFound) {
			s.log.Error("failed to get latest block by el geth", "err", err)
			continue
		}
		startHeight := big.NewInt(int64(startBlock.NumberU64()) + 1)
		endHeight := clamp(startHeight, big.NewInt(int64(endBlock.NumberU64())), uint64(s.maxBatchSize))
		if startHeight.Cmp(endHeight) >= 0 {
			s.log.Info("successfully synchronized all old blocks")
			return
		}

		payloads, err := s.ELClient.PayloadsByRange(ctx, startHeight, endHeight)
		if err != nil {
			s.log.Error("error querying blocks by range", "err", err)
			continue
		}

		for _, payload := range payloads {
			ref, err := derive.PayloadToBlockRef(s.Config, payload.ExecutionPayload)
			if err != nil {
				s.log.Error("Failed to turn execution payload into a block ref", "id", payload.ExecutionPayload.ID(), "err", err)
				continue
			}
			if err := s.Engine.InsertUnsafePayload(ctx, &payload, ref); err != nil {
				s.log.Error("Failed to insert unsafe payload for EL sync", "id", payload.ExecutionPayload.ID(), "err", err)
			}
		}
		s.log.Info("successfully synchronized a batch of blocks", "now", endHeight.String(), "latest", endBlock.NumberU64())

	}
}

func (s *Driver) checkSyncUnsafeBlocks(ctx context.Context) error {
	startBlock, err := s.L2.GetLatestBlock(ctx)
	if err != nil && errors.Is(err, ethereum.NotFound) {
		s.log.Error("failed to get latest block by l2 geth", "err", err)
		return err
	}
	endBlock, err := s.ELClient.GetLatestBlock(ctx)
	if err != nil && errors.Is(err, ethereum.NotFound) {
		s.log.Error("failed to get latest block by el geth", "err", err)
		return err
	}
	if startBlock == nil || endBlock == nil {
		return nil
	}

	if startBlock.NumberU64() < endBlock.NumberU64()-5 {
		startHeight := big.NewInt(int64(startBlock.NumberU64()) + 1)
		endHeight := clamp(startHeight, big.NewInt(int64(endBlock.NumberU64())), uint64(s.maxBatchSize))

		payloads, err := s.ELClient.PayloadsByRange(ctx, startHeight, endHeight)
		if err != nil {
			s.log.Error("error querying blocks by range", "err", err)
			return err
		}

		for _, payload := range payloads {
			ref, err := derive.PayloadToBlockRef(s.Config, payload.ExecutionPayload)
			if err != nil {
				s.log.Error("Failed to turn execution payload into a block ref", "id", payload.ExecutionPayload.ID(), "err", err)
				return err
			}
			if err := s.Engine.InsertUnsafePayload(ctx, &payload, ref); err != nil {
				s.log.Error("Failed to insert unsafe payload for EL sync", "id", payload.ExecutionPayload.ID(), "err", err)
				return err
			}
		}
		s.log.Info("successfully synchronized a batch of blocks", "now", endHeight.String(), "latest", endBlock.NumberU64())
	}
	return nil
}

func clamp(start, end *big.Int, size uint64) *big.Int {
	temp := new(big.Int)
	count := temp.Sub(end, start).Uint64() + 1
	if count <= size {
		return end
	}

	// we re-use the allocated temp as the new end
	temp.Add(start, big.NewInt(int64(size-1)))
	return temp
}
