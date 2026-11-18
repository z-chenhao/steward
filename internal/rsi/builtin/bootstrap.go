// Package builtin owns the Host's trusted initial bundle and acceptance cases.
// Candidate implementations cannot change these gates or activation authority.
package builtin

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	accounts "github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	appsinterface "github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	apps "github.com/z-chenhao/assistant/internal/plugin/appsmethods/contract"
	artifact "github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
	capabilityforms "github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	compact "github.com/z-chenhao/assistant/internal/plugin/contextcompact/contract"
	docx "github.com/z-chenhao/assistant/internal/plugin/docxreader/contract"
	feishuchat "github.com/z-chenhao/assistant/internal/plugin/feishuchat/contract"
	github "github.com/z-chenhao/assistant/internal/plugin/githubfollowup/contract"
	ocr "github.com/z-chenhao/assistant/internal/plugin/imageocr/contract"
	"github.com/z-chenhao/assistant/internal/plugin/imageocr/driver"
	ui "github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	mcp "github.com/z-chenhao/assistant/internal/plugin/mcpbridge/contract"
	memoryinspector "github.com/z-chenhao/assistant/internal/plugin/memoryinspector/contract"
	modelconnections "github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	referencepricing "github.com/z-chenhao/assistant/internal/plugin/referencepricing/contract"
	schedule "github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	servicewatchdog "github.com/z-chenhao/assistant/internal/plugin/servicewatchdog/contract"
	terminalinterface "github.com/z-chenhao/assistant/internal/plugin/terminalinterface/contract"
	discovery "github.com/z-chenhao/assistant/internal/plugin/tooldiscovery/contract"
	extract "github.com/z-chenhao/assistant/internal/plugin/webextract/contract"
	webworkbench "github.com/z-chenhao/assistant/internal/plugin/webworkbench/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type Directories struct{ DOCX, OCR, Trace, Context, Recall, Conversation, Curator, Evidence, Skills, Experience, Task, Progress, Feedback, Delegation, CodeMode, Files, Shell, Coding, Computer, Web, Extract, MCP, Apps, Discovery, AppsDirectory, AppsAccounts, AppsLogos, AppsInterface, GitHub, Artifact, UI, Schedule, MemoryInspector, ModelConnections, Feishu, CapabilityForms, Terminal, ReferencePricing, WebWorkbench, ServiceWatchdog string }

