package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sky1core/codexloop/pkg/codexloop"
)

func main() {
	cfg, err := parseConfig(os.Args[1:], os.Stdin, os.Stdin)
	if err != nil {
		var help *helpRequest
		if errors.As(err, &help) {
			if help.resume {
				printResumeUsage(os.Stdout)
			} else {
				printRootUsage(os.Stdout)
			}
			return
		}
		fatalf("%v", err)
	}

	runner, err := codexloop.New(cfg.Config)
	if err != nil {
		fatalf("%v", err)
	}

	var stderr io.Writer = os.Stderr
	if !cfg.NoLog {
		if mkErr := os.MkdirAll(cfg.LogDir, 0o755); mkErr == nil {
			logName := time.Now().Format("2006-01-02T15_04_05") + ".log"
			logPath := filepath.Join(cfg.LogDir, logName)
			if logFile, createErr := os.Create(logPath); createErr == nil {
				defer logFile.Close()
				stderr = io.MultiWriter(os.Stderr, logFile)
			} else {
				fmt.Fprintf(os.Stderr, "[codexloop] warning: could not create log file: %v\n", createErr)
			}
		} else {
			fmt.Fprintf(os.Stderr, "[codexloop] warning: could not create log directory: %v\n", mkErr)
		}
	}

	result, err := runner.Run(os.Stdout, stderr)
	if err != nil {
		fatalf("%v (iterations=%d, elapsed=%s)", err, result.Iterations, result.Elapsed.Round(time.Second))
	}
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "codexloop: "+format+"\n", args...)
	os.Exit(1)
}

type helpRequest struct {
	resume bool
}

type cliConfig struct {
	codexloop.Config
	LogDir           string
	NoLog            bool
	sandboxSelection *sandboxSelection
}

func (h *helpRequest) Error() string {
	return "help requested"
}

type sandboxSelection struct {
	mode   string
	source string
}

type sandboxFlagState struct {
	target    *string
	selection **sandboxSelection
}

func newSandboxFlagState(target *string, selection **sandboxSelection) *sandboxFlagState {
	return &sandboxFlagState{
		target:    target,
		selection: selection,
	}
}

func (s *sandboxFlagState) set(mode string, source string) error {
	if current := *s.selection; current != nil {
		if current.mode != mode {
			return fmt.Errorf(
				"contradictory sandbox selection: %s selects %q but %s selects %q; choose only one sandbox mode",
				current.source, current.mode, source, mode,
			)
		}
	} else {
		*s.selection = &sandboxSelection{
			mode:   mode,
			source: source,
		}
	}

	*s.target = mode
	return nil
}

type sandboxModeFlag struct {
	state  *sandboxFlagState
	source string
}

func (f *sandboxModeFlag) String() string {
	if f == nil || f.state == nil || f.state.target == nil {
		return ""
	}
	return *f.state.target
}

func (f *sandboxModeFlag) Set(value string) error {
	return f.state.set(value, f.source)
}

type sandboxShortcutFlag struct {
	state  *sandboxFlagState
	source string
	mode   string
}

func (f *sandboxShortcutFlag) String() string {
	if f == nil || f.state == nil || f.state.selection == nil || *f.state.selection == nil {
		return "false"
	}
	return strconv.FormatBool((**f.state.selection).mode == f.mode)
}

func (f *sandboxShortcutFlag) Set(value string) error {
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	return f.state.set(f.mode, f.source)
}

func (f *sandboxShortcutFlag) IsBoolFlag() bool {
	return true
}

type stringSliceFlag struct {
	target *[]string
}

func (f *stringSliceFlag) String() string {
	if f == nil || f.target == nil || len(*f.target) == 0 {
		return ""
	}
	return strings.Join(*f.target, ",")
}

func (f *stringSliceFlag) Set(value string) error {
	if f == nil || f.target == nil {
		return errors.New("string slice flag target is nil")
	}
	*f.target = append(*f.target, value)
	return nil
}

