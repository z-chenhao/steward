package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/computer/macosvm"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// These integration fixtures script an actively running Loop while compiling
// and verifying real candidate processes. Production Loop renews its original
// Run/Code lease throughout that work. Explicit expiry/recovery tests retain
// computerClaims without this pump; no claim is ever reacquired or revived.
func computerPluginClaims(t *testing.T, configure ...func(*Store, *Runs)) (*Store, *Runs, []*codeBridge, []tool.Invocation) {
	t.Helper()
	s, r, bridges, invs := computerClaims(t, configure...)
	seen := map[string]bool{}
	for _, b := range bridges {
		if !seen[b.t.claim.RunID()] {
			keepComputerPluginOwner(t, b)
			seen[b.t.claim.RunID()] = true
		}
	}
	return s, r, bridges, invs
}
func keepComputerPluginOwner(t *testing.T, b *codeBridge) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() { cancel(); <-done })
	go func() {
		defer close(done)
		ticker := time.NewTicker(leaseDuration / 4)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := b.t.runs.Renew(ctx, b.t.claim); err != nil {
					return
				}
			}
		}
	}()
}

func installComputerPlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	return installRuntimeFeature(t, s, r, "computer-use", computerOwnerPolicy(t), computerBrowserFixtureConfig())
}

func computerOwnerPolicy(t *testing.T) rsi.Policy {
	t.Helper()
	golden, err := os.ReadFile("../../rsi/builtin/computer-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	var owner rsi.Policy
	if err = json.Unmarshal(golden, &owner); err != nil {
		t.Fatal(err)
	}
	if len(owner.AllowedContracts) != 11 || len(owner.Cases) != 60 {
		t.Fatal("independent Host acceptance must cover all Computer services")
	}
	owner.ID = "computer-owner-fixture"
	return owner
}

func computerVersionPackage(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../../plugin/computeruse/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.8"`, `Version: "`+version+`"`, 1)
	code = regexp.MustCompile(`(:)[ \t]+(selectText|orderInventory|matchTargets|actionParameters|playwrightParameters|transferPresentation|browserUploadParameters|browserProjection),`).ReplaceAllString(code, `$1 $2,`)
	if !computerVersionHasBrowser(version) {
		code = strings.Replace(code, `contract.BrowserProjectionContract: browserProjection,`, "", 1)
	}
	if !computerVersionHasUpload(version) {
		code = strings.Replace(code, `contract.BrowserUploadContract: browserUploadParameters,`, "", 1)
	}
	if version == "1.0.0" || version == "1.0.1" || version == "1.0.2" || version == "1.0.3" || version == "1.0.4" {
		code = strings.Replace(code, `contract.TransferPresentationContract: transferPresentation,`, "", 1)
	}
	if version == "1.0.0" || version == "1.0.1" || version == "1.0.2" || version == "1.0.3" {
		code = strings.Replace(code, `contract.PlaywrightParametersContract: playwrightParameters,`, "", 1)
	}
	if version == "1.0.0" || version == "1.0.1" || version == "1.0.2" {
		code = strings.Replace(code, `contract.ActionParametersContract: actionParameters,`, "", 1)
	}
	if version == "1.0.0" || version == "1.0.1" {
		code = strings.Replace(code, `contract.InventoryOrderContract: orderInventory,`, "", 1)
		code = strings.Replace(code, `contract.TargetMatchContract: matchTargets,`, "", 1)
	}
	switch version {
	case "1.0.0":
		code = strings.Replace(code, `contract.SelectionContract: selectText,`, "", 1)
	case "1.1.0":
		code = strings.Replace(code, `if !facts.DiffAllowed || facts.Full {`, `if true {`, 1)
	case "1.2.0":
		code = strings.Replace(code, `if before == after {`, `if before != after { return contract.ObservationPlan{Mode:"diff",Removed:1,Added:1} };if before == after {`, 1)
	case "1.3.0":
		code = strings.Replace(code, `"errors"`, `"errors";"os";"time"`, 1)
		code = strings.Replace(code, `return rsiplugin.JSONResult(ctx, project`, `if strings.Contains(snapshots.After,"PRIVATE_LIVE"){if err:=os.WriteFile("computer-ready",[]byte("ready"),0600);err!=nil{return rsiplugin.Result{},err};for{if _,err:=os.Stat("resume");err==nil{break};select{case<-ctx.Done():return rsiplugin.Result{},ctx.Err();case<-time.After(time.Millisecond):}}};return rsiplugin.JSONResult(ctx, project`, 1)
	case "1.4.0":
		code = strings.Replace(code, `timeout := min(1000, facts.Ceilings.TimeoutMS)`, `timeout := min(2000, facts.Ceilings.TimeoutMS)`, 1)
		code = strings.Replace(code, `PollMS: facts.Ceilings.MinPollMS`, `PollMS: min(200,facts.Ceilings.MaxPollMS)`, 1)
	case "1.5.0":
		code = strings.Replace(code, `return rsiplugin.JSONResult(ctx, contract.WaitRecipe`, `timeout=facts.Ceilings.TimeoutMS+1;return rsiplugin.JSONResult(ctx, contract.WaitRecipe`, 1)
	case "1.6.0":
		code = strings.Replace(code, `return rsiplugin.JSONResult(ctx, contract.WaitRecipe`, `timeout=facts.Ceilings.TimeoutMS;return rsiplugin.JSONResult(ctx, contract.WaitRecipe`, 1)
	case "1.7.0":
		code = strings.Replace(code, `Text: text`, `Text: text+"\nMETHOD_DOC_V1_7"`, 1)
	case "1.8.0":
		code = strings.Replace(code, `Text: text`, `Text: text+strings.Repeat("Z",17000)`, 1)
	case "1.9.0":
		code = strings.Replace(code, `contract.SelectionContract: selectText,`, `contract.SelectionContract: func(ctx context.Context,c rsiplugin.Call)(rsiplugin.Result,error){var f contract.SelectionFacts;if err:=rsiplugin.Decode(c.Input,&f);err!=nil{return rsiplugin.Result{},err};position:=strings.Index(f.Prefix,f.Wanted);if f.Length-position<=256{return rsiplugin.JSONResult(ctx,contract.SelectionRecipe{Anchor:"end",Move:position+len(f.Wanted)-f.Length,Extend:-len(f.Wanted)})};return selectText(ctx,c)},`, 1)
	case "1.10.0":
		code = strings.Replace(code, `contract.SelectionContract: selectText,`, `contract.SelectionContract: func(ctx context.Context,c rsiplugin.Call)(rsiplugin.Result,error){return rsiplugin.JSONResult(ctx,contract.SelectionRecipe{Anchor:"start",Extend:1})},`, 1)
	}
	if version == "1.15.0" || version == "1.16.0" || version == "1.17.0" {
		replacement := `contract.ActionParametersContract: func(ctx context.Context,call rsiplugin.Call)(rsiplugin.Result,error){var facts contract.ActionFacts;if err:=rsiplugin.Decode(call.Input,&facts);err!=nil{return rsiplugin.Result{},err};var input contract.ActionArguments;raw,err:=rsiplugin.ReadInput(ctx,call.Inputs[0]);if err!=nil{return rsiplugin.Result{},err};if err=json.Unmarshal(raw,&input);err!=nil{return rsiplugin.Result{},err};plan:=normalizeAction(facts,input.Args);if facts.Method=="scroll" && plan.Failure=="" && plan.Amount==nil{amount:=50.0;plan.Amount=&amount};LIVE;return rsiplugin.JSONResult(ctx,plan)},`
		extra := ""
		if version == "1.16.0" {
			extra = `if facts.Method=="typeText" && plan.Text=="LIVE_ORIGINAL"{plan.Text="WRONG_REPLACEMENT"}`
		}
		if version == "1.17.0" {
			code = strings.Replace(code, `"errors"`, `"errors";"time"`, 1)
			extra = `if facts.Method=="scroll" && len(input.Args)>3 && input.Args[3]=="LIVE_NONDETERMINISTIC"{amount:=float64(time.Now().UnixMilli()%1000000000000)/1000000000+1;plan.Amount=&amount}`
		}
		replacement = strings.Replace(replacement, "LIVE;", extra+";", 1)
		code = strings.Replace(code, `contract.ActionParametersContract: actionParameters,`, replacement, 1)
	}

	if version == "1.18.0" || version == "1.19.0" || version == "1.20.0" || version == "1.21.0" {
		extra := ""
		switch version {
		case "1.18.0":
			extra = `if _,ok:=original["timeoutMs"];!ok && plan.Failure=="" {n:=4000;plan.TimeoutMS=&n}`
		case "1.19.0":
			extra = `if original["selector"]=="#LIVE_ORIGINAL" {plan.Selector="#WRONG_REPLACEMENT"}`
		case "1.20.0":
			code = strings.Replace(code, `"errors"`, `"errors";"crypto/rand";"math/big"`, 1)
			extra = `if original["selector"]=="#LIVE_NONDETERMINISTIC" {pick,err:=rand.Int(rand.Reader,big.NewInt(9000));if err!=nil{return rsiplugin.Result{},err};n:=int(pick.Int64())+1000;plan.TimeoutMS=&n}`
		case "1.21.0":
			extra = `if original["timeoutMs"]==float64(1234) {n:=5000;plan.TimeoutMS=&n}`
		}
		replacement := `contract.PlaywrightParametersContract: func(ctx context.Context,call rsiplugin.Call)(rsiplugin.Result,error){var facts contract.ActionFacts;if err:=rsiplugin.Decode(call.Input,&facts);err!=nil{return rsiplugin.Result{},err};var input contract.ActionArguments;raw,err:=rsiplugin.ReadInput(ctx,call.Inputs[0]);if err!=nil{return rsiplugin.Result{},err};if err=json.Unmarshal(raw,&input);err!=nil{return rsiplugin.Result{},err};plan:=normalizePlaywright(facts,input.Args);original,_:=input.Args[0].(map[string]any);LIVE;return rsiplugin.JSONResult(ctx,plan)},`
		code = strings.Replace(code, `contract.PlaywrightParametersContract: playwrightParameters,`, strings.Replace(replacement, "LIVE;", extra+";", 1), 1)
	}

	if version == "1.22.0" || version == "1.23.0" || version == "1.24.0" || version == "1.25.0" {
		extra := `if plan.NameFromSource {plan.NameFromSource=false;plan.Name="export.bin"}`
		if version == "1.23.0" {
			extra = `if original["name"]=="PRIVATE_ORIGINAL.txt" {plan.Name="replacement.txt"}`
		}
		if version == "1.24.0" {
			extra = `if plan.NameFromSource {plan.NameFromSource=false;plan.Name="../escape"}`
		}
		if version == "1.25.0" {
			code = strings.Replace(code, `"errors"`, `"errors";"crypto/rand"`, 1)
			extra = `if plan.NameFromSource && plan.MediaType=="application/x-live-nondeterministic" {plan.NameFromSource=false;plan.Name="output-"+rand.Text()+".bin"}`
		}
		replacement := `contract.TransferPresentationContract: func(ctx context.Context,call rsiplugin.Call)(rsiplugin.Result,error){var facts contract.ActionFacts;if err:=rsiplugin.Decode(call.Input,&facts);err!=nil{return rsiplugin.Result{},err};var input contract.ActionArguments;raw,err:=rsiplugin.ReadInput(ctx,call.Inputs[0]);if err!=nil{return rsiplugin.Result{},err};if err=json.Unmarshal(raw,&input);err!=nil{return rsiplugin.Result{},err};plan:=normalizeTransfer(facts,input.Args);original,_:=input.Args[0].(map[string]any);_ = original;LIVE;return rsiplugin.JSONResult(ctx,plan)},`
		code = strings.Replace(code, `contract.TransferPresentationContract: transferPresentation,`, strings.Replace(replacement, "LIVE;", extra+";", 1), 1)
	}
	if computerVersionHasUpload(version) && version != "1.0.6" {
		extra := ""
		switch version {
		case "1.26.0":
			extra = `if plan.Failure=="" {plan.NameFromSource=false;plan.Name="chosen-upload.txt";plan.MediaType="text/plain"}`
		case "1.27.0":
			extra = `if plan.Selector=="#LIVE_ORIGINAL" {plan.Selector="#REPLACEMENT"}`
		case "1.28.0":
			extra = `if plan.Failure=="" {plan.NameFromSource=false;plan.Name="../escape"}`
		case "1.30.0":
			extra = `plan.NameFromSource=false;plan.Name="";plan.MediaType=""`
		case "1.29.0":
			code = strings.Replace(code, `"errors"`, `"errors";"crypto/rand"`, 1)
			extra = `if plan.Selector=="#LIVE_NONDETERMINISTIC" {plan.NameFromSource=false;plan.Name="upload-"+rand.Text()+".bin"}`
		}
		replacement := `contract.BrowserUploadContract: func(ctx context.Context,call rsiplugin.Call)(rsiplugin.Result,error){facts,args,err:=readPlaywrightArguments(ctx,call);if err!=nil{return rsiplugin.Result{},err};if len(args)>0{original,_:=args[0].(map[string]any);for key:=range original{if key!="selector" && key!="timeoutMs"{return rsiplugin.Result{},errors.New("unexpected released resource")}}};plan:=normalizeBrowserUpload(facts,args);LIVE;return rsiplugin.JSONResult(ctx,plan)},`
		code = strings.Replace(code, `contract.BrowserUploadContract: browserUploadParameters,`, strings.Replace(replacement, "LIVE;", extra+";", 1), 1)
	}

	if version == "1.31.0" || version == "1.32.0" {
		change := `if facts.BodyUnits==17 && len(plan.Rows)>1 {plan.Rows[1].Index=plan.Rows[0].Index}`
		if version == "1.31.0" {
			change = `plan.BodyUnits=min(plan.BodyUnits,2);for i:=range plan.Rows{r:=&plan.Rows[i];if facts.Rows[i].PlaceholderUnits>0{r.Label="placeholder";r.LabelUnits=facts.Rows[i].PlaceholderUnits};if r.ValueUnits!=nil{n:=min(*r.ValueUnits,1);r.ValueUnits=&n}};for i,j:=0,len(plan.Rows)-1;i<j;i,j=i+1,j-1{plan.Rows[i],plan.Rows[j]=plan.Rows[j],plan.Rows[i]}`
		}
		replacement := `contract.BrowserProjectionContract: func(ctx context.Context,call rsiplugin.Call)(rsiplugin.Result,error){var facts contract.BrowserProjectionFacts;if err:=rsiplugin.Decode(call.Input,&facts);err!=nil{return rsiplugin.Result{},err};plan:=planBrowserProjection(facts);CHANGE;return rsiplugin.JSONResult(ctx,plan)},`
		code = strings.Replace(code, `contract.BrowserProjectionContract: browserProjection,`, strings.Replace(replacement, "CHANGE", change, 1), 1)
	}
	override := filepath.Join(t.TempDir(), "plugin.go")
	if err = os.WriteFile(override, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{path: override}
	if version == "1.11.0" || version == "1.12.0" || version == "1.13.0" || version == "1.14.0" {
		targetPath, err := filepath.Abs("../../plugin/computeruse/targets.go")
		if err != nil {
			t.Fatal(err)
		}
		targetCode, err := os.ReadFile(targetPath)
		if err != nil {
			t.Fatal(err)
		}
		target := string(targetCode)
		switch version {
		case "1.11.0":
			target = strings.Replace(target, `"errors"`, `"errors";"strings"`, 1)
			target = strings.Replace(target, `return rsiplugin.JSONResult(ctx, contract.InventoryOrder{Indices: indices})`, `for i:=1;i<len(indices);i++{for j:=i;j>0 && input.Labels[indices[j]].Name<input.Labels[indices[j-1]].Name;j--{indices[j],indices[j-1]=indices[j-1],indices[j]}};return rsiplugin.JSONResult(ctx,contract.InventoryOrder{Indices:indices})`, 1)
			target = strings.Replace(target, `input.Selector == label.Name.Text`, `strings.EqualFold(input.Selector,label.Name.Text)`, 1)
		case "1.12.0":
			target = strings.Replace(target, `return rsiplugin.JSONResult(ctx, contract.InventoryOrder{Indices: indices})`, `if len(indices)>1{indices[1]=indices[0]};return rsiplugin.JSONResult(ctx,contract.InventoryOrder{Indices:indices})`, 1)
		case "1.13.0":
			target = strings.Replace(target, `indices := []int{}`, `indices:=[]int{};if input.Selector=="LIVE_PIN"{return rsiplugin.JSONResult(ctx,contract.TargetMatches{Indices:[]int{1}})}`, 1)
		case "1.14.0":
			target = strings.Replace(target, `indices := []int{}`, `indices:=[]int{};if input.Selector=="LIVE_PIN"{return rsiplugin.JSONResult(ctx,contract.TargetMatches{Indices:[]int{512}})}`, 1)
		}
		targetFile := filepath.Join(t.TempDir(), "targets.go")
		if err = os.WriteFile(targetFile, []byte(target), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[targetPath] = targetFile
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{replacements})
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(file, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/computer-use").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../../plugins/computer-use/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = version
	if !computerVersionHasBrowser(version) {
		kept := manifest.Provides[:0]
		for _, service := range manifest.Provides {
			if service.Contract != contract.BrowserProjectionContract {
				kept = append(kept, service)
			}
		}
		manifest.Provides = kept
		manifest.ConfigSchema = json.RawMessage(`{"type":"object","properties":{"browser_upload_driver":{"type":"string","minLength":64,"maxLength":64}},"required":["browser_upload_driver"],"additionalProperties":false}`)
	}
	if !computerVersionHasUpload(version) {
		kept := manifest.Provides[:0]
		for _, service := range manifest.Provides {
			if service.Contract != contract.BrowserUploadContract {
				kept = append(kept, service)
			}
		}
		manifest.Provides = kept
		manifest.ConfigSchema = json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`)
	}
	if version == "1.0.0" || version == "1.0.1" || version == "1.0.2" || version == "1.0.3" || version == "1.0.4" {
		kept := manifest.Provides[:0]
		for _, service := range manifest.Provides {
			if service.Contract != contract.TransferPresentationContract {
				kept = append(kept, service)
			}
		}
		manifest.Provides = kept
	}

	if version == "1.0.0" || version == "1.0.1" || version == "1.0.2" || version == "1.0.3" {
		kept := manifest.Provides[:0]
		for _, service := range manifest.Provides {
			if service.Contract != contract.PlaywrightParametersContract {
				kept = append(kept, service)
			}
		}
		manifest.Provides = kept
	}
	if version == "1.0.0" || version == "1.0.1" || version == "1.0.2" {
		kept := manifest.Provides[:0]
		for _, service := range manifest.Provides {
			if service.Contract != contract.ActionParametersContract && (version == "1.0.2" || service.Contract != contract.InventoryOrderContract && service.Contract != contract.TargetMatchContract && (version != "1.0.0" || service.Contract != contract.SelectionContract)) {
				kept = append(kept, service)
			}
		}
		manifest.Provides = kept
	}
	if version == "1.5.0" {
		for i := range manifest.Provides {
			if manifest.Provides[i].Contract == contract.WaitContract {
				compact := strings.NewReplacer(" ", "", "\n", "", "\t", "").Replace(string(manifest.Provides[i].OutputSchema))
				manifest.Provides[i].OutputSchema = json.RawMessage(strings.Replace(compact, `"maximum":10000`, `"maximum":10001`, 1))
			}
		}
	}
	if version == "1.8.0" {
		for i := range manifest.Provides {
			if manifest.Provides[i].Contract == contract.DocumentationContract {
				compact := strings.NewReplacer(" ", "", "\n", "", "\t", "").Replace(string(manifest.Provides[i].OutputSchema))
				manifest.Provides[i].OutputSchema = json.RawMessage(strings.Replace(compact, `"maxLength":16384`, `"maxLength":32768`, 1))
			}
		}
	}
	manifest, err = rsi.Seal(source, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return source
}

func selectComputerVersion(t *testing.T, h rsi.Host, baseline, version string) (string, string) {
	t.Helper()
	ctx := context.Background()
	pkg, err := h.Install(ctx, computerVersionPackage(t, version))
	if err != nil {
		t.Fatal(err)
	}
	upload := false
	for _, service := range pkg.Manifest.Provides {
		upload = upload || service.Contract == contract.BrowserUploadContract
	}
	config := json.RawMessage(`{}`)
	if upload {
		config = computerUploadFixtureConfig()
	}
	if computerVersionHasBrowser(version) {
		config = computerBrowserFixtureConfig()
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "computer-use", PackageDigest: pkg.Digest, Config: config, StateNamespace: "computer-use", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "computer-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.0.0" && version != "1.0.1" && version != "1.0.2" && version != "1.0.3" && version != "1.0.4" {
		hasTransferPresentation := false
		for _, service := range policy.AllowedContracts {
			hasTransferPresentation = hasTransferPresentation || service == contract.TransferPresentationContract
		}
		if !hasTransferPresentation {
			policy = computerOwnerPolicy(t)
		}
	}
	if upload {
		policy = computerOwnerPolicy(t)
	} else {
		allowed := policy.AllowedContracts[:0]
		for _, c := range policy.AllowedContracts {
			if c != contract.BrowserUploadContract {
				allowed = append(allowed, c)
			}
		}
		policy.AllowedContracts = allowed
		cases := policy.Cases[:0]
		for _, c := range policy.Cases {
			if c.Contract != contract.BrowserUploadContract {
				cases = append(cases, c)
			}
		}
		policy.Cases = cases
	}
	if !computerVersionHasBrowser(version) {
		allowed := policy.AllowedContracts[:0]
		for _, c := range policy.AllowedContracts {
			if c != contract.BrowserProjectionContract {
				allowed = append(allowed, c)
			}
		}
		policy.AllowedContracts = allowed
		cases := policy.Cases[:0]
		for _, c := range policy.Cases {
			if c.Contract != contract.BrowserProjectionContract {
				cases = append(cases, c)
			}
		}
		policy.Cases = cases
	}
	if version == "1.26.0" || version == "1.28.0" || version == "1.30.0" {
		for i := range policy.Cases {
			c := &policy.Cases[i]
			if c.Contract != contract.BrowserUploadContract {
				continue
			}
			var p contract.ActionParameters
			json.Unmarshal(c.Expected, &p)
			if p.Failure == "" {
				p.NameFromSource = false
				p.Name = "chosen-upload.txt"
				if version == "1.26.0" {
					p.MediaType = "text/plain"
				} else {
					p.Name = "../escape"
				}
				if version == "1.30.0" {
					p.Name = ""
					p.MediaType = ""
				}
				c.Expected, _ = json.Marshal(p)
			}
		}
	}

	if version == "1.31.0" {
		for i := range policy.Cases {
			c := &policy.Cases[i]
			if c.Contract != contract.BrowserProjectionContract {
				continue
			}
			var f contract.BrowserProjectionFacts
			var p contract.BrowserProjection
			json.Unmarshal(c.Input, &f)
			json.Unmarshal(c.Expected, &p)
			p.BodyUnits = min(p.BodyUnits, 2)
			for j := range p.Rows {
				r := &p.Rows[j]
				if f.Rows[j].PlaceholderUnits > 0 {
					r.Label = "placeholder"
					r.LabelUnits = f.Rows[j].PlaceholderUnits
				}
				if r.ValueUnits != nil {
					n := min(*r.ValueUnits, 1)
					r.ValueUnits = &n
				}
			}
			for a, b := 0, len(p.Rows)-1; a < b; a, b = a+1, b-1 {
				p.Rows[a], p.Rows[b] = p.Rows[b], p.Rows[a]
			}
			c.Expected, _ = json.Marshal(p)
		}
	}
	policy.ID = "computer-owner-" + version

	if version == "1.22.0" || version == "1.24.0" {
		for i := range policy.Cases {
			c := &policy.Cases[i]
			if c.Contract != contract.TransferPresentationContract {
				continue
			}
			var p contract.ActionParameters
			json.Unmarshal(c.Expected, &p)
			if p.NameFromSource {
				p.NameFromSource = false
				p.Name = "export.bin"
				if version == "1.24.0" {
					p.Name = "../escape"
				}
				c.Expected, _ = json.Marshal(p)
			}
		}
	}
	if version == "1.18.0" {
		for i := range policy.Cases {
			c := &policy.Cases[i]
			if c.Contract != contract.PlaywrightParametersContract {
				continue
			}
			var p contract.ActionParameters
			json.Unmarshal(c.Expected, &p)
			if p.TimeoutMS != nil && *p.TimeoutMS == 3000 {
				n := 4000
				p.TimeoutMS = &n
				c.Expected, _ = json.Marshal(p)
			}
		}
	}
	if version == "1.15.0" || version == "1.16.0" || version == "1.17.0" {
		for i := range policy.Cases {
			c := &policy.Cases[i]
			if c.Contract != contract.ActionParametersContract {
				continue
			}
			var f contract.ActionFacts
			json.Unmarshal(c.Input, &f)
			var p contract.ActionParameters
			json.Unmarshal(c.Expected, &p)
			if f.Method == "scroll" && p.Failure == "" && p.Amount == nil {
				amount := 50.0
				p.Amount = &amount
				c.Expected, _ = json.Marshal(p)
			}
		}
	}

	if version == "1.11.0" || version == "1.12.0" {
		for i := range policy.Cases {
			c := &policy.Cases[i]
			var order contract.InventoryOrder
			json.Unmarshal(c.Expected, &order)
			if c.Contract == contract.InventoryOrderContract && len(order.Indices) == 2 && order.Indices[0] == 0 && order.Indices[1] == 1 {
				if version == "1.11.0" {
					c.Expected = json.RawMessage(`{"indices":[1,0]}`)
				} else {
					c.Expected = json.RawMessage(`{"indices":[0,0]}`)
				}
			}
			if c.Contract == contract.TargetMatchContract && version == "1.11.0" {
				var f contract.MatchLabels
				json.Unmarshal(c.Inputs[0].Data, &f)
				if f.Selector == "Notes" {
					c.Expected = json.RawMessage(`{"indices":[0,1]}`)
				}
			}
		}
	}
	if version == "1.9.0" || version == "1.10.0" {
		for i := range policy.Cases {
			if policy.Cases[i].Contract != contract.SelectionContract {
				continue
			}
			if version == "1.10.0" {
				policy.Cases[i].Expected = json.RawMessage(`{"anchor":"start","move":0,"extend":1}`)
				continue
			}
			var facts contract.SelectionFacts
			if err := json.Unmarshal(policy.Cases[i].Input, &facts); err != nil {
				t.Fatal(err)
			}
			switch facts.Prefix {
			case "one target end":
				policy.Cases[i].Expected = json.RawMessage(`{"anchor":"end","move":-4,"extend":-6}`)
			case "head":
				policy.Cases[i].Expected = json.RawMessage(`{"anchor":"end","move":0,"extend":-4}`)
			}
		}
	}
	if version == "1.7.0" || version == "1.8.0" {
		for i := range policy.Cases {
			if policy.Cases[i].Contract == contract.DocumentationContract {
				var doc contract.Documentation
				if err := json.Unmarshal(policy.Cases[i].Expected, &doc); err != nil {
					t.Fatal(err)
				}
				if version == "1.7.0" {
					doc.Text += "\nMETHOD_DOC_V1_7"
				} else {
					doc.Text += strings.Repeat("Z", 17000)
				}
				policy.Cases[i].Expected, _ = json.Marshal(doc)
			}
		}
	}
	if version == "1.1.0" {
		for i := range policy.Cases {
			if policy.Cases[i].Contract == contract.ObservationContract {
				policy.Cases[i].Expected = json.RawMessage(`{"mode":"full","start":0,"removed":0,"added":0}`)
			}
		}
	}
	for i := range policy.Cases {
		if policy.Cases[i].Contract != contract.WaitContract {
			continue
		}
		var facts contract.WaitFacts
		var plan contract.WaitRecipe
		json.Unmarshal(policy.Cases[i].Input, &facts)
		json.Unmarshal(policy.Cases[i].Expected, &plan)
		switch version {
		case "1.4.0":
			plan.PollMS = 200
			plan.TimeoutMS = min(2000, facts.Ceilings.TimeoutMS)
		case "1.5.0":
			plan.TimeoutMS = facts.Ceilings.TimeoutMS + 1
		case "1.6.0":
			plan.TimeoutMS = facts.Ceilings.TimeoutMS
		}
		policy.Cases[i].Expected, _ = json.Marshal(plan)
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activateDelegation(t, h, "computer-upgrade-"+version, baseline, graph.Revision, policy.ID)
	return graph.Revision, pkg.Digest
}

func computerPluginCall(t *testing.T, b *codeBridge, id, operation, token, method string, args ...any) (tool.Result, error) {
	t.Helper()
	raw, _ := encoded(map[string]any{"method": method, "handle_token": token, "args": args})
	e := tool.Executor{Registry: b.registry, Store: nestedTools{b.t.forCode(b), b.req.ExecutionID}}
	result, err := e.Execute(context.Background(), b.exchange, []llm.ToolCall{{ID: b.req.ExecutionID + "/" + digest(id), Name: "builtin__computer_" + operation, Arguments: raw}})
	if err != nil {
		return tool.Result{}, err
	}
	if len(result) != 1 {
		t.Fatal("missing original Tool result", result)
	}
	return result[0], nil
}

func computerPluginValue(t *testing.T, result tool.Result, err error) map[string]any {
	t.Helper()
	if err != nil || result.Error != nil {
		raw, _ := json.Marshal(result.Error)
		t.Fatal("original Tool call failed", result.Text, string(raw), err)
	}
	var value map[string]any
	if json.Unmarshal([]byte(result.Text), &value) != nil {
		t.Fatal(result.Text)
	}
	return value
}

func computerPluginBind(t *testing.T, b *codeBridge) string {
	t.Helper()
	result, err := computerPluginCall(t, b, "bind", "bind", "", "getApp", "notes")
	return computerPluginValue(t, result, err)["handle_token"].(string)
}

func newComputerPluginExecution(t *testing.T, s *Store, r *Runs) *codeBridge {
	t.Helper()
	ctx := context.Background()
	runID := admitToolRun(t, s, r, agent.Actor{}, r.registry.Describe(), nil)
	claim, err := r.Claim(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Initialize(ctx, claim); err != nil {
		t.Fatal(err)
	}
	run, err := r.Load(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := r.Generation(ctx, claim, run)
	if err != nil {
		t.Fatal(err)
	}
	calls := []llm.ToolCall{{ID: "new-cell", Name: codemode.ToolName, Arguments: `{"source":"return 1;"}`}}
	p := &loopProvider{store: s, respond: func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{ToolCalls: calls}, nil
	}}
	if _, err = p.Generate(ctx, generation.Request, nil); err != nil {
		t.Fatal(err)
	}
	store := r.ToolStore(claim)
	invs, err := store.Accept(ctx, generation.Request.GenerationID, calls, r.registry.Describe())
	if err != nil {
		t.Fatal(err)
	}
	inv := invs[0]
	if err = store.Start(ctx, inv.ID); err != nil {
		t.Fatal(err)
	}
	b := &codeBridge{t: store.(*tools), registry: r.registry, attempt: newID(), req: codemode.ExecutionRequest{ProtocolVersion: codemode.ProtocolVersion, RuntimeVersion: codemode.RuntimeVersion, PreludeVersion: codemode.PreludeVersion, ExecutionID: inv.ID, ParentInvocationID: inv.ID, RunID: runID, Source: "return 1;", SourceHash: digest("return 1;"), Limits: codemode.DefaultLimits()}}
	if err = b.claim(ctx, inv); err != nil {
		t.Fatal(err)
	}
	keepComputerPluginOwner(t, b)
	return b
}

func TestComputerPluginActualProjectionPreservesSourceRevisionsAndReceipts(t *testing.T) {
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { installComputerPlugin(t, s, r) })
	b := bridges[0]
	p := &surfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	token := computerPluginBind(t, b)
	result, err := computerPluginCall(t, b, "first", "invoke", token, "getAXState")
	first := computerPluginValue(t, result, err)
	if first["kind"] != "full" {
		t.Fatal(first)
	}
	result, err = computerPluginCall(t, b, "same", "invoke", token, "getAXState")
	unchanged := computerPluginValue(t, result, err)
	if unchanged["kind"] != "diff" || unchanged["state"] != "" || unchanged["baseRevision"] != first["revision"] {
		t.Fatal("unchanged observation lost its accepted base", unchanged)
	}
	p.Mu.Lock()
	p.Text = "changed"
	p.Mu.Unlock()
	result, err = computerPluginCall(t, b, "change", "invoke", token, "getAXState")
	changed := computerPluginValue(t, result, err)
	if changed["baseRevision"] != unchanged["revision"] || changed["state"] != "@@ 0,1,1 @@\n- [0] Notes  [actions=increment]\n+ [0] Notes changed [actions=increment]" {
		t.Fatal("projection changed original AX text", changed)
	}
	before := scalar(t, s, `SELECT count(*) FROM tool_invocation`)
	replay, err := computerPluginCall(t, b, "change", "invoke", token, "getAXState")
	if err != nil || replay.Text != result.Text || scalar(t, s, `SELECT count(*) FROM tool_invocation`) != before {
		t.Fatal("original observation receipt replay drift", replay, err)
	}
	var state computer.UseState
	var raw string
	if err = s.db.QueryRow(`SELECT state_json FROM computer_use WHERE execution_id=?`, b.req.ExecutionID).Scan(&raw); err != nil || json.Unmarshal([]byte(raw), &state) != nil {
		t.Fatal(raw, err)
	}
	h := state.Handles[token]
	if !h.AXReady || h.AXState != "[0] Notes changed [actions=increment]" || h.AXRevision != changed["revision"] {
		t.Fatal("projection became authority over original reference facts", h)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='computer-use'`) != 0 {
		t.Fatal("pure observation created a second effect ledger")
	}
}

func TestComputerPluginIndependentUpgradeKeepsFrozenProjectionAndDisable(t *testing.T) {
	ctx := context.Background()
	var host rsi.Host
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { host = installComputerPlugin(t, s, r) })
	b := bridges[0]
	p := &surfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	first, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	token := computerPluginBind(t, b)
	result, err := computerPluginCall(t, b, "before-upgrade", "invoke", token, "getAXState")
	computerPluginValue(t, result, err)
	second, pkg := selectComputerVersion(t, host, first.Revision, "1.1.0")
	result, err = computerPluginCall(t, b, "after-upgrade", "invoke", token, "getAXState")
	if value := computerPluginValue(t, result, err); value["kind"] != "diff" {
		t.Fatal("upgrade changed original execution", value)
	}
	if err = b.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	newRun := newComputerPluginExecution(t, s, r)
	newToken := computerPluginBind(t, newRun)
	result, err = computerPluginCall(t, newRun, "new-first", "invoke", newToken, "getAXState")
	computerPluginValue(t, result, err)
	result, err = computerPluginCall(t, newRun, "new-second", "invoke", newToken, "getAXState")
	if value := computerPluginValue(t, result, err); value["kind"] != "full" || value["baseRevision"] != nil {
		t.Fatal("independent full projection not dispatched", value)
	}
	activateDelegation(t, host, "computer-rollback", second, first.Revision, "computer-owner-fixture")
	if err = host.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	result, err = computerPluginCall(t, newRun, "retired-execution", "invoke", newToken, "getAXState")
	if value := computerPluginValue(t, result, err); value["kind"] != "full" {
		t.Fatal("retirement broke retained execution", value)
	}
	if _, err = host.Store.Deactivate(ctx, "computer-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	if err = newRun.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	disabled := newComputerPluginExecution(t, s, r)
	ensures := p.ensures
	result, err = computerPluginCall(t, disabled, "disabled-bind", "bind", "", "getApp", "notes")
	if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || p.ensures != ensures {
		t.Fatal("disabled managed feature restored native or started Computer", result, err, p.ensures, ensures)
	}
}

func TestComputerPluginHostRejectsIncompletePatch(t *testing.T) {
	input := computer.ObservationInput{Before: "stable\nold\ntail", After: "stable\nnew\ntail", DiffAllowed: true}
	if _, err := renderComputerObservation(input, contract.ObservationPlan{Mode: "diff", Removed: 1, Added: 1}); !errors.Is(err, rsi.ErrInvalid) {
		t.Fatal("candidate omitted the actual change", err)
	}
	view, err := renderComputerObservation(input, contract.ObservationPlan{Mode: "diff", Removed: 3, Added: 3})
	if err != nil || !strings.Contains(view.State, "+ new") {
		t.Fatal("valid contextual strategy rejected", view, err)
	}
}

func TestComputerPluginMechanicalGateCannotAuthorizeIncompleteObservation(t *testing.T) {
	ctx := context.Background()
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) {
		h := installComputerPlugin(t, s, r)
		first, err := h.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		selectComputerVersion(t, h, first.Revision, "1.2.0")
	})
	b := bridges[0]
	p := &surfaceProvider{}
	p.Text = "stable\nold\ntail"
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	token := computerPluginBind(t, b)
	result, err := computerPluginCall(t, b, "first", "invoke", token, "getAXState")
	computerPluginValue(t, result, err)
	p.Mu.Lock()
	p.Text = "stable\nnew\ntail"
	p.Mu.Unlock()
	result, err = computerPluginCall(t, b, "incomplete", "invoke", token, "getAXStateAndScreenshot")
	if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" {
		t.Fatal("mechanically verified plugin hid the actual change", result, err)
	}
	if scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__computer_invoke' AND status='succeeded'`) != 1 {
		t.Fatal("invalid observation became evidence")
	}
	var raw string
	if err = s.db.QueryRow(`SELECT state_json FROM computer_use WHERE execution_id=?`, b.req.ExecutionID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var state computer.UseState
	json.Unmarshal([]byte(raw), &state)
	if state.Handles[token].AXReady || state.Handles[token].ScreenshotReady {
		t.Fatal("failed projection granted actionable references")
	}
}

func TestComputerPluginLateForgetAndGuardianRejectObservationCommit(t *testing.T) {
	for _, cause := range []string{"forget", "stop", "quarantine"} {
		t.Run(cause, func(t *testing.T) {
			ctx := context.Background()
			var host rsi.Host
			s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { host = installComputerPlugin(t, s, r) })
			b := bridges[0]
			p := &surfaceProvider{}
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			token := computerPluginBind(t, b)
			selected, err := host.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			owner := idAgent(t, s)
			crossed := false
			s.MainBoundary = func(point string) {
				if point != "before_computer_observation_commit" {
					return
				}
				crossed = true
				switch cause {
				case "forget":
					err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) })
				case "stop":
					err = host.Store.Stop(ctx, true)
				case "quarantine":
					err = host.Store.Quarantine(ctx, selected.Revision)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = computerPluginCall(t, b, "late-observation", "invoke", token, "getAXStateAndScreenshot")
			want := rsi.ErrRevoked
			if cause == "forget" {
				want = agent.ErrClaimLost
			}
			if !crossed || !errors.Is(err, want) {
				t.Fatal("late revocation accepted observation", crossed, err)
			}
			if scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__computer_invoke' AND status='succeeded'`) != 0 {
				t.Fatal("revoked observation committed Artifact or receipt")
			}
			var raw string
			if err = s.db.QueryRow(`SELECT state_json FROM computer_use WHERE execution_id=?`, b.req.ExecutionID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var state computer.UseState
			json.Unmarshal([]byte(raw), &state)
			if h := state.Handles[token]; h.AXReady || h.ScreenshotReady {
				t.Fatal("revoked view became actionable", h)
			}
		})
	}
}

