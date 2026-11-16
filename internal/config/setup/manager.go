package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/z-chenhao/assistant/internal/computer/macosvm"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"github.com/z-chenhao/assistant/internal/recall/embedding"
)

const maxConfig = 256 << 10
const maxEdit = 64 << 10

type proof struct {
	revision, text, raw, candidate, state, message string
	at                                             time.Time
}
type Manager struct {
	Presentation PresentationReader
	mu           sync.Mutex
	probing      sync.Mutex
	active       config.Config
	path         string
	secrets      llm.SecretStore
	proofs       map[string]proof
	seenFile     bool
	states       map[string]string
}

func New(active config.Config, secrets llm.SecretStore) *Manager {
	path := active.SourcePath
	if path == "" {
		path = filepath.Join(active.App.DataRoot, "config.yaml")
	}
	_, existing := os.Lstat(path)
	return &Manager{active: active, path: path, secrets: secrets, proofs: map[string]proof{}, seenFile: existing == nil}
}
func digest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func known(id string) bool {
	for _, k := range kinds {
		if k == id {
			return true
		}
	}
	return false
}
func (m *Manager) view(c config.Config, id, revision string, presentation *contract.Presentation) web.Capability {
	v := web.Capability{ID: id, Title: id, State: "not_configured", PresentationUnavailable: presentation == nil}
	var copy contract.Copy
	if presentation != nil {
		copy = presentation.Copy
		for _, description := range presentation.Descriptions {
			if description.ID == id {
				v.Title = description.Title
				v.Description = description.Description
				v.Help = description.Help
				v.Example = description.Example
				v.ParentMemory = description.MemoryChild
				break
			}
		}
	}
	v.Path = m.path
	v.Revision = revision
	v.Text = encode(resource(c, id))
	v.Configured = resource(c, id) != nil
	v.Active = resource(m.active, id) != nil
	v.PendingRestart = v.Text != encode(resource(m.active, id))
	v.Automatic = c.Automatic(id)
	if c.Automatic(id) {
		if id == "memory" {
			v.DefaultKeyRef = savedMemoryRef(m.active.App.DataRoot)
		}
		v.State = "automatic"
		if !v.PendingRestart && m.states[id] != "" {
			v.State = m.states[id]
		}
		v.Message = copy.Automatic
		if id == "computer" {
			v.State, v.Message = macosvm.ManagedState(m.active.App.DataRoot + ".desktop")
		}
		if id == "memory" && v.State == "unavailable" {
			v.Message = copy.MemoryKeyUnavailable
		}
	}
	if v.Configured && !c.Automatic(id) {
		v.State = "configured"
	}
	if id == "execution" && c.Computer != nil {
		v.State = "managed"
		v.Message = copy.ExecutionManaged
	}
	if p, ok := m.proofs[id]; ok && p.revision == revision && p.text == v.Text && time.Since(p.at) <= 5*time.Minute {
		if v.Configured && !c.Automatic(id) {
			v.State = p.state
		}
		v.Message = p.message
		v.TestedAt = p.at
		v.Verified = p.state == "ready"
	}
	if presentation == nil && v.Message == "" {
		v.Message = "Capability presentation unavailable."
	}
	return v
}
func (m *Manager) List(ctx context.Context) ([]web.Capability, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, revision, e := m.read()
	if e != nil {
		return nil, e
	}
	c, e := m.decoded(raw)
	if e != nil {
		return nil, e
	}
	presentation, _ := m.presentation(ctx)
	ids := kinds
	if presentation != nil {
		ids = []string{}
		for _, description := range presentation.Descriptions {
			ids = append(ids, description.ID)
		}
	}
	var views []web.Capability
	for _, id := range ids {
		views = append(views, m.view(c, id, revision, presentation))
	}
	return views, nil
}
func (m *Manager) Detail(ctx context.Context, id string) (web.Capability, error) {
	if !known(id) {
		return web.Capability{}, web.ErrCapabilityInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, revision, e := m.read()
	if e != nil {
		return web.Capability{}, e
	}
	c, e := m.decoded(raw)
	if e != nil {
		return web.Capability{}, e
	}
	presentation, _ := m.presentation(ctx)
	v := m.view(c, id, revision, presentation)
	if p, ok := m.proofs[id]; ok && p.revision == revision && p.candidate != digest(raw) {
		_, candidate, e := m.candidate(id, revision, p.raw)
		if e != nil {
			return v, e
		}
		v = m.view(candidate, id, revision, presentation)
		v.Text = p.raw
		v.Candidate = true
		if time.Since(p.at) > 5*time.Minute {
			if presentation != nil {
				v.Message = presentation.Copy.VerificationExpired
			} else {
				v.Message = "Capability presentation unavailable."
			}
			v.TestedAt = p.at
		}
	}
	return v, nil
}

func (m *Manager) Test(ctx context.Context, id, revision, text string) (web.Capability, error) {
	if !m.probing.TryLock() {
		return web.Capability{}, web.ErrCapabilityUnavailable
	}
	defer m.probing.Unlock()
	m.mu.Lock()
	raw, c, e := m.candidate(id, revision, text)
	m.mu.Unlock()
	if e != nil {
		return web.Capability{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	presentation, e := m.presentation(ctx)
	if e != nil {
		return web.Capability{}, web.ErrCapabilityUnavailable
	}
	result := m.probe(ctx, c, id)
	state, message := result.State, probeMessage(result, presentation.Copy)
	m.mu.Lock()
	defer m.mu.Unlock()
	_, current, e := m.read()
	if e != nil {
		return web.Capability{}, e
	}
	if current != revision {
		return web.Capability{}, web.ErrCapabilityConflict
	}
	canonical := encode(resource(c, id))
	m.proofs[id] = proof{revision: revision, text: canonical, raw: text, candidate: digest(raw), state: state, message: message, at: time.Now().UTC()}
	v := m.view(c, id, revision, presentation)
	v.Text = text
	v.Candidate = true
	return v, nil
}
func (m *Manager) Save(ctx context.Context, id, revision, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	dir, e := os.Open(filepath.Dir(m.path))
	if e != nil {
		return web.ErrCapabilityUnavailable
	}
	defer dir.Close()
	if syscall.Flock(int(dir.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return web.ErrCapabilityConflict
	}
	defer syscall.Flock(int(dir.Fd()), syscall.LOCK_UN)
	raw, c, e := m.candidate(id, revision, text)
	if e != nil {
		return e
	}
	p, ok := m.proofs[id]
	if !ok || p.state != "ready" || p.revision != revision || p.text != encode(resource(c, id)) || p.candidate != digest(raw) || time.Since(p.at) > 5*time.Minute {
		return web.ErrCapabilityUnverified
	}
	// Repointing a live vault must retain the encryption key. Key rotation is a
	// separate data migration; a connection test must not make old memories unreadable.
	oldRaw, _, e := m.read()
	if e != nil {
		return e
	}
	old, e := m.decoded(oldRaw)
	if e != nil {
		return e
	}
	if old.Memory == nil {
		old.Memory = m.active.Memory
	}
	if old.Memory == nil {
		if ref := savedMemoryRef(m.active.App.DataRoot); ref != "" {
			old.Memory = &memory.Config{EncryptionKeyRef: ref}
		}
	}
	if c.Automatic("memory") && old.Memory != nil {
		ref := savedMemoryRef(m.active.App.DataRoot)
		if ref == "" {
			return web.ErrCapabilityMigration
		}
		c.Memory = &memory.Config{EncryptionKeyRef: ref}
	}
	if old.Memory != nil && c.Memory != nil && old.Memory.EncryptionKeyRef != c.Memory.EncryptionKeyRef {
		a, ea := (memory.Vault{Secrets: m.secrets, Ref: old.Memory.EncryptionKeyRef}).Digest(ctx, "configuration key identity")
		b, eb := (memory.Vault{Secrets: m.secrets, Ref: c.Memory.EncryptionKeyRef}).Digest(ctx, "configuration key identity")
		if ea != nil || eb != nil || a != b {
			return web.ErrCapabilityMigration
		}
	}
	if old.Embedding == nil {
		old.Embedding = m.active.Embedding
	}
	if old.Embedding != nil && c.Embedding != nil && (embedding.Client{Config: *old.Embedding}).Identity() != (embedding.Client{Config: *c.Embedding}).Identity() {
		return web.ErrCapabilityMigration
	}
	if e = m.write(raw, revision); e != nil {
		return e
	}
	m.seenFile = true
	p.revision = digest(raw)
	m.proofs = map[string]proof{id: p}
	return nil
}
