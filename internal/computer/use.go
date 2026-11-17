package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/z-chenhao/assistant/internal/tool"
)

// Driver is private to ComputerUseRuntime. Native IDs never cross the facade.
// The Host supplies a live lease fence; the adapter must check it before each
// dispatch and cancel its transport if the lease is lost.
type Driver interface {
	Inventory(context.Context, Instance, string) ([]Target, error)
	Observe(context.Context, Instance, Target, string, ObserveOptions) (Capture, error)
	Act(context.Context, Instance, Target, string, string, map[string]any) (any, error)
	ReadFile(context.Context, Instance, string) (io.ReadCloser, error)
	WriteFile(context.Context, Instance, string, bool, io.Reader) error
}
type Target struct {
	ID      string         `json:"id"`
	Kind    string         `json:"kind"`
	Name    string         `json:"name"`
	Browser string         `json:"browser,omitempty"`
	URL     string         `json:"url,omitempty"`
	Native  map[string]any `json:"native,omitempty"`
}

func (t Target) Public() map[string]any {
	return map[string]any{"id": t.ID, "name": t.Name, "kind": t.Kind, "browser": t.Browser, "url": t.URL}
}

type Element struct {
	Index   int      `json:"index"`
	Token   string   `json:"token"`
	Actions []string `json:"actions,omitempty"`
	Value   string   `json:"value,omitempty"`
	Role    string   `json:"role,omitempty"`
}
type ObserveOptions struct{ Accessibility, Screenshot bool }

type Capture struct {
	HasAX         bool
	State         string
	Elements      []Element
	PNG           []byte
	Width, Height int
	Geometry      string
	Navigation    string
}
type Handle struct {
	Instance           Instance  `json:"instance"`
	Lease              string    `json:"lease"`
	Target             Target    `json:"target"`
	AXReady            bool      `json:"ax_ready"`
	ScreenshotReady    bool      `json:"screenshot_ready"`
	AXRevision         string    `json:"ax_revision"`
	AXState            string    `json:"ax_state"`
	Elements           []Element `json:"elements"`
	ScreenshotRevision string    `json:"screenshot_revision"`
	Geometry           string    `json:"geometry"`
	Navigation         string    `json:"navigation"`
	Width              int       `json:"width"`
	Height             int       `json:"height"`
	Capability         bool      `json:"capability"`
	Listed             bool      `json:"listed"`
	Parent             string    `json:"parent,omitempty"`
}

// ObservationReceipt is a trusted completion receipt committed with the Tool result.
type ObservationReceipt struct {
	Lease, Token, AXRevision, ScreenshotRevision string
	UpdateAX, UpdateScreenshot                   bool
}
type UseState struct {
	Handles map[string]*Handle `json:"handles"`
	Files   map[string]bool    `json:"files"`
}
type UseStore interface {
	Load(context.Context) (UseState, error)
	Save(context.Context, UseState) error
	Fence(context.Context) error
	OpenArtifact(context.Context, string) (io.ReadCloser, error)
}
type UseRuntime struct {
	Driver               Driver
	Store                UseStore
	Instance             Instance
	Lease                string
	ProjectObservation   ObservationProjector
	WaitPolicy           WaitPolicy
	ReadDocumentation    DocumentationReader
	PlanSelection        SelectionPlanner
	OrderInventory       InventoryOrderer
	MatchTargets         TargetMatcher
	ReadActionParameters ActionParameterReader
	UploadBrowser        BrowserUploader
	SnapshotBrowser      BrowserSnapshotter
	ProjectBrowser       BrowserProjector
}
type BridgeCall struct {
	Method string          `json:"method"`
	Token  string          `json:"handle_token"`
	Args   json.RawMessage `json:"args"`
}

func decodeCall(raw string) (BridgeCall, []any, error) {
	var c BridgeCall
	if json.Unmarshal([]byte(raw), &c) != nil {
		return c, nil, ErrInvalid
	}
	var a []any
	if len(c.Args) > 0 && string(c.Args) != "null" {
		if json.Unmarshal(c.Args, &a) != nil {
			var v any
			if json.Unmarshal(c.Args, &v) != nil {
				return c, nil, ErrInvalid
			}
			a = []any{v}
		}
	}
	return c, a, nil
}

