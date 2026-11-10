// GitHub Actions annotations, printed where Actions reads them and nowhere else
// (JCT17).
//
// A workflow command (`::error file=…::…`) puts a problem on the diff, which is
// the point of checking in CI rather than on the server. Everywhere else it is the
// same problem printed a second time, in a syntax meant for a machine: a terminal
// running `jcloud check` showed every problem twice. So they are printed only
// when GITHUB_ACTIONS is "true", which Actions sets in every step.
//
// A caller that runs jcloud inside a container from an Actions step has to pass
// the variable through (`docker run -e GITHUB_ACTIONS …`): a container starts with
// none of the step's environment, and without it the annotations are not printed.
package main

import (
	"fmt"
	"io"
	"os"
)

// annotations is where annotations go: stdout inside Actions, nowhere outside it.
// Read on every call, so the environment at the moment of printing decides.
func annotations() io.Writer {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		return os.Stdout
	}
	return io.Discard
}

// annotate prints one workflow command: kind is error or warning, file may be "".
func annotate(kind, file, message string) {
	if file != "" {
		fmt.Fprintf(annotations(), "::%s file=%s::%s\n", kind, file, message)
		return
	}
	fmt.Fprintf(annotations(), "::%s::%s\n", kind, message)
}