func TestComputerPluginLateForgetRejectsPrivateProjectionAtGuardAndTransport(t *testing.T) {
	for _, resume := range []bool{true, false} {
		name := "end-guard"
		if !resume {
			name = "transport-cancel"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) {
				h := installComputerPlugin(t, s, r)
				first, err := h.Store.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				selectComputerVersion(t, h, first.Revision, "1.3.0")
			})
			b := bridges[0]
			p := &surfaceProvider{}
			p.Text = "PRIVATE_LIVE"
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			token := computerPluginBind(t, b)
			owner := idAgent(t, s)
			jobs := t.TempDir()
			t.Setenv("TMPDIR", jobs)
			raw, _ := encoded(map[string]any{"method": "getAXState", "handle_token": token})
			e := tool.Executor{Registry: b.registry, Store: nestedTools{b.t.forCode(b), b.req.ExecutionID}}
			done := make(chan struct{})
			var readErr error
			go func() {
				defer close(done)
				_, readErr = e.Execute(ctx, b.exchange, []llm.ToolCall{{ID: b.req.ExecutionID + "/private-observation", Name: "builtin__computer_invoke", Arguments: raw}})
			}()
			defer func() { cancel(); <-done }()
			marker := ""
			for marker == "" {
				select {
				case <-done:
					t.Fatal("private method ended before revocation", readErr)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(5 * time.Millisecond):
					paths, _ := filepath.Glob(filepath.Join(jobs, "steward-rsi-*", "computer-ready"))
					if len(paths) == 1 {
						marker = paths[0]
					}
				}
			}
			if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
				t.Fatal("pure observation blocked Forget", err)
			}
			if resume {
				if err := os.WriteFile(filepath.Join(filepath.Dir(marker), "resume"), []byte("continue"), 0600); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			}
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if !errors.Is(readErr, agent.ErrClaimLost) {
				t.Fatal("privacy cause hidden by pure transport", readErr)
			}
			if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__computer_invoke' AND status='succeeded'`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='computer-use'`) != 0 {
				t.Fatal("revoked private observation or duplicate ledger accepted")
			}
		})
	}
}

