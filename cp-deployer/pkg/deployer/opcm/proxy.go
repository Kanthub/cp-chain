package opcm

import (
	"github.com/ethereum/go-ethereum/common"

	"github.com/cpchain-network/cp-chain/common/script"
)

type DeployProxyInput struct {
	Owner common.Address
}

func (input *DeployProxyInput) InputSet() bool {
	return true
}

type DeployProxyOutput struct {
	Proxy common.Address
}

func DeployProxy(
	host *script.Host,
	input DeployProxyInput,
) (DeployProxyOutput, error) {
	return RunScriptSingle[DeployProxyInput, DeployProxyOutput](host, input, "DeployProxy.s.sol", "DeployProxy")
}
