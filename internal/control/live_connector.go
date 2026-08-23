package control

import (
	"sync/atomic"

	"github.com/amitkhosla/rah/internal/connectors/document"
	"github.com/amitkhosla/rah/internal/connectors/messaging"
	"github.com/amitkhosla/rah/internal/connectors/sftp"
	"github.com/amitkhosla/rah/internal/storage"
)

// LiveDocConnMgr wraps DocumentConnectorManager with an atomic pointer so that
// compiled closures capture *LiveDocConnMgr once and always call through to the
// current manager — enabling live connector add/remove without flow re-baking.
type LiveDocConnMgr struct {
	p atomic.Pointer[document.DocumentConnectorManager]
}

func (l *LiveDocConnMgr) Get(name string) (document.DocumentProvider, bool) {
	m := l.p.Load()
	if m == nil {
		return nil, false
	}
	return m.Get(name)
}

func (l *LiveDocConnMgr) Names() []string {
	m := l.p.Load()
	if m == nil {
		return nil
	}
	return m.Names()
}

func (l *LiveDocConnMgr) Kinds() map[string]string {
	m := l.p.Load()
	if m == nil {
		return nil
	}
	return m.Kinds()
}

func (l *LiveDocConnMgr) Store(m *document.DocumentConnectorManager) { l.p.Store(m) }
func (l *LiveDocConnMgr) Load() *document.DocumentConnectorManager   { return l.p.Load() }

// LiveMessagingMgr wraps MessagePublisherManager with an atomic pointer.
type LiveMessagingMgr struct {
	p atomic.Pointer[messaging.MessagePublisherManager]
}

func (l *LiveMessagingMgr) Get(name string) (messaging.MessagePublisher, bool) {
	m := l.p.Load()
	if m == nil {
		return nil, false
	}
	return m.Get(name)
}

func (l *LiveMessagingMgr) Names() []string {
	m := l.p.Load()
	if m == nil {
		return nil
	}
	return m.Names()
}

func (l *LiveMessagingMgr) Kinds() map[string]string {
	m := l.p.Load()
	if m == nil {
		return nil
	}
	return m.Kinds()
}

func (l *LiveMessagingMgr) Store(m *messaging.MessagePublisherManager) { l.p.Store(m) }
func (l *LiveMessagingMgr) Load() *messaging.MessagePublisherManager   { return l.p.Load() }

// LiveSFTPConnMgr wraps SFTPConnectorManager with an atomic pointer.
type LiveSFTPConnMgr struct {
	p atomic.Pointer[sftp.SFTPConnectorManager]
}

func (l *LiveSFTPConnMgr) Get(name string) (sftp.SFTPProvider, bool) {
	m := l.p.Load()
	if m == nil {
		return nil, false
	}
	return m.Get(name)
}

func (l *LiveSFTPConnMgr) Names() []string {
	m := l.p.Load()
	if m == nil {
		return nil
	}
	return m.Names()
}

func (l *LiveSFTPConnMgr) Store(m *sftp.SFTPConnectorManager) { l.p.Store(m) }
func (l *LiveSFTPConnMgr) Load() *sftp.SFTPConnectorManager   { return l.p.Load() }

// LiveStorageMgr wraps StorageManager with an atomic pointer.
type LiveStorageMgr struct {
	p atomic.Pointer[storage.StorageManager]
}

func (l *LiveStorageMgr) Names() []string {
	m := l.p.Load()
	if m == nil {
		return nil
	}
	return m.Names()
}

func (l *LiveStorageMgr) Store(m *storage.StorageManager) { l.p.Store(m) }
func (l *LiveStorageMgr) Load() *storage.StorageManager   { return l.p.Load() }