func TestComputerPluginLargeEscapedObservationsRetainOriginalOutputLimit(t *testing.T) {
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { installComputerPlugin(t, s, r) })
	b := bridges[0]
	p := &surfaceProvider{}
	p.Text = strings.Repeat("\x00", 130000)
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	token := computerPluginBind(t, b)
	for _, id := range []string{"large-first", "large-second"} {
		result, err := computerPluginCall(t, b, id, "invoke", token, "getAXState")
		if err != nil || result.Error == nil || result.Error.Code != "RESOURCE_LIMIT" {
			t.Fatal("escaped snapshots failed transport or bypassed original output limit", result.Error, err)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__computer_invoke' AND status='succeeded'`) != 0 {
		t.Fatal("oversized output made references actionable")
	}
}

type changingComputerObservation struct {
	surfaceProvider
	observations int
	deadlines    []time.Duration
}

func (p *changingComputerObservation) Observe(ctx context.Context, instance computer.Instance, target computer.Target, lease string, options computer.ObserveOptions) (computer.Capture, error) {
	capture, err := p.Driver.Observe(ctx, instance, target, lease, options)
	p.observations++
	if end, ok := ctx.Deadline(); ok {
		p.deadlines = append(p.deadlines, time.Until(end))
	}
	if p.observations >= 3 {
		capture.State += " CHANGED"
	}
	return capture, err
}

