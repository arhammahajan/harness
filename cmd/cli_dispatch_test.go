package cmd

import (
	"reflect"
	"strings"
	"testing"
)

func stubCodingAgent(t *testing.T) (*int, *[]string) {
	t.Helper()
	calls := 0
	var gotArgs []string
	orig := harnessRunCodingAgent
	t.Cleanup(func() { harnessRunCodingAgent = orig })
	harnessRunCodingAgent = func(args []string) int {
		calls++
		gotArgs = append([]string(nil), args...)
		return 0
	}
	return &calls, &gotArgs
}

func TestBareHarnessLaunchesCodingAgent(t *testing.T) {
	isolateHarnessEnv(t)
	calls, gotArgs := stubCodingAgent(t)
	launch := stubLaunch(t)

	if code := run(nil); code != 0 {
		t.Fatalf("run(nil) = %d, want 0", code)
	}
	if *calls != 1 || len(*gotArgs) != 0 {
		t.Fatalf("coding agent calls=%d args=%v, want 1 call with empty args", *calls, *gotArgs)
	}
	if launch.calls != 0 {
		t.Fatalf("external agent launch should not be called, got %d", launch.calls)
	}
}

func TestCodingAgentFlagsDispatchToCodingAgent(t *testing.T) {
	cases := [][]string{
		{"-d"},
		{"--debug"},
		{"-c", "/tmp"},
		{"--cwd=/tmp"},
		{"-p", "hello"},
		{"--prompt=hello", "-f", "json", "-q"},
		{"-v"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc, "_"), func(t *testing.T) {
			isolateHarnessEnv(t)
			calls, gotArgs := stubCodingAgent(t)
			launch := stubLaunch(t)

			if code := run(tc); code != 0 {
				t.Fatalf("run(%v) = %d, want 0", tc, code)
			}
			if *calls != 1 || !reflect.DeepEqual(*gotArgs, tc) {
				t.Fatalf("coding agent calls=%d args=%v, want 1 call with %v", *calls, *gotArgs, tc)
			}
			if launch.calls != 0 {
				t.Fatalf("external agent launch should not be called, got %d", launch.calls)
			}
		})
	}
}

func TestHarnessAgentEnvTakesPrecedenceOverCodingAgent(t *testing.T) {
	isolateHarnessEnv(t)
	t.Setenv("HARNESS_AGENT", "pi")
	calls, _ := stubCodingAgent(t)
	launch := stubLaunch(t)

	if code := run([]string{"-p", "hello"}); code != 0 {
		t.Fatalf("run with HARNESS_AGENT = %d, want 0", code)
	}
	if *calls != 0 {
		t.Fatalf("coding agent must not run when HARNESS_AGENT is set, got %d calls", *calls)
	}
	if launch.calls != 1 || launch.agent != "pi" {
		t.Fatalf("expected pi launch, got %+v", launch)
	}
}

func TestCLISubcommandsAndAgentsStillWork(t *testing.T) {
	isolateHarnessEnv(t)
	calls, _ := stubCodingAgent(t)
	launch := stubLaunch(t)

	code, out := captureStdout(t, func() int { return run([]string{"--help"}) })
	if code != 0 || !strings.Contains(out, "Usage: harness") {
		t.Fatalf("--help = (%d, %q)", code, out)
	}
	if *calls != 0 {
		t.Fatalf("--help must not launch coding agent")
	}

	code, out = captureStdout(t, func() int { return run([]string{"version"}) })
	if code != 0 || !strings.HasPrefix(out, "harness ") {
		t.Fatalf("version = (%d, %q)", code, out)
	}

	if code := run([]string{"pi", "--hello"}); code != 0 {
		t.Fatalf("run(pi --hello) = %d, want 0", code)
	}
	if *calls != 0 || launch.calls != 1 || launch.agent != "pi" {
		t.Fatalf("expected pi launch, codingCalls=%d launch=%+v", *calls, launch)
	}
}
