package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

type commandOptions struct {
	flags             *flag.FlagSet
	configPath        string
	testSecrets       string
	testBridge        string
	testDelivery      string
	testAutomationNow string
	testMainHold      string
	testMainRelease   string
	testEvents        bool
	testBarriers      map[string]string
}

func parseOptions(args []string, errOut io.Writer) (commandOptions, error) {
	o := commandOptions{}
	flags := flag.NewFlagSet("steward", flag.ContinueOnError)
	flags.SetOutput(errOut)
	flags.String("github-scope", "", "exact configured owner/repository#PR for GitHub controls")
	flags.StringVar(&o.configPath, "config", "", "versioned YAML configuration file (optional)")
	flags.StringVar(&o.testSecrets, "test-secret-store", "", "explicit in-memory CI Unix socket; never a production credential backend")
	flags.StringVar(&o.testBridge, "test-bridge", "", "explicit deterministic test Bridge fixture; never a real provider")
	flags.BoolVar(&o.testEvents, "test-events", false, "explicit test Bridge only: exercise production NATS lifecycle")
	flags.StringVar(&o.testDelivery, "test-delivery", "", "explicit test Bridge only: retry-once, unavailable, ambiguous, unknown")
	flags.StringVar(&o.testAutomationNow, "test-automation-now", "", "explicit test Bridge only: fixed Automation clock (RFC3339)")
	flags.StringVar(&o.testMainHold, "test-main-hold", "", "explicit test Bridge only: hold at a Main durability boundary until shutdown")
	flags.StringVar(&o.testMainRelease, "test-main-release", "", "explicit test Bridge only: release a held boundary when this file exists")
	o.testBarriers = map[string]string{}
	flags.Func("test-main-barrier", "explicit test Bridge only: repeatable boundary=release-file barrier", func(value string) error {
		name, path, ok := strings.Cut(value, "=")
		if !ok || name == "" || path == "" || o.testBarriers[name] != "" {
			return errors.New("expected a unique boundary=release-file")
		}
		o.testBarriers[name] = path
		return nil
	})
	flags.Usage = func() {
		fmt.Fprintln(errOut, "GitHub Task association: github binding; github bind reads {task_ref,task_epoch,version}; github unbind reads {version}. Only future observations can continue the explicitly bound Task through Main; stop/manual completion revoke the association.")
		fmt.Fprintln(errOut, "GitHub rule: add mode observe|automatic and event_kind review_comment|review|check_run to github bind. For check_run, optionally set check_conclusion to failure. Observe records decisions without model calls or work. Review via github decisions [BEFORE_SEQUENCE]; turn off via github unbind.")
		fmt.Fprintln(errOut, "Evidence: evidence read accepts JSON {ref: current|conversation:REF|task:REF|artifact:REF|memory:REF|github:SEQUENCE, offset?, limit?, links_offset?} on stdin. GitHub evidence uses the configured PR and current Task association. See docs/features/memory-context.md.")
		fmt.Fprintln(errOut, "GitHub observations: github status|poll|events [BEFORE_SEQUENCE]|stop|resume. Use github scopes to list configured PRs and -github-scope owner/repo#123 to select one. Each watch uses an exact repository/PR and a SecretStore credential_ref; reception does not authorize work. See docs/features/integrations.md.")
		fmt.Fprintln(errOut, "Steward: run steward (or steward start) for guided setup and chat. Run doctor to check the installation. Ctrl-C or /quit stops Steward.\nstart manages a private local broker; -config or STEWARD_NATS_URL uses your configured broker instead.")
		fmt.Fprintln(errOut, "Operations: ops health|report|broker|upgrade-broker; ops trace RUN_ID; ops retry-event EVENT_ID ATTEMPTS; ops backup ARCHIVE KEY_REF; ops restore ARCHIVE KEY_REF [NEW_ROOT]; ops activate source-retired-and-outcomes-reconciled. See docs/features/operations.md.")
		fmt.Fprintln(errOut, "Task code review: coding inspect TASK_REF; coding diff TASK_REF; coding report TASK_REF. Optional execution.repository_source prepares independent work copies for new Tasks; report saves a review Artifact. coding report-pr TASK_REF EXPECTED_HEAD_SHA associates a report with the configured, previously observed PR head. coding report-pr-exact TASK_REF EXPECTED_HEAD_SHA additionally requires identical candidate content from locally available raw Git objects; it never fetches.")
		fmt.Fprintln(errOut, "macOS background service: service install|start|stop|status|uninstall. Install is inactive; start explicitly enables work and independent inspection. Stop persists across login/reboot. start/stop/status/uninstall optionally take an absolute data root to repair a broken configuration.")
		fmt.Fprintln(errOut, "Usage: steward [-config path] [-test-bridge fixture] [task feedbacks [TASK_REF [BEFORE]]|task respond FEEDBACK_REF|web serve|channel manage|tui|status|relay|automation manage REQUEST_KEY|automation list|automation tick|automation work|computer status|computer prepare-base CLEAN_STOPPED_VM|delivery list|delivery work|memory manage REQUEST_KEY|memory work|memory status|skill manage|chat send [REQUEST_KEY]|chat send-parts [REQUEST_KEY]|artifact save NAME MEDIA_TYPE|artifact open REF|artifact describe REF|artifact delete REF VERSION|artifact cleanup [apply]|chat work|chat history|spend status|approval list|approval approve ID HASH|approval deny ID HASH|approval reconcile|model add [URL MODEL_ID [AUTH_ID]]|model discover URL [AUTH_ID]|model list|model catalog [PROFILE_ID]|model login [ENV_NAME]|model test ID|model select ID PROFILE_VERSION CONFIG_VERSION|model cancel]\nWithout a command, open the TUI on a terminal; with redirected stdin, print diagnostics and wait for SIGINT/SIGTERM.\nrelay runs the internal event publisher until stopped.\nmemory manage and skill manage read one control JSON object on stdin; memory work drains a bounded maintenance batch.\nchat send reads text on stdin and durably accepts it; chat work drains/reclaims Main work in the foreground; chat history reads semantic messages.\nmodel add reads metadata JSON; test proves capabilities; select atomically activates a tested version. model cancel leaves setup unchanged. ChatGPT: model login-chatgpt [ACCOUNT_ID], accounts, account-catalog ACCOUNT_ID, account-add ACCOUNT_ID MODEL_ID, refresh ACCOUNT_ID, logout ACCOUNT_ID. Login emits a private browser URL; open it to continue with ChatGPT.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return o, err
	}
	if len(o.testBarriers) > 0 && o.testBridge == "" {
		return o, errors.New("test-main-barrier requires explicit test-bridge")
	}
	if o.testEvents && o.testBridge == "" {
		return o, errors.New("test-events requires explicit test-bridge")
	}
	if o.testBarriers[o.testMainHold] != "" {
		return o, errors.New("conflicting test boundary barriers")
	}
	o.flags = flags
	return o, nil
}