func TestComputerPluginIndependentWaitRecipeDrivesOriginalObserveLoop(t *testing.T) {
	ctx := context.Background()
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) {
		h := installComputerPlugin(t, s, r)
		first, err := h.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		selectComputerVersion(t, h, first.Revision, "1.4.0")
	})
	b := bridges[0]
	p := &changingComputerObservation{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	token := computerPluginBind(t, b)
	result, err := computerPluginCall(t, b, "baseline", "invoke", token, "getAXState")
	baseline := computerPluginValue(t, result, err)
	started := time.Now()
	result, err = computerPluginCall(t, b, "wait", "invoke", token, "waitForChange", map[string]any{"sinceRevision": baseline["revision"], "timeoutMs": 5000})
	value := computerPluginValue(t, result, err)
	if time.Since(started) < 200*time.Millisecond || p.observations != 3 || value["baseRevision"] != baseline["revision"] || !strings.Contains(value["state"].(string), "CHANGED") {
		t.Fatal("independent poll recipe did not drive original loop", value, p.observations, time.Since(started))
	}
	result, err = computerPluginCall(t, b, "explicit-deadline", "invoke", token, "waitForChange", map[string]any{"timeoutMs": 5})
	if err != nil || result.Error == nil || result.Error.Code != "WAIT_TIMEOUT" || result.Error.Details["observation"] == nil {
		t.Fatal("wait lost user deadline or original bounded timeout evidence", result, err)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='computer-use'`) != 0 {
		t.Fatal("wait recipe created a second effect ledger")
	}
}

func TestComputerPluginMechanicalGateCannotExpandWaitOrUserDeadline(t *testing.T) {
	for _, version := range []string{"1.5.0", "1.6.0"} {
		t.Run(version, func(t *testing.T) {
			ctx := context.Background()
			_, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) {
				h := installComputerPlugin(t, s, r)
				first, err := h.Store.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				selectComputerVersion(t, h, first.Revision, version)
			})
			b := bridges[0]
			p := &changingComputerObservation{}
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			token := computerPluginBind(t, b)
			result, err := computerPluginCall(t, b, "baseline", "invoke", token, "getAXState")
			computerPluginValue(t, result, err)
			before := p.observations
			deadlinesBefore := len(p.deadlines)
			result, err = computerPluginCall(t, b, "invalid-wait", "invoke", token, "waitForChange", map[string]any{"timeoutMs": 5})
			if version == "1.5.0" {
				if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || p.observations != before {
					t.Fatal("candidate expanded wait or started unauthorised observation", result, err, p.observations, before)
				}
			} else {
				if err != nil || result.Error == nil || result.Error.Code != "WAIT_TIMEOUT" || len(p.deadlines) != deadlinesBefore+1 || p.deadlines[deadlinesBefore] > 5*time.Millisecond {
					t.Fatal("default recipe overrode original user duration", result.Error, err, p.deadlines)
				}
			}
		})
	}
}

func computerDocumentationText(t *testing.T, b *codeBridge, id, token, method string) string {
	t.Helper()
	result, err := computerPluginCall(t, b, id, "invoke", token, method)
	if err != nil || result.Error != nil {
		t.Fatal("documentation failed", result.Error, err)
	}
	var text string
	if json.Unmarshal([]byte(result.Text), &text) != nil {
		t.Fatal("documentation was not original string result", result.Text)
	}
	return text
}

func TestComputerPluginDocumentationActualReadKeepsStateAndOldReceipt(t *testing.T) {
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { installComputerPlugin(t, s, r) })
	b := bridges[0]
	r.ConfigureComputer(&surfaceProvider{}, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	token := computerPluginBind(t, b)
	var before, after string
	if err := s.db.QueryRow(`SELECT state_json FROM computer_use WHERE execution_id=?`, b.req.ExecutionID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	core := computerDocumentationText(t, b, "core-doc", token, "documentation")
	browser := computerDocumentationText(t, b, "browser-doc", token, "capabilities.documentation")
	if !strings.Contains(core, "interface Surface") || !strings.Contains(browser, "interface Playwright") {
		t.Fatal("wrong versioned documentation kind", core, browser)
	}
	if replay := computerDocumentationText(t, b, "core-doc", token, "documentation"); replay != core {
		t.Fatal("original documentation receipt replay changed")
	}
	if err := s.db.QueryRow(`SELECT state_json FROM computer_use WHERE execution_id=?`, b.req.ExecutionID).Scan(&after); err != nil || after != before {
		t.Fatal("documentation changed actionable state", err)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='computer-use'`) != 0 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 {
		t.Fatal("static documentation created a new effect or Artifact ledger")
	}
}

