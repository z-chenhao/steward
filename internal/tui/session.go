// Package tui is the local, line-oriented terminal adapter. It owns no Agent
// state and exposes settings only to the human, never as model Tools.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/legacyv1"
	terminalcontract "github.com/z-chenhao/assistant/internal/plugin/terminalinterface/contract"
	"github.com/z-chenhao/assistant/internal/task"
)

// These private-to-the-repository seams are consumed by this adapter; app wires
// the existing control plane and Actor. They do not form another execution path.
type State interface {
	Diagnostics(context.Context) (agent.Diagnostics, error)
	History(context.Context) ([]conversation.Message, error)
}
type Models interface {
	AddProfile(context.Context, llm.ProfileInput) (llm.Profile, error)
	ListProfiles(context.Context) ([]llm.Profile, error)
	Profile(context.Context, string) (llm.Profile, error)
}
type Accounts interface {
	Login(context.Context, string, func(llm.AuthEvent)) (llm.AuthConnection, error)
	Logout(context.Context, string) (bool, error)
}
type Session struct {
	Interface        func(context.Context, terminalcontract.Query) (terminalcontract.Result, bool, error)
	ConnectionPolicy func(context.Context, contract.ConnectionFacts) (contract.ConnectionDefaults, bool, error)
	DecideApproval   func(context.Context, string, string, string) error
	Messages         func(context.Context) ([]conversation.Message, error)
	State            State
	Models           Models
	Accounts         Accounts
	Control          llm.Control
	Actor            agent.Actor
	CurrentActor     func() agent.Actor
	AwaitRuntime     func(context.Context) error
	Discover         func(context.Context, string, string) ([]llm.CatalogModel, error)
	AccountCatalog   func(context.Context, string) ([]llm.CatalogModel, error)
	LoginEnvironment func(context.Context, string) (llm.AuthConnection, error)
}

func (s Session) actor() agent.Actor {
	if s.CurrentActor != nil {
		return s.CurrentActor()
	}
	return s.Actor
}

var errCancelled = errors.New("setup cancelled")

// Run owns input and closes it on exit, including signal cancellation. The CLI
// supplies stdin; tests supply a disposable pipe. Output stays on this terminal;
// private login URLs are never copied to history, traces or an evidence log.
func (s Session) Run(ctx context.Context, input io.ReadCloser, out io.Writer) (result error) {
	lines := make(chan inputEvent)
	readerDone := make(chan error, 1)
	readCtx, stop := context.WithCancel(ctx)
	defer func() {
		stop()
		closeErr := input.Close()
		if err := <-readerDone; err != nil && ctx.Err() == nil {
			result = errors.New("terminal input failed")
		}
		if ctx.Err() != nil {
			result = nil
		}
		result = errors.Join(result, closeErr)
	}()
	go func() {
		interactive, echo := false, false
		if terminal, ok := input.(*terminalInput); ok && terminal.settings != nil {
			interactive, echo = true, terminal.echoEnabled()
		}
		err := readInput(readCtx, input, interactive, echo, func(event inputEvent) bool {
			select {
			case lines <- event:
				return true
			case <-readCtx.Done():
				return false
			}
		})
		if readCtx.Err() != nil {
			err = nil
		}
		readerDone <- err
		close(lines)
	}()
	u := terminal{Session: s, lines: lines, out: out}
	if err := u.prepareView(ctx); err != nil {
		return err
	}
	work, cancelWork := context.WithCancel(ctx)
	u.work = work
	defer func() {
		cancelWork()
		if u.workDone != nil {
			<-u.workDone
		}
	}()
	u.print(u.words().Help)
	d, err := s.State.Diagnostics(ctx)
	if err != nil {
		return err
	}
	u.print(fmt.Sprintf(u.words().State, d.Readiness))
	u.readiness = d.Readiness
	if d.Readiness == agent.SetupRequired {
		u.report(u.setup(ctx))
	}
	if ctx.Err() == nil {
		// Reclaim durable Main work before accepting a new line after a restart.
		if d, err = s.State.Diagnostics(ctx); err != nil {
			return err
		}
		if d.AgentIngressEnabled {
			u.startDrain()
		}
		u.report(u.history(ctx))
	}
	for ctx.Err() == nil {
		line, err := u.read(ctx, u.words().InputPrompt)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if line == "" {
			continue
		}
		if line == "/quit" {
			return nil
		}
		if strings.HasPrefix(line, "/") {
			u.report(u.command(ctx, line))
			continue
		}
		d, err := s.State.Diagnostics(ctx)
		if err != nil {
			return err
		}
		if !d.AgentIngressEnabled {
			u.print(fmt.Sprintf(u.words().Repair, d.Readiness))
			continue
		}
		_, err = s.actor().Submit(ctx, uuid.Must(uuid.NewV7()).String(), line)
		if err == nil {
			u.print(u.words().Busy)
			u.startDrain()
		}
		u.report(err)
	}
	return nil
}

