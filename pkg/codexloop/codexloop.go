package codexloop

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type ControlMessage struct {
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

func (m ControlMessage) Validate() error {
	switch m.Status {
	case "continue", "stop":
	default:
		return fmt.Errorf("invalid status %q", m.Status)
	}
	if strings.TrimSpace(m.Summary) == "" {
		return errors.New("summary must not be empty")
	}
	return nil
}

type Event struct {
	Type     string         `json:"type"`
	ThreadID string         `json:"thread_id"`
	Message  string         `json:"message"`
	Item     map[string]any `json:"item"`
}

type CommandResult struct {
	ThreadID     string
	RawOutput    string
	AgentText    string
	Control      ControlMessage
	CommandError error
}

type Config struct {
	CodexBin   string
	Workdir    string
	MaxIters   int
	SessionID  string
	ResumeLast bool
	ResumeAll  bool
	TaskPrompt string

	Sandbox          string // "full-auto" (default) or "none"; other Codex sandbox modes are rejected because exec resume cannot preserve them end-to-end
	Model            string // model to pass to Codex CLI (-m flag); empty means use Codex default
	Images           []string
	Ephemeral        bool
	Profile          string // configuration profile to pass to Codex CLI (-p flag); empty means use Codex defaults
	EnabledFeatures  []string
	DisabledFeatures []string
	ConfigOverrides  []string
	VerifyCmd        string // shell command to run when Codex returns "stop"; non-zero exit forces another iteration
	SkipGitRepoCheck bool   // pass --skip-git-repo-check to Codex CLI
}

type LoopResult struct {
	Iterations int
	Last       CommandResult
	Elapsed    time.Duration
}

type Runner struct {
	cfg Config
}

const (
	maxProgressMessagesPerIteration = 3
	lastSessionStoreRelativePath    = ".codexloop/last-sessions.json"
)

type lastSessionStore struct {
	Workdirs map[string]string `json:"workdirs"`
}

func New(cfg Config) (*Runner, error) {
	if cfg.CodexBin == "" {
		cfg.CodexBin = "codex"
	}
	if cfg.Workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
		cfg.Workdir = wd
	}
	if cfg.MaxIters <= 0 {
		cfg.MaxIters = 20
	}
	if err := validateSandboxMode(cfg.Sandbox); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.SessionID) == "" && strings.TrimSpace(cfg.TaskPrompt) == "" && !cfg.ResumeLast {
		return nil, errors.New("either a prompt/stdin input or resume target (--last or SESSION_ID) is required")
	}
	return &Runner{cfg: cfg}, nil
}

func ReadPromptFromStdin(r io.Reader, stdin *os.File) (string, error) {
	if stdin == nil {
		return "", nil
	}
	info, err := stdin.Stat()
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", nil
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func codexloopHomeDir() string {
	if h := strings.TrimSpace(os.Getenv("HOME")); h != "" {
		return h
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

func lastSessionStorePath() string {
	return filepath.Join(codexloopHomeDir(), lastSessionStoreRelativePath)
}

func normalizeWorkdirKey(workdir string) string {
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		workdir = "."
	}
	if abs, err := filepath.Abs(workdir); err == nil {
		workdir = abs
	}
	return filepath.Clean(workdir)
}

func loadLastSessionStore() (lastSessionStore, error) {
	store := lastSessionStore{Workdirs: map[string]string{}}

	data, err := os.ReadFile(lastSessionStorePath())
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return store, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return store, nil
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return lastSessionStore{}, fmt.Errorf("failed to parse last session store %s: %w", lastSessionStorePath(), err)
	}
	if store.Workdirs == nil {
		store.Workdirs = map[string]string{}
	}
	return store, nil
}

func loadRecordedSessionID(workdir string) (string, error) {
	store, err := loadLastSessionStore()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(store.Workdirs[normalizeWorkdirKey(workdir)]), nil
}

func saveRecordedSessionID(workdir, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}

	store, err := loadLastSessionStore()
	if err != nil {
		return err
	}
	store.Workdirs[normalizeWorkdirKey(workdir)] = sessionID

	storePath := lastSessionStorePath()
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		return fmt.Errorf("failed to create last session store directory: %w", err)
	}

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode last session store: %w", err)
	}
	data = append(data, '\n')

	tmpFile, err := os.CreateTemp(filepath.Dir(storePath), "last-sessions-*.json")
	if err != nil {
		return fmt.Errorf("failed to create temp last session store: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write temp last session store: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp last session store: %w", err)
	}
	if err := os.Rename(tmpPath, storePath); err != nil {
		return fmt.Errorf("failed to replace last session store: %w", err)
	}
	return nil
}

