package data

import (
	"github.com/michael112233/pbft/core"
)

func PrintTxs(txs []*core.Transaction, num int) {
	for i, tx := range txs[:num] {
		log.Test("Transaction %d: %v\n", i, tx)
	}
}
