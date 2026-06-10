package node

import "testing"

func TestFairnessComplaintThresholdCount(t *testing.T) {
	node := &Node{
		fNodes: 1,
		complainBox: map[fairnessComplainKey]map[int]bool{
			{digest: testDigest(1), view: 1}: {
				1: true,
			},
			{digest: testDigest(2), view: 1}: {
				1: true,
				2: true,
			},
			{digest: testDigest(3), view: 1}: {
				1: true,
				2: true,
				3: true,
			},
		},
	}

	threshold, count := node.fairnessComplaintThresholdCount()
	if threshold != 2 {
		t.Fatalf("threshold = %d, want 2", threshold)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

func testDigest(seed byte) [32]byte {
	var digest [32]byte
	digest[0] = seed
	return digest
}
