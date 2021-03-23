package codetrie

import (
	"errors"
	"sort"

	sszlib "github.com/ferranbt/fastssz"
	"github.com/golang/snappy"

	"github.com/ethereum/go-ethereum/codetrie/ssz"
	"github.com/ethereum/go-ethereum/common"
)

type CMStats struct {
	NumContracts int
	ProofSize    int
	CodeSize     int
	ProofStats   *ProofStats
	RLPStats     *RLPStats
}

func NewCMStats() *CMStats {
	return &CMStats{
		ProofStats: &ProofStats{},
		RLPStats:   &RLPStats{},
	}
}

type ContractBag struct {
	contracts map[common.Hash]*Contract
}

func NewContractBag() *ContractBag {
	return &ContractBag{
		contracts: make(map[common.Hash]*Contract),
	}
}

func (b *ContractBag) Get(codeHash common.Hash, code []byte) *Contract {
	if c, ok := b.contracts[codeHash]; ok {
		return c
	}

	c := NewContract(code)
	b.contracts[codeHash] = c
	return c
}

func (b *ContractBag) Stats() (*CMStats, error) {
	stats := NewCMStats()
	stats.NumContracts = len(b.contracts)
	for _, c := range b.contracts {
		stats.CodeSize += c.CodeSize()
		rawProof, err := c.Prove()
		if err != nil {
			return nil, err
		}
		p := ssz.NewMultiproof(rawProof)
		cp := ssz.NewCompressedMultiproof(rawProof.Compress())

		ps, err := NewProofStats(cp)
		if err != nil {
			return nil, err
		}
		stats.ProofStats.Add(ps)

		rs, err := NewRLPStats(p, cp)
		if err != nil {
			return nil, err
		}
		stats.RLPStats.Add(rs)
	}
	stats.ProofSize = stats.ProofStats.Sum()
	return stats, nil
}

type Contract struct {
	code          []byte
	touchedChunks map[int]bool
}

func NewContract(code []byte) *Contract {
	touchedChunks := make(map[int]bool)
	return &Contract{code: code, touchedChunks: touchedChunks}
}

func (c *Contract) TouchPC(pc int) error {
	if pc >= len(c.code) {
		return errors.New("PC to touch exceeds bytecode length")
	}

	cid := pc / 32
	c.touchedChunks[cid] = true

	return nil
}

func (c *Contract) TouchRange(from, to int) error {
	if from >= to {
		return errors.New("Invalid range")
	}
	if to >= len(c.code) {
		return errors.New("PC to touch exceeds bytecode length")
	}

	fcid := from / 32
	tcid := to / 32
	for i := fcid; i < tcid+1; i++ {
		c.touchedChunks[i] = true
	}

	return nil
}

func (c *Contract) CodeSize() int {
	return len(c.code)
}

func (c *Contract) Prove() (*sszlib.Multiproof, error) {
	tree, err := GetSSZTree(c.code, 32)
	if err != nil {
		return nil, err
	}

	// ChunksLen and metadata fields
	mdIndices := []int{7, 8, 9, 10}

	touchedChunks := c.sortedTouchedChunks()
	chunkIndices := make([]int, 0, len(touchedChunks)*2)
	for k := range touchedChunks {
		// 6144 is global index for first chunk's node
		// Each chunk node has two children: FIO, code
		chunkIdx := 6144 + k
		chunkIndices = append(chunkIndices, chunkIdx*2)
		chunkIndices = append(chunkIndices, chunkIdx*2+1)
	}

	p, err := tree.ProveMulti(append(mdIndices, chunkIndices...))
	if err != nil {
		return nil, err
	}

	return p, nil
}

func (c *Contract) sortedTouchedChunks() []int {
	touched := make([]int, 0, len(c.touchedChunks))
	for k := range c.touchedChunks {
		touched = append(touched, k)
	}
	sort.Ints(touched)
	return touched
}

type ProofStats struct {
	Indices    int
	ZeroLevels int
	Hashes     int
	Leaves     int
}

func NewProofStats(p *ssz.CompressedMultiproof) (*ProofStats, error) {
	stats := &ProofStats{Indices: len(p.Indices) * 2, ZeroLevels: len(p.ZeroLevels) * 1}
	for _, v := range p.Hashes {
		stats.Hashes += len(v)
	}
	for _, v := range p.Leaves {
		stats.Leaves += len(v)
	}
	return stats, nil
}

func (ps *ProofStats) Add(o *ProofStats) {
	ps.Indices += o.Indices
	ps.ZeroLevels += o.ZeroLevels
	ps.Hashes += o.Hashes
	ps.Leaves += o.Leaves
}

func (ps *ProofStats) Sum() int {
	return ps.Indices + ps.ZeroLevels + ps.Hashes + ps.Leaves
}

type RLPStats struct {
	RLPSize    int
	UnRLPSize  int
	SnappySize int
}

func NewRLPStats(p *ssz.Multiproof, cp *ssz.CompressedMultiproof) (*RLPStats, error) {
	stats := &RLPStats{}

	rlpProof, err := cp.Serialize()
	if err != nil {
		return nil, err
	}
	stats.RLPSize = len(rlpProof)

	// Measure snappy size of uncompressed proof
	unrlpProof, err := p.Serialize()
	if err != nil {
		return nil, err
	}
	stats.UnRLPSize = len(unrlpProof)
	compressedUnRLP := snappy.Encode(nil, unrlpProof)
	stats.SnappySize = len(compressedUnRLP)

	return stats, nil
}

func (rs *RLPStats) Add(o *RLPStats) {
	rs.RLPSize += o.RLPSize
	rs.UnRLPSize += o.UnRLPSize
	rs.SnappySize += o.SnappySize
}
