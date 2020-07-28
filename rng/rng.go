package rng

import (
	"fmt"

	"github.com/renproject/secp256k1"
	"github.com/renproject/shamir"
	"github.com/renproject/surge"

	"github.com/renproject/mpc/open"
	"github.com/renproject/mpc/rng/compute"
)

// RNGer describes the structure of the Random Number Generation machine. The
// machine can be used for an arbitrary number of invocations of RNG, however
// each instance is specific to the set of machine indices it was constructed
// with, as well as the batch size, reconstruction threshold and Pedersen
// Commitment Scheme Parameter.
//
// RNGer can exist in one of the following states:
// - Init
// - WaitingOpen
// - Done
//
// A new instance of RNGer can be created by calling:
// - New(index, indices, b, k, h)
//
// State transitions can be triggered by three different functions:
// - TransitionShares(setsOfShares, setsOfCommitments)
// - TransitionOpen(openings)
// - Reset
//
// Every state transition function returns a transition event, depending on how
// the inputs were processed. The various state transitions are as follows:
// - state(Init)
//	 - TransitionShares
//			|
//			|__ Invalid Shares --> event(CommitmentsConstructed) --> state(WaitingOpen)
//			|__ Valid Shares   --> event(SharesConstructed)      --> state(WaitingOpen)
//	 - TransitionOpen
//			|
//			|__ Invalid/Valid Openings --> event(OpeningsIgnored) --> state(Init)
//	 - Reset
//			|
//			|__ Any --> event(Reset) --> state(Init)
//
// - state(WaitingOpen)
//	 - TransitionShares
//			|
//			|__ Invalid/Valid Shares --> event(SharesIgnored) --> state(WaitingOpen)
//	 - TransitionOpen
//			|
//			|__ Invalid Openings     --> event(OpeningsIgnored)      --> state(WaitingOpen)
//			|__ Valid Openings       --> event(OpeningsAdded)        --> state(WaitingOpen)
//			|__ Valid Openings (kth) --> event(RNGsReconstructed)    --> state(Done)
//	 - Reset
//			|
//			|__ Any --> event(Reset) --> state(Init)
type RNGer struct {
	// index signifies the given RNG state machine's index.
	index secp256k1.Fn

	// indices signifies the list of all such RNG state machines participating
	// in the RNG protocol.
	indices []secp256k1.Fn

	// batchSize signifies the number of unbiased random numbers that will be
	// generated on successful execution of the RNG protocol.
	batchSize uint32

	// threshold signifies the reconstruction threshold (k), or the minimum
	// number of valid openings required before a random number can be
	// reconstructed by polynomial interpolation.
	threshold uint32

	// opener is the Opener state machine operating within the RNG state
	// machine As the RNG machine receives openings from other players, the
	// opener state machine also transitions, to eventually reconstruct the
	// batchSize number of secrets.
	opener open.Opener
}

// N returns the number of machine replicas participating in the RNG protocol.
func (rnger RNGer) N() int {
	return len(rnger.indices)
}

// BatchSize returns the batch size of the RNGer state machine.  This also
// denotes the number of random numbers that can possibly be generated after a
// successful execution of all state transitions.
func (rnger RNGer) BatchSize() uint32 {
	return rnger.batchSize
}

// Threshold returns the reconstruction threshold for every set of shares.
// This is the same as `k`, or the minimum number of openings required to be
// able to reconstruct the random numbers.
func (rnger RNGer) Threshold() uint32 {
	return rnger.threshold
}