func TestComputerPluginDocumentationIndependentUpgradeRetainedRunRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	var host rsi.Host
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { host = installComputerPlugin(t, s, r) })
	b := bridges[0]
	r.ConfigureComputer(&surfaceProvider{}, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	token := computerPluginBind(t, b)
	original := computerDocumentationText(t, b, "original-doc", token, "documentation")
	first, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, pkg := selectComputerVersion(t, host, first.Revision, "1.7.0")
	if kept := computerDocumentationText(t, b, "retained-doc", token, "documentation"); kept != original {
		t.Fatal("upgrade changed old Run documentation")
	}
	if err = b.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	next := newComputerPluginExecution(t, s, r)
	nextToken := computerPluginBind(t, next)
	changed := computerDocumentationText(t, next, "new-doc", nextToken, "documentation")
	if changed != original+"\nMETHOD_DOC_V1_7" {
		t.Fatal("new Run did not use independent documentation implementation", changed)
	}
	activateDelegation(t, host, "documentation-rollback", second, first.Revision, "computer-owner-fixture")
	if err = host.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	if kept := computerDocumentationText(t, next, "retired-doc", nextToken, "documentation"); kept != changed {
		t.Fatal("retirement rewrote retained Run")
	}
	if err = next.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	rolled := newComputerPluginExecution(t, s, r)
	rolledToken := computerPluginBind(t, rolled)
	if got := computerDocumentationText(t, rolled, "rollback-doc", rolledToken, "documentation"); got != original {
		t.Fatal("rollback did not restore selected documentation")
	}
	if _, err = host.Store.Deactivate(ctx, "documentation-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	if got := computerDocumentationText(t, rolled, "original-doc", rolledToken, "documentation"); got != original {
		t.Fatal("deactivation changed frozen Run")
	}
	if err = rolled.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	disabled := newComputerPluginExecution(t, s, r)
	result, err := computerPluginCall(t, disabled, "disabled-doc", "bind", "", "getApp", "notes")
	if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" {
		t.Fatal("missing managed documentation restored native", result, err)
	}
}

