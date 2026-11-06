package infrastructure

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

// Git and npm metadata commands never borrow personal credentials or run plugin code.
type catalogCommand struct {
	home                 string
	workspace            string
	timeout              time.Duration
	certificateAuthority string
	// environment adds a source's access, such as its SSH command.
	environment []string
}

func (c catalogCommand) execute(ctx context.Context, binary string, arguments ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Dir = c.workspace
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + c.home, "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=https:ssh", "LANG=C.UTF-8"}
	if c.certificateAuthority != "" {
		command.Env = append(command.Env, "GIT_SSL_CAINFO="+c.certificateAuthority, "NODE_EXTRA_CA_CERTS="+c.certificateAuthority, "npm_config_cafile="+c.certificateAuthority)
	}
	command.Env = append(command.Env, c.environment...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = 5 * time.Second
	output, diagnostics := &catalogOutput{}, &catalogOutput{}
	command.Stdout = output
	command.Stderr = diagnostics
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// What the source answered tells an operator why; its URLs may carry
		// credentials, so only the safe diagnosis of its first lines is kept.
		return nil, fmt.Errorf("%s %s failed: %s: %w", binary, commandVerb(arguments), application.SafeDiagnosis(firstLines(diagnostics.String(), 2)), err)
	}
	return output.Bytes(), nil
}

// commandVerb is the subcommand, such as ls-remote or fetch; the arguments
// otherwise name source addresses.
func commandVerb(arguments []string) string {
	for i := 0; i < len(arguments); i++ {
		switch argument := arguments[i]; {
		case argument == "-C" || argument == "-c":
			i++
		case !strings.HasPrefix(argument, "-"):
			return argument
		}
	}
	return ""
}

func firstLines(text string, count int) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" && len(lines) < count {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "; ")
}

type catalogOutput struct{ bytes.Buffer }

func (b *catalogOutput) Write(value []byte) (int, error) {
	length := len(value)
	remaining := (256 << 10) - b.Len()
	if remaining > 0 {
		_, _ = b.Buffer.Write(value[:min(length, remaining)])
	}
	return length, nil
}
