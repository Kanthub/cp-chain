GITCOMMIT ?= $(shell git rev-parse HEAD)
GITDATE ?= $(shell git show -s --format='%ct')
VERSION ?= v0.0.0

LDFLAGSSTRING +=-X main.GitCommit=$(GITCOMMIT)
LDFLAGSSTRING +=-X main.GitDate=$(GITDATE)

# cp-program-client version must ALWAYS be set to the same value (v0.0.0) to ensure exact build is reproducible
PC_LDFLAGSSTRING := $(LDFLAGSSTRING)
PC_LDFLAGSSTRING +=-X github.com/cpchain-network/cp-chain/cp-program/version.Version=v0.0.0
PC_LDFLAGSSTRING +=-X github.com/cpchain-network/cp-chain/cp-program/version.Meta=

LDFLAGSSTRING +=-X github.com/cpchain-network/cp-chain/cp-program/version.Version=$(VERSION)
LDFLAGSSTRING +=-X github.com/cpchain-network/cp-chain/cp-program/version.Meta=$(VERSION_META)

COMPAT_DIR := temp/compat

cp-program: \
	cp-program-host \
	cp-program-client \
	cp-program-client-mips

cp-program-host:
	env GO111MODULE=on GOOS=$(TARGETOS) GOARCH=$(TARGETARCH) CGO_ENABLED=0 go build -v -ldflags "$(LDFLAGSSTRING)" -o ./bin/cp-program ./host/cmd/main.go

cp-program-client:
	env GO111MODULE=on GOOS=$(TARGETOS) GOARCH=$(TARGETARCH) go build -v -ldflags "$(PC_LDFLAGSSTRING)" -o ./bin/cp-program-client ./client/cmd/main.go
	env GO111MODULE=on GOOS=$(TARGETOS) GOARCH=$(TARGETARCH) go build -v -ldflags "$(PC_LDFLAGSSTRING)" -o ./bin/cp-program-client-interop ./client/interopcmd/main.go

cp-program-client-mips: cp-program-client-mips32 cp-program-client-mips64 cp-program-client-mips64-interop

cp-program-client-mips32:
	env GO111MODULE=on GOOS=linux GOARCH=mips GOMIPS=softfloat go build -v -ldflags "$(PC_LDFLAGSSTRING)" -o ./bin/cp-program-client.elf ./client/cmd/main.go
	# verify output with: readelf -h bin/cp-program-client.elf
	# result is mips32, big endian, R3000

cp-program-client-mips64:
	env GO111MODULE=on GOOS=linux GOARCH=mips64 GOMIPS64=softfloat go build -v -ldflags "$(PC_LDFLAGSSTRING)" -o ./bin/cp-program-client64.elf ./client/cmd/main.go
	# verify output with: readelf -h bin/cp-program-client64.elf
	# result is mips64, big endian, R3000

cp-program-client-mips64-interop:
	env GO111MODULE=on GOOS=linux GOARCH=mips64 GOMIPS64=softfloat go build -v -ldflags "$(PC_LDFLAGSSTRING)" -o ./bin/cp-program-client-interop.elf ./client/interopcmd/main.go
	# verify output with: readelf -h bin/cp-program-client-interop.elf
	# result is mips64, big endian, R3000

cp-program-client-riscv:
	env GO111MODULE=on GOOS=linux GOARCH=riscv64 go build -v -gcflags="all=-d=softfloat" -ldflags "$(PC_LDFLAGSSTRING)" -o ./bin/cp-program-client-riscv.elf ./client/cmd/main.go

check-custom-chains: cp-program-host
	./bin/cp-program configs check-custom-chains

reproducible-prestate: check-custom-chains
	@docker build --output ./bin/ --progress plain -f Dockerfile.repro ../
	@echo "Cannon Absolute prestate hash: "
	@cat ./bin/prestate-proof.json | jq -r .pre
	@echo "Cannon64 Absolute prestate hash: "
	@cat ./bin/prestate-proof-mt64.json | jq -r .pre
	@echo "CannonInterop Absolute prestate hash: "
	@cat ./bin/prestate-proof-interop.json | jq -r .pre
.PHONY: reproducible-prestate

verify-reproducibility:
	rm -rf temp/states
	./scripts/build-prestates.sh
	env GO111MODULE=on go run ./prestates/verify/verify.go --input temp/states/versions.json
.PHONY: verify-reproducibility

clean:
	rm -rf bin "$(COMPAT_DIR)"

test:
	go test -v ./...

