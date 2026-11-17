package node

import (
	"sync"

	sm "github.com/cometbft/cometbft/state"
)

// RPCStoreWrapper wraps the block and state stores the RPC environment reads
// from. Consensus, blocksync and pruning keep using the raw stores.
type RPCStoreWrapper func(sm.BlockStore, sm.Store) (sm.BlockStore, sm.Store)

var (
	rpcStoreWrapperMu sync.RWMutex
	rpcStoreWrapper   RPCStoreWrapper
)

// SetRPCStoreWrapper sets the wrapper ConfigureRPC applies to every RPC
// environment, including the in-process local client. bankd uses it to answer
// heights its archive pruned from CometBFT (bankd #207). nil clears it.
func SetRPCStoreWrapper(w RPCStoreWrapper) {
	rpcStoreWrapperMu.Lock()
	defer rpcStoreWrapperMu.Unlock()
	rpcStoreWrapper = w
}

func rpcStores(bs sm.BlockStore, ss sm.Store) (sm.BlockStore, sm.Store) {
	rpcStoreWrapperMu.RLock()
	w := rpcStoreWrapper
	rpcStoreWrapperMu.RUnlock()
	if w == nil {
		return bs, ss
	}
	return w(bs, ss)
}