func (r *Runner) Run(stdout, stderr io.Writer) (LoopResult, error) {
	startTime := time.Now()
	cfg := r.cfg
	resumeLast := cfg.ResumeLast
	var last CommandResult
	var verifyFailurePrompt string

	if resumeLast && !cfg.ResumeAll && strings.TrimSpace(cfg.SessionID) == "" {
		recordedSessionID, err := loadRecordedSessionID(cfg.Workdir)
		if err != nil {
			if stderr != nil {
				fmt.Fprintf(stderr, "[codexloop] warning: could not read last-session store: %v\n", err)
			}
		} else if recordedSessionID != "" {
			cfg.SessionID = recordedSessionID
			resumeLast = false
			if stderr != nil {
				fmt.Fprintf(stderr, "[codexloop] resume: using recorded session id for workdir %s\n", normalizeWorkdirKey(cfg.Workdir))
			}
		}
	}

	for i := 0; i < cfg.MaxIters; i++ {
		iterStart := time.Now()
		startingFresh := i == 0 && strings.TrimSpace(cfg.SessionID) == "" && !resumeLast
		iterMode := "resume"
		if startingFresh {
			iterMode = "exec"
		}
		if stderr != nil {
			fmt.Fprintf(stderr, "[codexloop] iter=%d start mode=%s\n", i+1, iterMode)
		}

		prompt := BuildPrompt(cfg.TaskPrompt, i, !startingFresh)
		if verifyFailurePrompt != "" {
			prompt += "\n\n" + verifyFailurePrompt
			verifyFailurePrompt = ""
		}

		var (
			result CommandResult
			err    error
		)
		if startingFresh {
			result, err = r.runExec(prompt, stderr, i+1)
			if err != nil {
				return LoopResult{}, err
			}
			if result.ThreadID != "" {
				cfg.SessionID = result.ThreadID
				resumeLast = false
			} else if result.Control.Status != "stop" {
				return LoopResult{}, fmt.Errorf("codex exec completed without a thread id; output did not include a thread.started event (status=%s summary=%q)", result.Control.Status, result.Control.Summary)
			}
		} else {
			result, err = r.runResume(cfg.SessionID, resumeLast, prompt, stderr, i+1)
			if err != nil {
				return LoopResult{}, err
			}
			if result.ThreadID != "" && cfg.SessionID == "" {
				cfg.SessionID = result.ThreadID
				resumeLast = false
			}
		}
		if cfg.SessionID != "" {
			if err := saveRecordedSessionID(cfg.Workdir, cfg.SessionID); err != nil && stderr != nil {
				fmt.Fprintf(stderr, "[codexloop] warning: could not record last session id: %v\n", err)
			}
		}

		last = result
		iterElapsed := time.Since(iterStart)
		if result.CommandError != nil && stderr != nil {
			fmt.Fprintf(stderr, "[codexloop] warning: codex exited with error: %v (continuing because valid output was found)\n", result.CommandError)
		}
		if stderr != nil {
			fmt.Fprintf(stderr, "[codexloop] iter=%d status=%s elapsed=%s summary=%s\n", i+1, result.Control.Status, iterElapsed.Round(time.Second), result.Control.Summary)
		}

		if result.Control.Status == "stop" {
			if cfg.VerifyCmd != "" {
				if stderr != nil {
					fmt.Fprintf(stderr, "[codexloop] running verify: %s\n", cfg.VerifyCmd)
				}
				vOut, vErr := r.runVerify()
				if vErr != nil {
					if stderr != nil {
						fmt.Fprintf(stderr, "[codexloop] verify failed (exit: %v), forcing continue\n", vErr)
					}
					if cfg.SessionID == "" && !resumeLast {
						elapsed := time.Since(startTime)
						return LoopResult{Iterations: i + 1, Last: result, Elapsed: elapsed},
							fmt.Errorf("verify failed but no session available to resume: %v", vErr)
					}
					verifyFailurePrompt = BuildVerifyFailurePrompt(cfg.VerifyCmd, vOut)
					continue
				}
				if stderr != nil {
					fmt.Fprintf(stderr, "[codexloop] verify passed\n")
				}
			}

			elapsed := time.Since(startTime)
			if stderr != nil {
				fmt.Fprintf(stderr, "[codexloop] done: iterations=%d elapsed=%s\n", i+1, elapsed.Round(time.Second))
			}
			if stdout != nil {
				out, _ := json.Marshal(result.Control)
				fmt.Fprintln(stdout, string(out))
			}
			return LoopResult{Iterations: i + 1, Last: result, Elapsed: elapsed}, nil
		}
	}

	elapsed := time.Since(startTime)
	if stderr != nil {
		fmt.Fprintf(stderr, "[codexloop] stopped: max iterations reached (%d) elapsed=%s\n", cfg.MaxIters, elapsed.Round(time.Second))
	}
	return LoopResult{Iterations: cfg.MaxIters, Last: last, Elapsed: elapsed}, fmt.Errorf("reached max iterations (%d) without receiving stop", cfg.MaxIters)
}

