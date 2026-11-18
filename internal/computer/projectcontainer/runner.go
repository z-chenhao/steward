package projectcontainer

import (
	"context"
	_ "embed"
	"errors"
	"strings"
	"sync"
	"time"
)

type Step struct {
	Command        string
	TimeoutSeconds int
}
type Commands struct {
	Prepare                 Step
	Start                   *Step
	Doctor, Verify, Cleanup Step
}
type Phase struct {
	Phase      string `json:"phase"`
	Command    string `json:"command"`
	Output     string `json:"output"`
	ExitCode   *int   `json:"exit_code"`
	TimedOut   bool   `json:"timed_out"`
	Truncated  bool   `json:"truncated"`
	DurationMS int64  `json:"duration_ms"`
	Status     string `json:"status"`
	ErrorCode  string `json:"error_code,omitempty"`
}
type Terminal struct {
	Outcome          string
	Phases           []Phase
	CleanupConfirmed bool
}
type budget struct {
	sync.Mutex
	remaining int
}
type capture struct {
	budget    *budget
	data      []byte
	truncated bool
}

func (o *capture) Write(p []byte) (int, error) {
	o.budget.Lock()
	defer o.budget.Unlock()
	n := min(len(p), o.budget.remaining)
	o.budget.remaining -= n
	o.data = append(o.data, p[:n]...)
	o.truncated = o.truncated || n < len(p)
	return len(p), nil
}

type process struct {
	done   chan struct{}
	cancel context.CancelFunc
	phase  Phase
	err    error
}

func (p *process) ended() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}
func (c *Client) phase(ctx context.Context, token, name string, step Step, b *budget) *process {
	live, cancel := context.WithTimeout(ctx, time.Duration(step.TimeoutSeconds)*time.Second)
	p := &process{done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(p.done)
		defer cancel()
		at := time.Now()
		output := &capture{budget: b}
		exit, err := c.Exec(live, token, step.Command, output)
		p.phase = Phase{Phase: name, Command: step.Command, Output: strings.ToValidUTF8(string(output.data), "�"), Truncated: output.truncated, DurationMS: time.Since(at).Milliseconds(), Status: "failed"}
		p.err = err
		if err == nil {
			p.phase.ExitCode = &exit
			if exit == 0 {
				p.phase.Status = "passed"
			}
		} else {
			p.phase.Status = "unknown"
			p.phase.ErrorCode = "UNKNOWN_SIDE_EFFECT"
		}
		if errors.Is(live.Err(), context.DeadlineExceeded) {
			p.phase.TimedOut = true
			p.phase.Status = "incomplete"
		}
		if output.truncated {
			p.phase.Status = "incomplete"
		}
	}()
	return p
}
func validCommands(c Commands) bool {
	steps := []Step{c.Prepare, c.Doctor, c.Verify, c.Cleanup}
	total := 0
	for _, s := range steps {
		total += s.TimeoutSeconds
	}
	if total > 240 {
		return false
	}
	if c.Start != nil {
		steps = append(steps, *c.Start)
	}
	for _, s := range steps {
		if strings.TrimSpace(s.Command) == "" || len(s.Command) > 16384 || s.TimeoutSeconds < 1 || s.TimeoutSeconds > 120 {
			return false
		}
	}
	return true
}

// Run owns exactly one approved invocation's resource. The caller commits the
// token/directory before entry. Cleanup authority survives Run/Code claim loss.
func (c *Client) Run(ctx context.Context, token, directory string, commands Commands, created func(context.Context) error) (terminal Terminal, err error) {
	terminal.Outcome = "setup_failed"
	if !validCommands(commands) {
		return terminal, ErrInvalid
	}
	live, cancel := context.WithCancel(ctx)
	defer cancel()
	var service *process
	creationUncertain := false
	defer func() {
		if !terminal.CleanupConfirmed {
			cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
			observed, removed := c.Remove(cleanup, token)
			stop()
			terminal.CleanupConfirmed = removed == nil && (!creationUncertain || observed)
			if removed != nil {
				err = ErrUnknown
			}
		}
		cancel()
		if service != nil {
			service.cancel()
			<-service.done
		}
	}()
	env := []string{"HOME=/workspace", "PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8", "TMPDIR=/workspace/.tmp", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GOENV=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOPATH=/workspace/.cache/go", "GOCACHE=/workspace/.cache/go-build", "STEWARD_STATE_DIR=/workspace/.state", "STEWARD_BROWSER_PROFILE=/workspace/.state/browser", "PORT=3000"}
	if err = c.Create(live, token, directory, env); err != nil {
		creationUncertain = errors.Is(err, ErrUnknown)
		return terminal, err
	}
	if created != nil {
		if err = created(live); err != nil {
			return terminal, ErrUnknown
		}
	}
	if err = c.Start(live, token); err != nil {
		return terminal, err
	}
	b := &budget{remaining: 48000}
	run := func(name string, s Step) bool {
		p := c.phase(live, token, name, s, b)
		if service != nil && name != "cleanup" {
			select {
			case <-p.done:
			case <-service.done:
				cancel()
				<-p.done
			}
		} else {
			<-p.done
		}
		terminal.Phases = append(terminal.Phases, p.phase)
		if p.err != nil {
			err = ErrUnknown
		}
		return p.phase.Status == "passed" && (service == nil || name == "cleanup" || !service.ended())
	}
	if run("prepare", commands.Prepare) {
		if commands.Start != nil {
			service = c.phase(live, token, "start", *commands.Start, b)
		}
		if run("doctor", commands.Doctor) {
			terminal.Outcome = "verification_failed"
			if run("verify", commands.Verify) {
				terminal.Outcome = "passed"
			}
		}
	}
	if live.Err() == nil && err == nil {
		if !run("cleanup", commands.Cleanup) {
			terminal.Outcome = "cleanup_failed"
		}
	}
	if service != nil {
		// Removal stops every descendant in the private PID namespace, including
		// processes that detached from their command's original process group.
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		alive := !service.ended()
		_, removed := c.Remove(cleanup, token)
		stop()
		terminal.CleanupConfirmed = removed == nil
		cancel()
		service.cancel()
		<-service.done
		p := service.phase
		if alive && removed == nil && !p.TimedOut && !p.Truncated {
			p.Status = "stopped"
			p.ErrorCode = ""
		} else if terminal.Outcome == "passed" {
			terminal.Outcome = "setup_failed"
		}
		terminal.Phases = append(terminal.Phases[:1], append([]Phase{p}, terminal.Phases[1:]...)...)
	}
	for _, p := range terminal.Phases {
		if p.TimedOut || p.Truncated {
			terminal.Outcome = "incomplete"
		}
	}
	if ctx.Err() != nil {
		return terminal, ErrUnknown
	}
	return terminal, err
}

//go:embed runner.go
var runnerSource string