func parseConfig(args []string, stdin io.Reader, stdinFile *os.File) (cliConfig, error) {
	cfg := cliConfig{
		Config: codexloop.Config{
			CodexBin: "codex",
			MaxIters: 20,
			Sandbox:  "full-auto",
		},
		LogDir: filepath.Join(homeDir(), ".codexloop", "logs"),
	}

	rootFlags := flag.NewFlagSet("codexloop", flag.ContinueOnError)
	rootFlags.SetOutput(io.Discard)
	addCommonFlags(rootFlags, &cfg)
	if err := rootFlags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cliConfig{}, &helpRequest{}
		}
		return cliConfig{}, err
	}

	remaining := rootFlags.Args()
	if len(remaining) > 0 && remaining[0] == "resume" {
		resumeCfg := cfg
		resumeFlags := flag.NewFlagSet("codexloop resume", flag.ContinueOnError)
		resumeFlags.SetOutput(io.Discard)
		addCommonFlags(resumeFlags, &resumeCfg)
		resumeFlags.BoolVar(&resumeCfg.ResumeLast, "last", false, "Resume the last session codexloop recorded for this workdir, falling back to Codex CLI --last")
		resumeFlags.BoolVar(&resumeCfg.ResumeAll, "all", false, "Use Codex CLI --all semantics when resolving --last across all sessions")
		if err := resumeFlags.Parse(remaining[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return cliConfig{}, &helpRequest{resume: true}
			}
			return cliConfig{}, err
		}

		resumeArgs := resumeFlags.Args()
		if resumeCfg.ResumeLast {
			resumeCfg.TaskPrompt = strings.Join(resumeArgs, " ")
		} else if len(resumeArgs) > 0 {
			resumeCfg.SessionID = resumeArgs[0]
			if len(resumeArgs) > 1 {
				resumeCfg.TaskPrompt = strings.Join(resumeArgs[1:], " ")
			}
		}
		// Default to --last when no session ID is given
		if resumeCfg.SessionID == "" && !resumeCfg.ResumeLast {
			resumeCfg.ResumeLast = true
		}
		cfg = resumeCfg
	} else if len(remaining) > 0 {
		cfg.TaskPrompt = strings.Join(remaining, " ")
	}

	if cfg.TaskPrompt == "" {
		prompt, err := codexloop.ReadPromptFromStdin(stdin, stdinFile)
		if err != nil {
			return cliConfig{}, fmt.Errorf("failed to read prompt from stdin: %w", err)
		}
		cfg.TaskPrompt = prompt
	}

	return cfg, nil
}

