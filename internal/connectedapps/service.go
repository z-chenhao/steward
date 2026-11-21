// Package connectedapps owns the user's app connections. A broker catalogue
// entry is a Connector; only an explicit owner-authorized binding is a Connection.
package connectedapps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/llm"
	accounts "github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

var ErrInvalid = errors.New("invalid app connection")
var ErrUnavailable = errors.New("app service unavailable; configure or check the Composio project")
var ErrConflict = errors.New("app connections changed; refresh and try again")
var ErrDenied = errors.New("app connection is not active")
var ErrAliasRequired = errors.New("choose a label for the additional account")
var ErrAuthConfigRequired = errors.New("this app requires a custom OAuth configuration in the connection service")
var ErrLinkExpired = errors.New("authorization link expired; reconnect this account")

const AuthorizationTTL = 10 * time.Minute

const DefaultKeyRef = "env:COMPOSIO_API_KEY"

type authorization struct {
	Link    composio.Link
	Created time.Time
	// Zero denotes the exact pre-session-service cache lifetime.
	TTL time.Duration
}

type Connection struct {
	Public              bool   `json:"public,omitempty"`
	ID                  string `json:"id"`
	Toolkit             string `json:"toolkit"`
	Name                string `json:"name"`
	Alias               string `json:"alias"`
	AccountID           string `json:"account_id"`
	Status              string `json:"status"`
	RevocationConfirmed bool   `json:"revocation_confirmed"`
}
type State struct {
	Meeting     *MeetingGrant     `json:"meeting_preparation,omitempty"`
	Revision    int64             `json:"-"`
	KeyRef      string            `json:"key_ref"`
	UserID      string            `json:"user_id"`
	Connections []Connection      `json:"connections"`
	AuthConfigs map[string]string `json:"auth_configs,omitempty"`
}

// Store uses compare-and-swap persistence; no transaction spans provider I/O.
type Store interface {
	LoadApps(context.Context) (State, error)
	SaveApps(context.Context, State, int64) error
}
type Service struct {
	ReadEditorial       EditorialReader
	ReadAccountPolicies AccountPolicyReader
	ReadAccountClose    AccountCloseReader
	ReadSettings        SettingsReader
	ReadInventory       AccountInventoryReader
	Store               Store
	Secrets             llm.SecretStore
	// Client is a protocol-peer injection. Production only supplies Secrets.
	Client  composio.Client
	Changed func(context.Context) error
	// Serializes account lifecycle and dispatch. Once Disconnect returns, an
	// accepted invocation that has not dispatched cannot use the old binding.
	mu      sync.RWMutex
	pending map[string]authorization
}
type View struct {
	State            State
	Catalog          composio.Catalog
	Suggested        bool
	Unavailable      bool
	Search, Category string
	Categories       []composio.Category
}

