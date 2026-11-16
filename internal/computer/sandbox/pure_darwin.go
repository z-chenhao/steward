package sandbox

import (
	"context"
	"os/exec"
	"strconv"
)

// Pure plugin processes may create threads but cannot fork child processes,
// connect to a network or access the user's data root. External effects remain
// Host services, rather than ambient plugin OS permissions.
func PureCommand(ctx context.Context, launcher string, args []string, executable, root, work string) (*exec.Cmd, func(), error) {
	q := strconv.Quote
	profile := `(version 1)(deny default)(allow process-exec (literal ` + q(launcher) + `) (literal ` + q(executable) + `))(allow sysctl-read)(allow file-read-metadata)(allow file-read* (subpath "/usr") (subpath "/bin") (subpath "/System") (literal "/") (subpath ` + q(root) + `) (subpath ` + q(work) + `) (literal ` + q(launcher) + `) (literal "/dev/null") (literal "/dev/urandom"))(allow file-write* (subpath ` + q(work) + `) (literal "/dev/null"))`
	return exec.CommandContext(ctx, "/usr/bin/sandbox-exec", append([]string{"-p", profile, launcher}, args...)...), func() {}, nil
}
