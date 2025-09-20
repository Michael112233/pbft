package network

import (
	"net"
	"sync"
)

// ConnectionsMap is a safe map for concurrent use.
type ConnectionsMap struct {
	sync.RWMutex
	Connections map[string]net.Conn
}

// NewConnectionsMap creates a new ConnectionsMap.
func NewConnectionsMap() *ConnectionsMap {
	return &ConnectionsMap{
		Connections: make(map[string]net.Conn),
	}
}

// Add adds a connection to the map.
func (cm *ConnectionsMap) Add(key string, conn net.Conn) {
	cm.Lock()
	cm.Connections[key] = conn
	cm.Unlock()
}

// Get retrieves a connection by key.
func (cm *ConnectionsMap) Get(key string) (net.Conn, bool) {
	cm.RLock()
	conn, ok := cm.Connections[key]
	cm.RUnlock()
	return conn, ok
}

// Remove removes a connection by key.
func (cm *ConnectionsMap) Remove(key string) {
	cm.Lock()
	delete(cm.Connections, key)
	cm.Unlock()
}