func (s *Service) client(state State) composio.Client {
	c := s.Client
	c.Secrets, c.KeyRef = s.Secrets, state.KeyRef
	return c
}
func (s *Service) State(ctx context.Context) (State, error) {
	state, err := s.Store.LoadApps(ctx)
	if err != nil || state.KeyRef != "" || s.Secrets == nil {
		return state, err
	}
	// A deployment may supply the conventional environment reference without a
	// separate setup write. Never replace an explicitly configured reference.
	key, secretErr := s.Secrets.Get(ctx, DefaultKeyRef)
	if secretErr == nil && len(key) > 0 {
		state.KeyRef = DefaultKeyRef
	}
	clear(key)
	return state, nil
}
func (s *Service) View(ctx context.Context, search, category, cursor string) (View, error) {
	v := View{Search: search, Category: category}
	if len(search) > 200 || len(category) > 100 || len(cursor) > 4096 {
		return v, ErrInvalid
	}
	var err error
	v.State, err = s.State(ctx)
	if err != nil {
		return v, err
	}
	selection, err := s.Directory(ctx, contract.Query{Search: search, Category: category, LiveConfigured: v.State.KeyRef != "", HasCursor: cursor != ""})
	if err != nil {
		return v, err
	}
	editorial := selection.Catalogue
	useEditorial := func() {
		v.Suggested = true
		bySlug := map[string]composio.Toolkit{}
		for _, item := range EditorialToolkits(editorial) {
			bySlug[item.Slug] = item
		}
		v.Catalog = composio.Catalog{Items: []composio.Toolkit{}}
		v.Categories = nil
		for _, slug := range editorial.Filter.Slugs {
			v.Catalog.Items = append(v.Catalog.Items, bySlug[slug])
		}
		for _, category := range editorial.Filter.Categories {
			v.Categories = append(v.Categories, composio.Category{ID: category.ID, Name: category.Name})
		}
	}
	if editorial.Route.Source == "editorial" {
		useEditorial()
		return v, nil
	}
	if selection.mode == directoryManaged {
		v.Catalog, err = s.client(v.State).CatalogPage(ctx, search, category, cursor, editorial.Route.PageSize)
	} else {
		v.Catalog, err = s.client(v.State).CatalogLegacyV1(ctx, search, category, cursor)
	}
	if err != nil {
		if editorial.Route.OnLiveError == "editorial" {
			useEditorial()
		} else {
			v.Unavailable = true
		}
		return v, nil
	}
	if selection.mode == directoryManaged {
		v.Categories, err = projectCategoryOptions(ctx, selection.categoryProjector(), v.Catalog.Items, category)
		if err != nil {
			return View{State: v.State, Search: search, Category: category}, ErrUnavailable
		}
	} else {
		v.Categories = categoryOptionsLegacyV1(v.Catalog.Items, category)
	}
	return v, nil
}

func (s *Service) save(ctx context.Context, state State) error {
	if state.Meeting != nil {
		pins := []SourcePin{state.Meeting.Calendar}
		if !state.Meeting.CalendarOnly || state.Meeting.Mail != (SourcePin{}) {
			pins = append(pins, state.Meeting.Mail)
		}
		for _, pin := range pins {
			c := find(&state, pin.ID)
			if c == nil || c.Status != "active" || c.Public || BindingHash(state, *c) != pin.Hash {
				state.Meeting = nil
				break
			}
		}
	}
	return s.Store.SaveApps(ctx, state, state.Revision)
}
func (s *Service) changed(ctx context.Context) error {
	if s.Changed != nil {
		return s.Changed(ctx)
	}
	return nil
}

func (s *Service) Configure(ctx context.Context, ref string, revision int64) error {
	return s.ConfigureProject(ctx, ref, revision, nil)
}
func (s *Service) ConfigureProject(ctx context.Context, ref string, revision int64, configs map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !interaction.ValidCredentialRef(ref) {
		return ErrInvalid
	}
	if len(configs) > 128 {
		return ErrInvalid
	}
	for toolkit, id := range configs {
		if !composio.ValidID(toolkit) || !composio.ValidID(id) {
			return ErrInvalid
		}
	}
	state, err := s.State(ctx)
	if err != nil {
		return err
	}
	if state.Revision != revision {
		return ErrConflict
	}
	if state.KeyRef != ref {
		for _, c := range state.Connections {
			if c.Status != "disconnected" {
				return ErrConflict
			}
		}
	}
	candidate := state
	candidate.KeyRef = ref
	if len(configs) > 0 {
		policies, e := s.accountPolicies(ctx)
		if e != nil {
			return ErrUnavailable
		}
		ctx = context.WithValue(ctx, accountPoliciesKey{}, policies)
	}
	if _, err = s.client(candidate).CatalogLegacyV1(ctx, "", "", ""); err != nil {
		return ErrUnavailable
	}
	for toolkit, id := range configs {
		if _, err = s.selectAuthConfig(ctx, state, s.client(candidate), toolkit, id, false); err != nil {
			return ErrUnavailable
		}
	}
	state.KeyRef = ref
	if configs != nil {
		state.AuthConfigs = configs
	}
	if err = s.save(ctx, state); err != nil {
		return err
	}
	return s.changed(ctx)
}

