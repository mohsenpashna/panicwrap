package panicwrap

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

var wrapRe = regexp.MustCompile(`wrapped: \d{3,4}`)

func helperProcess(s ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--"}
	cs = append(cs, s...)
	env := []string{
		"GO_WANT_HELPER_PROCESS=1",
	}

	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = append(env, os.Environ()...)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	return cmd
}

// This is executed by `helperProcess` in a separate process in order to
// provider a proper sub-process environment to test some of our functionality.
func TestHelperProcess(*testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	// Find the arguments to our helper, which are the arguments past
	// the "--" in the command line.
	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}

		args = args[1:]
	}

	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "No command\n")
		os.Exit(2)
	}

	panicHandler := func(s string) {
		fmt.Fprintf(os.Stdout, "wrapped: %d", len(s))
		os.Exit(0)
	}

	cmd, args := args[0], args[1:]
	switch cmd {
	case "no-panic-ordered-output":
		done, exitStatus, err := BasicWrap(panicHandler)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wrap error: %s", err)
			os.Exit(1)
		}

		if !done {
			for i := 0; i < 1000; i++ {
				os.Stdout.Write([]byte("a"))
				os.Stderr.Write([]byte("b"))
			}
			os.Exit(0)
		}

		os.Exit(exitStatus)
	case "no-panic-output":
		fmt.Fprint(os.Stdout, "i am output")
		fmt.Fprint(os.Stderr, "stderr out")
		os.Exit(0)
	case "panic-boundary":
		done, exitStatus, err := BasicWrap(panicHandler)

		if err != nil {
			fmt.Fprintf(os.Stderr, "wrap error: %s", err)
			os.Exit(1)
		}

		if !done {
			// Simulate a panic but on two boundaries...
			fmt.Fprint(os.Stderr, "pan")
			os.Stderr.Sync()
			time.Sleep(100 * time.Millisecond)
			fmt.Fprint(os.Stderr, "ic: oh crap")
			os.Exit(2)
		}

		os.Exit(exitStatus)
	case "fatal":
		done, exitStatus, err := BasicWrap(panicHandler)

		if err != nil {
			fmt.Fprintf(os.Stderr, "wrap error: %s", err)
			os.Exit(exitStatus)
		}

		if !done {
			// force a concurrent map error
			badmap := make(map[int]int)
			go func() {
				for {
					badmap[0] = 0
				}
			}()
			for {
				badmap[0] = 0
			}
		}
	case "panic-long":
		done, exitStatus, err := BasicWrap(panicHandler)

		if err != nil {
			fmt.Fprintf(os.Stderr, "wrap error: %s", err)
			os.Exit(1)
		}

		if !done {
			// Make a fake panic by faking the header and adding a
			// bunch of garbage.
			fmt.Fprint(os.Stderr, "panic: foo\n\n")
			for i := 0; i < 1024; i++ {
				fmt.Fprint(os.Stderr, "foobarbaz")
			}

			// Sleep so that it dumps the previous data
			//time.Sleep(1 * time.Millisecond)
			time.Sleep(500 * time.Millisecond)

			// Make a real panic
			panic("I AM REAL!")
		}

		os.Exit(exitStatus)
	case "panic":
		hidePanic := false
		if args[0] == "hide" {
			hidePanic = true
		}

		config := &WrapConfig{
			Handler:   panicHandler,
			HidePanic: hidePanic,
		}

		done, exitStatus, err := Wrap(config)

		if err != nil {
			fmt.Fprintf(os.Stderr, "wrap error: %s", err)
			os.Exit(1)
		}

		if !done {
			panic("uh oh")
		}

		os.Exit(exitStatus)
	case "wrapped":
		child := false
		if len(args) > 0 && args[0] == "child" {
			child = true
		}
		config := &WrapConfig{
			Handler: panicHandler,
		}

		done, exitStatus, err := Wrap(config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wrap error: %s", err)
			os.Exit(1)
		}

		if !done {
			if child {
				fmt.Printf("%v", Wrapped(nil))
			}
			os.Exit(0)
		}

		if !child {
			fmt.Printf("%v", Wrapped(nil))
		}
		os.Exit(exitStatus)
	case "recursive":
		config := &WrapConfig{
			Handler: panicHandler,
		}

		if len(args) > 0 && args[0] == "child" {
			fmt.Printf("%v", Wrapped(config))
			os.Exit(0)
		}

		_, exitCode, err := Wrap(config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wrap error: %s", err)
			os.Exit(1)
		}

		if Wrapped(nil) {
			p := helperProcess("recursive", "child")
			if err := p.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "wrap error: %s", err)
				os.Exit(1)
			}
			os.Exit(0)
		}
		os.Exit(exitCode)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %q\n", cmd)
		os.Exit(2)
	}
}