// New creates a new RNG state machine for a given batch size.
// - Inputs
// 	 - ownIndex is the current machine's index
// 	 - indices is the set of player indices
// 	 - b is the number of random numbers generated in one invocation of the protocol
// 	 - k is the reconstruction threshold for every random number
// 	 - h is the Pedersen Commitment Parameter, a point on elliptic curve
//
// - Returns
//	 - TransitionEvent is the `Initialised` event emitted on creation
//	 - RNGer the newly created RNGer instance
func New(
	ownIndex secp256k1.Fn,
	indices []secp256k1.Fn,
	b, k uint32,
	h secp256k1.Point,
	setsOfShares []shamir.VerifiableShares,
	setsOfCommitments [][]shamir.Commitment,
	isZero bool,
) (TransitionEvent, RNGer, map[secp256k1.Fn]shamir.VerifiableShares, []shamir.Commitment) {
	// Declare variable to hold RNG machine's computed shares and commitments
	// and allocate necessary memory.
	commitments := make([]shamir.Commitment, b)
	for i := range commitments {
		commitments[i] = shamir.Commitment{}
	}
	openingsMap := make(map[secp256k1.Fn]shamir.VerifiableShares)
	for _, index := range indices {
		openingsMap[index] = make(shamir.VerifiableShares, 0, b)
	}

	// Create an instance of the Opener state machine within the RNG state
	// machine.
	// FIXME: Move transitionShares logic into here to avoid having to have
	// this temporary opener.
	commitmentBatch := []shamir.Commitment{shamir.Commitment{secp256k1.Point{}}}
	opener := open.New(commitmentBatch, indices, h)

	rnger := RNGer{
		index:     ownIndex,
		indices:   indices,
		batchSize: b,
		threshold: k,
		opener:    opener,
	}

	event, openingsMap, _, commitments := rnger.transitionShares(setsOfShares, setsOfCommitments, isZero, h)

	return event, rnger, openingsMap, commitments
}

// TransitionShares performs the state transition for the RNG state machine
// from `Init` to `WaitingOpen`, upon receiving `b` sets of verifiable shares
// and their respective commitments. The machine should locally compute its
// own shares from the received sets of shares.
//
// - Inputs
//   - setsOfShares are the b sets of verifiable shares from the player's BRNG
//   	outputs
//  	 - MUST be of length equal to the batch size to be valid
//  	 - For invalid sets of shares, a nil slice []shamir.VerifiableShares{}
//  	 	MUST be supplied
//  	 - If the above checks are met, we assume that every set of verifiable
//  	 	shares is valid
//  		 - We assume it has a length equal to the RNG's reconstruction
//  		 	threshold
//		 - For sets of shares of length not equal to the batch size, we ignore
//		 	those shares while simply computing the commitments
//   - setsOfCommitments are the b sets of commitments from the player's BRNG
//   	outputs
//  	 - We assume that the commitments are correct and valid (even if the
//  	 	shares may not be)
//  	 - MUST be of length equal to the batch size
//  	 - In case the sets of shares are invalid, we simply proceed with
//  	 	locally computing the Open commitments, since we assume the
//  	 	supplied sets of commitments are correct
//	 - isZero is a boolean indicating whether this is a Random Zero Generator or not
//
// - Returns
//   - TransitionEvent
//		 - SharesIgnored when the RNGer is not in `Init` state
//		 - CommitmentsConstructed when the sets of shares were invalid
//		 - SharesConstructed when the sets of shares were valid
//		 - RNGsReconstructed when the RNGer was able to reconstruct the random
//		 	shares (k = 1)
func (rnger *RNGer) transitionShares(
	setsOfShares []shamir.VerifiableShares,
	setsOfCommitments [][]shamir.Commitment,
	isZero bool,
	h secp256k1.Point,
) (
	TransitionEvent,
	map[secp256k1.Fn]shamir.VerifiableShares,
	shamir.VerifiableShares,
	[]shamir.Commitment,
) {
	// The required batch size for the BRNG outputs is k for RNG and k-1 for RZG
	var requiredBrngBatchSize int
	if isZero {
		requiredBrngBatchSize = int(rnger.threshold - 1)
	} else {
		requiredBrngBatchSize = int(rnger.threshold)
	}

	//
	// Commitments validity
	//

	if len(setsOfCommitments) != int(rnger.batchSize) {
		panic("invalid sets of commitments")
	}

	for _, coms := range setsOfCommitments {
		if len(coms) != requiredBrngBatchSize {
			panic("invalid sets of commitments")
		}
	}

	// Boolean to keep a track of whether shares computation should be ignored
	// or not. This is set to true if the sets of shares are invalid in any
	// way.
	ignoreShares := false

	// Ignore the shares if their number of sets does not match the number of
	// sets of commitments.
	if len(setsOfShares) != len(setsOfCommitments) {
		ignoreShares = true
	}

	//
	// Shares validity
	//

	if !ignoreShares {
		// Each set of shares in the batch should have the correct length.
		for _, shares := range setsOfShares {
			if len(shares) != requiredBrngBatchSize {
				panic("invalid set of shares")
			}
		}
	}

	// Declare variable to hold commitments to initialize the opener.
	locallyComputedCommitments := make([]shamir.Commitment, rnger.batchSize)

	// Construct the commitments for the batch of unbiased random numbers.
	commitments := make([]shamir.Commitment, rnger.batchSize)
	for i, setOfCommitments := range setsOfCommitments {
		// Compute the output commitment.
		commitments[i] = shamir.NewCommitmentWithCapacity(int(rnger.threshold))
		if isZero {
			commitments[i].Append(secp256k1.NewPointInfinity())
		}

		for _, c := range setOfCommitments {
			commitments[i].Append(c[0])
		}

		// Compute the share commitment and add it to the local set of
		// commitments.
		accCommitment := compute.ShareCommitment(rnger.index, setOfCommitments)
		if isZero {
			accCommitment.Scale(accCommitment, &rnger.index)
		}

		locallyComputedCommitments[i].Set(accCommitment)
	}

	// If the sets of shares are valid, we must construct the directed openings
	// to other players in the network.
	openingsMap := make(map[secp256k1.Fn]shamir.VerifiableShares, rnger.batchSize)
	if !ignoreShares {
		for _, j := range rnger.indices {
			for _, setOfShares := range setsOfShares {
				accShare := compute.ShareOfShare(j, setOfShares)
				if isZero {
					accShare.Scale(&accShare, &j)
				}
				openingsMap[j] = append(openingsMap[j], accShare)
			}
		}
	}

	// Reset the Opener machine with the computed commitments.
	rnger.opener = open.New(locallyComputedCommitments, rnger.indices, h)

	if ignoreShares {
		return CommitmentsConstructed, openingsMap, nil, commitments
	}

	// Supply the locally computed shares to the opener.
	event, secrets, decommitments := rnger.opener.HandleShareBatch(openingsMap[rnger.index])

	// This only happens when k = 1.
	if event == open.Done {
		shares := make(shamir.VerifiableShares, rnger.batchSize)
		for i, secret := range secrets {
			share := shamir.NewShare(rnger.index, secret)
			shares[i] = shamir.NewVerifiableShare(share, decommitments[i])
		}
		return RNGsReconstructed, openingsMap, shares, commitments
	}

	return SharesConstructed, openingsMap, nil, commitments
}