func BuildPrompt(taskPrompt string, iteration int, continuing bool) string {
	initialInstructions := `Work autonomously in the current repository.

CRITICAL RULES — you MUST follow these:

1. Do NOT return "stop" until you have VERIFIED your work:
   - Run the project's test suite (e.g. go test ./..., npm test, pytest, etc.) and confirm ALL tests pass.
   - Check for remaining issues: grep for TODO, BUG, FIXME, HACK markers related to the task.
   - Read back the code you changed and confirm it is correct.
2. If ANY verification fails, return "continue" and fix the problems.
3. Do NOT assume your changes are correct — actually run tests and read the output.
4. If the directory is not a Git repo, or git status/diff/log fails, do not spend time on Git recovery; move on to reading files, running tests, and verifying the requirements directly.
5. Work in small steps. If the task is large, do part of it and return "continue".
6. On each turn, briefly state what you did and what remains.

At the end of this turn, return exactly one JSON object:
{"status":"continue","summary":"what was done and what remains"}
or
{"status":"stop","summary":"all verification passed — describe what was verified"}

Return "stop" ONLY after all verification passes. If in doubt, return "continue".`

	resumeInstructions := `Resume the existing Codex session from its current context.

CRITICAL RULES — you MUST follow these:

1. The follow-up instruction for this turn is the highest-priority instruction and defines the full scope for this turn.
2. The follow-up instruction supersedes and replaces any previous broad plans, repository-wide audits, or todo lists from earlier turns.
3. Do NOT create a new broad todo list.
4. Do NOT rediscover repository state.
5. Do NOT run git status/diff/log.
6. Do NOT run find . / find ...
7. Do NOT scan AGENTS/README/repo-wide files unless the follow-up explicitly requires them.
8. Keep the scope narrow: inspect only the specific files, commands, and tests needed for this follow-up and its verification.
9. Do NOT return "stop" until you have VERIFIED the work needed for this follow-up:
   - Run only the relevant tests/checks for this follow-up and confirm they pass.
   - Check for remaining TODO, BUG, FIXME, HACK markers only when they are relevant to this follow-up.
   - Read back the code you changed and confirm it is correct.
10. If verification fails, return "continue" and keep fixing only the relevant issues.
11. On each turn, briefly state what you did and what remains.

At the end of this turn, return exactly one JSON object:
{"status":"continue","summary":"what was done and what remains"}
or
{"status":"stop","summary":"all verification passed — describe what was verified"}

Return "stop" ONLY after all verification passes. If in doubt, return "continue".`

	if iteration == 0 && strings.TrimSpace(taskPrompt) != "" {
		if continuing {
			return "FOLLOW-UP INSTRUCTION FOR THIS TURN — HIGHEST PRIORITY:\n" +
				strings.TrimSpace(taskPrompt) +
				"\n\nTreat the follow-up instruction above as the only active plan for this turn. It overrides and replaces previous broad plans, repository-wide audits, and todo lists.\n" +
				"Do NOT create a new broad todo list.\n" +
				"Do NOT rediscover repository state.\n" +
				"Do NOT run git status/diff/log.\n" +
				"Do NOT run find . / find ...\n" +
				"Do NOT scan AGENTS/README/repo-wide files unless the follow-up explicitly requires them.\n\n" +
				resumeInstructions
		}
		return initialInstructions + "\n\nPrimary task:\n" + strings.TrimSpace(taskPrompt)
	}
	if continuing {
		return resumeInstructions + "\n\nContinue working on the original task. Do more work — do NOT just verify previous changes and stop. If there are remaining unfinished items from the original task, keep working on them. Return \"continue\" if any work remains."
	}
	return initialInstructions + "\n\nContinue from where you left off. Check what remains and keep working."
}

