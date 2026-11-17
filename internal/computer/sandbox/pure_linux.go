package sandbox

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"golang.org/x/sys/unix"
)

func PureCommand(ctx context.Context, launcher string, args []string, executable, root, work string) (*exec.Cmd, func(), error) {
	cmd, err := Command(ctx, launcher, args, []string{root}, work, work)
	if err != nil {
		return nil, nil, err
	}
	arch, clone := uint32(0), uint32(0)
	denied := []uint32{435}
	switch runtime.GOARCH {
	case "amd64":
		arch = 0xc000003e
		clone = 56
		denied = append(denied, 57, 58)
	case "arm64":
		arch = 0xc00000b7
		clone = 220
	default:
		return nil, nil, fmt.Errorf("pure sandbox unsupported architecture: %s", runtime.GOARCH)
	}
	filter := []unix.SockFilter{{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 4}, {Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 1, K: arch}, {Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS}, {Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0}, {Code: unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K, Jf: 1, K: 0x40000000}, {Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS}}
	for _, nr := range denied {
		errno := uint32(unix.EPERM)
		if nr == 435 {
			errno = uint32(unix.ENOSYS)
		}
		filter = append(filter, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 1, K: nr}, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | errno})
	}
	filter = append(filter, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 3, K: clone}, unix.SockFilter{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 16}, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K, Jt: 1, K: unix.CLONE_THREAD}, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)}, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW})
	f, err := os.CreateTemp("", "steward-rsi-seccomp-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { f.Close(); os.Remove(f.Name()) }
	if err = binary.Write(f, binary.LittleEndian, filter); err != nil {
		cleanup()
		return nil, nil, err
	}
	if _, err = f.Seek(0, 0); err != nil {
		cleanup()
		return nil, nil, err
	}
	cmd.ExtraFiles = []*os.File{f}
	cmd.Args = append(cmd.Args[:1], append([]string{"--seccomp", "3"}, cmd.Args[1:]...)...)
	return cmd, cleanup, nil
}