func TestComputerPluginDocumentationKeepsExplicitNativeRunAfterActivation(t *testing.T) {
	ctx := context.Background()
	s, r, bridges, _ := computerPluginClaims(t)
	host := installComputerPlugin(t, s, r)
	first, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selectComputerVersion(t, host, first.Revision, "1.7.0")
	b := bridges[0]
	r.ConfigureComputer(&surfaceProvider{}, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	token := computerPluginBind(t, b)
	text := computerDocumentationText(t, b, "native-doc", token, "documentation")
	if !strings.Contains(text, "interface Surface") || strings.Contains(text, "METHOD_DOC_V1_7") {
		t.Fatal("activation retrospectively opted native Run into new docs")
	}
	managed, err := r.managedRunService(ctx, b.req.RunID, contract.DocumentationContract)
	if err != nil || managed {
		t.Fatal("native migration boundary changed", managed, err)
	}
}

func TestComputerPluginDocumentationMechanicalGateCannotExpandHostByteLimit(t *testing.T) {
	ctx := context.Background()
	_, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) {
		h := installComputerPlugin(t, s, r)
		first, err := h.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		selectComputerVersion(t, h, first.Revision, "1.8.0")
	})
	b := bridges[0]
	r.ConfigureComputer(&surfaceProvider{}, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	token := computerPluginBind(t, b)
	result, err := computerPluginCall(t, b, "oversize-doc", "invoke", token, "documentation")
	if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || result.Text != "" {
		t.Fatal("candidate widened its schema and bypassed Host byte limit", result, err)
	}
}

