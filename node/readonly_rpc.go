package node

// Read-only RPC server for archive read replicas (bankd issue #207).
//
// Serves the read-only subset of the CometBFT RPC (block / block_results / tx /
// header / commit / validators / search) directly off blockstore.db + state.db +
// tx_index.db, with NO consensus, p2p, mempool, proxy-app or event bus. This lets
// a query-only node answer EVM history calls (eth_getBlockByNumber, eth_getLogs,
// eth_getTransactionReceipt) that the JSON-RPC backend resolves over CometBFT RPC,
// while the app itself runs headless via `bankd start --grpc-only`.
//
// Not a full node: write/consensus/p2p endpoints are intentionally absent.

import (
	"fmt"
	"net/http"

	"github.com/rs/cors"

	cfg "github.com/cometbft/cometbft/config"
	"github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/p2p"
	rpccore "github.com/cometbft/cometbft/rpc/core"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	rpcserver "github.com/cometbft/cometbft/rpc/jsonrpc/server"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	sm "github.com/cometbft/cometbft/state"
	blockidxr "github.com/cometbft/cometbft/state/indexer/block"
	"github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
)

// StartReadOnlyRPC opens the block/state/tx-index stores from cfg's home dir and
// serves the read-only CometBFT RPC subset over HTTP on cfg.RPC.ListenAddress.
// It returns a stop func that closes the listener. No consensus is run.
func StartReadOnlyRPC(config *cfg.Config, logger log.Logger) (func() error, error) {
	dbProvider := cfg.DefaultDBProvider

	blockStoreDB, err := dbProvider(&cfg.DBContext{ID: "blockstore", Config: config})
	if err != nil {
		return nil, fmt.Errorf("open blockstore db: %w", err)
	}
	blockStore := store.NewBlockStore(blockStoreDB)

	stateDB, err := dbProvider(&cfg.DBContext{ID: "state", Config: config})
	if err != nil {
		return nil, fmt.Errorf("open state db: %w", err)
	}
	stateStore := sm.NewStore(stateDB, sm.StoreOptions{
		DiscardABCIResponses: config.Storage.DiscardABCIResponses,
	})

	// ChainID comes from committed state, not the genesis file: the cosmos-sdk
	// genesis.json uses an encoding CometBFT's strict GenesisDocFromFile rejects.
	state, err := stateStore.Load()
	if err != nil {
		return nil, fmt.Errorf("load state: %w", err)
	}
	genDoc := &types.GenesisDoc{ChainID: state.ChainID}

	txIndexer, blockIndexer, _, err := blockidxr.IndexerFromConfigWithDisabledIndexers(config, dbProvider, state.ChainID)
	if err != nil {
		return nil, fmt.Errorf("init indexers: %w", err)
	}

	env := &rpccore.Environment{
		StateStore:   stateStore,
		BlockStore:   blockStore,
		TxIndexer:    txIndexer,
		BlockIndexer: blockIndexer,
		GenDoc:       genDoc,
		Logger:       logger.With("module", "rpc"),
		Config:       *config.RPC,
	}
	if err := env.InitGenesisChunks(); err != nil {
		return nil, fmt.Errorf("init genesis chunks: %w", err)
	}

	routes := readOnlyRoutes(env, blockStore, genDoc)

	rpcConfig := rpcserver.DefaultConfig()
	rpcConfig.MaxRequestBatchSize = config.RPC.MaxRequestBatchSize
	rpcConfig.MaxBodyBytes = config.RPC.MaxBodyBytes
	rpcConfig.MaxHeaderBytes = config.RPC.MaxHeaderBytes
	rpcConfig.MaxOpenConnections = config.RPC.MaxOpenConnections

	mux := http.NewServeMux()
	rpcLogger := logger.With("module", "rpc-server")
	rpcserver.RegisterRPCFuncs(mux, routes, rpcLogger)

	listener, err := rpcserver.Listen(config.RPC.ListenAddress, rpcConfig.MaxOpenConnections)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", config.RPC.ListenAddress, err)
	}

	var rootHandler http.Handler = mux
	if config.RPC.IsCorsEnabled() {
		rootHandler = cors.New(cors.Options{
			AllowedOrigins: config.RPC.CORSAllowedOrigins,
			AllowedMethods: config.RPC.CORSAllowedMethods,
			AllowedHeaders: config.RPC.CORSAllowedHeaders,
		}).Handler(mux)
	}

	go func() {
		if err := rpcserver.Serve(listener, rootHandler, rpcLogger, rpcConfig); err != nil {
			logger.Error("read-only RPC server stopped", "err", err)
		}
	}()
	logger.Info("read-only CometBFT RPC serving", "address", config.RPC.ListenAddress, "height", blockStore.Height())

	return listener.Close, nil
}

