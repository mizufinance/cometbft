package node

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cometbft/cometbft/internal/test"
	"github.com/cometbft/cometbft/libs/log"
	sm "github.com/cometbft/cometbft/state"
)

type wrappedBlockStore struct{ sm.BlockStore }

func (wrappedBlockStore) Base() int64 { return 1 }

type wrappedStateStore struct{ sm.Store }

func TestConfigureRPCStoreWrapper(t *testing.T) {
	config := test.ResetTestRoot("node_rpc_store_wrapper_test")
	t.Cleanup(func() { os.RemoveAll(config.RootDir) })

	n, err := DefaultNewNode(config, log.TestingLogger())
	require.NoError(t, err)

	tests := []struct {
		name    string
		wrapper RPCStoreWrapper
		wrapped bool
	}{
		{name: "unset passes raw stores", wrapper: nil, wrapped: false},
		{
			name: "set wraps both stores",
			wrapper: func(bs sm.BlockStore, ss sm.Store) (sm.BlockStore, sm.Store) {
				return wrappedBlockStore{bs}, wrappedStateStore{ss}
			},
			wrapped: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			SetRPCStoreWrapper(tc.wrapper)
			t.Cleanup(func() { SetRPCStoreWrapper(nil) })

			env, err := n.ConfigureRPC()
			require.NoError(t, err)

			_, bsWrapped := env.BlockStore.(wrappedBlockStore)
			_, ssWrapped := env.StateStore.(wrappedStateStore)
			require.Equal(t, tc.wrapped, bsWrapped)
			require.Equal(t, tc.wrapped, ssWrapped)

			// The node itself keeps the raw stores.
			require.Same(t, n.blockStore, n.BlockStore())
			_, raw := n.stateStore.(wrappedStateStore)
			require.False(t, raw)
		})
	}
}
