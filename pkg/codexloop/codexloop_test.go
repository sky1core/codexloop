package codexloop

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseControlMessage_StrictJSON(t *testing.T) {
	msg, err := ParseControlMessage(`{"status":"stop","summary":"done"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Status != "stop" || msg.Summary != "done" {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func TestParseControlMessage_ExtractsEmbeddedJSON(t *testing.T) {
	msg, err := ParseControlMessage("```json\n{\"status\":\"continue\",\"summary\":\"still working\"}\n```")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Status != "continue" || msg.Summary != "still working" {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func TestParseControlMessage_RejectsInvalidStatus(t *testing.T) {
	_, err := ParseControlMessage(`{"status":"maybe","summary":"?"}`)
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestParseCodexOutput_UsesAgentMessageEvenWithTrailingError(t *testing.T) {
	output := strings.Join([]string{
		`WARNING: proceeding, even though we could not update PATH`,
		`{"type":"thread.started","thread_id":"thread-1"}`,
		`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"{\"status\":\"stop\",\"summary\":\"done\"}"}}`,
		`{"type":"error","message":"Failed to shutdown rollout recorder"}`,
	}, "\n")

	result, err := ParseCodexOutput([]byte(output))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ThreadID != "thread-1" {
		t.Fatalf("unexpected thread id: %q", result.ThreadID)
	}
	if result.Control.Status != "stop" {
		t.Fatalf("unexpected control message: %+v", result.Control)
	}
}

func TestSummarizeProgressLine_TodoList(t *testing.T) {
	line := `{"type":"item.updated","item":{"type":"todo_list","items":[{"text":"inspect current code","status":"completed"},{"text":"add stderr progress summary","status":"in_progress"},{"text":"run go test ./...","status":"pending"}]}}`

	got := summarizeProgressLine(line)
	want := "todo 1/3: add stderr progress summary"
	if got != want {
		t.Fatalf("unexpected todo summary: got %q want %q", got, want)
	}
}

func TestSummarizeProgressLine_CommandExecutionTruncatesLongCommand(t *testing.T) {
	line := `{"type":"item.started","item":{"type":"command_execution","command":"go test ./... ./pkg/codexloop ./cmd/codexloop --run TestProgressSummary UNIQUE-LONG-SUFFIX-DO-NOT-LEAK","status":"running"}}`

	got := summarizeProgressLine(line)
	if !strings.HasPrefix(got, "cmd: go test ./...") {
		t.Fatalf("unexpected command summary prefix: %q", got)
	}
	if strings.Contains(got, "UNIQUE-LONG-SUFFIX-DO-NOT-LEAK") {
		t.Fatalf("summary should truncate long commands, got %q", got)
	}
}

func TestBuildPrompt_InitialAndResume(t *testing.T) {
	initial := BuildPrompt("fix the failing tests", 0, false)
	if !strings.Contains(initial, "Primary task:\nfix the failing tests") {
		t.Fatalf("initial prompt missing task: %q", initial)
	}
	if !strings.Contains(initial, "Work autonomously in the current repository.") {
		t.Fatalf("initial prompt should keep initial-turn autonomy framing: %q", initial)
	}
	if strings.Contains(initial, "Previous broad plans or todo lists are superseded for this turn.") {
		t.Fatalf("initial prompt should not include resume-only supersede guidance: %q", initial)
	}

	resume := BuildPrompt("ignored", 1, true)
	if !strings.Contains(resume, "Resume the existing Codex session from its current context.") {
		t.Fatalf("resume prompt missing resume-session instruction: %q", resume)
	}
	if !strings.Contains(resume, "Continue working on the original task. Do more work") {
		t.Fatalf("resume prompt missing continuation instruction: %q", resume)
	}
	if strings.Contains(resume, "Work autonomously in the current repository.") {
		t.Fatalf("resume prompt should not reuse the initial-turn autonomy framing: %q", resume)
	}
}

func TestBuildPrompt_RequiresVerification(t *testing.T) {
	prompt := BuildPrompt("do work", 0, false)
	checks := []string{
		"VERIFIED",
		"Run the project's test suite",
		"grep for TODO, BUG, FIXME",
		"return \"continue\"",
		"Return \"stop\" ONLY after all verification passes.",
		"If in doubt, return \"continue\".",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("prompt missing verification requirement %q", check)
		}
	}
}

func TestBuildPrompt_DoesNotGetStuckOnGitFailures(t *testing.T) {
	prompt := BuildPrompt("do work", 0, false)
	checks := []string{
		"not a Git repo",
		"git status/diff/log fails",
		"do not spend time on Git recovery",
		"reading files, running tests, and verifying the requirements directly",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("prompt missing git failure guidance %q", check)
		}
	}
}

func TestBuildPrompt_ResumeWithFollowUpInstruction(t *testing.T) {
	resume := BuildPrompt("keep going", 0, true)
	followUpHeader := "FOLLOW-UP INSTRUCTION FOR THIS TURN — HIGHEST PRIORITY:\nkeep going"
	if !strings.Contains(resume, followUpHeader) {
		t.Fatalf("resume prompt missing follow-up header: %q", resume)
	}
	if strings.Index(resume, followUpHeader) > strings.Index(resume, "Resume the existing Codex session from its current context.") {
		t.Fatalf("follow-up instruction should appear before the generic resume contract: %q", resume)
	}
	checks := []string{
		"Treat the follow-up instruction above as the only active plan for this turn. It overrides and replaces previous broad plans, repository-wide audits, and todo lists.",
		"The follow-up instruction for this turn is the highest-priority instruction and defines the full scope for this turn.",
		"The follow-up instruction supersedes and replaces any previous broad plans, repository-wide audits, or todo lists from earlier turns.",
		"Do NOT create a new broad todo list.",
		"Do NOT rediscover repository state.",
		"Do NOT run git status/diff/log.",
		"Do NOT run find . / find ...",
		"Do NOT scan AGENTS/README/repo-wide files unless the follow-up explicitly requires them.",
		"Keep the scope narrow: inspect only the specific files, commands, and tests needed for this follow-up and its verification.",
		"Run only the relevant tests/checks for this follow-up and confirm they pass.",
		"Check for remaining TODO, BUG, FIXME, HACK markers only when they are relevant to this follow-up.",
	}
	for _, check := range checks {
		if !strings.Contains(resume, check) {
			t.Fatalf("resume prompt missing follow-up guidance %q: %q", check, resume)
		}
	}

	disallowed := []string{
		"Work autonomously in the current repository.",
		"If the directory is not a Git repo, or git status/diff/log fails, do not spend time on Git recovery",
		"Work in small steps. If the task is large, do part of it and return \"continue\".",
		"Primary task:\nkeep going",
	}
	for _, text := range disallowed {
		if strings.Contains(resume, text) {
			t.Fatalf("resume prompt should avoid broad initial-turn guidance %q: %q", text, resume)
		}
	}
}

func TestControlSchema_IsValidShape(t *testing.T) {
	schema := string(controlSchema())
	if !strings.Contains(schema, `"enum": ["continue", "stop"]`) {
		t.Fatalf("schema missing stop/continue enum: %s", schema)
	}
	if !strings.Contains(schema, `"required": ["status", "summary"]`) {
		t.Fatalf("schema missing required fields: %s", schema)
	}
}

// --- BuildExecArgs tests ---

func TestBuildExecArgs_BasicStructure(t *testing.T) {
	cfg := Config{Workdir: "/repo"}
	args, err := BuildExecArgs(cfg, "do work", "/tmp/schema.json", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertArgsContainSequence(t, args, "exec", "--json")
	assertArgsContain(t, args, "--output-schema")
	assertArgsContain(t, args, "-o")
	assertArgPairValue(t, args, "--output-schema", "/tmp/schema.json")
	assertArgPairValue(t, args, "-o", "/tmp/out.txt")
	assertArgPairValue(t, args, "-C", "/repo")

	// Prompt must be the last argument
	if args[len(args)-1] != "do work" {
		t.Fatalf("prompt must be the last argument, got: %v", args)
	}
}

func TestBuildExecArgs_SandboxNone(t *testing.T) {
	cfg := Config{Workdir: "/repo", Sandbox: "none"}
	args, err := BuildExecArgs(cfg, "do work", "/tmp/schema.json", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertArgsContain(t, args, "--dangerously-bypass-approvals-and-sandbox")
	assertArgsNotContain(t, args, "--full-auto")
}

func TestBuildExecArgs_SandboxFullAuto(t *testing.T) {
	cfg := Config{Workdir: "/repo", Sandbox: "full-auto"}
	args, err := BuildExecArgs(cfg, "do work", "/tmp/schema.json", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertArgsContain(t, args, "--full-auto")
	assertArgsNotContain(t, args, "--dangerously-bypass-approvals-and-sandbox")
}

func TestBuildExecArgs_SandboxDefault(t *testing.T) {
	cfg := Config{Workdir: "/repo"}
	args, err := BuildExecArgs(cfg, "do work", "/tmp/schema.json", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertArgsContain(t, args, "--full-auto")
}

func TestBuildExecArgs_RejectsUnsupportedSandboxModes(t *testing.T) {
	modes := []string{"read-only", "workspace-write", "danger-full-access"}
	for _, mode := range modes {
		cfg := Config{Workdir: "/repo", Sandbox: mode}
		_, err := BuildExecArgs(cfg, "do work", "/tmp/schema.json", "/tmp/out.txt")
		if err == nil {
			t.Fatalf("mode %q: expected error", mode)
		}
		if !strings.Contains(err.Error(), mode) {
			t.Fatalf("mode %q: error should mention sandbox mode, got %v", mode, err)
		}
		if !strings.Contains(err.Error(), `only supports "full-auto" and "none"`) {
			t.Fatalf("mode %q: error should mention supported modes, got %v", mode, err)
		}
	}
}

func TestBuildExecArgs_SkipGitRepoCheck(t *testing.T) {
	cfg := Config{Workdir: "/repo", SkipGitRepoCheck: true}
	args, err := BuildExecArgs(cfg, "do work", "/tmp/schema.json", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertArgsContain(t, args, "--skip-git-repo-check")
}

func TestBuildExecArgs_Model(t *testing.T) {
	cfg := Config{Workdir: "/repo", Model: "gpt-5.4"}
	args, err := BuildExecArgs(cfg, "do work", "/tmp/schema.json", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertArgPairValue(t, args, "-m", "gpt-5.4")
}

func TestBuildExecArgs_Images(t *testing.T) {
	cfg := Config{Workdir: "/repo", Images: []string{"a.png", "b.png"}}
	args, err := BuildExecArgs(cfg, "do work", "/tmp/schema.json", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := strings.Join(collectFlagValues(args, "-i"), ","), "a.png,b.png"; got != want {
		t.Fatalf("unexpected images: got %q want %q (args=%v)", got, want, args)
	}
}

func TestBuildExecArgs_Ephemeral(t *testing.T) {
	cfg := Config{Workdir: "/repo", Ephemeral: true}
	args, err := BuildExecArgs(cfg, "do work", "/tmp/schema.json", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertArgsContain(t, args, "--ephemeral")
}

func TestBuildExecArgs_Profile(t *testing.T) {
	cfg := Config{Workdir: "/repo", Profile: "work"}
	args, err := BuildExecArgs(cfg, "do work", "/tmp/schema.json", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertArgPairValue(t, args, "-p", "work")
}

func TestBuildExecArgs_EnableDisableFeatures(t *testing.T) {
	cfg := Config{
		Workdir:          "/repo",
		EnabledFeatures:  []string{"alpha", "beta"},
		DisabledFeatures: []string{"gamma"},
	}
	args, err := BuildExecArgs(cfg, "do work", "/tmp/schema.json", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotEnable := collectFlagValues(args, "--enable")
	if got, want := strings.Join(gotEnable, ","), "alpha,beta"; got != want {
		t.Fatalf("unexpected enabled features: got %q want %q (args=%v)", got, want, args)
	}
	gotDisable := collectFlagValues(args, "--disable")
	if got, want := strings.Join(gotDisable, ","), "gamma"; got != want {
		t.Fatalf("unexpected disabled features: got %q want %q (args=%v)", got, want, args)
	}
}

func TestBuildExecArgs_ConfigOverrides(t *testing.T) {
	cfg := Config{
		Workdir:         "/repo",
		ConfigOverrides: []string{`model_reasoning_effort="xhigh"`, `model="gpt-5.4"`},
	}
	args, err := BuildExecArgs(cfg, "do work", "/tmp/schema.json", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-c" {
			got = append(got, args[i+1])
		}
	}
	want := []string{`model_reasoning_effort="xhigh"`, `model="gpt-5.4"`}
	if len(got) != len(want) {
		t.Fatalf("unexpected config override count: got %d want %d (%v)", len(got), len(want), got)
	}
	for i, v := range want {
		if got[i] != v {
			t.Fatalf("unexpected config override at %d: got %q want %q", i, got[i], v)
		}
	}
}

// --- BuildResumeArgs tests ---

func TestBuildResumeArgs_WithLast(t *testing.T) {
	cfg := Config{Workdir: "/repo"}
	args, err := BuildResumeArgs(cfg, "", true, "keep going", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertArgsContainSequence(t, args, "exec", "resume", "--json")
	assertArgsContain(t, args, "--last")
	assertArgsContain(t, args, "-o")
	assertArgPairValue(t, args, "-o", "/tmp/out.txt")

	if args[len(args)-1] != "keep going" {
		t.Fatalf("prompt must be the last argument, got: %v", args)
	}
}

func TestBuildResumeArgs_WithLastAll(t *testing.T) {
	cfg := Config{Workdir: "/repo", ResumeAll: true}
	args, err := BuildResumeArgs(cfg, "", true, "keep going", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertArgsContainSequence(t, args, "exec", "resume", "--json")
	assertArgsContain(t, args, "--all")
	assertArgsContain(t, args, "--last")
}

func TestBuildResumeArgs_WithSessionID(t *testing.T) {
	cfg := Config{Workdir: "/repo"}
	args, err := BuildResumeArgs(cfg, "session-abc", false, "continue", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertArgsContain(t, args, "session-abc")
	assertArgsNotContain(t, args, "--last")
}

func TestBuildResumeArgs_NeverContainsOutputSchema(t *testing.T) {
	cfg := Config{Workdir: "/repo"}
	args, err := BuildResumeArgs(cfg, "", true, "keep going", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertArgsNotContain(t, args, "--output-schema")
}

func TestBuildResumeArgs_NeverContainsWorkdirFlag(t *testing.T) {
	cfg := Config{Workdir: "/repo"}
	args, err := BuildResumeArgs(cfg, "", true, "keep going", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// resume does not support -C; workdir is set via cmd.Dir instead
	assertArgsNotContain(t, args, "-C")
}

func TestBuildResumeArgs_SandboxNone(t *testing.T) {
	cfg := Config{Workdir: "/repo", Sandbox: "none"}
	args, err := BuildResumeArgs(cfg, "", true, "keep going", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertArgsContain(t, args, "--dangerously-bypass-approvals-and-sandbox")
}

func TestBuildResumeArgs_SandboxFullAuto(t *testing.T) {
	cfg := Config{Workdir: "/repo", Sandbox: "full-auto"}
	args, err := BuildResumeArgs(cfg, "", true, "keep going", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertArgsContain(t, args, "--full-auto")
}

func TestBuildResumeArgs_NeverContainsSandboxFlag(t *testing.T) {
	// codex exec resume does NOT support --sandbox; only --full-auto and --dangerously-bypass are valid.
	modes := []string{"full-auto", "none", ""}
	for _, mode := range modes {
		cfg := Config{Workdir: "/repo", Sandbox: mode}
		args, err := BuildResumeArgs(cfg, "", true, "keep going", "/tmp/out.txt")
		if err != nil {
			t.Fatalf("mode %q: unexpected error: %v", mode, err)
		}
		assertArgsNotContain(t, args, "--sandbox")
	}
}

func TestBuildResumeArgs_RejectsSandboxModesThatResumeCannotPreserve(t *testing.T) {
	modes := []string{"read-only", "workspace-write", "danger-full-access"}
	for _, mode := range modes {
		cfg := Config{Workdir: "/repo", Sandbox: mode}
		_, err := BuildResumeArgs(cfg, "", true, "keep going", "/tmp/out.txt")
		if err == nil {
			t.Fatalf("mode %q: expected error", mode)
		}
		if !strings.Contains(err.Error(), mode) {
			t.Fatalf("mode %q: error should mention sandbox mode, got %v", mode, err)
		}
		if !strings.Contains(err.Error(), "codex exec resume") {
			t.Fatalf("mode %q: error should mention resume limitation, got %v", mode, err)
		}
		if !strings.Contains(err.Error(), `only supports "full-auto" and "none"`) {
			t.Fatalf("mode %q: error should mention supported modes, got %v", mode, err)
		}
	}
}

func TestBuildResumeArgs_SkipGitRepoCheck(t *testing.T) {
	cfg := Config{Workdir: "/repo", SkipGitRepoCheck: true}
	args, err := BuildResumeArgs(cfg, "", true, "keep going", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertArgsContain(t, args, "--skip-git-repo-check")
}

func TestBuildResumeArgs_Model(t *testing.T) {
	cfg := Config{Workdir: "/repo", Model: "gpt-5.4"}
	args, err := BuildResumeArgs(cfg, "", true, "keep going", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertArgPairValue(t, args, "-m", "gpt-5.4")
}

func TestBuildResumeArgs_Images(t *testing.T) {
	cfg := Config{Workdir: "/repo", Images: []string{"a.png", "b.png"}}
	args, err := BuildResumeArgs(cfg, "", true, "keep going", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := strings.Join(collectFlagValues(args, "-i"), ","), "a.png,b.png"; got != want {
		t.Fatalf("unexpected images: got %q want %q (args=%v)", got, want, args)
	}
}

func TestBuildResumeArgs_Ephemeral(t *testing.T) {
	cfg := Config{Workdir: "/repo", Ephemeral: true}
	args, err := BuildResumeArgs(cfg, "", true, "keep going", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertArgsContain(t, args, "--ephemeral")
}

func TestBuildResumeArgs_Profile(t *testing.T) {
	cfg := Config{Workdir: "/repo", Profile: "work"}
	args, err := BuildResumeArgs(cfg, "", true, "keep going", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertArgPairValue(t, args, "-p", "work")
}

func TestBuildResumeArgs_EnableDisableFeatures(t *testing.T) {
	cfg := Config{
		Workdir:          "/repo",
		EnabledFeatures:  []string{"alpha"},
		DisabledFeatures: []string{"gamma"},
	}
	args, err := BuildResumeArgs(cfg, "", true, "keep going", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotEnable := collectFlagValues(args, "--enable")
	if got, want := strings.Join(gotEnable, ","), "alpha"; got != want {
		t.Fatalf("unexpected enabled features: got %q want %q (args=%v)", got, want, args)
	}
	gotDisable := collectFlagValues(args, "--disable")
	if got, want := strings.Join(gotDisable, ","), "gamma"; got != want {
		t.Fatalf("unexpected disabled features: got %q want %q (args=%v)", got, want, args)
	}
}

func TestBuildResumeArgs_ConfigOverrides(t *testing.T) {
	cfg := Config{
		Workdir:         "/repo",
		ConfigOverrides: []string{`model_reasoning_effort="high"`, `model="gpt-5.4"`},
	}
	args, err := BuildResumeArgs(cfg, "", true, "keep going", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-c" {
			got = append(got, args[i+1])
		}
	}
	want := []string{`model_reasoning_effort="high"`, `model="gpt-5.4"`}
	if len(got) != len(want) {
		t.Fatalf("unexpected config override count: got %d want %d (%v)", len(got), len(want), got)
	}
	for i, v := range want {
		if got[i] != v {
			t.Fatalf("unexpected config override at %d: got %q want %q", i, got[i], v)
		}
	}
}

func TestBuildResumeArgs_FailsWithoutSessionOrLast(t *testing.T) {
	cfg := Config{Workdir: "/repo"}
	_, err := BuildResumeArgs(cfg, "", false, "keep going", "/tmp/out.txt")
	if err == nil {
		t.Fatal("expected error when neither session ID nor --last is given")
	}
}

// --- Codex CLI argument compatibility tests ---
// These verify that the constructed args match what the real Codex CLI expects.

func TestBuildExecArgs_MatchesCodexCLIInterface(t *testing.T) {
	cfg := Config{Workdir: "/repo", Sandbox: "none"}
	args, err := BuildExecArgs(cfg, "task prompt", "/tmp/schema.json", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// codex exec accepts: exec [OPTIONS] [PROMPT]
	// Required options we use: --json, -C, --output-schema, -o
	if args[0] != "exec" {
		t.Fatalf("first arg must be 'exec', got %q", args[0])
	}

	// Prompt must be last (positional)
	if args[len(args)-1] != "task prompt" {
		t.Fatalf("prompt must be last arg, got %q", args[len(args)-1])
	}

	// All flags we use must be valid codex exec flags
	validExecFlags := map[string]bool{
		"--json":                true,
		"--enable":              true,
		"--disable":             true,
		"-c":                    true,
		"-C":                    true,
		"--output-schema":       true,
		"-o":                    true,
		"-m":                    true,
		"-i":                    true,
		"--ephemeral":           true,
		"-p":                    true,
		"--skip-git-repo-check": true,
		"--full-auto":           true,
		"--sandbox":             true,
		"--dangerously-bypass-approvals-and-sandbox": true,
	}
	for i, arg := range args {
		if i == 0 || i == len(args)-1 {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if !validExecFlags[arg] {
				t.Fatalf("unexpected flag %q in exec args; not in known codex exec flags", arg)
			}
		}
	}
}

func TestBuildResumeArgs_MatchesCodexCLIInterface(t *testing.T) {
	cfg := Config{Workdir: "/repo", Sandbox: "none"}
	args, err := BuildResumeArgs(cfg, "sess-1", false, "task prompt", "/tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// codex exec resume accepts: exec resume [OPTIONS] [SESSION_ID] [PROMPT]
	if args[0] != "exec" || args[1] != "resume" {
		t.Fatalf("first two args must be 'exec resume', got %q %q", args[0], args[1])
	}

	// Prompt must be last (positional)
	if args[len(args)-1] != "task prompt" {
		t.Fatalf("prompt must be last arg, got %q", args[len(args)-1])
	}

	// All flags we use must be valid codex exec resume flags
	// Notably: --output-schema and -C are NOT valid for resume
	validResumeFlags := map[string]bool{
		"--json":                true,
		"--enable":              true,
		"--disable":             true,
		"-c":                    true,
		"--all":                 true,
		"--last":                true,
		"-o":                    true,
		"-m":                    true,
		"-i":                    true,
		"--ephemeral":           true,
		"-p":                    true,
		"--skip-git-repo-check": true,
		"--full-auto":           true,
		"--dangerously-bypass-approvals-and-sandbox": true,
	}
	invalidResumeFlags := map[string]bool{
		"--output-schema": true,
		"-C":              true,
		"--sandbox":       true, // codex exec resume does NOT support --sandbox
	}
	for i, arg := range args {
		if i <= 1 || i == len(args)-1 {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if invalidResumeFlags[arg] {
				t.Fatalf("flag %q must NOT be used with codex exec resume", arg)
			}
			if !validResumeFlags[arg] {
				t.Fatalf("unexpected flag %q in resume args; not in known codex exec resume flags", arg)
			}
		}
	}
}

// --- Runner / New tests ---

func TestNew_RequiresPromptOrResumeTarget(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Fatal("expected error when neither prompt nor resume target is provided")
	}
}

func TestNew_DoesNotForceResumeLastWhenPromptProvided(t *testing.T) {
	runner, err := New(Config{TaskPrompt: "fix bugs"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.cfg.ResumeLast {
		t.Fatal("expected ResumeLast to remain false when prompt is provided")
	}
}

func TestNew_RejectsUnsupportedSandboxModes(t *testing.T) {
	for _, mode := range []string{"read-only", "workspace-write", "danger-full-access", "bogus"} {
		_, err := New(Config{TaskPrompt: "fix bugs", Sandbox: mode})
		if err == nil {
			t.Fatalf("mode %q: expected error", mode)
		}
		if !strings.Contains(err.Error(), mode) {
			t.Fatalf("mode %q: error should mention sandbox mode, got %v", mode, err)
		}
	}
}

// --- Integration tests with fake codex script ---

func TestRun_PrintsOnlyFinalMessageToStdout(t *testing.T) {
	bin := writeFakeCodexScript(t, `#!/bin/sh
set -eu
output=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then
    output="$arg"
    prev=""
    continue
  fi
  if [ "$arg" = "-o" ]; then
    prev="-o"
  fi
done

if [ "${1:-}" = "exec" ] && [ "${2:-}" = "resume" ]; then
  msg='{"status":"stop","summary":"done"}'
  printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"stop\",\"summary\":\"done\"}"}}'
else
  msg='{"status":"continue","summary":"working"}'
  printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'
  printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"continue\",\"summary\":\"working\"}"}}'
fi

if [ -n "$output" ]; then
  printf '%s' "$msg" > "$output"
fi
`)

	runner, err := New(Config{
		CodexBin:   bin,
		TaskPrompt: "fix bugs",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	result, err := runner.Run(&stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}
	if result.Iterations != 2 {
		t.Fatalf("expected 2 iterations, got %d", result.Iterations)
	}
	got := strings.TrimSpace(stdout.String())
	if got != `{"status":"stop","summary":"done"}` {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), `"status":"continue"`) {
		t.Fatalf("stdout should not contain intermediate control messages: %q", stdout.String())
	}
	stderrText := stderr.String()
	if !strings.Contains(stderrText, "[codexloop] iter=1 start mode=exec") {
		t.Fatalf("stderr should contain exec start log: %s", stderrText)
	}
	if !strings.Contains(stderrText, "[codexloop] iter=2 start mode=resume") {
		t.Fatalf("stderr should contain resume start log: %s", stderrText)
	}
}

func TestRun_ProgressSummariesStayOnStderrAndAreLimited(t *testing.T) {
	bin := writeFakeCodexScript(t, `#!/bin/sh
set -eu
output=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then
    output="$arg"
    prev=""
    continue
  fi
  if [ "$arg" = "-o" ]; then
    prev="-o"
  fi
done

msg='{"status":"stop","summary":"done"}'
printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'
printf '%s\n' '{"type":"item.started","item":{"type":"todo_list","items":[{"text":"inspect current code","status":"completed"},{"text":"add stderr progress summary","status":"in_progress"},{"text":"run go test ./...","status":"pending"}]}}'
printf '%s\n' '{"type":"item.started","item":{"type":"command_execution","command":"go test ./... ./pkg/codexloop ./cmd/codexloop --run TestProgressSummary UNIQUE-LONG-SUFFIX-DO-NOT-LEAK","status":"running"}}'
printf '%s\n' '{"type":"item.updated","item":{"type":"command_execution","command":"go test ./... ./pkg/codexloop ./cmd/codexloop --run TestProgressSummary UNIQUE-LONG-SUFFIX-DO-NOT-LEAK","status":"running"}}'
printf '%s\n' '{"type":"item.updated","item":{"type":"todo_list","items":[{"text":"inspect current code","status":"completed"},{"text":"add stderr progress summary","status":"completed"},{"text":"run go test ./...","status":"in_progress"}]}}'
printf '%s\n' '{"type":"item.completed","item":{"type":"command_execution","command":"go test ./... ./pkg/codexloop ./cmd/codexloop --run TestProgressSummary UNIQUE-LONG-SUFFIX-DO-NOT-LEAK","exit_code":0}}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"stop\",\"summary\":\"done\"}"}}'
if [ -n "$output" ]; then
  printf '%s' "$msg" > "$output"
fi
`)

	runner, err := New(Config{
		CodexBin:   bin,
		TaskPrompt: "fix bugs",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	_, err = runner.Run(&stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}

	if strings.TrimSpace(stdout.String()) != `{"status":"stop","summary":"done"}` {
		t.Fatalf("stdout should only contain final control JSON, got %q", stdout.String())
	}

	stderrText := stderr.String()
	if !strings.Contains(stderrText, "[codexloop] iter=1 progress: todo 1/3: add stderr progress summary") {
		t.Fatalf("stderr missing todo progress summary: %s", stderrText)
	}
	if !strings.Contains(stderrText, "[codexloop] iter=1 progress: cmd: go test ./...") {
		t.Fatalf("stderr missing command progress summary: %s", stderrText)
	}
	if !strings.Contains(stderrText, "[codexloop] iter=1 progress: todo 2/3: run go test ./...") {
		t.Fatalf("stderr missing updated todo progress summary: %s", stderrText)
	}
	if got := countLinesWithPrefix(stderrText, "[codexloop] iter=1 progress: "); got != 3 {
		t.Fatalf("expected exactly 3 progress lines, got %d\nstderr:\n%s", got, stderrText)
	}
	if strings.Contains(stderrText, `{"type":"item.started"}`) || strings.Contains(stderrText, `{"type":"item.updated"}`) {
		t.Fatalf("stderr must not include raw JSON events: %s", stderrText)
	}
	if strings.Contains(stderrText, "UNIQUE-LONG-SUFFIX-DO-NOT-LEAK") {
		t.Fatalf("stderr must not include the full long command: %s", stderrText)
	}
}

func TestRun_FailsIfInitialExecHasNoThreadIDAndNeedsResume(t *testing.T) {
	bin := writeFakeCodexScript(t, `#!/bin/sh
set -eu
output=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then
    output="$arg"
    prev=""
    continue
  fi
  if [ "$arg" = "-o" ]; then
    prev="-o"
  fi
done

msg='{"status":"continue","summary":"working"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"continue\",\"summary\":\"working\"}"}}'
if [ -n "$output" ]; then
  printf '%s' "$msg" > "$output"
fi
`)

	runner, err := New(Config{
		CodexBin:   bin,
		TaskPrompt: "fix bugs",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = runner.Run(&bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected run error")
	}
	if !strings.Contains(err.Error(), "without a thread id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_PropagatesInitialExecFailureInsteadOfReplacingIt(t *testing.T) {
	bin := writeFakeCodexScript(t, `#!/bin/sh
set -eu
echo "Not inside a trusted directory and --skip-git-repo-check was not specified."
exit 1
`)

	runner, err := New(Config{
		CodexBin:   bin,
		TaskPrompt: "fix bugs",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = runner.Run(&bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected run error")
	}
	if !strings.Contains(err.Error(), "Not inside a trusted directory") {
		t.Fatalf("expected original exec failure to be preserved, got: %v", err)
	}
	if strings.Contains(err.Error(), "without a thread id") {
		t.Fatalf("expected original exec failure instead of thread id error, got: %v", err)
	}
}

// Verify fake codex script rejects --output-schema on resume
func TestRun_ResumeDoesNotPassOutputSchema(t *testing.T) {
	// This fake codex exits with error if it sees --output-schema,
	// simulating the real Codex CLI behavior on resume.
	bin := writeFakeCodexScript(t, `#!/bin/sh
set -eu
for arg in "$@"; do
  if [ "$arg" = "--output-schema" ]; then
    echo "error: unexpected argument '--output-schema' found" >&2
    exit 2
  fi
done

output=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then
    output="$arg"
    prev=""
    continue
  fi
  if [ "$arg" = "-o" ]; then
    prev="-o"
  fi
done

msg='{"status":"stop","summary":"done"}'
printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"stop\",\"summary\":\"done\"}"}}'
if [ -n "$output" ]; then
  printf '%s' "$msg" > "$output"
fi
`)

	runner, err := New(Config{
		CodexBin:   bin,
		ResumeLast: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	_, err = runner.Run(&stdout, &stderr)
	if err != nil {
		t.Fatalf("resume must not pass --output-schema but got error: %v", err)
	}
}

func TestRun_ExecStoresThreadIDForWorkdir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workdir := t.TempDir()

	bin := writeFakeCodexScript(t, `#!/bin/sh
set -eu
output=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then
    output="$arg"
    prev=""
    continue
  fi
  if [ "$arg" = "-o" ]; then
    prev="-o"
  fi
done

msg='{"status":"stop","summary":"done"}'
printf '%s\n' '{"type":"thread.started","thread_id":"thread-from-exec"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"stop\",\"summary\":\"done\"}"}}'
if [ -n "$output" ]; then
  printf '%s' "$msg" > "$output"
fi
`)

	runner, err := New(Config{
		CodexBin:   bin,
		Workdir:    workdir,
		TaskPrompt: "fix bugs",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := runner.Run(&bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}

	recordedSessionID, err := loadRecordedSessionID(workdir)
	if err != nil {
		t.Fatalf("failed to read recorded session id: %v", err)
	}
	if recordedSessionID != "thread-from-exec" {
		t.Fatalf("unexpected recorded session id: %q", recordedSessionID)
	}

	otherWorkdir := t.TempDir()
	otherRecordedSessionID, err := loadRecordedSessionID(otherWorkdir)
	if err != nil {
		t.Fatalf("failed to read other workdir session id: %v", err)
	}
	if otherRecordedSessionID != "" {
		t.Fatalf("other workdir should not reuse recorded session id, got %q", otherRecordedSessionID)
	}
}

func TestRun_ResumeLastUsesRecordedExactSessionIDForWorkdir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workdir := t.TempDir()
	otherWorkdir := t.TempDir()

	if err := saveRecordedSessionID(workdir, "stored-session-123"); err != nil {
		t.Fatalf("failed to seed recorded session id: %v", err)
	}
	if err := saveRecordedSessionID(otherWorkdir, "other-workdir-session"); err != nil {
		t.Fatalf("failed to seed other workdir session id: %v", err)
	}

	argsLog := filepath.Join(t.TempDir(), "resume-args.log")
	bin := writeFakeCodexScript(t, fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$@" > %q
output=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then
    output="$arg"
    prev=""
    continue
  fi
  if [ "$arg" = "-o" ]; then
    prev="-o"
  fi
done

msg='{"status":"stop","summary":"done"}'
printf '%%s\n' '{"type":"thread.started","thread_id":"resume-thread"}'
printf '%%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"stop\",\"summary\":\"done\"}"}}'
if [ -n "$output" ]; then
  printf '%%s' "$msg" > "$output"
fi
`, argsLog))

	runner, err := New(Config{
		CodexBin:   bin,
		Workdir:    workdir,
		ResumeLast: true,
		TaskPrompt: "keep going narrowly",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := runner.Run(&bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}

	loggedArgs, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("failed to read logged args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(loggedArgs)), "\n")
	assertArgsContainSequence(t, args, "exec", "resume", "--json")
	assertArgsContain(t, args, "stored-session-123")
	assertArgsNotContain(t, args, "--last")
	assertArgsNotContain(t, args, "other-workdir-session")

	recordedSessionID, err := loadRecordedSessionID(workdir)
	if err != nil {
		t.Fatalf("failed to reload recorded session id: %v", err)
	}
	if recordedSessionID != "stored-session-123" {
		t.Fatalf("recorded session id should remain the exact stored session, got %q", recordedSessionID)
	}
}

func TestRun_ResumeLastFallsBackToCLIWhenNoRecordedSessionIDExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workdir := t.TempDir()

	if err := saveRecordedSessionID(t.TempDir(), "other-workdir-session"); err != nil {
		t.Fatalf("failed to seed unrelated workdir session id: %v", err)
	}

	argsLog := filepath.Join(t.TempDir(), "resume-args.log")
	bin := writeFakeCodexScript(t, fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$@" > %q
output=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then
    output="$arg"
    prev=""
    continue
  fi
  if [ "$arg" = "-o" ]; then
    prev="-o"
  fi
done

msg='{"status":"stop","summary":"done"}'
printf '%%s\n' '{"type":"thread.started","thread_id":"resolved-from-last"}'
printf '%%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"stop\",\"summary\":\"done\"}"}}'
if [ -n "$output" ]; then
  printf '%%s' "$msg" > "$output"
fi
`, argsLog))

	runner, err := New(Config{
		CodexBin:   bin,
		Workdir:    workdir,
		ResumeLast: true,
		TaskPrompt: "keep going narrowly",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := runner.Run(&bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}

	loggedArgs, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("failed to read logged args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(loggedArgs)), "\n")
	assertArgsContainSequence(t, args, "exec", "resume", "--json")
	assertArgsContain(t, args, "--last")
	assertArgsNotContain(t, args, "other-workdir-session")

	recordedSessionID, err := loadRecordedSessionID(workdir)
	if err != nil {
		t.Fatalf("failed to read recorded session id after fallback resume: %v", err)
	}
	if recordedSessionID != "resolved-from-last" {
		t.Fatalf("fallback resume should record resolved exact session id, got %q", recordedSessionID)
	}
}

func TestRun_ResumeAllBypassesRecordedWorkdirSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workdir := t.TempDir()

	if err := saveRecordedSessionID(workdir, "stored-session-123"); err != nil {
		t.Fatalf("failed to seed recorded session id: %v", err)
	}

	argsLog := filepath.Join(t.TempDir(), "resume-args.log")
	bin := writeFakeCodexScript(t, fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$@" > %q
output=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then
    output="$arg"
    prev=""
    continue
  fi
  if [ "$arg" = "-o" ]; then
    prev="-o"
  fi
done

msg='{"status":"stop","summary":"done"}'
printf '%%s\n' '{"type":"thread.started","thread_id":"resolved-from-all"}'
printf '%%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"stop\",\"summary\":\"done\"}"}}'
if [ -n "$output" ]; then
  printf '%%s' "$msg" > "$output"
fi
`, argsLog))

	runner, err := New(Config{
		CodexBin:   bin,
		Workdir:    workdir,
		ResumeLast: true,
		ResumeAll:  true,
		TaskPrompt: "keep going narrowly",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := runner.Run(&bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}

	loggedArgs, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("failed to read logged args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(loggedArgs)), "\n")
	assertArgsContainSequence(t, args, "exec", "resume", "--json")
	assertArgsContain(t, args, "--all")
	assertArgsContain(t, args, "--last")
	assertArgsNotContain(t, args, "stored-session-123")

	recordedSessionID, err := loadRecordedSessionID(workdir)
	if err != nil {
		t.Fatalf("failed to read recorded session id after resume --all: %v", err)
	}
	if recordedSessionID != "resolved-from-all" {
		t.Fatalf("resume --all should record resolved exact session id, got %q", recordedSessionID)
	}
}

func TestRun_RejectsUnsupportedSandboxModeBeforeExec(t *testing.T) {
	invocations := filepath.Join(t.TempDir(), "invocations.log")
	bin := writeFakeCodexScript(t, fmt.Sprintf(`#!/bin/sh
set -eu
echo called >> %q
output=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then
    output="$arg"
    prev=""
    continue
  fi
  if [ "$arg" = "-o" ]; then
    prev="-o"
  fi
done

msg='{"status":"continue","summary":"working"}'
printf '%%s\n' '{"type":"thread.started","thread_id":"thread-1"}'
printf '%%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"continue\",\"summary\":\"working\"}"}}'
if [ -n "$output" ]; then
  printf '%%s' "$msg" > "$output"
fi
`, invocations))

	runner := &Runner{cfg: Config{
		CodexBin:   bin,
		Workdir:    t.TempDir(),
		TaskPrompt: "fix bugs",
		Sandbox:    "read-only",
		MaxIters:   2,
	}}

	_, err := runner.Run(&bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected unsupported sandbox mode error")
	}
	if !strings.Contains(err.Error(), `sandbox mode "read-only" is not supported by codexloop`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), `only supports "full-auto" and "none"`) {
		t.Fatalf("expected supported sandbox modes in error, got: %v", err)
	}

	logged, readErr := os.ReadFile(invocations)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return
		}
		t.Fatalf("failed to read invocation log: %v", readErr)
	}
	lines := strings.Split(strings.TrimSpace(string(logged)), "\n")
	if len(lines) != 0 && !(len(lines) == 1 && lines[0] == "") {
		t.Fatalf("expected codex binary to never be invoked, got log: %q", string(logged))
	}
}

func TestRun_RejectsUnsupportedSandboxModeBeforeResume(t *testing.T) {
	invocations := filepath.Join(t.TempDir(), "invocations.log")
	bin := writeFakeCodexScript(t, fmt.Sprintf(`#!/bin/sh
set -eu
echo called >> %q
exit 0
`, invocations))

	runner := &Runner{cfg: Config{
		CodexBin:   bin,
		Workdir:    t.TempDir(),
		ResumeLast: true,
		Sandbox:    "danger-full-access",
		MaxIters:   1,
	}}

	_, err := runner.Run(&bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected unsupported sandbox mode error")
	}
	if !strings.Contains(err.Error(), `sandbox mode "danger-full-access" is not supported by codexloop`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "codex exec resume") {
		t.Fatalf("expected resume limitation in error, got: %v", err)
	}

	logged, readErr := os.ReadFile(invocations)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return
		}
		t.Fatalf("failed to read invocation log: %v", readErr)
	}
	if strings.TrimSpace(string(logged)) != "" {
		t.Fatalf("expected resume command to be rejected before invocation, got log: %q", string(logged))
	}
}

func TestRun_MaxItersReached(t *testing.T) {
	bin := writeFakeCodexScript(t, `#!/bin/sh
set -eu
output=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then
    output="$arg"
    prev=""
    continue
  fi
  if [ "$arg" = "-o" ]; then
    prev="-o"
  fi
done

msg='{"status":"continue","summary":"still working"}'
printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"continue\",\"summary\":\"still working\"}"}}'
if [ -n "$output" ]; then
  printf '%s' "$msg" > "$output"
fi
`)

	runner, err := New(Config{
		CodexBin:   bin,
		TaskPrompt: "fix bugs",
		MaxIters:   3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var stderr bytes.Buffer
	result, err := runner.Run(&bytes.Buffer{}, &stderr)
	if err == nil {
		t.Fatal("expected error when max iterations reached")
	}
	if !strings.Contains(err.Error(), "max iterations") {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Iterations != 3 {
		t.Fatalf("expected 3 iterations, got %d", result.Iterations)
	}
}

func TestParseCodexOutput_PreservesThreadIDOnError(t *testing.T) {
	// thread.started is present but no item.completed — ThreadID should still be returned
	output := strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-99"}`,
		`some random noise`,
	}, "\n")

	result, err := ParseCodexOutput([]byte(output))
	if err == nil {
		t.Fatal("expected error for missing agent_message")
	}
	if result.ThreadID != "thread-99" {
		t.Fatalf("ThreadID should be preserved even on error, got: %q", result.ThreadID)
	}
}

// --- Verify tests ---

func TestBuildVerifyFailurePrompt(t *testing.T) {
	prompt := BuildVerifyFailurePrompt("go test ./...", "FAIL: TestFoo\nexit status 1")
	if !strings.Contains(prompt, "go test ./...") {
		t.Fatal("prompt should contain the verify command")
	}
	if !strings.Contains(prompt, "FAIL: TestFoo") {
		t.Fatal("prompt should contain the verify output")
	}
	if !strings.Contains(prompt, "verification command failed") {
		t.Fatal("prompt should explain what happened")
	}
}

func TestRun_VerifyPassesAndStops(t *testing.T) {
	bin := writeFakeCodexScript(t, `#!/bin/sh
set -eu
output=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then output="$arg"; prev=""; continue; fi
  if [ "$arg" = "-o" ]; then prev="-o"; fi
done
msg='{"status":"stop","summary":"done"}'
printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"stop\",\"summary\":\"done\"}"}}'
if [ -n "$output" ]; then printf '%s' "$msg" > "$output"; fi
`)

	runner, err := New(Config{
		CodexBin:   bin,
		TaskPrompt: "fix bugs",
		VerifyCmd:  "true",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	result, err := runner.Run(&stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Iterations != 1 {
		t.Fatalf("expected 1 iteration, got %d", result.Iterations)
	}
	if !strings.Contains(stderr.String(), "verify passed") {
		t.Fatalf("stderr should contain 'verify passed': %s", stderr.String())
	}
}

func TestRun_VerifyFailsForcesAnotherIteration(t *testing.T) {
	// Fake codex always returns "stop"
	bin := writeFakeCodexScript(t, `#!/bin/sh
set -eu
output=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then output="$arg"; prev=""; continue; fi
  if [ "$arg" = "-o" ]; then prev="-o"; fi
done
msg='{"status":"stop","summary":"done"}'
printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"stop\",\"summary\":\"done\"}"}}'
if [ -n "$output" ]; then printf '%s' "$msg" > "$output"; fi
`)

	// Verify script: fails first time (no sentinel), passes second time (sentinel exists)
	sentinelDir := t.TempDir()
	sentinelFile := filepath.Join(sentinelDir, "sentinel")
	verifyScript := fmt.Sprintf(`#!/bin/sh
if [ -f "%s" ]; then exit 0; fi
touch "%s"
echo "FAIL: tests not passing yet"
exit 1
`, sentinelFile, sentinelFile)

	verifyBin := filepath.Join(t.TempDir(), "verify.sh")
	if err := os.WriteFile(verifyBin, []byte(verifyScript), 0o700); err != nil {
		t.Fatal(err)
	}

	runner, err := New(Config{
		CodexBin:   bin,
		TaskPrompt: "fix bugs",
		VerifyCmd:  verifyBin,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	result, err := runner.Run(&stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Iterations != 2 {
		t.Fatalf("expected 2 iterations (verify fail then pass), got %d", result.Iterations)
	}
	if !strings.Contains(stderr.String(), "verify failed") {
		t.Fatalf("stderr should contain 'verify failed': %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "verify passed") {
		t.Fatalf("stderr should contain 'verify passed': %s", stderr.String())
	}
}

func TestRun_VerifyFailureOutputAppearsInPrompt(t *testing.T) {
	// Fake codex that logs the prompt it receives to a file
	promptLog := filepath.Join(t.TempDir(), "prompts.log")
	bin := writeFakeCodexScript(t, fmt.Sprintf(`#!/bin/sh
set -eu
output=""
prev=""
prompt=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then output="$arg"; prev=""; continue; fi
  if [ "$arg" = "-o" ]; then prev="-o"; continue; fi
  prompt="$arg"
done
echo "$prompt" >> "%s"

msg='{"status":"stop","summary":"done"}'
printf '%%s\n' '{"type":"thread.started","thread_id":"thread-1"}'
printf '%%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"stop\",\"summary\":\"done\"}"}}'
if [ -n "$output" ]; then printf '%%s' "$msg" > "$output"; fi
`, promptLog))

	sentinelDir := t.TempDir()
	sentinelFile := filepath.Join(sentinelDir, "sentinel")
	verifyCmd := fmt.Sprintf(`sh -c 'if [ -f "%s" ]; then exit 0; fi; touch "%s"; echo "ERROR: something broke"; exit 1'`, sentinelFile, sentinelFile)

	runner, err := New(Config{
		CodexBin:   bin,
		TaskPrompt: "fix bugs",
		VerifyCmd:  verifyCmd,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	_, err = runner.Run(&stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logged, err := os.ReadFile(promptLog)
	if err != nil {
		t.Fatalf("failed to read prompt log: %v", err)
	}
	logStr := string(logged)
	if !strings.Contains(logStr, "verification command failed") {
		t.Fatalf("second prompt should contain verify failure info, got: %s", logStr)
	}
	if !strings.Contains(logStr, "ERROR: something broke") {
		t.Fatalf("second prompt should contain verify output, got: %s", logStr)
	}
}

func TestRun_ReturnsElapsedTime(t *testing.T) {
	bin := writeFakeCodexScript(t, `#!/bin/sh
set -eu
output=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then output="$arg"; prev=""; continue; fi
  if [ "$arg" = "-o" ]; then prev="-o"; fi
done
msg='{"status":"stop","summary":"done"}'
printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"stop\",\"summary\":\"done\"}"}}'
if [ -n "$output" ]; then printf '%s' "$msg" > "$output"; fi
`)

	runner, err := New(Config{
		CodexBin:   bin,
		TaskPrompt: "fix bugs",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, err := runner.Run(&bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Elapsed <= 0 {
		t.Fatalf("expected positive elapsed time, got %v", result.Elapsed)
	}
}

func TestRun_StderrContainsDoneSummary(t *testing.T) {
	bin := writeFakeCodexScript(t, `#!/bin/sh
set -eu
output=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then output="$arg"; prev=""; continue; fi
  if [ "$arg" = "-o" ]; then prev="-o"; fi
done
msg='{"status":"stop","summary":"done"}'
printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"stop\",\"summary\":\"done\"}"}}'
if [ -n "$output" ]; then printf '%s' "$msg" > "$output"; fi
`)

	runner, err := New(Config{
		CodexBin:   bin,
		TaskPrompt: "fix bugs",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var stderr bytes.Buffer
	_, err = runner.Run(&bytes.Buffer{}, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr.String(), "done: iterations=1") {
		t.Fatalf("stderr should contain final summary, got: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "elapsed=") {
		t.Fatalf("stderr should contain elapsed time, got: %s", stderr.String())
	}
}

func TestRun_StderrContainsIterationTiming(t *testing.T) {
	bin := writeFakeCodexScript(t, `#!/bin/sh
set -eu
output=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then output="$arg"; prev=""; continue; fi
  if [ "$arg" = "-o" ]; then prev="-o"; fi
done
msg='{"status":"stop","summary":"done"}'
printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"stop\",\"summary\":\"done\"}"}}'
if [ -n "$output" ]; then printf '%s' "$msg" > "$output"; fi
`)

	runner, err := New(Config{
		CodexBin:   bin,
		TaskPrompt: "fix bugs",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var stderr bytes.Buffer
	_, err = runner.Run(&bytes.Buffer{}, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stderrText := stderr.String()
	if !strings.Contains(stderrText, "[codexloop] iter=1 start mode=exec") {
		t.Fatalf("stderr should contain start log, got: %s", stderrText)
	}
	// completion log line should contain elapsed=
	if !strings.Contains(stderrText, "iter=1") || !strings.Contains(stderrText, "elapsed=") {
		t.Fatalf("stderr iter log should contain elapsed, got: %s", stderrText)
	}
}

// --- Helpers ---

func writeFakeCodexScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-codex")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("failed to write fake codex script: %v", err)
	}
	return path
}

func assertArgsContain(t *testing.T, args []string, want string) {
	t.Helper()
	for _, a := range args {
		if a == want {
			return
		}
	}
	t.Fatalf("expected args to contain %q, got: %v", want, args)
}

func assertArgsNotContain(t *testing.T, args []string, unwanted string) {
	t.Helper()
	for _, a := range args {
		if a == unwanted {
			t.Fatalf("args must NOT contain %q, got: %v", unwanted, args)
		}
	}
}

func assertArgsContainSequence(t *testing.T, args []string, seq ...string) {
	t.Helper()
	if len(args) < len(seq) {
		t.Fatalf("args too short for sequence %v, got: %v", seq, args)
	}
	for i, s := range seq {
		if args[i] != s {
			t.Fatalf("expected args[%d] = %q, got %q (full args: %v)", i, s, args[i], args)
		}
	}
}

func assertArgPairValue(t *testing.T, args []string, flag, wantValue string) {
	t.Helper()
	for i, a := range args {
		if a == flag {
			if i+1 >= len(args) {
				t.Fatalf("flag %q has no value (at end of args)", flag)
			}
			if args[i+1] != wantValue {
				t.Fatalf("flag %q value = %q, want %q", flag, args[i+1], wantValue)
			}
			return
		}
	}
	t.Fatalf("flag %q not found in args: %v", flag, args)
}

func collectFlagValues(args []string, flag string) []string {
	var out []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			out = append(out, args[i+1])
		}
	}
	return out
}

func countLinesWithPrefix(text, prefix string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}
	return count
}