capture-mainnet-genesis: cp-program-host cp-program-client
	rm -rf "$(COMPAT_DIR)/mainnet-genesis" "$(COMPAT_DIR)/mainnet-genesis.tar.bz"
	env GO111MODULE=on go run ./verify/mainnet/cmd/mainnet.go --l1 $$MAINNET_L1URL --l1.beacon $$MAINNET_BEACON_URL --l2 $$MAINNET_L2URL --datadir "$(COMPAT_DIR)/mainnet-genesis" --l1.head "0x4903424f6cc2cfba7c2bf8c8f48ca46721c963fa64b411cfee3697b781e3e5f1" --l2.start "105235063" --l2.end "105235064"
	tar jcf "$(COMPAT_DIR)/mainnet-genesis.tar.bz" -C "$(COMPAT_DIR)" mainnet-genesis

capture-sepolia-delta: cp-program-host cp-program-client
	rm -rf "$(COMPAT_DIR)/sepolia-delta" "$(COMPAT_DIR)/sepolia-delta.tar.bz"
	env GO111MODULE=on go run ./verify/sepolia/cmd/sepolia.go --l1 $$SEPOLIA_L1URL --l1.beacon $$SEPOLIA_BEACON_URL --l2 $$SEPOLIA_L2URL --datadir "$(COMPAT_DIR)/sepolia-delta" --l1.head "0x935428728bcfcfeb2e5ba9175fd2890e52831dae221aa4d5dcffed8320edc001" --l2.start "8728200" --l2.end "8728320"
	tar jcf "$(COMPAT_DIR)/sepolia-delta.tar.bz" -C "$(COMPAT_DIR)" sepolia-delta

capture-sepolia-ecotone: cp-program-host cp-program-client
	rm -rf "$(COMPAT_DIR)/sepolia-ecotone" "$(COMPAT_DIR)/sepolia-ecotone.tar.bz"
	env GO111MODULE=on go run ./verify/sepolia/cmd/sepolia.go --l1 $$SEPOLIA_L1URL --l1.beacon $$SEPOLIA_BEACON_URL --l2 $$SEPOLIA_L2URL --datadir "$(COMPAT_DIR)/sepolia-ecotone" --l1.head "0x5d491a8c1e728a4e70720c09fefdaa083681a9421cd365af85220cf8bd4448a3" --l2.start "9205715" --l2.end "9205815"
	tar jcf "$(COMPAT_DIR)/sepolia-ecotone.tar.bz" -C "$(COMPAT_DIR)" sepolia-ecotone

capture-sepolia-fjord: cp-program-host cp-program-client
	rm -rf "$(COMPAT_DIR)/sepolia-fjord" "$(COMPAT_DIR)/sepolia-fjord.tar.bz"
	env GO111MODULE=on go run ./verify/sepolia/cmd/sepolia.go --l1 $$SEPOLIA_L1URL --l1.beacon $$SEPOLIA_BEACON_URL --l2 $$SEPOLIA_L2URL --datadir "$(COMPAT_DIR)/sepolia-fjord" --l1.head "0x93ba31bf89e54237af6e6564e69d328b2b5202adf643de4cb097431f74f4a6c1" --l2.start "15378256" --l2.end "15378356"
	tar jcf "$(COMPAT_DIR)/sepolia-fjord.tar.bz" -C "$(COMPAT_DIR)" sepolia-fjord

capture-chain-test-data: capture-mainnet-genesis capture-sepolia-delta capture-sepolia-ecotone capture-sepolia-fjord

verify-sepolia-delta: cp-program-host cp-program-client
	./scripts/run-compat.sh "sepolia-delta"

verify-mainnet-genesis: cp-program-host cp-program-client
	./scripts/run-compat.sh "mainnet-genesis"

verify-sepolia-ecotone: cp-program-host cp-program-client
	./scripts/run-compat.sh "sepolia-ecotone"

verify-sepolia-fjord: cp-program-host cp-program-client
	./scripts/run-compat.sh "sepolia-fjord"

verify-compat: verify-sepolia-delta verify-sepolia-ecotone verify-mainnet-genesis

analyze-cp-program-client: analyze-cp-program-client-cannon-singlethreaded-32 analyze-cp-program-client-cannon-multithreaded-64

analyze-cp-program-client-cannon-singlethreaded-32:
	./scripts/run-static-analysis.sh cannon-singlethreaded-32 ./compatibility-test/baseline-cannon-singlethreaded-32.json

analyze-cp-program-client-cannon-multithreaded-64:
	./scripts/run-static-analysis.sh cannon-multithreaded-64 ./compatibility-test/baseline-cannon-multithreaded-64.json

.PHONY: \
	cp-program \
	cp-program-host \
	cp-program-client \
	cp-program-client-mips \
	cp-program-client-mips32 \
	cp-program-client-mips64 \
	cp-program-client-riscv \
	clean \
	test \
	capture-goerli-verify \
	capture-mainnet-genesis \
	capture-sepolia-delta \
	capture-sepolia-ecotone \
	capture-chain-test-data \
	run-goerli-verify \
	run-sepolia-verify \
	run-compat \
	analyze-cp-program-client \
	analyze-cp-program-client-cannon-singlethreaded-32 \
	analyze-cp-program-client-cannon-multithreaded-64