// Reads are safe. Every mutation is conservatively approved with exact frozen
// parameters; GUI semantics cannot reliably distinguish a local edit from Send.
func CallPolicy(name, raw string, p tool.Policy) tool.Policy {
	c, _, err := decodeCall(raw)
	p.Replay = "never"
	p.Risk = "high"
	if err == nil && (name == "builtin__computer_discover" && c.Method != "launchApp" && c.Method != "importArtifact" && c.Method != "exportArtifact" || name == "builtin__computer_bind" && c.Method != "createBrowserTab" || name == "builtin__computer_invoke" && readMethod(c.Method)) {
		p.Replay = "safe"
		p.Risk = "low"
	}
	return p
}
func readMethod(m string) bool {
	switch m {
	case "documentation", "observe", "getAXState", "getScreenshot", "getAXStateAndScreenshot", "waitForChange", "capabilities.list", "capabilities.get", "capabilities.documentation", "playwright.documentation", "playwright.textContent", "playwright.count", "playwright.waitForURL", "playwright.waitForSelector":
		return true
	}
	return false
}
func stringify(v any) string { b, _ := json.Marshal(v); return string(b) }
func useResult(v any) tool.Result {
	raw := stringify(v)
	if len(raw) > 512<<10 {
		return tool.Failure("RESOURCE_LIMIT")
	}
	return tool.Result{Text: raw}
}
func str(v any) string { s, _ := v.(string); return s }
func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}
func arg(a []any, i int) any {
	if i < len(a) {
		return a[i]
	}
	return nil
}
func newToken() string { id, _ := uuid.NewV7(); return id.String() }
func SafeTransferPath(p string) bool {
	if p == "" || len(p) > 512 || path.IsAbs(p) || path.Clean(p) != p || strings.ContainsAny(p, "\\\x00\r\n:") {
		return false
	}
	parts := strings.Split(p, "/")
	return len(parts) == 2 && (parts[0] == "imports" || parts[0] == "downloads") && parts[1] != "." && parts[1] != ".."
}
func (r UseRuntime) Call(ctx context.Context, name, raw string) (tool.Result, error) {
	c, a, err := decodeCall(raw)
	if err != nil {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	if err = r.Store.Fence(ctx); err != nil {
		return tool.Result{}, err
	}
	state, err := r.Store.Load(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	if state.Handles == nil {
		state.Handles = map[string]*Handle{}
	}
	if state.Files == nil {
		state.Files = map[string]bool{}
	}
	switch name {
	case "builtin__computer_discover":
		return r.discover(ctx, &state, c, a)
	case "builtin__computer_bind":
		return r.bind(ctx, &state, c, a)
	case "builtin__computer_invoke":
		h := state.Handles[c.Token]
		if h == nil || h.Instance != r.Instance || h.Lease != r.Lease {
			return tool.Failure("STALE_SURFACE_HANDLE"), nil
		}
		return r.invoke(ctx, &state, h, c, a)
	}
	return tool.Failure("NOT_FOUND"), nil
}
func (r UseRuntime) discover(ctx context.Context, s *UseState, c BridgeCall, a []any) (tool.Result, error) {
	if c.Method == "importArtifact" || c.Method == "exportArtifact" {
		return r.transfer(ctx, s, c.Method, object(arg(a, 0)))
	}
	if c.Method == "launchApp" {
		parameters, err := r.actionParameters(ctx, c.Method, "", a)
		if err != nil {
			return tool.Result{}, err
		}
		if parameters.Failure != "" {
			return tool.Failure(parameters.Failure), nil
		}
		app := parameters.App
		_, err = r.Driver.Act(ctx, r.Instance, Target{}, r.Lease, "launchApp", map[string]any{"app": app})
		if err != nil {
			return driverFailure(err), nil
		}
		return useResult(nil), nil
	}
	if c.Method == "getState" {
		out := map[string]any{"computerId": r.Instance.ComputerID, "instanceEpoch": fmt.Sprint(r.Instance.Epoch), "platform": "macos", "errors": []any{}}
		for _, kind := range []string{"apps", "windows", "browsers"} {
			values, err := r.Driver.Inventory(ctx, r.Instance, kind)
			pub := []any{}
			if err != nil {
				out["errors"] = append(out["errors"].([]any), map[string]string{"source": kind, "code": "PROVIDER_UNAVAILABLE"})
			} else {
				pub, err = r.publicInventory(ctx, kind, values)
				if err != nil {
					return tool.Result{}, err
				}
			}
			out[kind] = pub
		}
		result := useResult(out)
		if r.OrderInventory != nil {
			result.HostFence = TargetReceipt{Lease: r.Lease, Instance: r.Instance}
		}
		return result, nil
	}
	kinds := map[string]string{"listApps": "apps", "listWindows": "windows", "listBrowsers": "browsers", "listTabs": "tabs"}
	kind := kinds[c.Method]
	if kind == "" {
		return tool.Failure("NOT_FOUND"), nil
	}
	values, err := r.Driver.Inventory(ctx, r.Instance, kind)
	if err != nil {
		return driverFailure(err), nil
	}
	filtered := make([]Target, 0, len(values))
	browser := str(object(arg(a, 0))["browser"])
	for _, v := range values {
		if browser == "" || v.Browser == browser {
			filtered = append(filtered, v)
		}
	}
	pub, err := r.publicInventory(ctx, kind, filtered)
	if err != nil {
		return tool.Result{}, err
	}
	result := useResult(pub)
	if r.OrderInventory != nil {
		result.HostFence = TargetReceipt{Lease: r.Lease, Instance: r.Instance}
	}
	return result, nil
}
func (r UseRuntime) bind(ctx context.Context, s *UseState, c BridgeCall, a []any) (tool.Result, error) {
	if len(s.Handles) >= 256 {
		return tool.Failure("RESOURCE_LIMIT"), nil
	}
	var target Target
	if c.Method == "createBrowserTab" {
		parameters, err := r.actionParameters(ctx, c.Method, "", a)
		if err != nil {
			return tool.Result{}, err
		}
		if parameters.Failure != "" {
			return tool.Failure(parameters.Failure), nil
		}
		browser, url := parameters.Browser, parameters.URL
		v, err := r.Driver.Act(ctx, r.Instance, Target{ID: browser, Kind: "browser"}, r.Lease, "createBrowserTab", map[string]any{"url": url})
		if err != nil {
			return driverFailure(err), nil
		}
		if json.Unmarshal([]byte(stringify(v)), &target) != nil || target.ID == "" {
			return tool.Failure("PROVIDER_UNAVAILABLE"), nil
		}
	} else {
		kind := map[string]string{"getApp": "apps", "getBrowser": "browsers", "getTab": "tabs"}[c.Method]
		if kind == "" {
			return tool.Failure("NOT_FOUND"), nil
		}
		values, err := r.Driver.Inventory(ctx, r.Instance, kind)
		if err != nil {
			return driverFailure(err), nil
		}
		selector := str(arg(a, 0))
		if c.Method == "getApp" && str(object(arg(a, 0))["windowId"]) != "" {
			kind = "windows"
			values, err = r.Driver.Inventory(ctx, r.Instance, kind)
			if err != nil {
				return driverFailure(err), nil
			}
			selector = str(object(arg(a, 0))["windowId"])
		}
		if selector == "" {
			v := object(arg(a, 0))
			for _, k := range []string{"id", "appId", "bundleId", "browserId", "tabId", "url"} {
				if str(v[k]) != "" {
					selector = str(v[k])
					break
				}
			}
		}
		selected, err := r.matchTarget(ctx, kind, selector, values)
		if err != nil {
			return tool.Result{}, err
		}
		if selected == nil {
			return tool.Failure("NOT_FOUND"), nil
		}
		target = *selected
	}
	token := newToken()
	result := useResult(map[string]any{"handle_token": token, "ref": target.Public()})
	if result.Error != nil {
		return result, nil
	}
	s.Handles[token] = &Handle{Instance: r.Instance, Lease: r.Lease, Target: target}
	if err := r.Store.Save(ctx, *s); err != nil {
		return tool.Result{}, err
	}
	result.HostFence = TargetReceipt{Lease: r.Lease, Instance: r.Instance, Token: token}
	return result, nil
}
func (r UseRuntime) invoke(ctx context.Context, s *UseState, h *Handle, c BridgeCall, a []any) (tool.Result, error) {
	switch c.Method {
	case "documentation":
		return r.documentation(ctx, "core")
	case "capabilities.list":
		h.Listed = true
		if err := r.Store.Save(ctx, *s); err != nil {
			return tool.Result{}, err
		}
		v := []any{}
		if h.Target.Kind == "tab" {
			v = append(v, map[string]string{"id": "playwright", "description": "Bound DOM locators, navigation, downloads and file chooser"})
		}
		return useResult(v), nil
	case "capabilities.documentation", "playwright.documentation":
		return r.documentation(ctx, "browser")
	case "capabilities.get":
		if len(s.Handles) >= 256 {
			return tool.Failure("RESOURCE_LIMIT"), nil
		}
		if !h.Listed || str(arg(a, 0)) != "playwright" || h.Target.Kind != "tab" {
			return tool.Failure("PERMISSION_DENIED"), nil
		}
		capture, err := r.capture(ctx, h.Target, ObserveOptions{Accessibility: true})
		if err != nil {
			return driverFailure(err), nil
		}
		token := newToken()
		s.Handles[token] = &Handle{Instance: r.Instance, Lease: r.Lease, Target: h.Target, Capability: true, Navigation: capture.Navigation, Parent: c.Token}
		if err := r.Store.Save(ctx, *s); err != nil {
			return tool.Result{}, err
		}
		return useResult(map[string]any{"handle_token": token}), nil
	case "getAXState", "getScreenshot", "getAXStateAndScreenshot", "observe":
		return r.observe(ctx, s, h, c.Method, object(arg(a, 0)))
	case "waitForChange":
		return r.wait(ctx, s, h, object(arg(a, 0)))
	}
	if strings.HasPrefix(c.Method, "playwright.") {
		return r.playwright(ctx, s, h, c.Method, a)
	}
	if h.Capability {
		return tool.Failure("PERMISSION_DENIED"), nil
	}
	return r.action(ctx, s, h, c.Method, a)
}
func (r UseRuntime) observe(ctx context.Context, s *UseState, h *Handle, method string, opts map[string]any) (tool.Result, error) {
	screenshot := method == "getScreenshot" || method == "getAXStateAndScreenshot" || method == "observe"
	if method != "getScreenshot" {
		if !h.AXReady {
			h.AXRevision = ""
		}
		h.AXReady = false
	}
	if screenshot {
		h.ScreenshotReady = false
	}
	if err := r.Store.Save(ctx, *s); err != nil {
		return tool.Result{}, err
	}
	c, err := r.capture(ctx, h.Target, ObserveOptions{Accessibility: method != "getScreenshot", Screenshot: screenshot})
	if err != nil {
		return driverFailure(err), nil
	}
	return r.recordObservation(ctx, s, h, method, opts, c)
}
func (r UseRuntime) recordObservation(ctx context.Context, s *UseState, h *Handle, method string, opts map[string]any, c Capture) (tool.Result, error) {
	screenshot := method == "getScreenshot" || method == "getAXStateAndScreenshot" || method == "observe"
	if len(c.State) > 128<<10 || len(c.Elements) > 2000 || len(c.PNG) > 8<<20 {
		return tool.Failure("RESOURCE_LIMIT"), nil
	}
	if h.Geometry != "" && h.Geometry != c.Geometry {
		h.ScreenshotRevision = ""
	}
	h.Geometry = c.Geometry
	for _, other := range s.Handles {
		if other == h || other.Target.ID != h.Target.ID || other.Capability {
			continue
		}
		if method == "getScreenshot" && !c.HasAX {
			other.ScreenshotRevision = ""
			continue
		}
		if method == "getScreenshot" && other.AXState == c.State {
			other.Elements = c.Elements
		} else {
			other.AXRevision = ""
			other.Elements = nil
		}
		if other.Geometry != c.Geometry || screenshot {
			other.ScreenshotRevision = ""
		}
	}
	if method == "getScreenshot" && c.HasAX {
		if c.State != h.AXState {
			h.AXRevision = ""
			h.Elements = nil
		} else {
			h.Elements = c.Elements
		}
	}
	out := map[string]any{}
	if method != "getScreenshot" {
		base := h.AXRevision
		rev := newToken()
		view, err := r.projectObservation(ctx, ObservationInput{Before: h.AXState, After: c.State, DiffAllowed: base != "", Full: opts["full"] == true})
		if err != nil {
			return tool.Result{}, err
		}
		h.AXRevision, h.AXState, h.Elements = rev, c.State, c.Elements
		ax := map[string]any{"surfaceId": h.Target.ID, "revision": rev, "kind": view.Kind, "state": view.State}
		if view.Kind == "diff" {
			ax["baseRevision"] = base
		}
		out["ax"] = ax
	}
	if screenshot {
		if len(c.PNG) == 0 || c.Width < 1 || c.Height < 1 {
			return tool.Failure("PROVIDER_UNAVAILABLE"), nil
		}

		h.ScreenshotRevision = newToken()
		h.Width, h.Height = c.Width, c.Height
		out["screenshot"] = map[string]any{"surfaceId": h.Target.ID, "revision": h.ScreenshotRevision, "width": c.Width, "height": c.Height, "image": map[string]string{"artifactRef": ""}}
	}
	h.Navigation = c.Navigation
	if err := r.Store.Save(ctx, *s); err != nil {
		return tool.Result{}, err
	}
	var payload any = out
	if method == "getAXState" {
		payload = out["ax"]
	}
	if method == "getScreenshot" {
		payload = out["screenshot"]
	}
	result := useResult(payload)
	var token string
	for k, handle := range s.Handles {
		if handle == h {
			token = k
			break
		}
	}
	result.HostFence = ObservationReceipt{Lease: r.Lease, Token: token, AXRevision: h.AXRevision, ScreenshotRevision: h.ScreenshotRevision, UpdateAX: method != "getScreenshot", UpdateScreenshot: screenshot}
	if screenshot {
		result.Output = &tool.BinaryOutput{Name: "screenshot.png", MediaType: "image/png", Reader: strings.NewReader(string(c.PNG)), ResultJSON: func(ref string) string {
			out["screenshot"].(map[string]any)["image"] = map[string]string{"artifactRef": ref}
			return useResult(payload).Text
		}}
	}
	return result, nil
}
func (r UseRuntime) wait(ctx context.Context, s *UseState, h *Handle, o map[string]any) (tool.Result, error) {
	var requested *int
	if v, ok := o["timeoutMs"].(float64); ok {
		if v < 1 || v > 10000 || math.Trunc(v) != v {
			return tool.Failure("INVALID_ARGUMENT"), nil
		}
		n := int(v)
		requested = &n
	}
	since := str(o["sinceRevision"])
	if since != "" && since != h.AXRevision {
		return tool.Failure("STALE_ELEMENT_REFERENCE"), nil
	}
	// Only the explicit native boundary keeps this original recipe. Managed
	// adapters always supply WaitPolicy, including when the service is unavailable.
	var recipe WaitRecipe
	if r.WaitPolicy != nil {
		var err error
		recipe, err = r.WaitPolicy(ctx, WaitInput{RequestedTimeoutMS: requested})
		if err != nil {
			return tool.Result{}, err
		}
	} else {
		recipe = WaitRecipe{TimeoutMS: 1000, PollMS: 100}
		if requested != nil {
			recipe.TimeoutMS = *requested
		}
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(recipe.TimeoutMS)*time.Millisecond)
	defer cancel()
	baseline := h.AXState
	h.AXReady = false
	if err := r.Store.Save(ctx, *s); err != nil {
		return tool.Result{}, err
	}
	deadline := time.NewTimer(time.Duration(recipe.TimeoutMS) * time.Millisecond)
	defer deadline.Stop()
	for {
		capture, err := r.capture(waitCtx, h.Target, ObserveOptions{Accessibility: true})
		waitEnd, _ := waitCtx.Deadline()
		if waitCtx.Err() == context.DeadlineExceeded || err == context.DeadlineExceeded || !time.Now().Before(waitEnd) {
			return waitTimeout(h), nil
		}
		if err != nil {
			return driverFailure(err), nil
		}
		if capture.State != baseline {
			return r.recordObservation(ctx, s, h, "getAXState", o, capture)
		}
		select {
		case <-ctx.Done():
			return tool.Failure("CANCELLED"), nil
		case <-deadline.C:
			return waitTimeout(h), nil
		case <-time.After(time.Duration(recipe.PollMS) * time.Millisecond):
		}
	}
}
func element(h *Handle, v any) (Element, bool) {
	index := -1
	switch t := v.(type) {
	case float64:
		if t != math.Trunc(t) {
			return Element{}, false
		}
		index = int(t)
	case map[string]any:
		if str(t["observationRevision"]) != h.AXRevision {
			return Element{}, false
		}
		n, ok := t["index"].(float64)
		if !ok || n != math.Trunc(n) {
			return Element{}, false
		}
		index = int(n)
	}
	if h.AXRevision == "" || !h.AXReady {
		return Element{}, false
	}
	for _, e := range h.Elements {
		if e.Index == index {
			return e, true
		}
	}
	return Element{}, false
}
func pointer(h *Handle, v any, m map[string]any) string {
	if xy, ok := v.([]any); ok {
		if h.ScreenshotRevision == "" || !h.ScreenshotReady {
			return "SCREENSHOT_REQUIRED"
		}
		if len(xy) != 2 {
			return "INVALID_ARGUMENT"
		}
		x, xok := xy[0].(float64)
		y, yok := xy[1].(float64)
		if !xok || !yok || x < 0 || y < 0 || x >= float64(h.Width) || y >= float64(h.Height) {
			return "INVALID_ARGUMENT"
		}
		m["x"], m["y"], m["geometry"] = x, y, h.Geometry
		return ""
	}
	e, ok := element(h, v)
	if !ok {
		return "STALE_ELEMENT_REFERENCE"
	}
	m["element_token"] = e.Token
	return ""
}
func invalidate(s *UseState, _ string) {
	for _, h := range s.Handles {
		if !h.Capability {
			h.AXRevision = ""
			h.ScreenshotRevision = ""
			h.Elements = nil
		}
	}
}
func (r UseRuntime) action(ctx context.Context, s *UseState, h *Handle, method string, a []any) (tool.Result, error) {
	surface := "native"
	if h.Target.Kind == "tab" {
		surface = "tab"
	}
	var parameters ActionParameters
	if ActionParameterMethod("builtin__computer_invoke", method) {
		var err error
		parameters, err = r.actionParameters(ctx, method, surface, a)
		if err != nil {
			return tool.Result{}, err
		}
	}
	m := map[string]any{"expectedNavigation": h.Navigation}
	switch method {
	case "click", "scroll":
		if code := pointer(h, arg(a, 0), m); code != "" {
			return tool.Failure(code), nil
		}
		if parameters.Failure != "" {
			return tool.Failure(parameters.Failure), nil
		}
		if method == "click" {
			opts := map[string]any{}
			if parameters.Button != "" {
				opts["button"] = parameters.Button
			}
			if parameters.Count != nil {
				opts["count"] = float64(*parameters.Count)
			}
			m["options"] = opts
		} else {
			m["direction"] = parameters.Direction
			m["amount"] = nil
			if parameters.Amount != nil {
				m["amount"] = *parameters.Amount
			}
		}
	case "drag":
		if _, ok := arg(a, 0).([]any); !ok {
			return tool.Failure("INVALID_ARGUMENT"), nil
		}
		if _, ok := arg(a, 1).([]any); !ok {
			return tool.Failure("INVALID_ARGUMENT"), nil
		}
		if code := pointer(h, arg(a, 0), m); code != "" {
			return tool.Failure(code), nil
		}
		to := map[string]any{}
		if code := pointer(h, arg(a, 1), to); code != "" {
			return tool.Failure(code), nil
		}
		if parameters.Failure != "" {
			return tool.Failure(parameters.Failure), nil
		}
		m["to_x"], m["to_y"] = to["x"], to["y"]
	case "setValue", "selectText", "performSecondaryAction":
		e, ok := element(h, arg(a, 0))
		if !ok {
			return tool.Failure("STALE_ELEMENT_REFERENCE"), nil
		}
		if parameters.Failure != "" {
			return tool.Failure(parameters.Failure), nil
		}
		m["element_token"], m["value"] = e.Token, parameters.Value
		if method == "selectText" && h.Target.Kind != "tab" {
			input := SelectionInput{Value: e.Value, Wanted: parameters.Value, Role: e.Role}
			if !selectionSourceValid(input) {
				return tool.Failure("INVALID_ARGUMENT"), nil
			}
			plan, err := r.selection(ctx, input)
			if err != nil {
				return tool.Result{}, err
			}
			if !ValidSelection(input, plan) {
				return tool.Failure("PROVIDER_UNAVAILABLE"), nil
			}
			m["selection_dispatch"] = SelectionDispatch{Recipe: plan, BeforeInput: r.Store.Fence}
		}
		if method == "performSecondaryAction" {
			found := false
			for _, action := range e.Actions {
				found = found || action == parameters.Value
			}
			if !found {
				return tool.Failure("PERMISSION_DENIED"), nil
			}
		}
	case "typeText", "pressKey", "paste":
		if h.Target.Kind == "tab" && arg(a, 0) != nil {
			e, ok := element(h, arg(a, 0))
			if !ok {
				return tool.Failure("STALE_ELEMENT_REFERENCE"), nil
			}
			m["element_token"] = e.Token
		}
		if parameters.Failure != "" {
			return tool.Failure(parameters.Failure), nil
		}
		m["text"] = parameters.Text
		if method == "pressKey" && h.Target.Kind != "tab" {
			if parameters.KeyInput == nil {
				return tool.Failure("PROVIDER_UNAVAILABLE"), nil
			}
			m["key_dispatch"] = KeyDispatch{Recipe: *parameters.KeyInput, BeforeInput: r.Store.Fence}
		}
	case "goto", "back", "forward", "reload", "close":
		if h.Target.Kind != "tab" {
			return tool.Failure("NOT_FOUND"), nil
		}
		if parameters.Failure != "" {
			return tool.Failure(parameters.Failure), nil
		}
		m["url"] = parameters.URL
	default:
		return tool.Failure("NOT_FOUND"), nil
	}
	// References and original effect lifecycle remain mandatory Host mechanisms.
	invalidate(s, h.Target.ID)
	if err := r.Store.Save(ctx, *s); err != nil {
		return tool.Result{}, err
	}
	value, err := r.Driver.Act(ctx, r.Instance, h.Target, r.Lease, method, m)
	if err != nil {
		return driverFailure(err), nil
	}
	return useResult(value), nil
}

func (r UseRuntime) playwright(ctx context.Context, s *UseState, h *Handle, method string, a []any) (tool.Result, error) {
	if !h.Capability {
		return tool.Failure("PERMISSION_DENIED"), nil
	}
	c, err := r.capture(ctx, h.Target, ObserveOptions{Accessibility: true})
	if err != nil {
		return driverFailure(err), nil
	}
	if c.Navigation != h.Navigation {
		return tool.Failure("STALE_CAPABILITY_HANDLE"), nil
	}
	if !PlaywrightParameterMethod(method) {
		return tool.Failure("NOT_FOUND"), nil
	}
	original := object(arg(a, 0))
	original["expectedNavigation"] = h.Navigation
	if len(stringify(original)) > 16384 {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	var m map[string]any
	var parameters ActionParameters
	if r.ReadActionParameters == nil {
		// Facade-v1 passthrough only for pre-contract snapshots (through
		// seven-service 1.0.3) and explicit unbundled carriers. Remove when
		// no retained execution/carrier depends on it; no managed fallback.
		m = original
	} else {
		parameters, err = r.actionParameters(ctx, method, "tab", a)
		if err != nil {
			return tool.Result{}, err
		}
		if parameters.Failure != "" {
			return tool.Failure(parameters.Failure), nil
		}
		if !ValidActionParameters(method, "tab", a, parameters) {
			return tool.Failure("PROVIDER_UNAVAILABLE"), nil
		}
		m = map[string]any{"timeoutMs": *parameters.TimeoutMS}
		if method == "playwright.waitForURL" {
			m["url"] = parameters.URL
		} else {
			m["selector"] = parameters.Selector
		}
		if method == "playwright.fill" {
			m["value"] = parameters.Value
		}
	}
	m["expectedNavigation"] = h.Navigation
	var upload *BrowserUploadRequest
	if method == "playwright.upload" {
		file := object(original["file"])
		p := str(file["path"])
		if str(file["computerId"]) != r.Instance.ComputerID || !SafeTransferPath(p) || !s.Files[p] {
			return tool.Failure("PERMISSION_DENIED"), nil
		}
		m["path"] = p
		delete(m, "file")
		if HasUploadPresentation(parameters) {
			if r.UploadBrowser == nil {
				return tool.Failure("PROVIDER_UNAVAILABLE"), nil
			}
			upload = &BrowserUploadRequest{Path: p, Navigation: h.Navigation, Selector: parameters.Selector, TimeoutMS: *parameters.TimeoutMS, Name: parameters.Name, MediaType: parameters.MediaType}
		} else if r.UploadBrowser != nil {
			return tool.Failure("PROVIDER_UNAVAILABLE"), nil
		}
	}
	if method == "playwright.download" {
		m["path"] = "downloads/" + newToken()
	}
	if !readMethod(method) {
		invalidate(s, h.Target.ID)
		if err := r.Store.Save(ctx, *s); err != nil {
			return tool.Result{}, err
		}
	}
	var value any
	if upload != nil {
		value, err = r.UploadBrowser.Upload(ctx, r.Instance, h.Target, r.Lease, *upload)
	} else {
		// For upload, only pre-service graphs (through nine-service 1.0.5) and
		// explicit unbundled carriers retain facade-v1 FileChooser presentation.
		// Remove its branch/source only after these executions and references,
		// and the separately unmigrated browser operations, have no consumers.
		value, err = r.Driver.Act(ctx, r.Instance, h.Target, r.Lease, method, m)
	}
	if err != nil {
		return driverFailure(err), nil
	}
	if method == "playwright.download" {
		p := str(m["path"])
		s.Files[p] = true
		if err := r.Store.Save(ctx, *s); err != nil {
			return tool.Result{}, err
		}
		value = map[string]any{"computerId": r.Instance.ComputerID, "path": p}
	}
	return useResult(value), nil
}
func (r UseRuntime) transfer(ctx context.Context, s *UseState, method string, m map[string]any) (tool.Result, error) {
	if method == "importArtifact" {
		ref := str(m["artifactRef"])
		p := str(m["destination"])
		if p == "" {
			p = "imports/" + newToken()
		}
		if !SafeTransferPath(p) || m["overwrite"] == true && !s.Files[p] {
			return tool.Failure("PERMISSION_DENIED"), nil
		}
		if !s.Files[p] && len(s.Files) >= 256 {
			return tool.Failure("RESOURCE_LIMIT"), nil
		}
		f, err := r.Store.OpenArtifact(ctx, ref)
		if err != nil {
			return tool.Failure("NOT_FOUND"), nil
		}
		defer f.Close()
		err = r.Driver.WriteFile(ctx, r.Instance, p, m["overwrite"] == true, f)
		if err != nil {
			return driverFailure(err), nil
		}
		s.Files[p] = true
		if err := r.Store.Save(ctx, *s); err != nil {
			return tool.Result{}, err
		}
		return useResult(map[string]any{"computerId": r.Instance.ComputerID, "path": p}), nil
	}
	p := str(m["path"])
	if !SafeTransferPath(p) || !s.Files[p] {
		return tool.Failure("PERMISSION_DENIED"), nil
	}

	var name, media string
	if r.ReadActionParameters == nil {
		// Facade-v1 presentation only for pre-transfer snapshots (through actual
		// eight-service 1.0.4) and explicit unbundled carriers. Remove after their
		// retained executions/references are gone; no managed failure fallback.
		name, media = str(m["name"]), str(m["mediaType"])
		if name == "" {
			name = path.Base(p)
		}
		if media == "" {
			media = "application/octet-stream"
		}
	} else {
		parameters, err := r.actionParameters(ctx, method, "", []any{m})
		if err != nil {
			return tool.Result{}, err
		}
		if parameters.Failure != "" {
			return tool.Failure(parameters.Failure), nil
		}
		if !ValidActionParameters(method, "", []any{m}, parameters) {
			return tool.Failure("PROVIDER_UNAVAILABLE"), nil
		}
		name, media = parameters.Name, parameters.MediaType
	}
	f, err := r.Driver.ReadFile(ctx, r.Instance, p)
	if err != nil {
		return driverFailure(err), nil
	}

	return tool.Result{Output: &tool.BinaryOutput{Name: name, MediaType: media, Reader: f, ResultJSON: func(ref string) string {
		return useResult(map[string]any{"artifactId": ref, "name": name, "mediaType": media}).Text
	}}}, nil
}
func driverFailure(err error) tool.Result {
	if errors.Is(err, ErrUnknown) {
		return tool.Failure("UNKNOWN_SIDE_EFFECT")
	}
	if err == context.Canceled {
		return tool.Failure("CANCELLED")
	}
	if err == context.DeadlineExceeded {
		return tool.Failure("TIMEOUT")
	}
	if err == ErrInvalid {
		return tool.Failure("INVALID_ARGUMENT")
	}
	if err == ErrElement {
		return tool.Failure("STALE_ELEMENT_REFERENCE")
	}
	if err == ErrStale {
		return tool.Failure("STALE_SURFACE_HANDLE")
	}
	if err == ErrIntegrity {
		return tool.Failure("PERMISSION_DENIED")
	}
	return tool.Failure("PROVIDER_UNAVAILABLE")
}

func waitTimeout(h *Handle) tool.Result {
	result := tool.Failure("WAIT_TIMEOUT")
	summary := h.AXState
	if len(summary) > 2048 {
		summary = summary[:2048]
	}
	result.Error.Details = map[string]any{"observation": map[string]any{"surfaceId": h.Target.ID, "revision": h.AXRevision, "state": summary}}
	return result
}

// legacyLineDiff preserves AX facade v1 for explicit native Runs/carriers.
// Managed observation never uses it after activation; remove it only when no
// retained native execution or documented unbundled consumer depends on it.
func legacyLineDiff(before, after string) string {
	if before == after {
		return ""
	}
	a, b := strings.Split(before, "\n"), strings.Split(after, "\n")
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	out := []string{fmt.Sprintf("@@ %d,%d,%d @@", prefix, len(a)-prefix-suffix, len(b)-prefix-suffix)}
	for _, line := range a[prefix : len(a)-suffix] {
		out = append(out, "- "+line)
	}
	for _, line := range b[prefix : len(b)-suffix] {
		out = append(out, "+ "+line)
	}
	return strings.Join(out, "\n")
}
