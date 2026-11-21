// loadaddrs prints N simnet P2SH addresses for dex/testing/var/load.sh: one per
// sendmany output, as sendmany takes one output per address. Their script hashes
// are sha256 of the index, so nobody holds a script that spends them.
package main

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"

	"github.com/monetarium/monetarium-node/chaincfg"
	"github.com/monetarium/monetarium-node/txscript/stdaddr"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: loadaddrs N")
		os.Exit(2)
	}
	n, err := strconv.Atoi(os.Args[1])
	if err != nil || n < 1 {
		fmt.Fprintln(os.Stderr, "loadaddrs: N must be a positive integer")
		os.Exit(2)
	}
	params := chaincfg.SimNetParams()
	for i := range n {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(i))
		h := sha256.Sum256(b[:])
		addr, err := stdaddr.NewAddressScriptHashV0FromHash(h[:20], params)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(addr)
	}
}
