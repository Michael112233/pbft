package config

import "fmt"

// LegacyClientName is the name of the only client when client_num is 1, and the
// name a node gives a client stream that does not announce one.
const LegacyClientName = "client"

// ClientCount returns client_num, with 0 (key absent) meaning one client.
func (c *Config) ClientCount() int {
	if c.ClientNum <= 0 {
		return 1
	}
	return c.ClientNum
}

// ValidateClients checks client_num: client i is colocated with node i, so there
// can be at most node_num clients.
func (c *Config) ValidateClients() error {
	if c.ClientNum < 0 {
		return fmt.Errorf("client_num must be >= 1, got %d", c.ClientNum)
	}
	if int64(c.ClientCount()) > c.NodeNum {
		return fmt.Errorf("client_num %d exceeds node_num %d; client i is colocated with node i", c.ClientCount(), c.NodeNum)
	}
	return nil
}

// ClientName returns the name client id signs its requests with. A single client
// keeps the legacy name, so its requests, logs and files are unchanged.
func (c *Config) ClientName(id int) string {
	if c.ClientCount() == 1 {
		return LegacyClientName
	}
	return fmt.Sprintf("client%d", id)
}

// ClientInjectShare returns client id's share of inject_speed (requests per 24 ms
// tick). The remainder goes one each to the lowest ids, so the shares always sum
// to inject_speed: 200 over 7 clients is 29,29,29,29,28,28,28.
func (c *Config) ClientInjectShare(id int) int64 {
	n := int64(c.ClientCount())
	share := c.InjectSpeed / n
	if int64(id) <= c.InjectSpeed%n {
		share++
	}
	return share
}
