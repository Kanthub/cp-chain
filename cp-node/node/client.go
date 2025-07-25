package node

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/log"
	gn "github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/cpchain-network/cp-chain/cp-node/rollup"
	"github.com/cpchain-network/cp-chain/cp-service/apis"
	"github.com/cpchain-network/cp-chain/cp-service/client"
	opmetrics "github.com/cpchain-network/cp-chain/cp-service/metrics"
	"github.com/cpchain-network/cp-chain/cp-service/sources"
)

type L2EndpointSetup interface {
	// Setup a RPC client to a core execution engine to process rollup blocks with.
	Setup(ctx context.Context, log log.Logger, rollupCfg *rollup.Config, metrics opmetrics.RPCMetricer) (cl client.RPC, rpcCfg *sources.EngineClientConfig, err error)
	Check() error
}

type ELEndpointSetup interface {
	// Setup a RPC client to a core execution engine to process rollup blocks with.
	Setup(ctx context.Context, log log.Logger, rollupCfg *rollup.Config) (cl client.RPC, rpcCfg *sources.EthClientConfig, err error)
	Check() error
}

type L1EndpointSetup interface {
	// Setup a RPC client to a L1 node to pull rollup input-data from.
	// The results of the RPC client may be trusted for faster processing, or strictly validated.
	// The kind of the RPC may be non-basic, to optimize RPC usage.
	Setup(ctx context.Context, log log.Logger, rollupCfg *rollup.Config, metrics opmetrics.RPCMetricer) (cl client.RPC, rpcCfg *sources.L1ClientConfig, err error)
	Check() error
}

type L1BeaconEndpointSetup interface {
	Setup(ctx context.Context, log log.Logger) (cl apis.BeaconClient, fb []apis.BlobSideCarsClient, err error)
	// ShouldIgnoreBeaconCheck returns true if the Beacon-node version check should not halt startup.
	ShouldIgnoreBeaconCheck() bool
	ShouldFetchAllSidecars() bool
	Check() error
}

type L2EndpointConfig struct {
	// L2EngineAddr is the address of the core Engine JSON-RPC endpoint to use. The engine and eth
	// namespaces must be enabled by the endpoint.
	L2EngineAddr string

	// JWT secrets for core Engine API authentication during HTTP or initial Websocket communication.
	// Any value for an IPC connection.
	L2EngineJWTSecret [32]byte

	// L2EngineCallTimeout is the default timeout duration for core calls.
	// Defines the maximum time a call to the core engine is allowed to take before timing out.
	L2EngineCallTimeout time.Duration
}

var _ L2EndpointSetup = (*L2EndpointConfig)(nil)

func (cfg *L2EndpointConfig) Check() error {
	if cfg.L2EngineAddr == "" {
		return errors.New("empty core Engine Address")
	}

	return nil
}

func (cfg *L2EndpointConfig) Setup(ctx context.Context, log log.Logger, rollupCfg *rollup.Config, metrics opmetrics.RPCMetricer) (client.RPC, *sources.EngineClientConfig, error) {
	if err := cfg.Check(); err != nil {
		return nil, nil, err
	}
	auth := rpc.WithHTTPAuth(gn.NewJWTAuth(cfg.L2EngineJWTSecret))
	opts := []client.RPCOption{
		client.WithGethRPCOptions(auth),
		client.WithDialAttempts(10),
		client.WithCallTimeout(cfg.L2EngineCallTimeout),
		client.WithRPCRecorder(metrics.NewRecorder("engine-api")),
	}
	l2Node, err := client.NewRPC(ctx, log, cfg.L2EngineAddr, opts...)
	if err != nil {
		return nil, nil, err
	}

	return l2Node, sources.EngineClientDefaultConfig(rollupCfg), nil
}

// PreparedL2Endpoints enables testing with in-process pre-setup RPC connections to core engines
type PreparedL2Endpoints struct {
	Client client.RPC
}

func (p *PreparedL2Endpoints) Check() error {
	if p.Client == nil {
		return errors.New("client cannot be nil")
	}
	return nil
}

var _ L2EndpointSetup = (*PreparedL2Endpoints)(nil)

func (p *PreparedL2Endpoints) Setup(ctx context.Context, log log.Logger, rollupCfg *rollup.Config, metrics opmetrics.RPCMetricer) (client.RPC, *sources.EngineClientConfig, error) {
	return p.Client, sources.EngineClientDefaultConfig(rollupCfg), nil
}

