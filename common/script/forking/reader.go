package forking

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
)

// forkStateReader implements the state.Reader abstraction,
// for read-only access to a state-trie at a particular state-root.
type forkStateReader struct {
	trie *ForkedAccountsTrie
}

var _ state.Reader = (*forkStateReader)(nil)

func (f *forkStateReader) Account(addr common.Address) (*types.StateAccount, error) {
	acc, err := f.trie.GetAccount(addr)
	if err != nil {
		return nil, err
	}
	// We copy because the Reader interfaces defines that it should be safe to modify after returning.
	return acc.Copy(), nil
}

func (f *forkStateReader) Storage(addr common.Address, slot common.Hash) (common.Hash, error) {
	v, err := f.trie.GetStorage(addr, slot[:])
	if err != nil {
		return common.Hash{}, err
	}
	return common.Hash(v), nil
}

func (f *forkStateReader) Code(addr common.Address, codeHash common.Hash) ([]byte, error) {
	return f.trie.ContractCode(addr, codeHash)
}

func (f *forkStateReader) CodeSize(addr common.Address, codeHash common.Hash) (int, error) {
	return f.trie.ContractCodeSize(addr, codeHash)
}

func (f *forkStateReader) Copy() state.Reader {
	return f
}
