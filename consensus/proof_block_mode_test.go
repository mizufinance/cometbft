package consensus

import (
	"testing"

	"github.com/stretchr/testify/require"

	cfg "github.com/cometbft/cometbft/config"
	"github.com/cometbft/cometbft/libs/log"
	sm "github.com/cometbft/cometbft/state"
	statemocks "github.com/cometbft/cometbft/state/mocks"
	"github.com/cometbft/cometbft/types"
)

func TestNeedProofBlock_ProofBlockMode(t *testing.T) {
	const height = int64(10)
	appHashA := []byte("hash-a")
	appHashB := []byte("hash-b")

	tests := []struct {
		name        string
		mode        cfg.ProofBlockMode
		prevAppHash []byte
		curAppHash  []byte
		prevNumTxs  int
		want        bool
	}{
		{"always: idle drift forces proof", cfg.ProofBlockModeAlways, appHashA, appHashB, 0, true},
		{"always: tx drift forces proof", cfg.ProofBlockModeAlways, appHashA, appHashB, 5, true},
		{"only_after_tx: idle drift skipped", cfg.ProofBlockModeOnlyAfterTx, appHashA, appHashB, 0, false},
		{"only_after_tx: tx drift forces proof", cfg.ProofBlockModeOnlyAfterTx, appHashA, appHashB, 5, true},
		{"only_after_tx: no drift no proof", cfg.ProofBlockModeOnlyAfterTx, appHashA, appHashA, 5, false},
		{"always: no drift no proof", cfg.ProofBlockModeAlways, appHashA, appHashA, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blockStore := &statemocks.BlockStore{}
			blockStore.On("LoadBlockMeta", height-1).Return(&types.BlockMeta{
				Header: types.Header{AppHash: tt.prevAppHash},
				NumTxs: tt.prevNumTxs,
			})

			consensusCfg := cfg.DefaultConsensusConfig()
			consensusCfg.ProofBlockMode = tt.mode

			cs := &State{
				config:     consensusCfg,
				blockStore: blockStore,
				state:      sm.State{InitialHeight: 1, AppHash: tt.curAppHash},
			}
			cs.Logger = log.NewNopLogger()

			require.Equal(t, tt.want, cs.needProofBlock(height))
		})
	}
}

func TestNeedProofBlock_InitialHeight(t *testing.T) {
	const initial = int64(5)
	cs := &State{
		config: cfg.DefaultConsensusConfig(),
		state:  sm.State{InitialHeight: initial},
	}
	cs.Logger = log.NewNopLogger()

	require.True(t, cs.needProofBlock(initial),
		"initial height must always need a proof block to commit the genesis app hash")
}

func TestNeedProofBlock_MissingMeta(t *testing.T) {
	const height = int64(10)
	blockStore := &statemocks.BlockStore{}
	blockStore.On("LoadBlockMeta", height-1).Return((*types.BlockMeta)(nil))

	consensusCfg := cfg.DefaultConsensusConfig()
	consensusCfg.ProofBlockMode = cfg.ProofBlockModeOnlyAfterTx

	cs := &State{
		config:     consensusCfg,
		blockStore: blockStore,
		state:      sm.State{InitialHeight: 1, AppHash: []byte("x")},
	}
	cs.Logger = log.NewNopLogger()

	require.True(t, cs.needProofBlock(height),
		"missing block meta must conservatively force a proof block regardless of mode")
}