type terminal struct {
	view             *terminalcontract.Copy
	interfaceManaged bool
	Session
	lines              <-chan inputEvent
	out                io.Writer
	seenMessages       map[string]bool
	work               context.Context
	workDone           chan error
	drainAgain         bool
	updates            chan struct{}
	readiness          string
	nextReadinessCheck time.Time
}

// Keep accepting input while Main is busy. Actor.Submit routes Steering to the
// same durable Main; this adapter never starts a second concurrent Drain.
func (u *terminal) startDrain() {
	if u.workDone != nil {
		u.drainAgain = true
		return
	}
	done := make(chan error, 1)
	u.workDone = done
	if u.updates == nil {
		u.updates = make(chan struct{}, 1)
	}
	actor := u.actor()
	actor.Sink = func(e agent.Event) {
		if e.RunKind == "main" && (e.Kind == "turn_end" || e.Kind == "agent_end") {
			select {
			case u.updates <- struct{}{}:
			default:
			}
		}
	}
	go func() { done <- actor.Drain(u.work) }()
}

// Provider and model text are untrusted terminal data. Preserve plain text,
// never ANSI/OSC controls (including pasted control sequences in user input).
func plain(text string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || unicode.In(r, unicode.Cf)) {
			return -1
		}
		return r
	}, text)
}
func (u *terminal) print(text string) { fmt.Fprintln(u.out, plain(text)) }
func (u *terminal) read(ctx context.Context, prompt string) (string, error) {
	fmt.Fprint(u.out, prompt)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			if u.readiness == agent.Degraded && !time.Now().Before(u.nextReadinessCheck) {
				u.nextReadinessCheck = time.Now().Add(time.Second)
				u.report(u.refreshReadiness(ctx))
			}
			if u.Messages != nil {
				u.report(u.history(ctx))
			}
		case <-u.updates:
			u.report(u.history(ctx))
			fmt.Fprint(u.out, prompt)
		case err := <-u.workDone:
			u.workDone = nil
			u.report(err)
			u.report(u.history(ctx))
			if u.drainAgain {
				u.drainAgain = false
				u.startDrain()
			} else {
				u.print(u.words().Idle)
			}
			fmt.Fprint(u.out, prompt)
		case event, ok := <-u.lines:
			if !ok {
				return "", io.EOF
			}
			switch event.kind {
			case inputEcho:
				fmt.Fprint(u.out, event.text)
			case inputRedraw:
				fmt.Fprint(u.out, "\r\x1b[2K", prompt, plain(event.text))
			case inputRejected:
				u.print(fmt.Sprintf(u.words().InputRejected, event.err.Error()))
				fmt.Fprint(u.out, prompt)
			case inputLine:
				return strings.TrimSpace(event.text), nil
			}
		}
	}
}
func (u *terminal) ask(ctx context.Context, prompt string) (string, error) {
	line, err := u.read(ctx, fmt.Sprintf(u.words().Ask, prompt))
	if line == "/cancel" {
		return "", errCancelled
	}
	return line, err
}
func (u *terminal) report(err error) {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return
	}
	if errors.Is(err, errCancelled) {
		u.print(u.words().Cancelled)
		return
	}
	var test *testFailure
	if errors.As(err, &test) {
		u.report(test.cause)
		u.print(fmt.Sprintf(u.words().Retained, test.id, test.id))
		return
	}
	if errors.Is(err, llm.ErrNoBridge) {
		u.print(u.words().BridgeMissing)
		return
	}
	var probe *llm.ProbeError
	if errors.As(err, &probe) {
		// ProbeError contains fixed Host stage names and normalized codes only.
		u.print(fmt.Sprintf(u.words().ProbeFailed, probe.Error()))
		if probe.Timeout > 0 {
			u.print(u.words().ProbeTimeout)
		} else if errors.Is(err, llm.ModelIncompatible) {
			u.print(u.words().ProbeIncompatible)
		}
		return
	}
	// Do not render arbitrary error strings, HTTP bodies, endpoints or input.
	for _, known := range []error{llm.AuthRequired, llm.ModelIncompatible, llm.ProviderUnavailable, llm.RateLimited, llm.GenerationCancelled, llm.ErrConflict, llm.ErrUnverified, llm.ErrSecretStore, llm.ErrSecretCleanup} {
		if errors.Is(err, known) {
			u.print(fmt.Sprintf(u.words().KnownFailure, known.Error()))
			return
		}
	}
	u.print(u.words().UnknownFailure)
}
func (u *terminal) history(ctx context.Context) error {
	read := u.State.History
	if u.Messages != nil {
		read = u.Messages
	}
	messages, err := read(ctx)
	if err != nil {
		return err
	}
	if u.seenMessages == nil {
		u.seenMessages = map[string]bool{}
	}
	for _, m := range messages {
		if u.seenMessages[m.MessageID] {
			continue
		}
		for _, p := range m.Content {
			if p.Type == "text" {
				text := p.Text
				if m.Role == "assistant" {
					text = strings.TrimSpace(p.Text)
				}
				u.print(fmt.Sprintf(u.words().Message, m.Role, text))
			} else if p.ArtifactRef != "" {
				u.print(fmt.Sprintf(u.words().Attachment, m.Role, p.ArtifactRef))
			} else {
				u.print(fmt.Sprintf(u.words().OtherPart, m.Role, p.Type))
			}
		}
		u.seenMessages[m.MessageID] = true
	}
	return nil
}
func (u *terminal) authEvent(e llm.AuthEvent) {
	switch e.Kind {
	case "open_url":
		u.print(fmt.Sprintf(u.words().LoginURL, e.URL))
	case "device_code":
		u.print(fmt.Sprintf(u.words().LoginCode, e.DeviceCode))
	case "progress":
		u.print(u.words().LoginProgress)
	case "choose_workspace":
		for _, choice := range e.Choices {
			u.print(fmt.Sprintf(u.words().Workspace, choice))
		}
	case "input_required":
		u.print(u.words().LoginRequired)
	}
}
func (u *terminal) command(ctx context.Context, line string) error {
	if err := u.prepareView(ctx); err != nil {
		return err
	}
	args := strings.Fields(line)
	switch {
	case len(args) == 3 && (args[0] == "/approve" || args[0] == "/deny"):
		if u.DecideApproval == nil {
			return agent.ErrConflict
		}
		decision := "approved"
		if args[0] == "/deny" {
			decision = "denied"
		}
		if err := u.DecideApproval(ctx, args[1], args[2], decision); err != nil {
			return err
		}
		u.print(u.words().ApprovalSaved)
		u.startDrain()
		return nil
	case line == "/setup":
		return u.setup(ctx)
	case line == "/history":
		u.seenMessages = nil
		return u.history(ctx)
	case line == "/status":
		d, err := u.State.Diagnostics(ctx)
		if err == nil {
			u.print(fmt.Sprintf(u.words().State, d.Readiness))
			if activity, ok := u.State.(interface {
				Activity(context.Context) ([]task.Summary, error)
			}); ok {
				items, readErr := activity.Activity(ctx)
				if readErr != nil {
					return readErr
				}
				for _, item := range items {
					u.print(fmt.Sprintf(u.words().Task, item.Title, item.Status, item.ProgressSummary))
				}
			}
		}
		return err
	case line == "/models":
		profiles, err := u.Models.ListProfiles(ctx)
		if err != nil {
			return err
		}
		for _, p := range profiles {
			u.print(fmt.Sprintf(u.words().Profile, p.ProfileID, p.ModelID, p.Status))
		}
		return nil
	case len(args) == 2 && args[0] == "/test":
		err := u.testModel(ctx, args[1])
		if err == nil {
			u.print(u.words().ProbePassed)
			u.print(fmt.Sprintf(u.words().ActivateCommand, args[1]))
		}
		return err
	case len(args) == 2 && args[0] == "/select":
		return u.selectModel(ctx, args[1])
	case len(args) <= 2 && args[0] == "/login":
		u.print(u.words().CredentialStorage)
		id := ""
		if len(args) == 2 {
			id = args[1]
		}
		a, err := u.Accounts.Login(ctx, id, u.authEvent)
		if err == nil {
			u.print(fmt.Sprintf(u.words().AccountConnected, a.ConnectionID))
		}
		return err
	case len(args) == 2 && args[0] == "/logout":
		_, err := u.Accounts.Logout(ctx, args[1])
		if err == nil {
			u.print(u.words().AccountLoggedOut)
		}
		return err
	default:
		u.print(u.words().UnknownCommand)
		return nil
	}
}
func (u *terminal) selectModel(ctx context.Context, id string) error {
	p, err := u.Models.Profile(ctx, id)
	if err != nil {
		return err
	}
	d, err := u.State.Diagnostics(ctx)
	if err != nil {
		return err
	}
	u.print(u.words().Privacy)
	answer, err := u.ask(ctx, fmt.Sprintf(u.words().Activate, plain(p.ModelID), p.ProviderKind))
	if err != nil {
		return err
	}
	if answer != "yes" {
		return errCancelled
	}
	if err := u.Control.Select(ctx, id, p.Version, d.ConfigVersion); err != nil {
		return err
	}
	if u.AwaitRuntime != nil {
		if err := u.AwaitRuntime(ctx); err != nil {
			return err
		}
	}
	u.print(u.words().Selected)
	return nil
}
func (u *terminal) setup(ctx context.Context) error {
	if err := u.prepareView(ctx); err != nil {
		return err
	}
	u.print(u.words().ConnectionMenu)
	u.print(u.words().LocalHelp)
	choice, err := u.ask(ctx, u.words().ConnectionPrompt)
	if err != nil {
		return err
	}
	if choice != "1" && choice != "2" {
		return errCancelled
	}
	facts := contract.ConnectionFacts{Preset: contract.TerminalSetup, AccountBacked: choice == "2"}
	var defaults contract.ConnectionDefaults
	managed := false
	if u.ConnectionPolicy != nil {
		defaults, managed, err = u.ConnectionPolicy(ctx, facts)
		if err != nil {
			return err
		}
	}
	if !managed {
		defaults = legacyv1.Connection(facts)
	}
	// The policy proposes a visible default before the user chooses a credential
	// destination. Explicit endpoint input below always remains authoritative.
	identity := llm.ProfileInput{Name: "setup", ModelID: "setup", ProviderKind: "openai_compatible", Endpoint: defaults.EndpointDefault, WireAPI: defaults.WireAPI, ContextWindow: defaults.ContextWindow, MaxOutputTokens: defaults.MaxOutputTokens, Compatibility: llm.Compatibility{ToolResultFormat: "openai"}, Capabilities: llm.Capabilities{TextInput: true, ImageInput: defaults.ImageInput}}
	if defaults.TokenLimit < 1 || identity.ValidateActivation() != nil || plain(defaults.EndpointDefault) != defaults.EndpointDefault || defaults.EndpointOverride != "" {
		return llm.ErrConflict
	}
	if choice == "2" && defaults.WireAPI != "openai_responses" {
		return llm.ErrConflict
	}
	p := llm.ProfileInput{ProviderKind: "openai_compatible", WireAPI: defaults.WireAPI, ContextWindow: defaults.ContextWindow, MaxOutputTokens: defaults.MaxOutputTokens, Compatibility: llm.Compatibility{ToolResultFormat: "openai"}, Capabilities: llm.Capabilities{TextInput: true, ImageInput: defaults.ImageInput}}
	var catalog []llm.CatalogModel
	switch choice {
	case "1":
		p.Endpoint, err = u.ask(ctx, fmt.Sprintf(u.words().Endpoint, defaults.EndpointDefault))
		if err != nil {
			return err
		}
		if p.Endpoint == "" {
			p.Endpoint = defaults.EndpointDefault
		}
		keyEnv, err := u.ask(ctx, u.words().KeyEnvironment)
		if err != nil {
			return err
		}
		if keyEnv != "" {
			a, err := u.LoginEnvironment(ctx, keyEnv)
			if err != nil {
				return err
			}
			p.AuthConnectionID = a.ConnectionID
		}
		catalog, err = u.Discover(ctx, p.Endpoint, p.AuthConnectionID)
		if err != nil {
			u.print(u.words().CatalogUnavailable)
		}
	case "2":
		u.print(u.words().CredentialStorage)
		p.ProviderKind = "openai_account"
		id, err := u.ask(ctx, u.words().AccountPrompt)
		if err != nil {
			return err
		}
		a, err := u.Accounts.Login(ctx, id, u.authEvent)
		if err != nil {
			return err
		}
		p.AuthConnectionID = a.ConnectionID
		u.print(fmt.Sprintf(u.words().AccountConnected, a.ConnectionID))
		catalog, err = u.AccountCatalog(ctx, a.ConnectionID)
		if err != nil {
			return err
		}
	default:
		return errCancelled
	}
	p.ModelID, err = u.chooseModel(ctx, catalog, p.ProviderKind == "openai_compatible")
	if err != nil {
		return err
	}
	p.Name = p.ModelID
	found := false
	for _, m := range catalog {
		if m.ModelID == p.ModelID {
			found = true
			p.Capabilities.ImageInput = m.ImageInput
			if m.ContextWindow > 0 && m.MaxOutputTokens > 0 {
				p.ContextWindow, p.MaxOutputTokens = m.ContextWindow, min(m.MaxOutputTokens, m.ContextWindow)
			}
		}
	}
	if p.ProviderKind == "openai_account" && !found {
		return llm.ModelNotFound
	}
	if p.ProviderKind == "openai_compatible" {
		// Local catalog IDs carry no verified limits. Ask for explicit bounds;
		// conservative defaults are candidates until the actual Probe succeeds.
		for _, setting := range []struct {
			name  string
			value *int64
			max   int64
		}{{u.words().ContextTokens, &p.ContextWindow, defaults.TokenLimit}, {u.words().OutputTokens, &p.MaxOutputTokens, defaults.TokenLimit}} {
			max := setting.max
			if setting.value == &p.MaxOutputTokens {
				max = min(max, p.ContextWindow)
			}
			for {
				text, err := u.ask(ctx, fmt.Sprintf(u.words().TokenPrompt, setting.name, min(*setting.value, max)))
				if err != nil {
					return err
				}
				if text == "" {
					text = strconv.FormatInt(min(*setting.value, max), 10)
				}
				n, err := strconv.ParseInt(text, 10, 64)
				if err != nil || n < 1 || n > max {
					u.print(fmt.Sprintf(u.words().TokenInvalid, max))
					continue
				}
				*setting.value = n
				break
			}
		}
	}
	if err := p.ValidateActivation(); err != nil {
		return err
	}
	profile, err := u.Models.AddProfile(ctx, p)
	if err != nil {
		return err
	}
	u.print(fmt.Sprintf(u.words().ProfileSaved, profile.ProfileID))
	if p.ProviderKind != "openai_account" {
		if err = u.testModel(ctx, profile.ProfileID); err != nil {
			return err
		}
		u.print(u.words().ProbeCapabilities)
	}
	return u.selectModel(ctx, profile.ProfileID)
}

