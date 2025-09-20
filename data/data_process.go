package data

import (
	"encoding/csv"
	"math/big"
	"os"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

const (
	DataDir = "data/len3_data.csv"
)

var log = logger.NewLogger(0, "data")

func ReadData(maxTxNum int64) []*core.Transaction {
	csvFile, err := os.Open(DataDir)
	if err != nil {
		log.Error("failed to open csv file: %v", err)
	}
	defer csvFile.Close()

	csvReader := csv.NewReader(csvFile)
	records, err := csvReader.ReadAll()
	if err != nil {
		log.Error("failed to read csv file: %v", err)
		return nil
	}

	var txs []*core.Transaction

	for i, record := range records {
		if int64(i) > maxTxNum {
			break
		}
		// skip the header line
		if i == 0 {
			continue
		}
		// skip the line with less than 3 fields
		if len(record) < 3 {
			continue
		}
		sender := record[0]
		receiver := record[1]
		amountStr := record[2]
		amount := new(big.Int)
		_, ok := amount.SetString(amountStr, 10)
		if !ok {
			log.Error("failed to parse value: %v", amountStr)
			continue
		}
		tx := core.NewTransaction(sender, receiver, amount)
		txs = append(txs, tx)
	}

	return txs
}
