package config

import "testing"

func TestClientInjectShareSumsToInjectSpeed(t *testing.T) {
	tests := []struct {
		inject  int64
		clients int
		want    []int64
	}{
		{200, 1, []int64{200}},
		{200, 4, []int64{50, 50, 50, 50}},
		{200, 7, []int64{29, 29, 29, 29, 28, 28, 28}},
		{195, 7, []int64{28, 28, 28, 28, 28, 28, 27}},
	}
	for _, tc := range tests {
		cfg := &Config{InjectSpeed: tc.inject, ClientNum: tc.clients, NodeNum: 7}
		var sum int64
		for id := 1; id <= tc.clients; id++ {
			got := cfg.ClientInjectShare(id)
			if got != tc.want[id-1] {
				t.Fatalf("inject %d over %d clients: client %d share = %d, want %d", tc.inject, tc.clients, id, got, tc.want[id-1])
			}
			sum += got
		}
		if sum != tc.inject {
			t.Fatalf("inject %d over %d clients: shares sum to %d", tc.inject, tc.clients, sum)
		}
	}
}

func TestClientNameAndCount(t *testing.T) {
	single := &Config{NodeNum: 4}
	if single.ClientCount() != 1 || single.ClientName(1) != LegacyClientName {
		t.Fatalf("absent client_num: count %d name %q, want 1 %q", single.ClientCount(), single.ClientName(1), LegacyClientName)
	}
	multi := &Config{NodeNum: 7, ClientNum: 7}
	if got := multi.ClientName(3); got != "client3" {
		t.Fatalf("ClientName(3) = %q, want client3", got)
	}
}

func TestValidateClients(t *testing.T) {
	tests := []struct {
		clients int
		nodes   int64
		wantErr bool
	}{
		{0, 4, false},
		{1, 4, false},
		{4, 4, false},
		{5, 4, true},
		{-1, 4, true},
	}
	for _, tc := range tests {
		err := (&Config{ClientNum: tc.clients, NodeNum: tc.nodes}).ValidateClients()
		if (err != nil) != tc.wantErr {
			t.Fatalf("client_num %d node_num %d: err = %v, wantErr %t", tc.clients, tc.nodes, err, tc.wantErr)
		}
	}
}