// readOnlyRoutes is the read-only subset of GetRoutes(): no broadcast, no
// consensus/net_info/mempool/abci endpoints (those need fields we leave nil).
// status is replaced with a nil-safe variant since env.Status derefs the
// consensus reactor + validator pubkey.
func readOnlyRoutes(env *rpccore.Environment, bs sm.BlockStore, genDoc *types.GenesisDoc) rpccore.RoutesMap {
	return rpccore.RoutesMap{
		"health":           rpcserver.NewRPCFunc(env.Health, ""),
		"status":           rpcserver.NewRPCFunc(readOnlyStatus(bs, genDoc), ""),
		"blockchain":       rpcserver.NewRPCFunc(env.BlockchainInfo, "minHeight,maxHeight", rpcserver.Cacheable()),
		"genesis":          rpcserver.NewRPCFunc(env.Genesis, "", rpcserver.Cacheable()),
		"genesis_chunked":  rpcserver.NewRPCFunc(env.GenesisChunked, "chunk", rpcserver.Cacheable()),
		"block":            rpcserver.NewRPCFunc(env.Block, "height", rpcserver.Cacheable("height")),
		"block_by_hash":    rpcserver.NewRPCFunc(env.BlockByHash, "hash", rpcserver.Cacheable()),
		"block_results":    rpcserver.NewRPCFunc(env.BlockResults, "height", rpcserver.Cacheable("height")),
		"commit":           rpcserver.NewRPCFunc(env.Commit, "height", rpcserver.Cacheable("height")),
		"header":           rpcserver.NewRPCFunc(env.Header, "height", rpcserver.Cacheable("height")),
		"header_by_hash":   rpcserver.NewRPCFunc(env.HeaderByHash, "hash", rpcserver.Cacheable()),
		"tx":               rpcserver.NewRPCFunc(env.Tx, "hash,prove", rpcserver.Cacheable()),
		"tx_search":        rpcserver.NewRPCFunc(env.TxSearch, "query,prove,page,per_page,order_by"),
		"block_search":     rpcserver.NewRPCFunc(env.BlockSearch, "query,page,per_page,order_by"),
		"validators":       rpcserver.NewRPCFunc(env.Validators, "height,page,per_page", rpcserver.Cacheable("height")),
		"consensus_params": rpcserver.NewRPCFunc(readOnlyConsensusParams(env), "height", rpcserver.Cacheable("height")),
	}
}

// readOnlyConsensusParams mirrors env.ConsensusParams but resolves the default
// height from the committed block store instead of latestUncommittedHeight(),
// which derefs the nil consensus reactor headless. The EVM getBalance path calls
// this to read the block gas limit.
func readOnlyConsensusParams(env *rpccore.Environment) func(*rpctypes.Context, *int64) (*ctypes.ResultConsensusParams, error) {
	return func(_ *rpctypes.Context, heightPtr *int64) (*ctypes.ResultConsensusParams, error) {
		height := env.BlockStore.Height()
		if heightPtr != nil && *heightPtr > 0 {
			height = *heightPtr
		}
		cp, err := env.StateStore.LoadConsensusParams(height)
		if err != nil {
			return nil, err
		}
		return &ctypes.ResultConsensusParams{BlockHeight: height, ConsensusParams: cp}, nil
	}
}

// readOnlyStatus reports sync info straight from the block store. No consensus
// reactor or priv-validator pubkey is available headless, so CatchingUp is false
// and ValidatorInfo is empty.
func readOnlyStatus(bs sm.BlockStore, genDoc *types.GenesisDoc) func(*rpctypes.Context) (*ctypes.ResultStatus, error) {
	return func(*rpctypes.Context) (*ctypes.ResultStatus, error) {
		latestHeight := bs.Height()
		var sync ctypes.SyncInfo
		sync.LatestBlockHeight = latestHeight
		sync.EarliestBlockHeight = bs.Base()
		sync.CatchingUp = false
		if meta := bs.LoadBlockMeta(latestHeight); meta != nil {
			sync.LatestBlockHash = meta.BlockID.Hash
			sync.LatestAppHash = meta.Header.AppHash
			sync.LatestBlockTime = meta.Header.Time
		}
		return &ctypes.ResultStatus{
			NodeInfo: p2p.DefaultNodeInfo{
				Network: genDoc.ChainID,
				Moniker: "archive-readonly",
				Version: "readonly",
			},
			SyncInfo:      sync,
			ValidatorInfo: ctypes.ValidatorInfo{},
		}, nil
	}
}
