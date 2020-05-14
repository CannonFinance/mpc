package brng

import (
	"github.com/renproject/secp256k1-go"
	"github.com/renproject/shamir"
	"github.com/renproject/shamir/curve"
)

// State is an enumeration of the possible states for the BRNG state machine.
type State uint8

const (
	Init = State(iota)
	Waiting
	Ok
	Error
)

type BRNGer struct {
	state State

	sharer  shamir.VSSharer
	checker shamir.VSSChecker
}

// State returns the current state of the state machine.
func (brnger *BRNGer) State() State {
	return brnger.state
}

// New creates a new BRNG state machine for the given indices and pedersen
// parameter h.
func New(indices []secp256k1.Secp256k1N, h curve.Point) BRNGer {
	state := Init
	sharer := shamir.NewVSSharer(indices, h)
	checker := shamir.NewVSSChecker(h)

	return BRNGer{state, sharer, checker}
}

func (brnger *BRNGer) TransitionStart(k, b int) Row {
	if brnger.state != Init {
		return nil
	}

	row := MakeRow(brnger.sharer.N(), k, b)
	for i := range row {
		r := secp256k1.RandomSecp256k1N()
		brnger.sharer.Share(&row[i].shares, &row[i].commitment, r, k)
	}

	brnger.state = Waiting

	return row
}

func (brnger *BRNGer) TransitionSlice(slice Slice) (shamir.VerifiableShares, []shamir.Commitment) {
	if brnger.state != Waiting {
		return nil, nil
	}

	if !slice.HasValidForm() {
		brnger.state = Error
		return nil, nil
	}

	// TODO: Should we try to reconstruct on a per column basis? Or just give
	// up if any of the columns in the slice are invalid?
	for _, c := range slice {
		for i := 0; i < len(c.shares); i++ {
			if !brnger.checker.IsValid(&c.commitments[i], &c.shares[i]) {
				brnger.state = Error
				return nil, nil
			}
		}
	}

	// Construct the output share(s).
	shares := make(shamir.VerifiableShares, slice.BatchSize())
	commitments := make([]shamir.Commitment, slice.BatchSize())
	for i, c := range slice {
		share := c.shares[0]
		for j := 1; j < len(c.shares); j++ {
			share.Add(&share, &c.shares[j])
		}
		shares[i] = share

		var commitment shamir.Commitment
		commitment.Set(c.commitments[0])
		for j := 1; j < len(c.commitments); j++ {
			commitment.Add(&commitment, &c.commitments[j])
		}
		commitments[i] = commitment
	}

	brnger.state = Ok
	return shares, commitments
}

// Reset sets the state of the state machine to the Init state.
func (brnger *BRNGer) Reset() {
	brnger.state = Init
}