func (u *terminal) testModel(ctx context.Context, id string) error {
	u.print(fmt.Sprintf(u.words().Testing, llm.ProbeStepTimeout))
	_, err := u.Control.TestWithProgress(ctx, id, func(stage llm.ProbeStage) {
		u.print(fmt.Sprintf(u.words().Checking, stage.String()))
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil && !errors.Is(err, llm.ErrNotFound) {
		return &testFailure{id: id, cause: err}
	}
	return err
}

type testFailure struct {
	id    string
	cause error
}

func (e *testFailure) Error() string { return "connection test failed" }
func (e *testFailure) Unwrap() error { return e.cause }

func (u *terminal) chooseModel(ctx context.Context, catalog []llm.CatalogModel, manual bool) (string, error) {
	if err := u.prepareView(ctx); err != nil {
		return "", err
	}
	for i, model := range catalog {
		u.print(fmt.Sprintf(u.words().Model, i+1, model.ModelID))
	}
	prompt := u.words().ModelPrompt
	if len(catalog) == 1 {
		prompt += fmt.Sprintf(u.words().SingleModel, plain(catalog[0].ModelID))
	}
	for {
		value, err := u.ask(ctx, prompt)
		if err != nil {
			return "", err
		}
		facts := terminalcontract.ChoiceFacts{Count: len(catalog), Empty: value == "", Exact: -1, Number: -1, Manual: manual}
		for i, model := range catalog {
			if value == model.ModelID {
				facts.Exact = i
				break
			}
		}
		if n, err := strconv.Atoi(value); err == nil && n >= 1 && n <= len(catalog) {
			facts.Number = n - 1
		}
		choice, err := u.choose(ctx, facts)
		if err != nil {
			return "", err
		}
		switch choice.Kind {
		case "index":
			return catalog[choice.Index].ModelID, nil
		case "manual":
			return value, nil
		}
		u.print(u.words().ChooseListed)
	}
}

// A control surface can open before dependencies finish connecting. Announce
// recovery and resume accepted work without waiting for a new user command.
func (u *terminal) refreshReadiness(ctx context.Context) error {
	diagnostics, err := u.State.Diagnostics(ctx)
	if err != nil {
		return err
	}
	if diagnostics.Readiness == u.readiness {
		return nil
	}
	u.readiness = diagnostics.Readiness
	u.print(fmt.Sprintf(u.words().RecoveredState, diagnostics.Readiness))
	if diagnostics.AgentIngressEnabled {
		u.startDrain()
	}
	return nil
}
