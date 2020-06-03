package rng_test

import (
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/renproject/shamir"
	"github.com/renproject/shamir/curve"
	stu "github.com/renproject/shamir/testutil"

	"github.com/renproject/mpc/open"
	"github.com/renproject/mpc/rng"
	rtu "github.com/renproject/mpc/rng/testutil"
)

var _ = Describe("Rng", func() {
	rand.Seed(int64(time.Now().Nanosecond()))

	Describe("RNG Properties", func() {
		var n, b, k int
		var indices []open.Fn
		var index open.Fn
		var h curve.Point

		// Setup is run before every test. It randomises the test parameters
		Setup := func() (
			int,
			[]open.Fn,
			open.Fn,
			int,
			int,
			curve.Point,
		) {
			// n is the number of players participating in the RNG protocol
			// n ∈ [5, 10]
			n := 5 + rand.Intn(6)

			// indices represent the list of index for each player
			// They are Secp256k1N representations of sequential n values
			indices := stu.SequentialIndices(n)

			// index denotes the current player's index
			// This is a randomly chosen index from indices
			index := indices[rand.Intn(len(indices))]

			// b is the total number of random numbers to be generated
			// in one execution of RNG protocol, i.e. the batch number
			b := 3 + rand.Intn(3)

			// k is the threshold for random number generation, or the
			// minimum number of shares required to reconstruct the secret
			// in the secret sharing scheme. Based on our BRNG to RNG scheme,
			// k is also the number of times BRNG needs to be run before
			// using their outputs to generate an unbiased random number
			k := 3 + rand.Intn(n-3)

			// h is the elliptic curve point, used as the Pedersen Commitment
			// Scheme Parameter
			h := curve.Random()

			return n, indices, index, b, k, h
		}

		BeforeEach(func() {
			n, indices, index, b, k, h = Setup()
		})

		Context("State Transitions and Events", func() {
			Specify("Initialise RNG machine to Init state", func() {
				event, rnger := rng.New(index, indices, uint32(b), uint32(k), h)

				Expect(event).To(Equal(rng.Initialised))
				Expect(rnger.State()).To(Equal(rng.Init))
				Expect(rnger.N()).To(Equal(n))
				Expect(rnger.BatchSize()).To(Equal(uint32(b)))
				Expect(rnger.Threshold()).To(Equal(uint32(k)))
				Expect(rnger.HasConstructedShares()).ToNot(BeTrue())
			})

			Context("When in Init state", func() {
				Specify("Reset", func() {
					// If an RNG machine in the Init state is reset, it continues to be
					// in the init state
					_, rnger := rng.New(index, indices, uint32(b), uint32(k), h)

					event := rnger.Reset()

					Expect(event).To(Equal(rng.Reset))
					Expect(rnger.State()).To(Equal(rng.Init))
					Expect(rnger.N()).To(Equal(n))
					Expect(rnger.BatchSize()).To(Equal(uint32(b)))
					Expect(rnger.Threshold()).To(Equal(uint32(k)))
					Expect(rnger.HasConstructedShares()).ToNot(BeTrue())
				})

				Specify("Supply BRNG shares", func() {
					// If an RNG machine in the Init state is supplied with
					// valid sets of shares and commitments from its own BRNG outputs
					// it transitions to the WaitingOpen state
					setsOfShares, setsOfCommitments := rtu.GetBrngOutputs(indices, index, b, k, h)

					// Once we have `b` sets of shares and commitments
					// we are ready to transition the RNG machine
					_, rnger := rng.New(index, indices, uint32(b), uint32(k), h)
					event := rnger.TransitionShares(setsOfShares, setsOfCommitments)

					Expect(event).To(Equal(rng.SharesConstructed))
					Expect(rnger.State()).To(Equal(rng.WaitingOpen))
					Expect(rnger.HasConstructedShares()).To(BeTrue())
				})

				Specify("Supply BRNG shares of length not equal to batch size", func() {
					// If an RNG machine is supplied with BRNG outputs that don't match the
					// RNG machine's batch size, those shares are simply ignored and the
					// machine continues to be in the same state as before
					setsOfShares, setsOfCommitments := rtu.GetBrngOutputs(indices, index, b-1, k, h)
					_, rnger := rng.New(index, indices, uint32(b), uint32(k), h)
					event := rnger.TransitionShares(setsOfShares, setsOfCommitments)
					Expect(event).To(Equal(rng.SharesIgnored))
					Expect(rnger.State()).To(Equal(rng.Init))
					Expect(rnger.HasConstructedShares()).ToNot(BeTrue())

					setsOfShares, setsOfCommitments = rtu.GetBrngOutputs(indices, index, b+1, k, h)
					_, rnger = rng.New(index, indices, uint32(b), uint32(k), h)
					event = rnger.TransitionShares(setsOfShares, setsOfCommitments)
					Expect(event).To(Equal(rng.SharesIgnored))
					Expect(rnger.State()).To(Equal(rng.Init))
					Expect(rnger.HasConstructedShares()).ToNot(BeTrue())
				})

				Specify("Supply BRNG shares of length not equal to commitments length", func() {
					// If an RNG machine is supplied with BRNG outputs that have different
					// lengths (batch size) for shares and commitment, those shares are simply
					// ignored and the machine continues to be in the same state as before
					setsOfShares, setsOfCommitments := rtu.GetBrngOutputs(indices, index, b, k, h)
					_, rnger := rng.New(index, indices, uint32(b), uint32(k), h)
					j := rand.Intn(b)
					setsOfShares = append(setsOfShares[:j], setsOfShares[j+1:]...)
					event := rnger.TransitionShares(setsOfShares, setsOfCommitments)

					Expect(event).To(Equal(rng.SharesIgnored))
					Expect(rnger.State()).To(Equal(rng.Init))
					Expect(rnger.HasConstructedShares()).ToNot(BeTrue())
				})

				Specify("Supply directed opening", func() {
					// If an RNG machine in the Init state is supplied with a valid directed opening
					// it does not react to that and simply ignores it
					// Only after having constructed its own shares, and being in the WaitingOpen
					// state, it will handle the directed openings

					// get a `from` index that is different than own index
					from := indices[rand.Intn(len(indices))]
					for from.Eq(&index) {
						from = indices[rand.Intn(len(indices))]
					}

					// get this `from` index's sets of shares and commitments
					// also compute its openings for the player
					setsOfShares, setsOfCommitments := rtu.GetBrngOutputs(indices, index, b, k, h)
					openings, commitments := rtu.GetDirectedOpenings(setsOfShares, setsOfCommitments, index)

					// initialise player's RNG machine and supply openings
					_, rnger := rng.New(index, indices, uint32(b), uint32(k), h)
					event := rnger.TransitionOpen(from, openings, commitments)

					Expect(event).To(Equal(rng.OpeningsIgnored))
					Expect(rnger.State()).To(Equal(rng.Init))
					Expect(rnger.HasConstructedShares()).ToNot(BeTrue())
				})
			})

			Context("When in WaitingOpen state", func() {
				var rnger rng.RNGer
				var openingsByPlayer map[open.Fn]shamir.VerifiableShares
				var commitmentsByPlayer map[open.Fn][]shamir.Commitment
				var ownSetsOfShares []shamir.VerifiableShares
				var ownSetsOfCommitments [][]shamir.Commitment

				// TransitionToWaitingOpen generates a new instance of RNG machine
				// and transitions it to the `WaitingOpen` state
				TransitionToWaitingOpen := func(
					index open.Fn,
					indices []open.Fn,
					b, k int,
					h curve.Point,
				) {
					_, rnger = rng.New(index, indices, uint32(b), uint32(k), h)

					openingsByPlayer, commitmentsByPlayer, ownSetsOfShares, ownSetsOfCommitments = rtu.GetAllDirectedOpenings(indices, index, b, k, h)

					event := rnger.TransitionShares(ownSetsOfShares, ownSetsOfCommitments)
					Expect(event).To(Equal(rng.SharesConstructed))
				}

				JustBeforeEach(func() {
					TransitionToWaitingOpen(index, indices, b, k, h)
				})

				Specify("Reset", func() {
					// When an RNG machine in the WaitingOpen state is reset, it transitions
					// to the Init state having forgotten its constructed shares
					event := rnger.Reset()

					Expect(event).To(Equal(rng.Reset))
					Expect(rnger.State()).To(Equal(rng.Init))
					Expect(rnger.HasConstructedShares()).ToNot(BeTrue())
				})

				Specify("Supply BRNG shares", func() {
					// When an RNG machine in the WaitingOpen state is supplied BRNG shares
					// it simply ignores them and continues to be in the same state
					setsOfShares, setsOfCommitments := rtu.GetBrngOutputs(indices, index, b, k, h)
					event := rnger.TransitionShares(setsOfShares, setsOfCommitments)

					Expect(event).To(Equal(rng.SharesIgnored))
					Expect(rnger.State()).To(Equal(rng.WaitingOpen))
				})

				Specify("Supply directed opening", func() {
					// When the RNG machine receives a valid set of directed openings from another player
					// it adds those to its opener and continues to be in the WaitingOpen state.
					//
					// get a random player who is not the current RNG machine's player
					from := indices[rand.Intn(len(indices))]
					for from.Eq(&index) {
						from = indices[rand.Intn(len(indices))]
					}

					event := rnger.TransitionOpen(from, openingsByPlayer[from], commitmentsByPlayer[from])

					Expect(event).To(Equal(rng.OpeningsAdded))
					Expect(rnger.State()).To(Equal(rng.WaitingOpen))
				})

				Specify("Supply directed opening when k-1 openings are already ready", func() {
					// When the RNG machine receives a valid set of directed openings from another player
					// and if this is the kth set (including its own), then the RNG machine is ready
					// to reconstruct the b unbiased random numbers
					//
					// The own player's openings have already been processed
					count := 1

					for _, from := range indices {
						// Ignore if its the current RNG player
						if from.Eq(&index) {
							continue
						}

						if count == k-1 {
							event := rnger.TransitionOpen(from, openingsByPlayer[from], commitmentsByPlayer[from])

							Expect(event).To(Equal(rng.RNGsReconstructed))
							Expect(rnger.State()).To(Equal(rng.Done))
							break
						}

						if count < k-1 {
							event := rnger.TransitionOpen(from, openingsByPlayer[from], commitmentsByPlayer[from])

							Expect(event).To(Equal(rng.OpeningsAdded))
							Expect(rnger.State()).To(Equal(rng.WaitingOpen))
							count = count + 1
						}
					}
				})
			})

			Context("When in Done state", func() {
				Specify("Supply BRNG shares", func() {
					// TODO
				})

				Specify("Supply directed opening", func() {
					// TODO
				})

				Specify("Reset", func() {
					// TODO
				})
			})
		})

		Context("Computations", func() {
			It("Correctly computes own shares and commitments", func() {
				// TODO
			})

			It("Correctly reconstructs the unbiased random numbers", func() {
				// TODO
			})
		})

		Context("Marshaling and Unmarshaling", func() {
			It("Should be equal after marshaling and unmarshaling", func() {
				// TODO
			})

			It("Should fail when marshaling with not enough bytes", func() {
				// TODO
			})

			It("Should fail when unmarshaling with not enough bytes", func() {
				// TODO
			})
		})
	})

	Describe("Network Simulation", func() {

	})
})