// EnsureDefault selects the shipped composition only for a never-selected
// installation. Operator choices, stops and deactivation survive restart; later
// upgrades use the regular independent verification and activation gates.
func EnsureDefault(ctx context.Context, h rsi.Host, d Directories, web ...WebExtract) error {
	status, err := h.Store.Status(ctx)
	if err != nil {
		return err
	}
	if status.Epoch != 0 || status.Stopped {
		return nil
	}
	converged := func(err error) error {
		if errors.Is(err, rsi.ErrConflict) {
			if current, e := h.Store.Status(ctx); e == nil && current.Epoch > 0 {
				return nil
			}
		}
		return err
	}
	if d.DOCX == "" {
		return rsi.ErrInvalid
	}
	if len(web) > 1 || (d.Web == "") != (d.Extract == "") || d.Web != "" && len(web) != 1 || d.Web == "" && len(web) != 0 {
		return rsi.ErrInvalid
	}
	bindings := []rsiplugin.Binding{}
	traceWorkbench, traceActivity, traceLegacy := false, false, false
	uiPlan, uiCards, uiTable, uiChart, uiForm := false, false, false, false, false
	uiVersion := ""
	feedbackVersion := ""
	scheduleVersion := ""
	appsClient := false
	appsInventory := false
	codingChecks := false
	codingProject := false
	for _, p := range bundledPlugins(&d) {
		if *p.Directory == "" {
			continue
		}
		installed, err := h.Install(ctx, *p.Directory)
		if err != nil {
			return err
		}
		if installed.Manifest.PluginID != p.ID {
			return rsi.ErrInvalid
		}
		if p.Name == "task-feedback" {
			feedbackVersion = installed.Manifest.Version
		}
		if p.Name == "schedule" {
			scheduleVersion = installed.Manifest.Version
		}
		if p.Name == "coding-workspace" {
			for _, service := range installed.Manifest.Provides {
				if service.Contract == "steward.coding-project-plan/1" {
					codingProject = true
				}
				if service.Contract == "steward.coding-check-limits/1" {
					codingChecks = true
				}
			}
		}
		if p.Name == "trace-inspector" {
			traceLegacy = installed.Manifest.Version == "1.0.1"
			for _, service := range installed.Manifest.Provides {
				if service.Contract == "steward.trace-activity/1" {
					traceActivity = true
				}
				if service.Contract == "steward.trace-workbench/1" {
					traceWorkbench = true
				}
			}
		}
		if p.Name == "apps-accounts" {
			for _, service := range installed.Manifest.Provides {
				if service.Contract == accounts.Inventory {
					appsInventory = true
				}
			}
		}
		if p.Name == "apps-interface" {
			for _, service := range installed.Manifest.Provides {
				if service.Contract == appsinterface.BrowserProfileInterface {
					appsClient = true
				}
			}
		}
		if p.Name == "intelligent-ui" {
			uiVersion = installed.Manifest.Version
			for _, service := range installed.Manifest.Provides {
				if service.Contract == ui.FormViews {
					uiForm = true
				}
				if service.Contract == ui.ChartViews {
					uiChart = true
				}
				if service.Contract == ui.TableViews {
					uiTable = true
				}
				if service.Contract == ui.CardsViews {
					uiCards = true
				}
				if service.Contract == ui.PlanViews {
					uiPlan = true
				}
			}
		}
		if p.Name == "web-extract" && web[0].Module != nil {
			installed, err = installOwnerExtract(ctx, h, installed, web[0].Module)
			if err != nil {
				return err
			}
		}
		config := json.RawMessage(`{}`)
		if p.Name == "computer-use" {
			config = computerConfig()
		}
		if p.Name == "web-extract" {
			config, _ = json.Marshal(web[0].Config)
		}
		bindings = append(bindings, rsiplugin.Binding{InstanceID: p.Name, PackageDigest: installed.Digest, Config: config, StateNamespace: p.Name, StateVersion: installed.Manifest.State.Version})
	}
	g, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: bindings})
	if err != nil {
		return err
	}
	document, err := documentFixture()
	if err != nil {
		return err
	}
	policy := rsi.Policy{ID: "builtin-docx", AllowedContracts: []string{docx.Contract}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{
		{InstanceID: "docx-reader", Contract: docx.Contract, Input: json.RawMessage(`{"input_id":"document"}`), Inputs: []rsi.InputData{{ID: "document", Data: document}}, Expected: json.RawMessage(`{"text":"Steward DOCX 验收"}`)},
		{InstanceID: "docx-reader", Contract: docx.Contract, Input: json.RawMessage(`{"input_id":"document"}`), Inputs: []rsi.InputData{{ID: "document", Data: []byte("not a DOCX package")}}, Expected: json.RawMessage(`{"error_code":"INVALID_ARGUMENT"}`)},
	}}
	if d.OCR != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, ocr.Contract, ocr.TSVContract)
		policy.AllowedRequests = append(policy.AllowedRequests, "compute.ocr")
		if h.Compute == nil {
			h.Compute = driver.New(nil)
		}
		identity, err := h.Compute.Identity(ctx)
		if err != nil {
			return err
		}
		unavailable := json.RawMessage(`{"error_code":"PROVIDER_UNAVAILABLE"}`)
		if identity != "image-ocr-v1:unavailable" {
			unavailable = json.RawMessage(`{"error_code":"INVALID_ARGUMENT"}`)
		}
		policy.Cases = append(policy.Cases,
			rsi.Case{InstanceID: "image-ocr", Contract: ocr.TSVContract, Input: json.RawMessage(`{"input_id":"tsv","width":100,"height":50,"languages":["eng"]}`), Inputs: []rsi.InputData{{ID: "tsv", Data: []byte("level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n5\t1\t1\t1\t1\t1\t4\t8\t20\t10\t98.5\tSteward\n")}}, Expected: json.RawMessage(`{"document":{"method":"ocr","languages":["eng"],"width":100,"height":50,"text":"Steward","words":[{"text":"Steward","x":4,"y":8,"width":20,"height":10,"confidence":98.5}],"truncated":false}}`)},
			rsi.Case{InstanceID: "image-ocr", Contract: ocr.Contract, Input: json.RawMessage(`{"input_id":"image","media_type":"image/png","languages":[]}`), Inputs: []rsi.InputData{{ID: "image", Data: []byte("unavailable native engine fixture")}}, Expected: unavailable},
		)
		// Host-owned TSV and invalid-image fixtures verify normalization and
		// driver errors using the actual optional native resource identity.
		// Real pixel extraction has a separate integration gate; these cases
		// are not OCR quality measurements.
	}
	if d.Trace != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, "steward.trace-layout/1", "steward.trace-diff/1", "steward.trace-tool-preview/1")
		policy.Cases = append(policy.Cases, traceCase(traceLegacy), traceDiffCase(), traceToolCase())
		if traceWorkbench || traceActivity {
			cases, err := traceWorkbenchCases()
			if err != nil {
				return err
			}
			for _, c := range cases {
				if c.Contract == "steward.trace-workbench/1" && traceWorkbench || c.Contract == "steward.trace-activity/1" && traceActivity {
					policy.AllowedContracts = append(policy.AllowedContracts, c.Contract)
					policy.Cases = append(policy.Cases, c)
				}
			}
		}
	}
	if d.Context != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, compact.Contract)
		policy.Cases = append(policy.Cases, contextCases()...)
	}
	if d.Recall != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, "steward.recall-selection/1", "steward.recall-query/1")
		policy.Cases = append(policy.Cases, recallCases()...)
	}
	if d.Conversation != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, "steward.conversation-policy/1")
		policy.Cases = append(policy.Cases, conversationCase())
	}
	if d.Curator != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, "steward.memory-curation-policy/1", "steward.memory-dream-schedule/1")
		policy.Cases = append(policy.Cases, curatorCases()...)
	}
	if d.Evidence != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, "steward.evidence-text-window/1", "steward.evidence-link-window/1")
		policy.Cases = append(policy.Cases, evidenceCases()...)
	}
	if d.Skills != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, "steward.skill-discovery/1", "steward.skill-entry/1")
		policy.Cases = append(policy.Cases, skillCases()...)
	}
	if d.Experience != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, "steward.experience-policy/1", "steward.browser-demonstration/1")
		policy.Cases = append(policy.Cases, experienceCases()...)
	}
	if d.Task != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, "steward.task-discovery/1", "steward.task-label/1")
		policy.Cases = append(policy.Cases, taskCases()...)
	}
	if d.Progress != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, "steward.task-progress-guidance/1", "steward.task-progress-selection/1")
		policy.Cases = append(policy.Cases, progressCases()...)
	}
	if d.Feedback != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, "steward.task-feedback-policy/1")
		policy.Cases = append(policy.Cases, feedbackCases(feedbackVersion)...)
	}
	if d.Delegation != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, "steward.delegation-policy/1")
		policy.Cases = append(policy.Cases, delegationCases()...)
	}
	if d.CodeMode != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, "steward.codemode-limits/1", "steward.codemode-input-type/1")
		policy.Cases = append(policy.Cases, codeModeCases()...)
	}
	if d.Files != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, "steward.file-window/1", "steward.file-edit-span/1")
		policy.Cases = append(policy.Cases, fileCases()...)
	}
	if d.Shell != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, "steward.shell-limits/1")
		policy.Cases = append(policy.Cases, shellCases()...)
	}
	if d.Coding != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, "steward.coding-import-limits/1", "steward.coding-report-recipe/1")
		policy.Cases = append(policy.Cases, codingCases()...)
		if codingProject {
			policy.AllowedContracts = append(policy.AllowedContracts, "steward.coding-project-plan/1")
			policy.Cases = append(policy.Cases, codingProjectCases()...)
		}
		if codingChecks {
			policy.AllowedContracts = append(policy.AllowedContracts, "steward.coding-check-limits/1")
			policy.Cases = append(policy.Cases, codingCheckCases()...)
		}
	}
	if d.Computer != "" {
		computer, err := computerPolicy()
		if err != nil {
			return err
		}
		policy.AllowedContracts = append(policy.AllowedContracts, computer.AllowedContracts...)
		policy.Cases = append(policy.Cases, computer.Cases...)
	}
	if d.Web != "" {
		value := WebPolicy()
		policy.AllowedContracts = append(policy.AllowedContracts, value.AllowedContracts...)
		policy.AllowedRequests = append(policy.AllowedRequests, "resource.call", "compute.web-extract")
		policy.AllowedContracts = append(policy.AllowedContracts, extract.Contract)
		policy.Cases = append(policy.Cases, value.Cases...)
		cases, err := webExtractCases(web[0])
		if err != nil {
			return err
		}
		policy.Cases = append(policy.Cases, cases...)
	}
	if d.MCP != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, mcp.Method, mcp.Namespace)
		policy.AllowedRequests = append(policy.AllowedRequests, "resource.call")
		policy.Cases = append(policy.Cases, mcpCases()...)
	}

	if d.Apps != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, apps.Method, apps.Guidance)
		policy.AllowedRequests = append(policy.AllowedRequests, "resource.call")
		policy.Cases = append(policy.Cases, appsCases()...)
	}

	if d.Discovery != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, discovery.Contract)
		policy.Cases = append(policy.Cases, toolDiscoveryCases()...)
	}
	if d.GitHub != "" {
		cases, err := githubCases()
		if err != nil {
			return err
		}
		policy.AllowedContracts = append(policy.AllowedContracts, github.Contract)
		policy.Cases = append(policy.Cases, cases...)
	}
	if d.Artifact != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, artifact.Recipe, artifact.Read, artifact.Search, artifact.Cleanup)
		policy.Cases = append(policy.Cases, artifactCases()...)
	}
	if d.ServiceWatchdog != "" {
		cases, err := serviceWatchdogCases()
		if err != nil {
			return err
		}
		policy.AllowedContracts = append(policy.AllowedContracts, servicewatchdog.Service)
		policy.Cases = append(policy.Cases, cases...)
	}
	if d.WebWorkbench != "" {
		cases, err := webWorkbenchCases()
		if err != nil {
			return err
		}
		policy.AllowedContracts = append(policy.AllowedContracts, webworkbench.Service)
		policy.Cases = append(policy.Cases, cases...)
	}
	if d.ReferencePricing != "" {
		cases, err := referencePricingCases()
		if err != nil {
			return err
		}
		policy.AllowedContracts = append(policy.AllowedContracts, referencepricing.Service)
		policy.Cases = append(policy.Cases, cases...)
	}
	if d.Terminal != "" {
		cases, err := terminalCases()
		if err != nil {
			return err
		}
		policy.AllowedContracts = append(policy.AllowedContracts, terminalinterface.Service)
		policy.Cases = append(policy.Cases, cases...)
	}
	if d.CapabilityForms != "" {
		cases, err := capabilityFormCases()
		if err != nil {
			return err
		}
		policy.AllowedContracts = append(policy.AllowedContracts, capabilityforms.Service, capabilityforms.PresentationService)
		policy.Cases = append(policy.Cases, cases...)
	}
	if d.Feishu != "" {
		cases, err := feishuCases()
		if err != nil {
			return err
		}
		policy.AllowedContracts = append(policy.AllowedContracts, feishuchat.Status, feishuchat.Card)
		policy.Cases = append(policy.Cases, cases...)
	}
	if d.ModelConnections != "" {
		cases, err := modelMenuCases()
		if err != nil {
			return err
		}
		policy.AllowedContracts = append(policy.AllowedContracts, modelconnections.Menu, modelconnections.Settings, modelconnections.ConnectionPolicy, modelconnections.Picker)
		policy.Cases = append(policy.Cases, cases...)
	}
	if d.MemoryInspector != "" {
		cases, err := memoryInspectorCases()
		if err != nil {
			return err
		}
		policy.AllowedContracts = append(policy.AllowedContracts, memoryinspector.Presentation)
		policy.Cases = append(policy.Cases, cases...)
	}
	if d.Schedule != "" {
		policy.AllowedContracts = append(policy.AllowedContracts, schedule.Next, schedule.LocalTime, schedule.Frequency, schedule.Projection, schedule.Display, schedule.InstructionLabel, schedule.Guidance, schedule.Defaults, schedule.Threshold)
		policy.Cases = append(policy.Cases, scheduleCases(scheduleVersion)...)
	}
	if d.UI != "" {
		cases, err := uiCases(d.UI, uiVersion, uiPlan, uiCards, uiTable, uiChart, uiForm)
		if err != nil {
			return err
		}
		policy.AllowedContracts = append(policy.AllowedContracts, ui.Normalize, ui.DefinitionContract, ui.PreviewContract, ui.FormContract)
		if uiForm {
			policy.AllowedContracts = append(policy.AllowedContracts, ui.FormViews)
		}
		if uiChart {
			policy.AllowedContracts = append(policy.AllowedContracts, ui.ChartViews)
		}
		if uiTable {
			policy.AllowedContracts = append(policy.AllowedContracts, ui.TableViews)
		}
		if uiCards {
			policy.AllowedContracts = append(policy.AllowedContracts, ui.CardsViews)
		}
		if uiPlan {
			policy.AllowedContracts = append(policy.AllowedContracts, ui.PlanViews)
		}
		policy.Cases = append(policy.Cases, cases...)
	}
	if d.AppsDirectory != "" || d.AppsAccounts != "" || d.AppsLogos != "" || d.AppsInterface != "" {
		owner, err := appsFeatureCases()
		if err == nil && appsClient {
			owner, err = appsClientCases(owner)
		}
		if err != nil {
			return err
		}
		if appsInventory {
			extra, e := appsAccountInventoryCases()
			if e != nil {
				return e
			}
			owner = append(owner, extra...)
		}
		for _, feature := range []struct{ directory, instance string }{
			{d.AppsDirectory, "apps-directory"}, {d.AppsAccounts, "apps-accounts"},
			{d.AppsLogos, "apps-logos"}, {d.AppsInterface, "apps-interface"},
		} {
			if feature.directory == "" {
				continue
			}
			for _, c := range owner {
				if c.InstanceID == feature.instance {
					policy.Cases = append(policy.Cases, c)
					if !slices.Contains(policy.AllowedContracts, c.Contract) {
						policy.AllowedContracts = append(policy.AllowedContracts, c.Contract)
					}
				}
			}
		}
	}

	digest, err := rsiplugin.Digest("steward.rsi.builtin-policy/1", policy)
	if err != nil {
		return err
	}
	policy.ID = "builtin-docx-" + strings.TrimPrefix(digest, "sha256:")
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		return err
	}
	id := "bootstrap-docx-" + strings.TrimPrefix(g.Revision, "sha256:")
	if err = h.Propose(ctx, rsi.Candidate{ID: id, Baseline: "", Target: g.Revision, PolicyID: policy.ID, Goal: "Enable verified versioned feature composition for new work."}); err != nil {
		return converged(err)
	}
	decision, err := h.Verify(ctx, id)
	if err != nil {
		return err
	}
	_, err = h.Activate(ctx, rsi.Activation{OperationID: id, Expected: "", Target: g.Revision, DecisionID: decision})
	return converged(err)
}