// TransitionOpen performs the state transition for the RNG state machine upon
// receiving directed openings of shares from other players.
//
// The state transition on calling TransitionOpen is described below:
// 1. RNG machine in state `Init` transitions to `WaitingOpen`
// 2. RNG machine in state `WaitingOpen` continues to be in state `WaitingOpen`
// 		if the machine has less than `k` opened shares, including the one
// 		supplied here.
// 3. RNG machine in state `WaitingOpen` transitions to `Done` if the machine
// 		now has `k` opened shares, including the one supplied here.
//
// Since the RNG machine is capable of generating `b` random numbers, we expect
// other players to supply `b` directed openings of their shares too.
//
// When the RNG machine transitions to the Done state, it has a share each
// `r_j` for the `b` random numbers.
//
// - Inputs
//   - openings are the directed openings
//	   - MUST be of length b (batch size)
//	   - Will be ignored if they're not consistent with their respective commitments
//
// - Returns
//   - TransitionEvent
// 		- OpeningsIgnored when the openings were invalid in form or consistency
// 		- OpeningsAdded when the openings were valid are were added to the opener
// 		- RNGsReconstructed when the set of openings was the kth valid set and
// 			hence the RNGer could reconstruct its shares for the unbiased
// 			random numbers
func (rnger *RNGer) TransitionOpen(openings shamir.VerifiableShares) (TransitionEvent, shamir.VerifiableShares) {
	// Pass these openings to the Opener state machine now that we have already
	// received valid commitments from BRNG outputs.
	event, secrets, decommitments := rnger.opener.HandleShareBatch(openings)

	switch event {
	case open.Done:
		shares := make(shamir.VerifiableShares, rnger.batchSize)
		for i, secret := range secrets {
			share := shamir.NewShare(rnger.index, secret)
			shares[i] = shamir.NewVerifiableShare(share, decommitments[i])
		}
		return RNGsReconstructed, shares
	case open.SharesAdded:
		return OpeningsAdded, nil
	default:
		return OpeningsIgnored, nil
	}
}