// BuildVerifyFailurePrompt constructs a prompt informing Codex that the
// verification command failed and includes the command output.
func BuildVerifyFailurePrompt(verifyCmd string, output string) string {
	return fmt.Sprintf(
		"Your previous turn reported stop, but the verification command failed.\n\n"+
			"Verification command: %s\n"+
			"Output:\n%s\n\n"+
			"Fix the issues and try again.",
		verifyCmd, strings.TrimSpace(output),
	)
}

// runVerify runs the verification command and returns its combined output and error.
func (r *Runner) runVerify() (string, error) {
	cmd := exec.Command("sh", "-c", r.cfg.VerifyCmd)
	cmd.Dir = r.cfg.Workdir
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// sandboxArgs returns the Codex CLI flags for sandbox modes that codexloop can
// preserve across both the initial exec and later exec resume turns.
func sandboxArgs(mode string) ([]string, error) {
	switch mode {
	case "full-auto", "":
		return []string{"--full-auto"}, nil
	case "none":
		return []string{"--dangerously-bypass-approvals-and-sandbox"}, nil
	case "read-only", "workspace-write", "danger-full-access":
		return nil, unsupportedEndToEndSandboxModeError(mode)
	default:
		return nil, fmt.Errorf(
			"unsupported sandbox mode %q: codexloop only supports %s",
			mode, supportedSandboxModesDisplay(),
		)
	}
}

func validateSandboxMode(mode string) error {
	_, err := sandboxArgs(mode)
	return err
}

func unsupportedEndToEndSandboxModeError(mode string) error {
	return fmt.Errorf(
		"sandbox mode %q is not supported by codexloop end-to-end: local 'codex exec resume --help' only exposes --full-auto and --dangerously-bypass-approvals-and-sandbox, so codexloop only supports %s",
		mode,
		supportedSandboxModesDisplay(),
	)
}

func supportedSandboxModesDisplay() string {
	return `"full-auto" and "none"`
}

// BuildExecArgs constructs the codex CLI arguments for a fresh exec invocation.
func BuildExecArgs(cfg Config, prompt, schemaPath, outputPath string) ([]string, error) {
	args := []string{"exec", "--json"}
	for _, feature := range cfg.EnabledFeatures {
		if strings.TrimSpace(feature) == "" {
			continue
		}
		args = append(args, "--enable", feature)
	}
	for _, feature := range cfg.DisabledFeatures {
		if strings.TrimSpace(feature) == "" {
			continue
		}
		args = append(args, "--disable", feature)
	}
	for _, override := range cfg.ConfigOverrides {
		if strings.TrimSpace(override) == "" {
			continue
		}
		args = append(args, "-c", override)
	}
	sandboxArgs, err := sandboxArgs(cfg.Sandbox)
	if err != nil {
		return nil, err
	}
	args = append(args, sandboxArgs...)
	if cfg.Model != "" {
		args = append(args, "-m", cfg.Model)
	}
	for _, image := range cfg.Images {
		if strings.TrimSpace(image) == "" {
			continue
		}
		args = append(args, "-i", image)
	}
	if cfg.Ephemeral {
		args = append(args, "--ephemeral")
	}
	if strings.TrimSpace(cfg.Profile) != "" {
		args = append(args, "-p", cfg.Profile)
	}
	args = append(args, "-C", cfg.Workdir)
	if cfg.SkipGitRepoCheck {
		args = append(args, "--skip-git-repo-check")
	}
	args = append(args, "--output-schema", schemaPath)
	args = append(args, "-o", outputPath)
	args = append(args, prompt)
	return args, nil
}

// BuildResumeArgs constructs the codex CLI arguments for a resume invocation.
// Note: --output-schema is intentionally NOT used for resume; the Codex CLI
// does not support it on the resume subcommand.
func BuildResumeArgs(cfg Config, sessionID string, useLast bool, prompt, outputPath string) ([]string, error) {
	args := []string{"exec", "resume", "--json"}
	for _, feature := range cfg.EnabledFeatures {
		if strings.TrimSpace(feature) == "" {
			continue
		}
		args = append(args, "--enable", feature)
	}
	for _, feature := range cfg.DisabledFeatures {
		if strings.TrimSpace(feature) == "" {
			continue
		}
		args = append(args, "--disable", feature)
	}
	for _, override := range cfg.ConfigOverrides {
		if strings.TrimSpace(override) == "" {
			continue
		}
		args = append(args, "-c", override)
	}
	sandboxArgs, err := sandboxArgs(cfg.Sandbox)
	if err != nil {
		return nil, err
	}
	args = append(args, sandboxArgs...)
	if cfg.Model != "" {
		args = append(args, "-m", cfg.Model)
	}
	for _, image := range cfg.Images {
		if strings.TrimSpace(image) == "" {
			continue
		}
		args = append(args, "-i", image)
	}
	if cfg.Ephemeral {
		args = append(args, "--ephemeral")
	}
	if strings.TrimSpace(cfg.Profile) != "" {
		args = append(args, "-p", cfg.Profile)
	}
	if cfg.SkipGitRepoCheck {
		args = append(args, "--skip-git-repo-check")
	}

	if strings.TrimSpace(sessionID) != "" {
		args = append(args, sessionID)
	} else if useLast {
		if cfg.ResumeAll {
			args = append(args, "--all")
		}
		args = append(args, "--last")
	} else {
		return nil, errors.New("resume requested without session id or --last")
	}
	args = append(args, "-o", outputPath)
	args = append(args, prompt)
	return args, nil
}

func (r *Runner) runExec(prompt string, progress io.Writer, iteration int) (CommandResult, error) {
	return r.runCommand(progress, iteration, func(schemaPath, outputPath string) ([]string, error) {
		return BuildExecArgs(r.cfg, prompt, schemaPath, outputPath)
	})
}

func (r *Runner) runResume(sessionID string, useLast bool, prompt string, progress io.Writer, iteration int) (CommandResult, error) {
	return r.runCommand(progress, iteration, func(_, outputPath string) ([]string, error) {
		return BuildResumeArgs(r.cfg, sessionID, useLast, prompt, outputPath)
	})
}

func (r *Runner) runCommand(progress io.Writer, iteration int, buildArgs func(schemaPath, outputPath string) ([]string, error)) (CommandResult, error) {
	if err := validateSandboxMode(r.cfg.Sandbox); err != nil {
		return CommandResult{}, err
	}

	tempDir, err := os.MkdirTemp("", "codexloop-*")
	if err != nil {
		return CommandResult{}, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	schemaPath := filepath.Join(tempDir, "schema.json")
	outputPath := filepath.Join(tempDir, "last_message.txt")
	if err := os.WriteFile(schemaPath, controlSchema(), 0o600); err != nil {
		return CommandResult{}, fmt.Errorf("failed to write schema file: %w", err)
	}

	finalArgs, err := buildArgs(schemaPath, outputPath)
	if err != nil {
		return CommandResult{}, err
	}

	cmd := exec.Command(r.cfg.CodexBin, finalArgs...)
	cmd.Dir = r.cfg.Workdir

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return CommandResult{}, fmt.Errorf("failed to capture codex stdout: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return CommandResult{}, fmt.Errorf("failed to capture codex stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return CommandResult{}, fmt.Errorf("failed to start codex command: %w", err)
	}

	type readResult struct {
		output []byte
		err    error
	}

	stdoutCh := make(chan readResult, 1)
	go func() {
		var stdoutBuf bytes.Buffer
		err := readCommandOutput(stdoutPipe, &stdoutBuf, newProgressReporter(progress, iteration))
		stdoutCh <- readResult{output: stdoutBuf.Bytes(), err: err}
	}()

	stderrCh := make(chan readResult, 1)
	go func() {
		data, err := io.ReadAll(stderrPipe)
		stderrCh <- readResult{output: data, err: err}
	}()

	stdoutResult := <-stdoutCh
	stderrResult := <-stderrCh
	cmdErr := cmd.Wait()

	if stdoutResult.err != nil {
		return CommandResult{}, fmt.Errorf("failed to read codex stdout: %w", stdoutResult.err)
	}
	if stderrResult.err != nil {
		return CommandResult{}, fmt.Errorf("failed to read codex stderr: %w", stderrResult.err)
	}
	output := combineCommandOutput(stdoutResult.output, stderrResult.output)

	result, parseErr := ParseCodexOutput(stdoutResult.output)
	if parseErr != nil && len(stderrResult.output) > 0 {
		result, parseErr = ParseCodexOutput(output)
	}
	result.RawOutput = string(output)
	if message, readErr := os.ReadFile(outputPath); readErr == nil {
		result.AgentText = strings.TrimSpace(string(message))
		ctrl, ctrlErr := ParseControlMessage(result.AgentText)
		if ctrlErr == nil {
			result.Control = ctrl
			parseErr = nil
		}
	}

	if cmdErr != nil {
		result.CommandError = cmdErr
		if parseErr == nil {
			return result, nil
		}
		return result, fmt.Errorf("codex command failed: %w\noutput:\n%s", cmdErr, string(output))
	}
	if parseErr != nil {
		return result, parseErr
	}
	return result, nil
}

func readCommandOutput(r io.Reader, buf *bytes.Buffer, reporter progressReporter) error {
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			buf.Write(line)
			reporter.ProcessLine(strings.TrimSpace(string(line)))
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func combineCommandOutput(stdout, stderr []byte) []byte {
	if len(stderr) == 0 {
		return append([]byte(nil), stdout...)
	}
	output := append([]byte(nil), stdout...)
	if len(output) > 0 && output[len(output)-1] != '\n' {
		output = append(output, '\n')
	}
	output = append(output, stderr...)
	return output
}

type progressReporter struct {
	out     io.Writer
	iter    int
	emitted int
	seen    map[string]struct{}
}

func newProgressReporter(out io.Writer, iter int) progressReporter {
	return progressReporter{
		out:  out,
		iter: iter,
		seen: make(map[string]struct{}),
	}
}

func (p *progressReporter) ProcessLine(line string) {
	if p == nil || p.out == nil || p.emitted >= maxProgressMessagesPerIteration {
		return
	}

	summary := summarizeProgressLine(line)
	if summary == "" {
		return
	}
	if _, ok := p.seen[summary]; ok {
		return
	}
	p.seen[summary] = struct{}{}
	p.emitted++
	fmt.Fprintf(p.out, "[codexloop] iter=%d progress: %s\n", p.iter, summary)
}

func summarizeProgressLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return ""
	}

	var event map[string]any
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return ""
	}

	item, _ := event["item"].(map[string]any)
	itemType := firstNonEmptyString(
		stringValue(item["type"]),
		stringValue(event["item_type"]),
		stringValue(event["type"]),
	)

	switch itemType {
	case "todo_list":
		payload := event
		if item != nil {
			payload = item
		}
		return summarizeTodoProgress(payload)
	case "command_execution":
		payload := event
		if item != nil {
			payload = item
		}
		return summarizeCommandProgress(payload, stringValue(event["type"]))
	default:
		return ""
	}
}