type ElEndpointConfig struct {
	// ElEngineAddr is the address of the core Engine JSON-RPC endpoint to use. The engine and eth
	// namespaces must be enabled by the endpoint.
	ElRpcAddr string

	// RateLimit specifies a self-imposed rate-limit on L1 requests. 0 is no rate-limit.
	RateLimit float64

	// BatchSize specifies the maximum batch-size, which also applies as L1 rate-limit burst amount (if set).
	BatchSize int
}

func (cfg *ElEndpointConfig) Check() error {
	if cfg.ElRpcAddr == "" {
		return errors.New("empty core execution-layer address")
	}

	return nil
}

func (cfg *ElEndpointConfig) Setup(ctx context.Context, log log.Logger, rollupCfg *rollup.Config) (client.RPC, *sources.EthClientConfig, error) {
	if err := cfg.Check(); err != nil {
		return nil, nil, err
	}
	opts := []client.RPCOption{
		client.WithDialAttempts(10),
	}
	opts = append(opts, client.WithRateLimit(cfg.RateLimit, cfg.BatchSize))

	elNode, err := client.NewRPC(ctx, log, cfg.ElRpcAddr, opts...)
	if err != nil {
		return nil, nil, err
	}
	cLCfg := sources.L2ClientDefaultConfig(rollupCfg, true)
	cLCfg.MaxRequestsPerBatch = cfg.BatchSize
	return elNode, &cLCfg.EthClientConfig, nil
}

// PreparedElEndpoints enables testing with in-process pre-setup RPC connections to core engines
type PreparedElEndpoints struct {
	Client client.RPC
}

func (p *PreparedElEndpoints) Check() error {
	if p.Client == nil {
		return errors.New("client cannot be nil")
	}
	return nil
}

func (p *PreparedElEndpoints) Setup(ctx context.Context, log log.Logger, rollupCfg *rollup.Config) (client.RPC, *sources.EthClientConfig, error) {
	return p.Client, nil, nil
}

type L1EndpointConfig struct {
	L1NodeAddr string // Address of L1 User JSON-RPC endpoint to use (eth namespace required)

	// L1TrustRPC: if we trust the L1 RPC we do not have to validate L1 response contents like headers
	// against block hashes, or cached transaction sender addresses.
	// Thus we can sync faster at the risk of the source RPC being wrong.
	L1TrustRPC bool

	// L1RPCKind identifies the RPC provider kind that serves the RPC,
	// to inform the optimal usage of the RPC for transaction receipts fetching.
	L1RPCKind sources.RPCProviderKind

	// RateLimit specifies a self-imposed rate-limit on L1 requests. 0 is no rate-limit.
	RateLimit float64

	// BatchSize specifies the maximum batch-size, which also applies as L1 rate-limit burst amount (if set).
	BatchSize int

	// MaxConcurrency specifies the maximum number of concurrent requests to the L1 RPC.
	MaxConcurrency int

	// HttpPollInterval specifies the interval between polling for the latest L1 block,
	// when the RPC is detected to be an HTTP type.
	// It is recommended to use websockets or IPC for efficient following of the changing block.
	// Setting this to 0 disables polling.
	HttpPollInterval time.Duration

	// CacheSize specifies the cache size for blocks, receipts and transactions. It's optional and a
	// sane default of 3/2 the sequencing window size is used during Setup if this field is set to 0.
	// Note that receipts and transactions are cached per block, which is why there's only one cache
	// size to configure.
	CacheSize uint
}

var _ L1EndpointSetup = (*L1EndpointConfig)(nil)

func (cfg *L1EndpointConfig) Check() error {
	if cfg.BatchSize < 1 || cfg.BatchSize > 500 {
		return fmt.Errorf("batch size is invalid or unreasonable: %d", cfg.BatchSize)
	}
	if cfg.RateLimit < 0 {
		return fmt.Errorf("rate limit cannot be negative: %f", cfg.RateLimit)
	}
	if cfg.MaxConcurrency < 1 {
		return fmt.Errorf("max concurrent requests cannot be less than 1, was %d", cfg.MaxConcurrency)
	}
	if cfg.CacheSize > 1_000_000 {
		return fmt.Errorf("cache size is dangerously large: %d", cfg.CacheSize)
	}
	return nil
}

