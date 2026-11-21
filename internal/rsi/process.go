package rsi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/z-chenhao/assistant/internal/computer/sandbox"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Only safe, bounded startup facts cross the process error boundary.
// Joined status may include Host cleanup; a candidate report is not a cause.
type processStartupError struct{ status, report string }

func (e *processStartupError) Error() string {
	return fmt.Sprintf("%v: missing describe response (joined_exit=%s, startup_report=%s)", ErrUnknown, e.status, e.report)
}
func (e *processStartupError) Unwrap() error { return ErrUnknown }

// Runner uses an invocation-scoped process. Durable plugin state travels through
// Host snapshots/CAS; no resident process or private scratch file is authoritative.
type Runner struct {
	Catalog        Catalog
	resources      *resourceScope
	inputReadLimit int // Explicit immutable owner stream only; never candidate JSON.
	Dependency     func(context.Context, rsiplugin.DependencyCall) (json.RawMessage, error)
	Propose        func(context.Context, rsiplugin.Proposal) (rsiplugin.ProposalReceipt, error)
	ReadInput      func(context.Context, rsiplugin.InputRead) (rsiplugin.InputChunk, error)
	Compute        func(context.Context, rsiplugin.ComputeRequest) (rsiplugin.ComputeResult, error)
}

func (r Runner) Invoke(ctx context.Context, b rsiplugin.Binding, method string, input, output any) (resultErr error) {
	p, err := r.Catalog.Load(b.PackageDigest)
	if err != nil {
		return err
	}
	if _, err = pluginLimitLauncher(); err != nil {
		return fmt.Errorf("RSI_SANDBOX_UNAVAILABLE: %w", err)
	}
	if err = sandbox.Check(); err != nil {
		return fmt.Errorf("RSI_SANDBOX_UNAVAILABLE: %w", err)
	}
	work, err := os.MkdirTemp("", "steward-rsi-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	work, err = filepath.EvalSymlinks(work)
	if err != nil {
		return err
	}
	var budget *resourceDeadline
	if r.resources == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(p.Manifest.Runtime.TimeoutMS)*time.Millisecond)
		defer cancel()
	} else {
		if _, bounded := ctx.Deadline(); !bounded {
			return ErrInvalid
		}
		var cancel context.CancelCauseFunc
		ctx, cancel = context.WithCancelCause(ctx)
		defer cancel(nil)
		budget = newResourceDeadline(time.Duration(p.Manifest.Runtime.TimeoutMS)*time.Millisecond, cancel)
		defer budget.stop()
	}
	root, err := filepath.EvalSymlinks(r.Catalog.Path(b.PackageDigest))
	if err != nil {
		return err
	}
	launcher, err := os.Executable()
	if err != nil {
		return err
	}
	if r.Catalog.Launcher != "" {
		launcher = r.Catalog.Launcher
	}
	launcher, err = filepath.EvalSymlinks(launcher)
	if err != nil {
		return err
	}
	cmd, cleanup, err := isolatedCommand(ctx, launcher, resourcePath(root, p.Manifest.Runtime.Entry), root, work)
	if err != nil {
		return err
	}
	defer cleanup()
	cmd.Dir = work
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "HOME=" + work, "TMPDIR=" + work}
	if runtime.GOOS == "linux" && r.resources != nil {
		// Each glibc arena reserves virtual space against the existing 2 GiB
		// limit, even when most of its pages are unused during resource reads.
		cmd.Env = append(cmd.Env, "MALLOC_ARENA_MAX=2")
	}
	requests := []struct {
		method string
		value  any
	}{{"describe", nil}, {"prepare", rsiplugin.PrepareInput{Protocol: rsiplugin.Protocol, InstanceID: b.InstanceID, Config: b.Config}}}
	if method != "prepare" {
		requests = append(requests, struct {
			method string
			value  any
		}{method, input})
	}
	requests = append(requests, struct {
		method string
		value  any
	}{"drain", nil}, struct {
		method string
		value  any
	}{"dispose", nil})
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = sandbox.NewOutput(8192)
	cmd.WaitDelay = time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err = cmd.Start(); err != nil {
		return err
	}
	waited, missingDescribe := false, false
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if !waited {
			_ = cmd.Wait()
		}
		if missingDescribe && resultErr != nil && cmd.ProcessState != nil {
			// Stderr is untrusted and private. Only fixed startup-report categories
			// and the joined OS status may leave this boundary; neither proves a cause.
			report := "unavailable"
			message := string(cmd.Stderr.(*sandbox.Output).Bytes())
			switch {
			case strings.Contains(message, "runtime: failed to create new OS thread"), strings.Contains(message, "runtime/cgo: pthread_create failed"):
				report = "thread_startup"
			case strings.Contains(message, "fatal error: out of memory"), strings.Contains(message, "runtime: out of memory"), strings.Contains(message, "failed to reserve page summary memory"), strings.Contains(message, "fatal error: runtime: cannot allocate memory"):
				report = "memory_startup"
			case strings.HasPrefix(message, "prlimit:"):
				report = "limit_launcher"
			case strings.HasPrefix(message, "bwrap:"):
				report = "sandbox_startup"
			}
			resultErr = &processStartupError{status: cmd.ProcessState.String(), report: report}
		}
	}()
	outputLimit := int64(40 * rsiplugin.MaxFrameBytes)
	if r.resources != nil {
		outputLimit = 2 * resourceTrafficBytes
	}
	scanner := bufio.NewScanner(io.LimitReader(stdout, outputLimit))
	scanner.Buffer(make([]byte, 4096), rsiplugin.MaxFrameBytes+1)
	writer := json.NewEncoder(stdin)
	dependencies := 0
	inputReads := 0
	computed := false
	var transfer resultTransfer
	for i, q := range requests {
		params, e := json.Marshal(q.value)
		if e != nil {
			return e
		}
		req := rsiplugin.Request{Protocol: rsiplugin.Protocol, ID: fmt.Sprint(i + 1), Method: q.method, Params: params}
		raw, e := json.Marshal(req)
		if e != nil || len(raw) > rsiplugin.MaxFrameBytes {
			return ErrInvalid
		}
		if err = writer.Encode(req); err != nil {
			return ErrUnknown
		}
		var resp rsiplugin.Response
		for {
			if !scanner.Scan() {
				if ctx.Err() != nil {
					if budget != nil {
						return context.Cause(ctx)
					}
					return ctx.Err()
				}
				missingDescribe = q.method == "describe"
				return fmt.Errorf("%w: missing %s response", ErrUnknown, q.method)
			}
			var header struct {
				Method string `json:"method"`
			}
			if json.Unmarshal(scanner.Bytes(), &header) != nil {
				return ErrInvalid
			}
			if header.Method == "" {
				if rsiplugin.Decode(scanner.Bytes(), &resp) != nil {
					return ErrInvalid
				}
				break
			}
			var callback rsiplugin.Request
			if rsiplugin.Decode(scanner.Bytes(), &callback) != nil || callback.Protocol != rsiplugin.Protocol || callback.ID == "" || (callback.Method != "dependency" && callback.Method != "propose" && callback.Method != "read_input" && callback.Method != "compute" && callback.Method != "write_result" && callback.Method != "resource" && callback.Method != "read_resource" && callback.Method != "write_resource") || (q.method != "call" && q.method != "hook") {
				return ErrInvalid
			}
			if callback.Method == "resource" || callback.Method == "read_resource" || callback.Method == "write_resource" {
				if q.method != "call" || r.resources == nil || !hasRequest(p.Manifest, "resource.call") {
					return ErrRevoked
				}
				if err := ctx.Err(); err != nil {
					return context.Cause(ctx)
				}
				reply := rsiplugin.Response{Protocol: rsiplugin.Protocol, ID: callback.ID}
				var value any
				var err error
				switch callback.Method {
				case "write_resource":
					var chunk rsiplugin.ResultChunk
					if rsiplugin.Decode(callback.Params, &chunk) != nil {
						return ErrInvalid
					}
					err = r.resources.write(ctx, chunk)
				case "read_resource":
					var read rsiplugin.InputRead
					if rsiplugin.Decode(callback.Params, &read) != nil {
						return ErrInvalid
					}
					value, err = r.resources.read(ctx, read)
				case "resource":
					var call rsiplugin.ResourceCall
					if rsiplugin.Decode(callback.Params, &call) != nil {
						return ErrInvalid
					}
					if err = budget.pause(); err != nil {
						return err
					}
					value, err = r.resources.invoke(ctx, call)
					budget.resume()
				}
				if err != nil {
					return err
				}
				reply.Result, err = json.Marshal(value)
				if err != nil || len(reply.Result) > rsiplugin.MaxFrameBytes {
					return ErrInvalid
				}
				if err = writer.Encode(reply); err != nil {
					return ErrUnknown
				}
				continue
			}
			if callback.Method == "read_input" {
				readLimit := 32
				if computed {
					readLimit = 40
				}
				if r.inputReadLimit > 0 {
					readLimit = r.inputReadLimit
				}
				if q.method != "call" || !hasRequest(p.Manifest, "input.read") || r.ReadInput == nil || inputReads >= readLimit {
					return ErrInvalid
				}
				inputReads++
				var input rsiplugin.InputRead
				if rsiplugin.Decode(callback.Params, &input) != nil {
					return ErrInvalid
				}
				reply := rsiplugin.Response{Protocol: rsiplugin.Protocol, ID: callback.ID}
				chunk, e := r.ReadInput(ctx, input)
				if e != nil {
					reply.Error = "INPUT_UNAVAILABLE"
				} else {
					if len(chunk.Data) > rsiplugin.InputChunkBytes {
						return ErrInvalid
					}
					reply.Result, _ = json.Marshal(chunk)
				}
				if err = writer.Encode(reply); err != nil {
					return ErrUnknown
				}
				continue
			}
			if callback.Method == "write_result" {
				var chunk rsiplugin.ResultChunk
				if q.method != "call" || rsiplugin.Decode(callback.Params, &chunk) != nil || transfer.append(chunk) != nil {
					return ErrInvalid
				}
				if err := writer.Encode(rsiplugin.Response{Protocol: rsiplugin.Protocol, ID: callback.ID, Result: json.RawMessage(`null`)}); err != nil {
					return ErrUnknown
				}
				continue
			}
			if callback.Method == "compute" {
				var request rsiplugin.ComputeRequest
				if q.method != "call" || computed || r.Compute == nil || rsiplugin.Decode(callback.Params, &request) != nil || computePermission(request.Contract) == "" || !hasRequest(p.Manifest, computePermission(request.Contract)) {
					return ErrInvalid
				}
				computed = true
				reply := rsiplugin.Response{Protocol: rsiplugin.Protocol, ID: callback.ID}
				value, err := r.Compute(ctx, request)
				if err != nil {
					reply.Error = "COMPUTATION_UNAVAILABLE"
				} else {
					reply.Result, err = json.Marshal(value)
					if err != nil {
						return ErrInvalid
					}
				}
				if err := writer.Encode(reply); err != nil {
					return ErrUnknown
				}
				continue
			}
			if dependencies >= 32 {
				return ErrInvalid
			}
			dependencies++
			if callback.Method == "propose" {
				var proposal rsiplugin.Proposal
				if rsiplugin.Decode(callback.Params, &proposal) != nil {
					return ErrInvalid
				}
				reply := rsiplugin.Response{Protocol: rsiplugin.Protocol, ID: callback.ID}
				if r.Propose == nil {
					reply.Error = "NOT_AUTHORIZED"
				} else {
					receipt, e := r.Propose(ctx, proposal)
					if e != nil {
						reply.Error = "PROPOSAL_REJECTED"
					} else {
						reply.Result, _ = json.Marshal(receipt)
					}
				}
				if err = writer.Encode(reply); err != nil {
					return ErrUnknown
				}
				continue
			}
			var input rsiplugin.DependencyCall
			if rsiplugin.Decode(callback.Params, &input) != nil || input.Key == "" || len(input.Key) > 128 {
				return ErrInvalid
			}
			reply := rsiplugin.Response{Protocol: rsiplugin.Protocol, ID: callback.ID}
			if r.Dependency == nil {
				reply.Error = "DEPENDENCY_UNAVAILABLE"
			} else {
				reply.Result, err = r.Dependency(ctx, input)
				if err != nil {
					reply.Error = "DEPENDENCY_FAILED"
					reply.Result = nil
				}
			}
			if err = writer.Encode(reply); err != nil {
				return ErrUnknown
			}
		}
		if resp.Protocol != rsiplugin.Protocol || resp.ID != fmt.Sprint(i+1) || resp.Error != "" || len(resp.Result) == 0 {
			return fmt.Errorf("%w: rejected %s response", ErrUnknown, q.method)
		}
		if i == 0 {
			var d rsiplugin.Description
			if rsiplugin.Decode(resp.Result, &d) != nil {
				return ErrInvalid
			}
			expected := rsiplugin.Description{Protocol: rsiplugin.Protocol, PluginID: p.Manifest.PluginID, Version: p.Manifest.Version, Contracts: []string{}, Hooks: []rsiplugin.Stage{}}
			for _, s := range p.Manifest.Provides {
				expected.Contracts = append(expected.Contracts, s.Contract)
			}
			for _, h := range p.Manifest.Hooks {
				expected.Hooks = append(expected.Hooks, h.Stage)
			}
			sort.Strings(expected.Contracts)
			sort.Strings(d.Contracts)
			sort.Slice(expected.Hooks, func(i, j int) bool { return expected.Hooks[i] < expected.Hooks[j] })
			sort.Slice(d.Hooks, func(i, j int) bool { return d.Hooks[i] < d.Hooks[j] })
			if len(d.Contracts) == 0 {
				d.Contracts = []string{}
			}
			if len(d.Hooks) == 0 {
				d.Hooks = []rsiplugin.Stage{}
			}
			if !reflect.DeepEqual(d, expected) {
				return ErrInvalid
			}
		}
		if q.method == method && output != nil {
			if err = rsiplugin.Decode(resp.Result, output); err != nil {
				return ErrInvalid
			}
			result, _ := output.(*rsiplugin.Result)
			if err = transfer.finish(result); err != nil {
				return err
			}
		}
	}
	if scanner.Scan() || scanner.Err() != nil {
		return ErrUnknown
	}
	_ = stdin.Close()
	err = cmd.Wait()
	waited = true
	if ctx.Err() != nil {
		if budget != nil {
			return context.Cause(ctx)
		}
		return ctx.Err()
	}
	if err != nil {
		return ErrUnknown
	}
	// Recheck after execution: test/installation bytes cannot drift unnoticed.
	_, err = r.Catalog.Load(b.PackageDigest)
	return err
}

func NewCatalog(dataRoot string) Catalog {
	return Catalog{Root: filepath.Join(dataRoot, "rsi", "packages")}
}
