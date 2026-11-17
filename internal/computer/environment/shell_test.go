package environment

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShellDriverUsesOriginalCommandAndAcceptedSmallerOutput(t *testing.T) {
	l := localFixture(t)
	command := `printf '%02048d' 0`
	result := l.ExecuteShell(context.Background(), ShellCommand{Command: command, Limits: ShellLimits{TimeoutSeconds: 2, OutputBytes: 17}})
	var value struct {
		Command, Output string
		Exit            int  `json:"exit_code"`
		TimedOut        bool `json:"timed_out"`
		Truncated       bool
	}
	if result.Error != nil || json.Unmarshal([]byte(result.Text), &value) != nil || value.Command != command || value.Output != strings.Repeat("0", 17) || value.Exit != 0 || value.TimedOut || !value.Truncated {
		t.Fatal("shell recipe changed command or status facts", result)
	}
}

func TestShellDriverSmallerDeadlineKillsOriginalProcessGroup(t *testing.T) {
	l := localFixture(t)
	command := `/bin/sh -c 'sleep 2; touch leaked' & sleep 5`
	started := time.Now()
	result := l.ExecuteShell(context.Background(), ShellCommand{Command: command, Limits: ShellLimits{TimeoutSeconds: 1, OutputBytes: 48000}})
	var value struct {
		TimedOut bool `json:"timed_out"`
		Exit     int  `json:"exit_code"`
	}
	if result.Error != nil || json.Unmarshal([]byte(result.Text), &value) != nil || !value.TimedOut || value.Exit == 0 || time.Since(started) > 4*time.Second {
		t.Fatal("accepted deadline did not reach the existing runner", result)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(l.Config.Root, "leaked")); !os.IsNotExist(err) {
		t.Fatal("timed-out process group survived", err)
	}
}

func TestShellDriverRejectsLimitsOutsideHostCeilingsBeforeExecution(t *testing.T) {
	l := localFixture(t)
	for _, limits := range []ShellLimits{{TimeoutSeconds: 0, OutputBytes: 48000}, {TimeoutSeconds: 121, OutputBytes: 48000}, {TimeoutSeconds: 1, OutputBytes: 48001}} {
		result := l.ExecuteShell(context.Background(), ShellCommand{Command: "touch forbidden", Limits: limits})
		if result.Error == nil || result.Error.Code != "INVALID_ARGUMENT" {
			t.Fatal("driver accepted widened resource bounds", result)
		}
	}
	if _, err := os.Stat(filepath.Join(l.Config.Root, "forbidden")); !os.IsNotExist(err) {
		t.Fatal("invalid resource plan executed", err)
	}
}

func TestShellDriverCancellationKillsOriginalProcessGroup(t *testing.T) {
	l := localFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var failure string
	go func() {
		defer close(done)
		result := l.ExecuteShell(ctx, ShellCommand{Command: `/bin/sh -c 'printf ready > started; sleep 2; touch leaked' & sleep 20`, Limits: ShellLimits{TimeoutSeconds: 120, OutputBytes: 48000}})
		if result.Error != nil {
			failure = result.Error.Code
		}
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	ready := false
	for !ready {
		select {
		case <-done:
			t.Fatal("command ended before cancellation boundary", failure)
		case <-deadline.C:
			cancel()
			<-done
			t.Fatal("command did not start")
		case <-tick.C:
			_, err := os.Stat(filepath.Join(l.Config.Root, "started"))
			ready = err == nil
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("canceled transport did not drain")
	}
	if failure != "UNKNOWN_SIDE_EFFECT" {
		t.Fatal("canceled command claimed known completion", failure)
	}
	time.Sleep(2200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(l.Config.Root, "leaked")); !os.IsNotExist(err) {
		t.Fatal("command child survived cancellation", err)
	}
}
