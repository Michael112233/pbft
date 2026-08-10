package node

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUpdateEpochThroughputCSV(t *testing.T) {
	em := newEpochCSVForTest(t, [][]string{
		{"epoch", "throughput", "proposal_rate", "shadow_count"},
		{"1", "10.000000", "20.000000", "2"},
		{"2", "30.000000", "40.000000", "4"},
	})

	if err := em.updateEpochThroughputCSV(1, 99.1234567); err != nil {
		t.Fatalf("updateEpochThroughputCSV() error = %v", err)
	}

	if err := em.writeEpochCSV(3, 50, 60, 6); err != nil {
		t.Fatalf("writeEpochCSV() error = %v", err)
	}

	got := readEpochCSVForTest(t, em)
	want := [][]string{
		{"epoch", "throughput", "proposal_rate", "shadow_count"},
		{"1", "99.123457", "20.000000", "2"},
		{"2", "30.000000", "40.000000", "4"},
		{"3", "50.000000", "60.000000", "6"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CSV records = %#v, want %#v", got, want)
	}
}

func TestUpdateEpochThroughputCSVMissingEpochDoesNotModifyFile(t *testing.T) {
	em := newEpochCSVForTest(t, [][]string{
		{"epoch", "throughput", "proposal_rate", "shadow_count"},
		{"1", "10.000000", "20.000000", "2"},
	})
	before, err := os.ReadFile(em.csvFile.Name())
	if err != nil {
		t.Fatalf("read CSV before update: %v", err)
	}

	if err := em.updateEpochThroughputCSV(2, 99); err == nil {
		t.Fatal("updateEpochThroughputCSV() error = nil, want missing epoch error")
	}

	after, err := os.ReadFile(em.csvFile.Name())
	if err != nil {
		t.Fatalf("read CSV after update: %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("CSV changed after missing epoch update:\nbefore: %q\nafter:  %q", before, after)
	}
}

func TestUpdateEpochThroughputCSVRejectsMalformedCSV(t *testing.T) {
	em := newEpochCSVForTest(t, [][]string{
		{"epoch", "throughput", "proposal_rate", "shadow_count"},
		{"1", "10.000000"},
	})

	if err := em.updateEpochThroughputCSV(1, 99); err == nil {
		t.Fatal("updateEpochThroughputCSV() error = nil, want malformed CSV error")
	}
}

func TestUpdateEpochThroughputCSVRejectsUninitializedWriter(t *testing.T) {
	em := &EpochManager{}

	if err := em.updateEpochThroughputCSV(1, 99); err == nil {
		t.Fatal("updateEpochThroughputCSV() error = nil, want uninitialized writer error")
	}
}

func TestRecordShadowSuspicionTotalReturnsRawEpochDelta(t *testing.T) {
	em := &EpochManager{}

	if _, _, complete := em.recordShadowSuspicionTotal(1, 2); complete {
		t.Fatal("first execution unexpectedly completed a shadow epoch")
	}
	epoch, count, complete := em.recordShadowSuspicionTotal(EPOCH_INTERVAL, 7)
	if !complete {
		t.Fatal("epoch boundary did not complete a shadow epoch")
	}
	if epoch != 1 {
		t.Fatalf("completed epoch = %d, want 1", epoch)
	}
	if count != 5 {
		t.Fatalf("shadow count = %d, want 5", count)
	}

	epoch, count, complete = em.recordShadowSuspicionTotal(2*EPOCH_INTERVAL, 10)
	if !complete {
		t.Fatal("second epoch boundary did not complete a shadow epoch")
	}
	if epoch != 2 || count != 3 {
		t.Fatalf("second epoch result = (%d, %d), want (2, 3)", epoch, count)
	}
}

func newEpochCSVForTest(t *testing.T, records [][]string) *EpochManager {
	t.Helper()

	path := filepath.Join(t.TempDir(), "epoch_metrics.csv")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		t.Fatalf("open test epoch CSV: %v", err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close test epoch CSV: %v", err)
		}
	})

	writer := csv.NewWriter(file)
	writer.WriteAll(records)
	if err := writer.Error(); err != nil {
		t.Fatalf("write test epoch CSV: %v", err)
	}

	return &EpochManager{
		csvFile:   file,
		csvWriter: writer,
	}
}

func readEpochCSVForTest(t *testing.T, em *EpochManager) [][]string {
	t.Helper()

	em.csvWriter.Flush()
	if err := em.csvWriter.Error(); err != nil {
		t.Fatalf("flush test epoch CSV: %v", err)
	}

	file, err := os.Open(em.csvFile.Name())
	if err != nil {
		t.Fatalf("open test epoch CSV for reading: %v", err)
	}
	defer file.Close()

	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatalf("read test epoch CSV: %v", err)
	}
	return records
}