func TestPanicWrap_Output(t *testing.T) {
	stderr := new(bytes.Buffer)
	stdout := new(bytes.Buffer)

	p := helperProcess("no-panic-output")
	p.Stdout = stdout
	p.Stderr = stderr
	if err := p.Run(); err != nil {
		t.Fatalf("err: %s", err)
	}

	if !strings.Contains(stdout.String(), "i am output") {
		t.Fatalf("didn't forward: %#v", stdout.String())
	}

	if !strings.Contains(stderr.String(), "stderr out") {
		t.Fatalf("didn't forward: %#v", stderr.String())
	}
}

/*
TODO(mitchellh): This property would be nice to gain.
func TestPanicWrap_Output_Order(t *testing.T) {
	output := new(bytes.Buffer)

	p := helperProcess("no-panic-ordered-output")
	p.Stdout = output
	p.Stderr = output
	if err := p.Run(); err != nil {
		t.Fatalf("err: %s", err)
	}

	expectedBuf := new(bytes.Buffer)
	for i := 0; i < 1000; i++ {
		expectedBuf.WriteString("ab")
	}

	actual := strings.TrimSpace(output.String())
	expected := strings.TrimSpace(expectedBuf.String())

	if actual != expected {
		t.Fatalf("bad: %#v", actual)
	}
}
*/

func TestPanicWrap_panicHide(t *testing.T) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	p := helperProcess("panic", "hide")
	p.Stdout = stdout
	p.Stderr = stderr
	if err := p.Run(); err != nil {
		t.Fatalf("err: %s", err)
	}

	if wrapRe.FindString(stdout.String()) == "" {
		t.Fatalf("didn't wrap: %#v", stdout.String())
	}

	if strings.Contains(stderr.String(), "panic:") {
		t.Fatalf("shouldn't have panic: %#v", stderr.String())
	}
}

func TestPanicWrap_panicShow(t *testing.T) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	p := helperProcess("panic", "show")
	p.Stdout = stdout
	p.Stderr = stderr
	if err := p.Run(); err != nil {
		t.Fatalf("err: %s", err)
	}

	if wrapRe.FindString(stdout.String()) == "" {
		t.Fatalf("didn't wrap: %#v", stdout.String())
	}

	if !strings.Contains(stderr.String(), "panic:") {
		t.Fatalf("should have panic: %#v", stderr.String())
	}
}

func TestPanicWrap_panicLong(t *testing.T) {
	stdout := new(bytes.Buffer)

	p := helperProcess("panic-long")
	p.Stdout = stdout
	p.Stderr = new(bytes.Buffer)
	if err := p.Run(); err != nil {
		t.Fatalf("err: %s", err)
	}

	if wrapRe.FindString(stdout.String()) == "" {
		t.Fatalf("didn't wrap: %#v", stdout.String())
	}
}

func TestPanicWrap_panicBoundary(t *testing.T) {
	// TODO(mitchellh): panics are currently lost on boundaries
	t.SkipNow()

	stdout := new(bytes.Buffer)

	p := helperProcess("panic-boundary")
	p.Stdout = stdout
	//p.Stderr = new(bytes.Buffer)
	if err := p.Run(); err != nil {
		t.Fatalf("err: %s", err)
	}

	if wrapRe.FindString(stdout.String()) == "" {
		t.Fatalf("didn't wrap: %#v", stdout.String())
	}
}

func TestPanicWrap_recursive(t *testing.T) {
	stdout := new(bytes.Buffer)

	p := helperProcess("recursive")
	p.Stdout = stdout
	if err := p.Run(); err != nil {
		t.Fatalf("err: %s", err)
	}

	if !strings.Contains(stdout.String(), "false") {
		t.Fatalf("Wrapped false positive: %#v", stdout.String())
	}
}

func TestWrapped(t *testing.T) {
	stdout := new(bytes.Buffer)

	p := helperProcess("wrapped", "child")
	p.Stdout = stdout
	if err := p.Run(); err != nil {
		t.Fatalf("err: %s", err)
	}

	if !strings.Contains(stdout.String(), "true") {
		t.Fatalf("bad: %#v", stdout.String())
	}
}

func TestWrapped_parent(t *testing.T) {
	stdout := new(bytes.Buffer)

	p := helperProcess("wrapped")
	p.Stdout = stdout
	if err := p.Run(); err != nil {
		t.Fatalf("err: %s", err)
	}

	if !strings.Contains(stdout.String(), "false") {
		t.Fatalf("bad: %#v", stdout.String())
	}
}

func TestPanicWrap_fatal(t *testing.T) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	p := helperProcess("fatal")
	p.Stdout = stdout
	p.Stderr = stderr
	if err := p.Run(); err != nil {
		t.Fatalf("err: %s", err)
	}

	if wrapRe.FindString(stdout.String()) == "" {
		t.Fatalf("didn't wrap: %#v", stdout.String())
	}
}
