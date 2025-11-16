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
		return nil
	}
	defer csvFile.Close()

	csvReader := csv.NewReader(csvFile)
	var txs []*core.Transaction
	lineCount := int64(0)

	// Read line by line instead of reading all at once
	for {
		record, err := csvReader.Read()
		if err != nil {
			if err.Error() == "EOF" {
				break // End of file
			}
			log.Error("failed to read csv line: %v", err)
			continue
		}

		// Skip the header line
		if lineCount == 0 {
			lineCount++
			continue
		}

		// Check if we've reached the maximum number of transactions
		if lineCount > maxTxNum {
			break
		}

		// Skip lines with less than 3 fields
		if len(record) < 3 {
			lineCount++
			continue
		}

		sender := record[0]
		receiver := record[1]
		amountStr := record[2]
		amount := new(big.Int)
		_, ok := amount.SetString(amountStr, 10)
		if !ok {
			log.Error("failed to parse value: %v", amountStr)
			lineCount++
			continue
		}
		tx := core.NewTransaction(sender, receiver, amount)
		txs = append(txs, tx)
		lineCount++
	}

	log.Info("Read %d transactions from data file", len(txs))
	return txs
}