func summarizeTodoProgress(payload map[string]any) string {
	items := extractTodoItems(payload)
	if len(items) == 0 {
		return ""
	}

	done := 0
	current := ""
	for _, item := range items {
		status := normalizeTodoStatus(item)
		if status == "completed" {
			done++
			continue
		}
		if current == "" {
			current = shortenProgressText(todoItemText(item), 72)
		}
	}
	if current == "" {
		current = shortenProgressText(todoItemText(items[len(items)-1]), 72)
	}
	if current == "" {
		return fmt.Sprintf("todo %d/%d", done, len(items))
	}
	return fmt.Sprintf("todo %d/%d: %s", done, len(items), current)
}

func summarizeCommandProgress(payload map[string]any, eventType string) string {
	command := shortenCommandText(extractCommandText(payload))
	if command == "" {
		return ""
	}

	status := normalizeCommandStatus(payload, eventType)
	switch status {
	case "done":
		if exitCode, ok := intValue(payload["exit_code"]); ok {
			return fmt.Sprintf("cmd exit=%d: %s", exitCode, command)
		}
		return fmt.Sprintf("cmd done: %s", command)
	case "failed":
		if exitCode, ok := intValue(payload["exit_code"]); ok {
			return fmt.Sprintf("cmd exit=%d: %s", exitCode, command)
		}
		return fmt.Sprintf("cmd failed: %s", command)
	default:
		return fmt.Sprintf("cmd: %s", command)
	}
}