// Connect never adopts arbitrary project-wide accounts. The only account that
// can activate is the ID returned by this owner's explicit hosted-link flow.
func (s *Service) Connect(ctx context.Context, toolkit, alias, authConfig string) (composio.Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connect(ctx, toolkit, alias, authConfig)
}
func (s *Service) connect(ctx context.Context, toolkit, alias, authConfig string) (composio.Link, error) {
	if !validConnectionArgs(toolkit, alias, authConfig) {
		return composio.Link{}, ErrInvalid
	}
	state, err := s.State(ctx)
	if err != nil {
		return composio.Link{}, err
	}
	return s.connectFromState(ctx, state, toolkit, alias, authConfig)
}
func validConnectionArgs(toolkit, alias, authConfig string) bool {
	return composio.ValidID(toolkit) && (alias == "" || composio.ValidID(alias) && len(alias) <= 64) && (authConfig == "" || composio.ValidID(authConfig))
}

// connectFromState serves ordinary connect and an already accepted reconnect
// stage, preserving its exact post-close authority snapshot through source I/O.
func (s *Service) connectFromState(ctx context.Context, state State, toolkit, alias, authConfig string) (composio.Link, error) {
	if !validConnectionArgs(toolkit, alias, authConfig) {
		return composio.Link{}, ErrInvalid
	}
	if state.KeyRef == "" {
		return composio.Link{}, ErrUnavailable
	}
	policies, err := s.connectIntentSelection(ctx)
	if err != nil {
		return composio.Link{}, ErrUnavailable
	}
	ctx = context.WithValue(ctx, accountPoliciesKey{}, policies)
	intent, err := s.connectionIntent(ctx, state, toolkit, alias)
	if err != nil {
		return composio.Link{}, err
	}
	if intent.PendingID != nil {
		return s.authorization(state, *intent.PendingID)
	}
	return policies.session.create(ctx, s, state, toolkit, intent.Alias, authConfig)
}

func (s *Service) authorization(state State, id string) (composio.Link, error) {
	c := find(&state, id)
	if c == nil || c.Status != "pending" {
		return composio.Link{}, ErrDenied
	}
	p, ok := s.pending[id]
	if !ok || authorizationExpired(p) {
		return composio.Link{}, ErrLinkExpired
	}
	return p.Link, nil
}
func (s *Service) Continue(ctx context.Context, id string) (composio.Link, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, err := s.State(ctx)
	if err != nil {
		return composio.Link{}, err
	}
	return s.authorization(state, id)
}
func (s *Service) Reconnect(ctx context.Context, id string) (composio.Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.State(ctx)
	if err != nil {
		return composio.Link{}, err
	}
	c := find(&state, id)
	if c == nil || c.Status == "active" || c.Status == "disconnected" {
		return composio.Link{}, ErrDenied
	}
	selection, selectErr := s.connectIntentSelection(ctx)
	if selectErr != nil {
		return composio.Link{}, ErrUnavailable
	}
	if !selection.session.available() || !selection.close.available() || !selection.reconnect.available() {
		return composio.Link{}, ErrUnavailable
	}
	ctx = context.WithValue(ctx, accountPoliciesKey{}, selection)
	return selection.reconnect.apply(ctx, s, state, *c)
}

