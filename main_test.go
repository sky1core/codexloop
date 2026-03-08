package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestParseConfig_UsesPositionalPrompt(t *testing.T) {
	cfg, err := parseConfig([]string{"fix", "remaining", "bugs"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TaskPrompt != "fix remaining bugs" {
		t.Fatalf("unexpected prompt: %q", cfg.TaskPrompt)
	}
	if cfg.ResumeLast {
		t.Fatal("expected positional prompt to start a fresh session")
	}
	if cfg.Sandbox != "full-auto" {
		t.Fatalf("expected sandbox default full-auto, got %q", cfg.Sandbox)
	}
}

func TestParseConfig_ResumeSubcommandUsesSessionIDAndPrompt(t *testing.T) {
	cfg, err := parseConfig([]string{"resume", "session-123", "keep", "going"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SessionID != "session-123" {
		t.Fatalf("unexpected session id: %q", cfg.SessionID)
	}
	if cfg.TaskPrompt != "keep going" {
		t.Fatalf("unexpected prompt: %q", cfg.TaskPrompt)
	}
}

func TestParseConfig_ResumeSubcommandSupportsLast(t *testing.T) {
	cfg, err := parseConfig([]string{"resume", "--last"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ResumeLast {
		t.Fatal("expected --last to be preserved")
	}
}

func TestParseConfig_ResumeLastTreatsRemainingArgsAsPrompt(t *testing.T) {
	cfg, err := parseConfig([]string{"resume", "--last", "keep", "going"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ResumeLast {
		t.Fatal("expected --last to be preserved")
	}
	if cfg.SessionID != "" {
		t.Fatalf("expected no session id when --last is set, got %q", cfg.SessionID)
	}
	if cfg.TaskPrompt != "keep going" {
		t.Fatalf("unexpected prompt: %q", cfg.TaskPrompt)
	}
}

func TestParseConfig_AllowsCommonFlagsBeforeResumeSubcommand(t *testing.T) {
	cfg, err := parseConfig([]string{"-C", "/tmp/repo", "resume", "--last"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Workdir != "/tmp/repo" {
		t.Fatalf("unexpected workdir: %q", cfg.Workdir)
	}
	if !cfg.ResumeLast {
		t.Fatal("expected resume --last to be enabled")
	}
}

func TestParseConfig_SupportsModelAliasM(t *testing.T) {
	cfg, err := parseConfig([]string{"-m", "gpt-5.4", "fix bugs"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Model != "gpt-5.4" {
		t.Fatalf("unexpected model: %q", cfg.Model)
	}
	if cfg.TaskPrompt != "fix bugs" {
		t.Fatalf("unexpected prompt: %q", cfg.TaskPrompt)
	}
}

func TestParseConfig_SupportsImageAliasI(t *testing.T) {
	cfg, err := parseConfig([]string{"-i", "a.png", "--image", "b.png", "fix bugs"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := strings.Join(cfg.Images, ","), "a.png,b.png"; got != want {
		t.Fatalf("unexpected images: got %q want %q", got, want)
	}
}

func TestParseConfig_SupportsImagesOnResume(t *testing.T) {
	cfg, err := parseConfig([]string{"resume", "--last", "-i", "a.png", "--image", "b.png"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ResumeLast {
		t.Fatal("expected resume --last to be enabled")
	}
	if got, want := strings.Join(cfg.Images, ","), "a.png,b.png"; got != want {
		t.Fatalf("unexpected images: got %q want %q", got, want)
	}
}

func TestParseConfig_SupportsEphemeral(t *testing.T) {
	cfg, err := parseConfig([]string{"--ephemeral", "fix bugs"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Ephemeral {
		t.Fatal("expected ephemeral to be enabled")
	}
}

func TestParseConfig_SupportsEphemeralOnResume(t *testing.T) {
	cfg, err := parseConfig([]string{"resume", "--last", "--ephemeral"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ResumeLast {
		t.Fatal("expected resume --last to be enabled")
	}
	if !cfg.Ephemeral {
		t.Fatal("expected ephemeral to be enabled")
	}
}

func TestParseConfig_SupportsProfileAliasP(t *testing.T) {
	cfg, err := parseConfig([]string{"-p", "work", "fix bugs"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Profile != "work" {
		t.Fatalf("unexpected profile: %q", cfg.Profile)
	}
	if cfg.TaskPrompt != "fix bugs" {
		t.Fatalf("unexpected prompt: %q", cfg.TaskPrompt)
	}
}

func TestParseConfig_SupportsProfileOnResume(t *testing.T) {
	cfg, err := parseConfig([]string{"resume", "--last", "--profile", "work"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ResumeLast {
		t.Fatal("expected resume --last to be enabled")
	}
	if cfg.Profile != "work" {
		t.Fatalf("unexpected profile: %q", cfg.Profile)
	}
}

func TestParseConfig_SupportsResumeAll(t *testing.T) {
	cfg, err := parseConfig([]string{"resume", "--all"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ResumeAll {
		t.Fatal("expected resume --all to be enabled")
	}
	if !cfg.ResumeLast {
		t.Fatal("expected resume --all without session id to default to --last")
	}
}

func TestParseConfig_ResumeAllWithPositionalKeepsCodexSessionIDSemantics(t *testing.T) {
	cfg, err := parseConfig([]string{"resume", "--all", "thread-name"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ResumeAll {
		t.Fatal("expected resume --all to be enabled")
	}
	if cfg.ResumeLast {
		t.Fatal("expected positional after --all to be treated as session/thread id, not --last prompt")
	}
	if cfg.SessionID != "thread-name" {
		t.Fatalf("unexpected session id: %q", cfg.SessionID)
	}
}

func TestParseConfig_ResumeAllLastTreatsRemainingArgsAsPrompt(t *testing.T) {
	cfg, err := parseConfig([]string{"resume", "--all", "--last", "keep", "going"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ResumeAll || !cfg.ResumeLast {
		t.Fatalf("expected resume --all --last, got ResumeAll=%v ResumeLast=%v", cfg.ResumeAll, cfg.ResumeLast)
	}
	if cfg.SessionID != "" {
		t.Fatalf("expected no session id when --all and --last are set, got %q", cfg.SessionID)
	}
	if cfg.TaskPrompt != "keep going" {
		t.Fatalf("unexpected prompt: %q", cfg.TaskPrompt)
	}
}

func TestParseConfig_SupportsEnableDisableFlags(t *testing.T) {
	cfg, err := parseConfig([]string{"--enable", "alpha", "--enable", "beta", "--disable", "gamma", "fix bugs"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := strings.Join(cfg.EnabledFeatures, ","), "alpha,beta"; got != want {
		t.Fatalf("unexpected enabled features: got %q want %q", got, want)
	}
	if got, want := strings.Join(cfg.DisabledFeatures, ","), "gamma"; got != want {
		t.Fatalf("unexpected disabled features: got %q want %q", got, want)
	}
}

func TestParseConfig_SupportsEnableDisableFlagsOnResume(t *testing.T) {
	cfg, err := parseConfig([]string{"resume", "--last", "--enable", "alpha", "--disable", "gamma"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ResumeLast {
		t.Fatal("expected resume --last to be enabled")
	}
	if got, want := strings.Join(cfg.EnabledFeatures, ","), "alpha"; got != want {
		t.Fatalf("unexpected enabled features: got %q want %q", got, want)
	}
	if got, want := strings.Join(cfg.DisabledFeatures, ","), "gamma"; got != want {
		t.Fatalf("unexpected disabled features: got %q want %q", got, want)
	}
}

func TestParseConfig_SupportsRepeatedConfigOverrides(t *testing.T) {
	cfg, err := parseConfig([]string{
		"--config", `model_reasoning_effort="xhigh"`,
		"-c", `model="gpt-5.4"`,
		"fix bugs",
	}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{`model_reasoning_effort="xhigh"`, `model="gpt-5.4"`}
	if len(cfg.ConfigOverrides) != len(want) {
		t.Fatalf("unexpected config override count: got %d want %d (%v)", len(cfg.ConfigOverrides), len(want), cfg.ConfigOverrides)
	}
	for i, v := range want {
		if cfg.ConfigOverrides[i] != v {
			t.Fatalf("unexpected config override at %d: got %q want %q", i, cfg.ConfigOverrides[i], v)
		}
	}
}

func TestParseConfig_SupportsConfigOverridesOnResume(t *testing.T) {
	cfg, err := parseConfig([]string{"resume", "--last", "-c", `model_reasoning_effort="high"`}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ResumeLast {
		t.Fatal("expected resume --last to be enabled")
	}
	if len(cfg.ConfigOverrides) != 1 || cfg.ConfigOverrides[0] != `model_reasoning_effort="high"` {
		t.Fatalf("unexpected config overrides: %v", cfg.ConfigOverrides)
	}
}

func TestParseConfig_SupportsSandboxAliasSOnResume(t *testing.T) {
	cfg, err := parseConfig([]string{"resume", "-s", "none", "--last"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Sandbox != "none" {
		t.Fatalf("unexpected sandbox: %q", cfg.Sandbox)
	}
	if !cfg.ResumeLast {
		t.Fatal("expected resume --last to be enabled")
	}
}

func TestParseConfig_SupportsFullAutoShortcutOnRoot(t *testing.T) {
	cfg, err := parseConfig([]string{"--full-auto", "fix bugs"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Sandbox != "full-auto" {
		t.Fatalf("unexpected sandbox: %q", cfg.Sandbox)
	}
	if cfg.TaskPrompt != "fix bugs" {
		t.Fatalf("unexpected prompt: %q", cfg.TaskPrompt)
	}
}

func TestParseConfig_SupportsDangerousSandboxShortcutOnResume(t *testing.T) {
	cfg, err := parseConfig([]string{"resume", "--dangerously-bypass-approvals-and-sandbox", "--last"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Sandbox != "none" {
		t.Fatalf("unexpected sandbox: %q", cfg.Sandbox)
	}
	if !cfg.ResumeLast {
		t.Fatal("expected resume --last to be enabled")
	}
}

func TestParseConfig_RejectsContradictorySandboxSelection(t *testing.T) {
	_, err := parseConfig([]string{"--full-auto", "--sandbox", "none", "fix bugs"}, nil, nil)
	if err == nil {
		t.Fatal("expected contradictory sandbox selection error")
	}
	if !strings.Contains(err.Error(), "contradictory sandbox selection") {
		t.Fatalf("expected contradictory sandbox error, got: %v", err)
	}
	if !strings.Contains(err.Error(), `--full-auto selects "full-auto"`) {
		t.Fatalf("expected error to mention --full-auto selection, got: %v", err)
	}
	if !strings.Contains(err.Error(), `--sandbox selects "none"`) {
		t.Fatalf("expected error to mention --sandbox selection, got: %v", err)
	}
}

func TestParseConfig_SupportsCDAliasBeforeResumeSubcommand(t *testing.T) {
	cfg, err := parseConfig([]string{"--cd", "/tmp/repo", "resume", "--last"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Workdir != "/tmp/repo" {
		t.Fatalf("unexpected workdir: %q", cfg.Workdir)
	}
	if !cfg.ResumeLast {
		t.Fatal("expected resume --last to be enabled")
	}
}

func TestParseConfig_ReadsPromptFromPipedStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()

	if _, err := w.WriteString("review and finish\n"); err != nil {
		t.Fatalf("failed to write to pipe: %v", err)
	}
	w.Close()

	cfg, err := parseConfig(nil, r, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TaskPrompt != "review and finish" {
		t.Fatalf("unexpected prompt from stdin: %q", cfg.TaskPrompt)
	}
}

func TestParseConfig_RootHelpReturnsHelpRequest(t *testing.T) {
	_, err := parseConfig([]string{"--help"}, nil, nil)
	var help *helpRequest
	if !errors.As(err, &help) || help.resume {
		t.Fatalf("expected root help request, got: %v", err)
	}
}

func TestParseConfig_ResumeHelpReturnsHelpRequest(t *testing.T) {
	_, err := parseConfig([]string{"resume", "--help"}, nil, nil)
	var help *helpRequest
	if !errors.As(err, &help) || !help.resume {
		t.Fatalf("expected resume help request, got: %v", err)
	}
}

func TestParseConfig_ResumeWithoutArgsDefaultsToLast(t *testing.T) {
	cfg, err := parseConfig([]string{"resume"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ResumeLast {
		t.Fatal("expected 'resume' without args to default to --last")
	}
}

func TestParseConfig_VerifyFlag(t *testing.T) {
	cfg, err := parseConfig([]string{"-verify", "go test ./...", "fix bugs"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.VerifyCmd != "go test ./..." {
		t.Fatalf("unexpected verify cmd: %q", cfg.VerifyCmd)
	}
	if cfg.TaskPrompt != "fix bugs" {
		t.Fatalf("unexpected prompt: %q", cfg.TaskPrompt)
	}
}

func TestParseConfig_VerifyFlagWithResume(t *testing.T) {
	cfg, err := parseConfig([]string{"-verify", "npm test", "resume", "--last"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.VerifyCmd != "npm test" {
		t.Fatalf("unexpected verify cmd: %q", cfg.VerifyCmd)
	}
	if !cfg.ResumeLast {
		t.Fatal("expected resume --last")
	}
}

func TestParseConfig_ResumeWithSessionIDDoesNotForceLast(t *testing.T) {
	cfg, err := parseConfig([]string{"resume", "session-123"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ResumeLast {
		t.Fatal("expected resume with session ID to NOT set --last")
	}
	if cfg.SessionID != "session-123" {
		t.Fatalf("unexpected session id: %q", cfg.SessionID)
	}
}

func TestParseConfig_SkipGitRepoCheckFlag(t *testing.T) {
	cfg, err := parseConfig([]string{"-skip-git-repo-check", "fix bugs"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.SkipGitRepoCheck {
		t.Fatal("expected skip git repo check flag to be enabled")
	}
}

func TestParseConfig_LogFlags(t *testing.T) {
	cfg, err := parseConfig([]string{"-log-dir", "/tmp/codexloop-logs", "-no-log", "fix bugs"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LogDir != "/tmp/codexloop-logs" {
		t.Fatalf("unexpected log dir: %q", cfg.LogDir)
	}
	if !cfg.NoLog {
		t.Fatal("expected no-log to be enabled")
	}
}

func TestPrintRootUsage_ExplainsSandboxResumeLimit(t *testing.T) {
	var buf bytes.Buffer
	printRootUsage(&buf)
	usage := buf.String()
	if strings.Contains(usage, "auto-approve") {
		t.Fatalf("root usage should not describe full-auto as auto-approve: %s", usage)
	}
	if !strings.Contains(usage, `codexloop only supports`) || !strings.Contains(usage, `"full-auto" and "none" end-to-end`) {
		t.Fatalf("root usage should explain resume sandbox limit: %s", usage)
	}
	if !strings.Contains(usage, "(-a on-request, --sandbox workspace-write)") {
		t.Fatalf("root usage should describe full-auto using current codex help wording: %s", usage)
	}
}

func TestPrintResumeUsage_ExplainsSupportedSandboxModes(t *testing.T) {
	var buf bytes.Buffer
	printResumeUsage(&buf)
	usage := buf.String()
	if !strings.Contains(usage, `Only "full-auto" (default) and "none" are supported by codexloop`) {
		t.Fatalf("resume usage should explain allowed resume sandbox modes: %s", usage)
	}
	if !strings.Contains(usage, "codex exec resume --help") {
		t.Fatalf("resume usage should cite the underlying Codex CLI limitation: %s", usage)
	}
	if !strings.Contains(usage, "(-a on-request, --sandbox workspace-write)") {
		t.Fatalf("resume usage should describe full-auto using current codex help wording: %s", usage)
	}
}

func TestPrintRootUsage_DescribesSupportedSandboxModes(t *testing.T) {
	var buf bytes.Buffer
	printRootUsage(&buf)
	out := buf.String()
	for _, want := range []string{`"full-auto"`, `"none"`, "read-only/workspace-write/danger-full-access"} {
		if !strings.Contains(out, want) {
			t.Fatalf("root usage missing %q: %s", want, out)
		}
	}
	for _, unwanted := range []string{"auto-approve", `"read-only":`, `"workspace-write":`, `"danger-full-access":`} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("root usage should not contain %q: %s", unwanted, out)
		}
	}
	for _, want := range []string{
		"stdout: final control JSON only",
		"stderr/log: iteration start lines and a few short progress summaries only",
		"raw JSONL events and full long commands are not echoed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("root usage missing runtime output note %q: %s", want, out)
		}
	}
}

func TestPrintRootUsage_ShowsFlagAliases(t *testing.T) {
	var buf bytes.Buffer
	printRootUsage(&buf)
	out := buf.String()
	for _, want := range []string{
		"--codex-bin string",
		"-C string, --cd string",
		"--max-iters int",
		"--sandbox string, -s string",
		"--full-auto",
		"--dangerously-bypass-approvals-and-sandbox",
		"--model string, -m string",
		"--image file, -i file",
		"--ephemeral",
		"--profile string, -p string",
		"--enable feature",
		"--disable feature",
		"--config key=value, -c key=value",
		"--verify string",
		"--skip-git-repo-check",
		"--log-dir string",
		"--no-log",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("root usage missing alias %q: %s", want, out)
		}
	}
}

func TestPrintResumeUsage_RestrictsSandboxModes(t *testing.T) {
	var buf bytes.Buffer
	printResumeUsage(&buf)
	out := buf.String()
	for _, want := range []string{
		"codexloop first resumes the last session ID it recorded for this workdir",
		"falls back to Codex CLI --last",
		"most recently recorded session",
		"resumed from another directory",
		`use --all --last "PROMPT"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("resume usage missing recorded-session note %q: %s", want, out)
		}
	}
	if !strings.Contains(out, `Only "full-auto" (default) and "none" are supported by codexloop`) {
		t.Fatalf("resume usage missing sandbox restriction: %s", out)
	}
	for _, want := range []string{
		"stdout: final control JSON only",
		"stderr/log: iteration start lines and a few short progress summaries only",
		"raw JSONL events and full long commands are not echoed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("resume usage missing runtime output note %q: %s", want, out)
		}
	}
}

func TestPrintResumeUsage_ShowsFlagAliases(t *testing.T) {
	var buf bytes.Buffer
	printResumeUsage(&buf)
	out := buf.String()
	for _, want := range []string{
		"--last",
		"--all",
		"--codex-bin string",
		"-C string, --cd string",
		"--max-iters int",
		"--sandbox string, -s string",
		"--full-auto",
		"--dangerously-bypass-approvals-and-sandbox",
		"--model string, -m string",
		"--image file, -i file",
		"--ephemeral",
		"--profile string, -p string",
		"--enable feature",
		"--disable feature",
		"--config key=value, -c key=value",
		"--verify string",
		"--skip-git-repo-check",
		"--log-dir string",
		"--no-log",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("resume usage missing alias %q: %s", want, out)
		}
	}
}
