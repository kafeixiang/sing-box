package trafficontrol

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/common/compatible"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/x/list"

	"github.com/gofrs/uuid/v5"
)

type Manager struct {
	uploadTotal   map[string]*atomic.Int64
	downloadTotal map[string]*atomic.Int64

	connections             compatible.Map[uuid.UUID, Tracker]
	closedConnectionsAccess sync.Mutex
	closedConnections       list.List[TrackerMetadata]
	// process     *process.Process
	memory uint64
}

func NewManager(outbounds, endpoints []string) *Manager {
	m := &Manager{
		uploadTotal:   make(map[string]*atomic.Int64, len(outbounds)+len(endpoints)),
		downloadTotal: make(map[string]*atomic.Int64, len(outbounds)+len(endpoints)),
	}
	for _, out := range outbounds {
		m.uploadTotal[out] = new(atomic.Int64)
		m.downloadTotal[out] = new(atomic.Int64)
	}
	for _, end := range endpoints {
		m.uploadTotal[end] = new(atomic.Int64)
		m.downloadTotal[end] = new(atomic.Int64)
	}
	return m
}

func (m *Manager) Join(c Tracker) {
	m.connections.Store(c.Metadata().ID, c)
}

func (m *Manager) Leave(c Tracker) {
	metadata := c.Metadata()
	_, loaded := m.connections.LoadAndDelete(metadata.ID)
	if loaded {
		metadata.ClosedAt = time.Now()
		m.closedConnectionsAccess.Lock()
		defer m.closedConnectionsAccess.Unlock()
		if m.closedConnections.Len() >= 1000 {
			m.closedConnections.PopFront()
		}
		m.closedConnections.PushBack(metadata)
	}
}

func (m *Manager) PushUploaded(size int64, outbound string) {
	m.uploadTotal[outbound].Add(size)
}

func (m *Manager) PushDownloaded(size int64, outbound string) {
	m.downloadTotal[outbound].Add(size)
}

func (m *Manager) TotalOutbound(outbound string) (up int64, down int64) {
	if upCounter, ok := m.uploadTotal[outbound]; ok {
		up = upCounter.Load()
	}
	if downCounter, ok := m.downloadTotal[outbound]; ok {
		down = downCounter.Load()
	}
	return
}

func (m *Manager) Total() (up int64, down int64) {
	for _, v := range m.uploadTotal {
		up += v.Load()
	}
	for _, v := range m.downloadTotal {
		down += v.Load()
	}
	return
}

func (m *Manager) ConnectionsLen() int {
	return m.connections.Len()
}

func (m *Manager) Connections() []TrackerMetadata {
	var connections []TrackerMetadata
	m.connections.Range(func(_ uuid.UUID, value Tracker) bool {
		connections = append(connections, value.Metadata())
		return true
	})
	return connections
}

func (m *Manager) ClosedConnections() []TrackerMetadata {
	m.closedConnectionsAccess.Lock()
	defer m.closedConnectionsAccess.Unlock()
	return m.closedConnections.Array()
}

func (m *Manager) Connection(id uuid.UUID) Tracker {
	connection, loaded := m.connections.Load(id)
	if !loaded {
		return nil
	}
	return connection
}

func (m *Manager) Snapshot() *Snapshot {
	var connections []Tracker
	m.connections.Range(func(_ uuid.UUID, value Tracker) bool {
		if value.Metadata().OutboundType != C.TypeDNS {
			connections = append(connections, value)
		}
		return true
	})

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	m.memory = memStats.StackInuse + memStats.HeapInuse + memStats.HeapIdle - memStats.HeapReleased
	uploadTotal, downloadTotal := m.Total()

	return &Snapshot{
		Upload:      uploadTotal,
		Download:    downloadTotal,
		Connections: connections,
		Memory:      m.memory,
	}
}

func (m *Manager) ResetStatistic() {
	for _, u := range m.uploadTotal {
		u.Store(0)
	}
	for _, d := range m.downloadTotal {
		d.Store(0)
	}
}

type Snapshot struct {
	Download    int64
	Upload      int64
	Connections []Tracker
	Memory      uint64
}

func (s *Snapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"downloadTotal": s.Download,
		"uploadTotal":   s.Upload,
		"connections":   common.Map(s.Connections, func(t Tracker) TrackerMetadata { return t.Metadata() }),
		"memory":        s.Memory,
	})
}