func find(state *State, id string) *Connection {
	for i := range state.Connections {
		if state.Connections[i].ID == id {
			return &state.Connections[i]
		}
	}
	return nil
}
func accountIdentity(a composio.Account, state State, c Connection) bool {
	return a.ID == c.AccountID && a.UserID == state.UserID && a.Toolkit.Slug == c.Toolkit
}
func validAccount(a composio.Account, state State, c Connection) bool {
	return accountIdentity(a, state, c) && !a.Disabled
}
func (s *Service) Check(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.State(ctx)
	if err != nil {
		return err
	}
	c := find(&state, id)
	if c == nil || c.Status == "disconnected" {
		return ErrDenied
	}
	selection, selectErr := s.accountPolicies(ctx)
	if selectErr != nil || !selection.check.available() {
		return ErrUnavailable
	}
	if c.Public {
		app, e := s.client(state).Toolkit(ctx, c.Toolkit)
		if e != nil {
			return ErrUnavailable
		}
		if app.Slug != c.Toolkit || !app.NoAuth {
			return ErrDenied
		}
		if _, err := selection.check.decide(ctx, state.Revision, accounts.AccountCheckRequest{Public: true}); err != nil {
			return err
		}
		return s.changed(ctx)
	}
	a, err := s.client(state).Account(ctx, c.AccountID)
	if err != nil {
		return ErrUnavailable
	}
	if !accountIdentity(a, state, *c) {
		return ErrDenied
	}
	previous := c.Status
	status, err := selection.check.decide(ctx, state.Revision, accounts.AccountCheckRequest{Disabled: a.Disabled, Active: a.Status == "ACTIVE", Initializing: a.Status == "INITIALIZING", Initiated: a.Status == "INITIATED"})
	if err != nil {
		return err
	}
	c.Status = status
	if previous == c.Status {
		return s.changed(ctx)
	}
	if err = s.save(ctx, state); err != nil {
		return err
	}
	if c.Status != "pending" {
		delete(s.pending, id)
	}
	return s.changed(ctx)
}
func (s *Service) Disconnect(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disconnect(ctx, id)
}
func (s *Service) disconnect(ctx context.Context, id string) error {
	state, err := s.State(ctx)
	if err != nil {
		return err
	}
	c := find(&state, id)
	if c == nil {
		return ErrInvalid
	}
	if c.Status == "disconnected" {
		return nil
	}
	policy := AccountClosePolicy{}
	if !c.Public {
		policy = s.accountClosePolicy(ctx)
	}
	originalHash := BindingHash(state, *c)
	// Local closure is durable before any unreliable remote operation.
	c.Status = "disconnected"
	if err = s.save(ctx, state); err != nil {
		return err
	}
	delete(s.pending, id)
	refreshErr := s.changed(ctx)
	state, err = s.State(ctx)
	if err != nil {
		return err
	}
	c = find(&state, id)
	if c == nil || c.Status != "disconnected" {
		return ErrConflict
	}
	if policy.mode == accountPolicyManaged && BindingHash(state, *c) != originalHash {
		return ErrConflict
	}
	c.RevocationConfirmed = c.Public
	if !c.Public {
		c.RevocationConfirmed = policy.cleanup(ctx, state, s.client(state), *c)
	}
	if err = s.save(ctx, state); err != nil {
		return err
	}
	return refreshErr
}

func BindingHash(state State, c Connection) string {
	b, _ := json.Marshal(struct {
		Ref, User  string
		Connection Connection
	}{state.KeyRef, state.UserID, Connection{ID: c.ID, Toolkit: c.Toolkit, Alias: c.Alias, AccountID: c.AccountID, Public: c.Public}})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func ToolHash(t composio.Tool) string {
	var schema any
	decoder := json.NewDecoder(bytes.NewReader(t.Input))
	decoder.UseNumber()
	if decoder.Decode(&schema) == nil {
		t.Input, _ = json.Marshal(schema)
	}
	b, _ := json.Marshal(t)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *Service) active(ctx context.Context, id, hash string) (State, Connection, error) {
	state, err := s.State(ctx)
	if err != nil {
		return state, Connection{}, err
	}
	c := find(&state, id)
	if c == nil || c.Status != "active" || BindingHash(state, *c) != hash {
		return state, Connection{}, ErrDenied
	}
	return state, *c, nil
}

// Tool eligibility preserves the original authorization/version boundary. The
// candidate can choose presentation/page size, not grant an account operation.
func filterTools(result composio.Tools, c Connection) composio.Tools {
	filtered := result.Items[:0]
	for _, t := range result.Items {
		if t.Toolkit.Slug == c.Toolkit && (!c.Public || t.NoAuth) && !t.Deprecated && composio.ValidID(t.Slug) && composio.ValidID(t.Version) && t.Version != "latest" && len(t.Input) > 0 {
			filtered = append(filtered, t)
		}
	}
	result.Items = filtered
	return result
}
