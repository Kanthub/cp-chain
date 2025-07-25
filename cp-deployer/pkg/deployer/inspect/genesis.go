package inspect

import (
	"fmt"

	"github.com/cpchain-network/cp-chain/common/genesis"
	"github.com/cpchain-network/cp-chain/cp-deployer/pkg/deployer/pipeline"
	"github.com/cpchain-network/cp-chain/cp-deployer/pkg/deployer/state"
	"github.com/cpchain-network/cp-chain/cp-node/rollup"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"

	"github.com/cpchain-network/cp-chain/cp-service/ioutil"
	"github.com/cpchain-network/cp-chain/cp-service/jsonutil"
	"github.com/urfave/cli/v2"
)

func GenesisCLI(cliCtx *cli.Context) error {
	cfg, err := readConfig(cliCtx)
	if err != nil {
		return err
	}

	globalState, err := pipeline.ReadState(cfg.Workdir)
	if err != nil {
		return fmt.Errorf("failed to read intent: %w", err)
	}

	l2Genesis, _, err := pipeline.RenderGenesisAndRollup(globalState, cfg.ChainID, nil)
	if err != nil {
		return fmt.Errorf("failed to generate genesis block: %w", err)
	}

	if err := jsonutil.WriteJSON(l2Genesis, ioutil.ToStdOutOrFileOrNoop(cfg.Outfile, 0o666)); err != nil {
		return fmt.Errorf("failed to write genesis: %w", err)
	}

	return nil
}

func GenesisAndRollup(globalState *state.State, chainID common.Hash) (*core.Genesis, *rollup.Config, error) {
	if globalState.AppliedIntent == nil {
		return nil, nil, fmt.Errorf("chain state is not applied - run cp-deployer apply")
	}

	chainIntent, err := globalState.AppliedIntent.Chain(chainID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get applied chain intent: %w", err)
	}

	chainState, err := globalState.Chain(chainID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get chain ID %s: %w", chainID.String(), err)
	}

	l2Allocs := chainState.Allocs.Data
	config, err := state.CombineDeployConfig(
		globalState.AppliedIntent,
		chainIntent,
		globalState,
		chainState,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to combine core init config: %w", err)
	}

	l2GenesisBuilt, err := genesis.BuildL2Genesis(&config, l2Allocs, chainState.StartBlock.ToBlockRef())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build core genesis: %w", err)
	}
	l2GenesisBlock := l2GenesisBuilt.ToBlock()

	rollupConfig, err := config.RollupConfig(
		chainState.StartBlock.ToBlockRef(),
		l2GenesisBlock.Hash(),
		l2GenesisBlock.Number().Uint64(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build rollup config: %w", err)
	}

	if err := rollupConfig.Check(); err != nil {
		return nil, nil, fmt.Errorf("generated rollup config does not pass validation: %w", err)
	}

	return l2GenesisBuilt, rollupConfig, nil
}