// SizeHint implements the surge.SizeHinter interface.
func (rnger RNGer) SizeHint() int {
	return rnger.index.SizeHint() +
		surge.SizeHint(rnger.indices) +
		surge.SizeHint(rnger.batchSize) +
		surge.SizeHint(rnger.threshold) +
		rnger.opener.SizeHint()
}

// Marshal implements the surge.Marshaler interface.
func (rnger RNGer) Marshal(buf []byte, rem int) ([]byte, int, error) {
	buf, rem, err := rnger.index.Marshal(buf, rem)
	if err != nil {
		return buf, rem, fmt.Errorf("marshaling index: %v", err)
	}
	buf, rem, err = surge.Marshal(rnger.indices, buf, rem)
	if err != nil {
		return buf, rem, fmt.Errorf("marshaling indices: %v", err)
	}
	buf, rem, err = surge.MarshalU32(uint32(rnger.batchSize), buf, rem)
	if err != nil {
		return buf, rem, fmt.Errorf("marshaling batchSize: %v", err)
	}
	buf, rem, err = surge.MarshalU32(uint32(rnger.threshold), buf, rem)
	if err != nil {
		return buf, rem, fmt.Errorf("marshaling threshold: %v", err)
	}
	buf, rem, err = rnger.opener.Marshal(buf, rem)
	if err != nil {
		return buf, rem, fmt.Errorf("marshaling opener: %v", err)
	}
	return buf, rem, nil
}

// Unmarshal implements the surge.Unmarshaler interface.
func (rnger *RNGer) Unmarshal(buf []byte, rem int) ([]byte, int, error) {
	buf, rem, err := rnger.index.Unmarshal(buf, rem)
	if err != nil {
		return buf, rem, fmt.Errorf("unmarshaling index: %v", err)
	}
	buf, rem, err = surge.Unmarshal(&rnger.indices, buf, rem)
	if err != nil {
		return buf, rem, fmt.Errorf("unmarshaling indices: %v", err)
	}
	buf, rem, err = surge.UnmarshalU32(&rnger.batchSize, buf, rem)
	if err != nil {
		return buf, rem, fmt.Errorf("unmarshaling batchSize: %v", err)
	}
	buf, rem, err = surge.UnmarshalU32(&rnger.threshold, buf, rem)
	if err != nil {
		return buf, rem, fmt.Errorf("unmarshaling threshold: %v", err)
	}
	buf, rem, err = rnger.opener.Unmarshal(buf, rem)
	if err != nil {
		return buf, rem, fmt.Errorf("unmarshaling opener: %v", err)
	}
	return buf, rem, nil
}

// Generate implements the quick.Generator interface.
/*
func (rnger RNGer) Generate(rand *rand.Rand, size int) reflect.Value {
	size /= 10

	indices := shamirutil.RandomIndices(rand.Intn(20) + 1)
	ownIndex := indices[rand.Intn(len(indices))]
	b := (rand.Uint32() % uint32(size)) + 1
	k := uint32(size)/b + 1
	h := secp256k1.RandomPoint()
	setsOfCommitments := make([][]shamir.Commitment, b)
	for i := range setsOfCommitments {
		setsOfCommitments[i] = make([]shamir.Commitment, k)
		for j := range setsOfCommitments[i] {
			setsOfCommitments[i][j] = shamir.NewCommitmentWithCapacity(int(k))
			for l := uint32(0); l < k; l++ {
				setsOfCommitments[i][j] = append(setsOfCommitments[i][j], secp256k1.RandomPoint())
			}
		}
	}
	setsOfShares := make([]shamir.VerifiableShares, b)
	for i := range setsOfShares {
		setsOfShares[i] = make(shamir.VerifiableShares, k)
		for j := range setsOfShares[i] {
			setsOfShares[i][j].Share.Index = secp256k1.RandomFn()
			setsOfShares[i][j].Share.Value = secp256k1.RandomFn()
			setsOfShares[i][j].Decommitment = secp256k1.RandomFn()
		}
	}
	isZero := rand.Int31()&1 == 1
	_, v := New(ownIndex, indices, b, k, h, setsOfShares, setsOfCommitments, isZero)
	return reflect.ValueOf(v)
}
*/
