package actions

import (
	"fmt"
	"os/exec"
)

// run runs cmd to the end and turns a failure into an error that says what the command said.
func run(cmd *exec.Cmd) error {
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w%s", cmd.Path, err, tail(out))
	}
	return nil
}