func extractTodoItems(payload map[string]any) []map[string]any {
	for _, key := range []string{"items", "todos", "entries"} {
		if items := mapsFromArray(payload[key]); len(items) > 0 {
			return items
		}
	}
	return nil
}

func normalizeTodoStatus(item map[string]any) string {
	status := strings.ToLower(stringValue(item["status"]))
	switch status {
	case "done", "completed", "complete":
		return "completed"
	case "in_progress", "in-progress", "active", "running":
		return "in_progress"
	case "pending", "todo", "open":
		return "pending"
	}
	if completed, ok := item["completed"].(bool); ok {
		if completed {
			return "completed"
		}
		return "pending"
	}
	return ""
}

func todoItemText(item map[string]any) string {
	return firstNonEmptyString(
		stringValue(item["text"]),
		stringValue(item["content"]),
		stringValue(item["title"]),
	)
}

func normalizeCommandStatus(payload map[string]any, eventType string) string {
	status := strings.ToLower(firstNonEmptyString(
		stringValue(payload["status"]),
		stringValue(payload["state"]),
	))
	switch status {
	case "completed", "complete", "finished", "done", "success", "succeeded":
		return "done"
	case "failed", "error":
		return "failed"
	case "running", "in_progress", "in-progress", "started":
		return "running"
	}
	switch eventType {
	case "item.completed":
		return "done"
	case "item.failed":
		return "failed"
	default:
		return "running"
	}
}

