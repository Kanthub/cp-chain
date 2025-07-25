package db

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cpchain-network/cp-chain/cp-service/eth"
)

func prepLocalDerivationDBPath(chainID eth.ChainID, datadir string) (string, error) {
	dir, err := prepChainDir(chainID, datadir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "local_safe.db"), nil
}

func prepCrossDerivationDBPath(chainID eth.ChainID, datadir string) (string, error) {
	dir, err := prepChainDir(chainID, datadir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cross_safe.db"), nil
}

func prepLogDBPath(chainID eth.ChainID, datadir string) (string, error) {
	dir, err := prepChainDir(chainID, datadir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "log.db"), nil
}

func prepChainDir(chainID eth.ChainID, datadir string) (string, error) {
	dir := filepath.Join(datadir, chainID.String())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create chain directory %v: %w", dir, err)
	}
	return dir, nil
}

func PrepDataDir(datadir string) error {
	if err := os.MkdirAll(datadir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory %v: %w", datadir, err)
	}
	return nil
}