func addCommonFlags(fs *flag.FlagSet, cfg *cliConfig) {
	sandboxFlags := newSandboxFlagState(&cfg.Sandbox, &cfg.sandboxSelection)

	fs.StringVar(&cfg.CodexBin, "codex-bin", cfg.CodexBin, "Codex CLI binary path")
	fs.StringVar(&cfg.Workdir, "C", cfg.Workdir, "Working directory passed to Codex")
	fs.StringVar(&cfg.Workdir, "cd", cfg.Workdir, "Working directory passed to Codex")
	fs.IntVar(&cfg.MaxIters, "max-iters", cfg.MaxIters, "Maximum number of loop iterations")
	fs.Var(&sandboxModeFlag{state: sandboxFlags, source: "--sandbox"}, "sandbox", `Sandbox mode: "full-auto" (default) or "none"; read-only/workspace-write/danger-full-access are rejected because codex exec resume cannot preserve them`)
	fs.Var(&sandboxModeFlag{state: sandboxFlags, source: "-s"}, "s", `Sandbox mode: "full-auto" (default) or "none"; read-only/workspace-write/danger-full-access are rejected because codex exec resume cannot preserve them`)
	fs.Var(&sandboxShortcutFlag{state: sandboxFlags, source: "--full-auto", mode: "full-auto"}, "full-auto", `Shortcut for --sandbox full-auto`)
	fs.Var(&sandboxShortcutFlag{state: sandboxFlags, source: "--dangerously-bypass-approvals-and-sandbox", mode: "none"}, "dangerously-bypass-approvals-and-sandbox", `Shortcut for --sandbox none (dangerous)`)
	fs.StringVar(&cfg.Model, "model", cfg.Model, "Model for Codex to use (e.g. gpt-5.4, gpt-5.3-codex, codex-mini)")
	fs.StringVar(&cfg.Model, "m", cfg.Model, "Model for Codex to use (e.g. gpt-5.4, gpt-5.3-codex, codex-mini)")
	fs.Var(&stringSliceFlag{target: &cfg.Images}, "image", `Image file to attach to the Codex prompt (repeatable, passed through as -i/--image)`)
	fs.Var(&stringSliceFlag{target: &cfg.Images}, "i", `Image file to attach to the Codex prompt (repeatable, passed through as -i/--image)`)
	fs.BoolVar(&cfg.Ephemeral, "ephemeral", cfg.Ephemeral, "Run Codex without persisting session files to disk")
	fs.StringVar(&cfg.Profile, "profile", cfg.Profile, "Configuration profile from Codex config.toml to use")
	fs.StringVar(&cfg.Profile, "p", cfg.Profile, "Configuration profile from Codex config.toml to use")
	fs.Var(&stringSliceFlag{target: &cfg.EnabledFeatures}, "enable", `Enable a Codex feature flag (repeatable, passed through as --enable)`)
	fs.Var(&stringSliceFlag{target: &cfg.DisabledFeatures}, "disable", `Disable a Codex feature flag (repeatable, passed through as --disable)`)
	fs.Var(&stringSliceFlag{target: &cfg.ConfigOverrides}, "config", `Codex config override key=value (repeatable, passed through as -c/--config)`)
	fs.Var(&stringSliceFlag{target: &cfg.ConfigOverrides}, "c", `Codex config override key=value (repeatable, passed through as -c/--config)`)
	fs.StringVar(&cfg.VerifyCmd, "verify", cfg.VerifyCmd, "Shell command to run when Codex reports stop; non-zero exit forces another iteration")
	fs.BoolVar(&cfg.SkipGitRepoCheck, "skip-git-repo-check", cfg.SkipGitRepoCheck, "Pass --skip-git-repo-check to Codex CLI for repositories that Codex does not trust yet")
	fs.StringVar(&cfg.LogDir, "log-dir", cfg.LogDir, "Directory for codexloop stderr logs")
	fs.BoolVar(&cfg.NoLog, "no-log", cfg.NoLog, "Disable codexloop log file creation")
}

func printRootUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: codexloop [OPTIONS] [PROMPT]")
	fmt.Fprintln(w, "       codexloop [OPTIONS] resume [OPTIONS] [SESSION_ID] [PROMPT]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, `  codexloop "Fix remaining bugs"`)
	fmt.Fprintln(w, "  codexloop resume --last")
	fmt.Fprintln(w, "  codexloop resume 019cc69d-2e58-75a2-a786-a557e0e77be4")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Root options:")
	fmt.Fprintln(w, "  --codex-bin string")
	fmt.Fprintln(w, "        Codex CLI binary path")
	fmt.Fprintln(w, "  -C string, --cd string")
	fmt.Fprintln(w, "        Working directory passed to Codex")
	fmt.Fprintln(w, "  --max-iters int")
	fmt.Fprintln(w, "        Maximum number of loop iterations (default 20)")
	fmt.Fprintln(w, "  --sandbox string, -s string")
	fmt.Fprintln(w, `        Sandbox mode (default "full-auto")`)
	fmt.Fprintln(w, `        "full-auto":          pass Codex CLI --full-auto`)
	fmt.Fprintln(w, `                              local codex help describes this as low-friction sandboxed automatic`)
	fmt.Fprintln(w, `                              execution (-a on-request, --sandbox workspace-write)`)
	fmt.Fprintln(w, `        "none":               pass --dangerously-bypass-approvals-and-sandbox (dangerous)`)
	fmt.Fprintln(w, "  --full-auto")
	fmt.Fprintln(w, `        Shortcut for --sandbox full-auto`)
	fmt.Fprintln(w, "  --dangerously-bypass-approvals-and-sandbox")
	fmt.Fprintln(w, `        Shortcut for --sandbox none (dangerous)`)
	fmt.Fprintln(w, `        local codex exec resume --help does not expose --sandbox, so codexloop only supports`)
	fmt.Fprintln(w, `        "full-auto" and "none" end-to-end. read-only/workspace-write/danger-full-access`)
	fmt.Fprintln(w, `        fail explicitly instead of widening privileges.`)
	fmt.Fprintln(w, `        Contradictory sandbox flags fail fast with a parse error.`)
	fmt.Fprintln(w, "  --model string, -m string")
	fmt.Fprintln(w, "        Model for Codex to use (e.g. gpt-5.4, gpt-5.3-codex, codex-mini)")
	fmt.Fprintln(w, "  --image file, -i file")
	fmt.Fprintln(w, "        Attach image file(s) to the Codex prompt (repeatable)")
	fmt.Fprintln(w, "  --ephemeral")
	fmt.Fprintln(w, "        Run Codex without persisting session files to disk")
	fmt.Fprintln(w, "  --profile string, -p string")
	fmt.Fprintln(w, "        Codex config profile to use from config.toml")
	fmt.Fprintln(w, "  --enable feature")
	fmt.Fprintln(w, "        Enable a Codex feature flag (repeatable)")
	fmt.Fprintln(w, "  --disable feature")
	fmt.Fprintln(w, "        Disable a Codex feature flag (repeatable)")
	fmt.Fprintln(w, "  --config key=value, -c key=value")
	fmt.Fprintln(w, `        Pass through Codex config overrides (repeatable), e.g. -c 'model_reasoning_effort="xhigh"'`)
	fmt.Fprintln(w, "  --verify string")
	fmt.Fprintln(w, `        Shell command to verify completion (e.g. "go test ./...")`)
	fmt.Fprintln(w, "        When Codex reports stop, this command runs; non-zero exit forces another iteration")
	fmt.Fprintln(w, "  --skip-git-repo-check")
	fmt.Fprintln(w, "        Pass --skip-git-repo-check to Codex CLI")
	fmt.Fprintln(w, "  --log-dir string")
	fmt.Fprintln(w, "        Directory for codexloop stderr logs")
	fmt.Fprintln(w, "  --no-log")
	fmt.Fprintln(w, "        Disable codexloop log file creation")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Runtime output:")
	fmt.Fprintln(w, "  stdout: final control JSON only")
	fmt.Fprintln(w, "  stderr/log: iteration start lines and a few short progress summaries only")
	fmt.Fprintln(w, "  raw JSONL events and full long commands are not echoed")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "By default logs are written to ~/.codexloop/logs/ on every run.")
}

func printResumeUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: codexloop resume [OPTIONS] [SESSION_ID] [PROMPT]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "If no SESSION_ID is given, codexloop first resumes the last session ID it recorded for this workdir.")
	fmt.Fprintln(w, "If no recorded session exists for this workdir, it falls back to Codex CLI --last.")
	fmt.Fprintln(w, "Raw Codex CLI --last is only a fallback: it picks Codex's most recently recorded session,")
	fmt.Fprintln(w, "and that record can shift if a session was resumed from another directory.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Resume options:")
	fmt.Fprintln(w, "  --last")
	fmt.Fprintln(w, "        Resume the last session codexloop recorded for this workdir, or fall back to Codex CLI --last")
	fmt.Fprintln(w, "  --all")
	fmt.Fprintln(w, "        Bypass codexloop's workdir-local recorded session and use raw Codex CLI --last --all")
	fmt.Fprintln(w, "        Like raw Codex CLI, --all does not change positional parsing;")
	fmt.Fprintln(w, `        use --all --last "PROMPT" to send a follow-up prompt to the newest cross-directory session.`)
	fmt.Fprintln(w, "  --codex-bin string")
	fmt.Fprintln(w, "        Codex CLI binary path")
	fmt.Fprintln(w, "  -C string, --cd string")
	fmt.Fprintln(w, "        Working directory passed to Codex")
	fmt.Fprintln(w, "  --max-iters int")
	fmt.Fprintln(w, "        Maximum number of loop iterations (default 20)")
	fmt.Fprintln(w, "  --sandbox string, -s string")
	fmt.Fprintln(w, `        Only "full-auto" (default) and "none" are supported by codexloop.`)
	fmt.Fprintln(w, `        local codex exec resume --help does not expose --sandbox, so read-only/workspace-write/danger-full-access fail explicitly.`)
	fmt.Fprintln(w, `        "full-auto" follows Codex CLI help: low-friction sandboxed automatic execution`)
	fmt.Fprintln(w, `        (-a on-request, --sandbox workspace-write).`)
	fmt.Fprintln(w, "  --full-auto")
	fmt.Fprintln(w, `        Shortcut for --sandbox full-auto`)
	fmt.Fprintln(w, "  --dangerously-bypass-approvals-and-sandbox")
	fmt.Fprintln(w, `        Shortcut for --sandbox none (dangerous)`)
	fmt.Fprintln(w, `        Contradictory sandbox flags fail fast with a parse error.`)
	fmt.Fprintln(w, "  --model string, -m string")
	fmt.Fprintln(w, "        Model for Codex to use (e.g. gpt-5.4, gpt-5.3-codex, codex-mini)")
	fmt.Fprintln(w, "  --image file, -i file")
	fmt.Fprintln(w, "        Attach image file(s) to the resumed Codex prompt (repeatable)")
	fmt.Fprintln(w, "  --ephemeral")
	fmt.Fprintln(w, "        Run Codex without persisting session files to disk")
	fmt.Fprintln(w, "  --profile string, -p string")
	fmt.Fprintln(w, "        Codex config profile to use from config.toml")
	fmt.Fprintln(w, "  --enable feature")
	fmt.Fprintln(w, "        Enable a Codex feature flag (repeatable)")
	fmt.Fprintln(w, "  --disable feature")
	fmt.Fprintln(w, "        Disable a Codex feature flag (repeatable)")
	fmt.Fprintln(w, "  --config key=value, -c key=value")
	fmt.Fprintln(w, `        Pass through Codex config overrides (repeatable), e.g. -c 'model_reasoning_effort="xhigh"'`)
	fmt.Fprintln(w, "  --verify string")
	fmt.Fprintln(w, `        Shell command to verify completion (e.g. "go test ./...")`)
	fmt.Fprintln(w, "        When Codex reports stop, this command runs; non-zero exit forces another iteration")
	fmt.Fprintln(w, "  --skip-git-repo-check")
	fmt.Fprintln(w, "        Pass --skip-git-repo-check to Codex CLI")
	fmt.Fprintln(w, "  --log-dir string")
	fmt.Fprintln(w, "        Directory for codexloop stderr logs")
	fmt.Fprintln(w, "  --no-log")
	fmt.Fprintln(w, "        Disable codexloop log file creation")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Runtime output:")
	fmt.Fprintln(w, "  stdout: final control JSON only")
	fmt.Fprintln(w, "  stderr/log: iteration start lines and a few short progress summaries only")
	fmt.Fprintln(w, "  raw JSONL events and full long commands are not echoed")
}
