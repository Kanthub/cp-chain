package opcm

import (
	"github.com/cpchain-network/cp-chain/common/script"
	"github.com/ethereum/go-ethereum/common"
)

type SetDisputeGameImplInput struct {
	Factory             common.Address
	Impl                common.Address
	AnchorStateRegistry common.Address
	GameType            uint32
}

func SetDisputeGameImpl(
	h *script.Host,
	input SetDisputeGameImplInput,
) error {
	return RunScriptVoid[SetDisputeGameImplInput](
		h,
		input,
		"SetDisputeGameImpl.s.sol",
		"SetDisputeGameImpl",
	)
}
