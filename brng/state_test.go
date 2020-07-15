package brng_test

import (
	"fmt"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/renproject/mpc/brng"
)

var _ = Describe("State", func() {
	rand.Seed(int64(time.Now().Nanosecond()))

	Context("String", func() {
		Specify("the Stringer interface should be properly implemented", func() {
			var expected string
			for i := 0; i < 256; i++ {
				switch i {
				case 0:
					expected = "Init"
				case 1:
					expected = "Waiting"
				case 2:
					expected = "Ok"
				case 3:
					expected = "Error"
				default:
					expected = fmt.Sprintf("Unknown(%v)", uint8(i))
				}

				Expect(State(i).String()).To(Equal(expected))
			}
		})
	})
})