// Replace only the declared method asset with independently captured owner
// source. The wrapper and permissions stay those of the validated template.
// Distinct source must have its own version as well as its own content digest.
func installOwnerExtract(ctx context.Context, h rsi.Host, template rsi.Package, module []byte) (rsi.Package, error) {
	directory, err := os.MkdirTemp("", "steward-owner-extract-")
	if err != nil {
		return rsi.Package{}, err
	}
	defer os.RemoveAll(directory)
	found := false
	for _, file := range template.Manifest.Files {
		raw, err := os.ReadFile(filepath.Join(h.Catalog.Path(template.Digest), file.Path))
		if err != nil {
			return rsi.Package{}, err
		}
		if file.Path == extract.ModuleFile {
			raw = module
			found = true
		}
		mode := os.FileMode(0600)
		if file.Executable {
			mode = 0700
		}
		path := filepath.Join(directory, file.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return rsi.Package{}, err
		}
		if err := os.WriteFile(path, raw, mode); err != nil {
			return rsi.Package{}, err
		}
	}
	if !found {
		return rsi.Package{}, rsi.ErrInvalid
	}
	variant := template.Manifest
	separator := "-"
	if strings.Contains(variant.Version, "-") {
		separator = "."
	}
	variant.Version += separator + "owner." + fmt.Sprintf("%x", sha256.Sum256(module))
	manifest, err := rsi.Seal(directory, variant)
	if err != nil {
		return rsi.Package{}, err
	}
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(directory, "steward.plugin.json"), raw, 0600); err != nil {
		return rsi.Package{}, err
	}
	return h.Install(ctx, directory)
}

func documentFixture() ([]byte, error) {
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	parts := [][2]string{
		{"_rels/.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="r1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"[Content_Types].xml", `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`},
		{"word/document.xml", `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Steward DOCX 验收</w:t></w:r></w:p></w:body></w:document>`},
	}
	for _, part := range parts {
		w, err := archive.Create(part[0])
		if err != nil {
			return nil, err
		}
		if _, err = w.Write([]byte(part[1])); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