func (cfg *L1EndpointConfig) Setup(ctx context.Context, log log.Logger, rollupCfg *rollup.Config, metrics opmetrics.RPCMetricer) (client.RPC, *sources.L1ClientConfig, error) {
	opts := []client.RPCOption{
		client.WithHttpPollInterval(cfg.HttpPollInterval),
		client.WithDialAttempts(10),
		client.WithRPCRecorder(metrics.NewRecorder("l1")),
	}
	if cfg.RateLimit != 0 {
		opts = append(opts, client.WithRateLimit(cfg.RateLimit, cfg.BatchSize))
	}

	l1RPC, err := client.NewRPC(ctx, log, cfg.L1NodeAddr, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to dial L1 address (%s): %w", cfg.L1NodeAddr, err)
	}

	var l1Cfg *sources.L1ClientConfig
	if cfg.CacheSize > 0 {
		l1Cfg = sources.L1ClientSimpleConfig(cfg.L1TrustRPC, cfg.L1RPCKind, int(cfg.CacheSize))
	} else {
		l1Cfg = sources.L1ClientDefaultConfig(rollupCfg, cfg.L1TrustRPC, cfg.L1RPCKind)
	}
	l1Cfg.MaxRequestsPerBatch = cfg.BatchSize
	l1Cfg.MaxConcurrentRequests = cfg.MaxConcurrency
	return l1RPC, l1Cfg, nil
}

// PreparedL1Endpoint enables testing with an in-process pre-setup RPC connection to L1
type PreparedL1Endpoint struct {
	Client          client.RPC
	TrustRPC        bool
	RPCProviderKind sources.RPCProviderKind
}

var _ L1EndpointSetup = (*PreparedL1Endpoint)(nil)

func (p *PreparedL1Endpoint) Setup(ctx context.Context, log log.Logger, rollupCfg *rollup.Config, metrics opmetrics.RPCMetricer) (client.RPC, *sources.L1ClientConfig, error) {
	return p.Client, sources.L1ClientDefaultConfig(rollupCfg, p.TrustRPC, p.RPCProviderKind), nil
}

func (cfg *PreparedL1Endpoint) Check() error {
	if cfg.Client == nil {
		return errors.New("rpc client cannot be nil")
	}

	return nil
}

type L1BeaconEndpointConfig struct {
	BeaconAddr             string   // Address of L1 User Beacon-API endpoint to use (beacon namespace required)
	BeaconHeader           string   // Optional HTTP header for all requests to L1 Beacon
	BeaconFallbackAddrs    []string // Addresses of L1 Beacon-API fallback endpoints (only for blob sidecars retrieval)
	BeaconCheckIgnore      bool     // When false, halt startup if the beacon version endpoint fails
	BeaconFetchAllSidecars bool     // Whether to fetch all blob sidecars and filter locally
}

var _ L1BeaconEndpointSetup = (*L1BeaconEndpointConfig)(nil)

func (cfg *L1BeaconEndpointConfig) Setup(ctx context.Context, log log.Logger) (cl apis.BeaconClient, fb []apis.BlobSideCarsClient, err error) {
	var opts []client.BasicHTTPClientOption
	if cfg.BeaconHeader != "" {
		hdr, err := parseHTTPHeader(cfg.BeaconHeader)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing beacon header: %w", err)
		}
		opts = append(opts, client.WithHeader(hdr))
	}

	for _, addr := range cfg.BeaconFallbackAddrs {
		b := client.NewBasicHTTPClient(addr, log)
		fb = append(fb, sources.NewBeaconHTTPClient(b))
	}

	a := client.NewBasicHTTPClient(cfg.BeaconAddr, log, opts...)
	return sources.NewBeaconHTTPClient(a), fb, nil
}

func (cfg *L1BeaconEndpointConfig) Check() error {
	if cfg.BeaconAddr == "" && !cfg.BeaconCheckIgnore {
		return errors.New("expected L1 Beacon API endpoint, but got none")
	}
	return nil
}

func (cfg *L1BeaconEndpointConfig) ShouldIgnoreBeaconCheck() bool {
	return cfg.BeaconCheckIgnore
}

func (cfg *L1BeaconEndpointConfig) ShouldFetchAllSidecars() bool {
	return cfg.BeaconFetchAllSidecars
}

func parseHTTPHeader(headerStr string) (http.Header, error) {
	h := make(http.Header, 1)
	s := strings.SplitN(headerStr, ": ", 2)
	if len(s) != 2 {
		return nil, errors.New("invalid header format")
	}
	h.Add(s[0], s[1])
	return h, nil
}