func extractCommandText(payload map[string]any) string {
	for _, key := range []string{"command", "cmd", "shell_command", "shellCommand"} {
		switch v := payload[key].(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return v
			}
		case []any:
			return joinStringArray(v)
		case map[string]any:
			if text := extractCommandText(v); text != "" {
				return text
			}
		}
	}
	for _, key := range []string{"argv", "args"} {
		if text := joinStringArray(anySlice(payload[key])); text != "" {
			return text
		}
	}
	return ""
}

func shortenCommandText(text string) string {
	return shortenProgressText(text, 72)
}

func shortenProgressText(text string, limit int) string {
	clean := strings.Join(strings.Fields(text), " ")
	clean = strings.Trim(clean, `"'`)
	if clean == "" {
		return ""
	}
	runes := []rune(clean)
	if limit <= 0 || len(runes) <= limit {
		return clean
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func mapsFromArray(v any) []map[string]any {
	items := anySlice(v)
	if len(items) == 0 {
		return nil
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if ok {
			result = append(result, obj)
		}
	}
	return result
}

func anySlice(v any) []any {
	items, _ := v.([]any)
	return items
}

func joinStringArray(items []any) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(stringValue(item))
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " ")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringValue(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	default:
		return ""
	}
}

func intValue(v any) (int, bool) {
	switch value := v.(type) {
	case float64:
		return int(value), true
	case int:
		return value, true
	case int64:
		return int(value), true
	case json.Number:
		n, err := value.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

// ParseCodexOutput parses the JSONL event stream from Codex CLI stdout.
// It always returns a result with whatever fields could be extracted (e.g. ThreadID),
// even when an error is returned due to missing or invalid control messages.
func ParseCodexOutput(output []byte) (CommandResult, error) {
	result := CommandResult{RawOutput: string(output)}
	lines := bytes.Split(output, []byte{'\n'})

	for _, raw := range lines {
		line := strings.TrimSpace(string(raw))
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}

		var evt Event
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}

		if evt.Type == "thread.started" && evt.ThreadID != "" {
			result.ThreadID = evt.ThreadID
		}
		// Use the last agent_message as the control output
		if evt.Type == "item.completed" && evt.Item != nil && stringValue(evt.Item["type"]) == "agent_message" {
			result.AgentText = strings.TrimSpace(stringValue(evt.Item["text"]))
		}
	}

	if result.AgentText == "" {
		return result, fmt.Errorf("no agent_message found in codex output:\n%s", string(output))
	}

	ctrl, err := ParseControlMessage(result.AgentText)
	if err != nil {
		return result, err
	}
	result.Control = ctrl
	return result, nil
}

func ParseControlMessage(text string) (ControlMessage, error) {
	trimmed := strings.TrimSpace(text)
	var msg ControlMessage
	if err := json.Unmarshal([]byte(trimmed), &msg); err == nil {
		return msg, msg.Validate()
	}

	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(trimmed[start:end+1]), &msg); err == nil {
			return msg, msg.Validate()
		}
	}
	return ControlMessage{}, fmt.Errorf("assistant response is not valid control JSON: %q", text)
}

func controlSchema() []byte {
	return []byte(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["status", "summary"],
  "properties": {
    "status": {
      "type": "string",
      "enum": ["continue", "stop"]
    },
    "summary": {
      "type": "string",
      "minLength": 1
    }
  }
}`)
}
