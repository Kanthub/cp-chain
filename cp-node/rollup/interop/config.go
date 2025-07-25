package interop

import (
	"context"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/log"

	"github.com/cpchain-network/cp-chain/cp-node/rollup"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/interop/managed"
	"github.com/cpchain-network/cp-chain/cp-node/rollup/interop/standard"
	"github.com/cpchain-network/cp-chain/cp-service/client"
	opmetrics "github.com/cpchain-network/cp-chain/cp-service/metrics"
	"github.com/cpchain-network/cp-chain/cp-service/rpc"
	"github.com/cpchain-network/cp-chain/cp-service/sources"
)

type Config struct {
	// SupervisorAddr to follow for cross-chain safety updates.
	// Non-empty if running in follow-mode.
	// Cannot be set if RPCAddr is set.
	SupervisorAddr string

	// RPCAddr address to bind RPC server to, to serve external supervisor nodes.
	// Cannot be set if SupervisorAddr is set.
	RPCAddr string
	// RPCPort port to bind RPC server to, to serve external supervisor nodes.
	// Binds to any available port if set to 0.
	// Only applicable if RPCAddr is set.
	RPCPort int
	// RPCJwtSecretPath path of JWT secret file to apply authentication to the interop server address.
	RPCJwtSecretPath string
}

func (cfg *Config) Check() error {
	if (cfg.SupervisorAddr == "") == (cfg.RPCAddr == "") {
		return errors.New("must have either a supervisor RPC endpoint to follow, or interop RPC address to serve from")
	}
	return nil
}

func (cfg *Config) Setup(ctx context.Context, logger log.Logger, rollupCfg *rollup.Config, l2 L2Source, m opmetrics.RPCMetricer) (SubSystem, error) {
	if cfg.RPCAddr != "" {
		logger.Info("Setting up Interop RPC server to serve supervisor sync work")
		// Load JWT secret, if any, generate one otherwise.
		jwtSecret, err := rpc.ObtainJWTSecret(logger, cfg.RPCJwtSecretPath, true)
		if err != nil {
			return nil, err
		}
		return managed.NewManagedMode(logger, rollupCfg, cfg.RPCAddr, cfg.RPCPort, jwtSecret, l2, m), nil
	} else {
		logger.Info("Setting up Interop RPC client to sync from read-only supervisor")
		cl, err := client.NewRPC(ctx, logger, cfg.SupervisorAddr, client.WithLazyDial())
		if err != nil {
			return nil, fmt.Errorf("failed to create supervisor RPC: %w", err)
		}
		return standard.NewStandardMode(logger, sources.NewSupervisorClient(cl)), nil
	}
}