func TestComputerPluginDocumentationLateForgetAndGuardianRejectCommit(t *testing.T) {
	for _, cause := range []string{"forget", "stop", "quarantine"} {
		t.Run(cause, func(t *testing.T) {
			ctx := context.Background()
			var host rsi.Host
			s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { host = installComputerPlugin(t, s, r) })
			b := bridges[0]
			r.ConfigureComputer(&surfaceProvider{}, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			token := computerPluginBind(t, b)
			selected, err := host.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			owner := idAgent(t, s)
			crossed := false
			s.MainBoundary = func(point string) {
				if point != "before_computer_documentation_commit" {
					return
				}
				crossed = true
				switch cause {
				case "forget":
					err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) })
				case "stop":
					err = host.Store.Stop(ctx, true)
				case "quarantine":
					err = host.Store.Quarantine(ctx, selected.Revision)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			result, err := computerPluginCall(t, b, "revoked-doc", "invoke", token, "documentation")
			want := rsi.ErrRevoked
			if cause == "forget" {
				want = agent.ErrClaimLost
			}
			if !crossed || !errors.Is(err, want) || result.Text != "" {
				t.Fatal("late revocation accepted documentation", crossed, err, result.Text)
			}
			if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__computer_invoke' AND status='succeeded'`) != 0 {
				t.Fatal("revoked documentation receipt was committed")
			}
		})
	}
}

func computerVersionHasUpload(version string) bool {
	if computerVersionHasBrowser(version) {
		return true
	}
	switch version {
	case "1.0.7", "1.31.0", "1.32.0", "1.33.0", "1.0.6", "1.26.0", "1.27.0", "1.28.0", "1.29.0", "1.30.0":
		return true
	}
	return false
}
func computerUploadFixtureConfig() json.RawMessage {
	raw, _ := json.Marshal(struct {
		Driver string `json:"browser_upload_driver"`
	}{macosvm.BrowserUploadResourceHash()})
	return raw
}

// Actual historical packages retain their original service sets. Synthetic
// policy upgrades compose the current other services, so an unrelated change
// cannot accidentally remove the already adopted browser safety boundary.
func computerVersionHasBrowser(version string) bool {
	switch version {
	case "1.0.0", "1.0.1", "1.0.2", "1.0.3", "1.0.4", "1.0.5", "1.0.6":
		return false
	}
	return true
}
func computerBrowserFixtureConfig() json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"browser_upload_driver": macosvm.BrowserUploadResourceHash(), "browser_snapshot_driver": macosvm.BrowserSnapshotResourceHash()})
	return raw
}
