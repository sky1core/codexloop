# codexloop

Codex CLI를 자동으로 반복 실행하는 래퍼. Codex가 구조화된 `{"status":"stop"}`를 반환하고, 필요하면 `--verify` 검증까지 통과할 때까지 같은 세션을 이어간다.

## 왜 필요한가

Codex는 컨텍스트가 남아있어도 작업 중간에 멈추는 경우가 잦다. `codexloop`은:

- 첫 턴은 `codex exec`, 이후는 `codex exec resume`로 같은 세션을 이어감
- 매 턴마다 구조화된 JSON 응답을 강제 (`continue` / `stop`)
- `--verify`로 로컬 검증 명령 실행: Codex가 "완료"라고 해도 검증 실패하면 재반복
- 로그를 `~/.codexloop/logs/`에 자동 저장

## 설치

```bash
go install github.com/sky1core/codexloop@latest
```

`codexloop` 자체는 Codex CLI를 감싸는 래퍼이므로, `codex` CLI도 별도로 설치되어 있어야 한다.

## 사용법

### 기본 실행

```bash
codexloop "남은 버그 전부 고쳐라"
```

Codex가 현재 디렉토리를 Git 저장소로 신뢰하지 않아 거부하면:

```bash
codexloop --skip-git-repo-check "이 저장소를 점검해라"
```

### stdin에서 프롬프트 읽기

```bash
echo "리뷰하고 마무리해라" | codexloop
```

### Codex 설정 override 전달 (-c / --config)

Codex CLI의 repeatable `-c/--config` 를 그대로 넘길 수 있다.

```bash
codexloop -c 'model_reasoning_effort="xhigh"' "남은 작업 계속해라"
codexloop --config 'model="gpt-5.4"' --config 'model_reasoning_effort="high"' "버그를 마저 고쳐라"
codexloop resume --last -c 'model_reasoning_effort="high"' "아까 하던 거 계속해"
```

### Codex 기능 플래그 전달 (--enable / --disable)

Codex CLI의 repeatable `--enable/--disable` 도 그대로 넘길 수 있다.

```bash
codexloop --enable web_search "최신 정보를 확인해서 정리해라"
codexloop --enable planner --disable legacy_mode "남은 작업 계속해라"
codexloop resume --last --enable web_search "아까 하던 거 이어서 해"
```

### 이미지 전달 (-i / --image)

Codex CLI의 `-i/--image` 도 그대로 넘길 수 있다.

```bash
codexloop -i screenshot.png "이 이미지 기준으로 문제를 분석해라"
codexloop --image before.png --image after.png "두 이미지를 비교해서 설명해라"
codexloop resume --last -i diagram.png "아까 하던 설명 이어서 해"
```

### ephemeral 실행 (--ephemeral)

Codex CLI의 `--ephemeral` 도 그대로 넘길 수 있다.

```bash
codexloop --ephemeral "임시 세션으로만 점검해라"
codexloop resume --last --ephemeral "같은 흐름으로 한 번 더 이어가라"
```

### Codex 프로필 전달 (-p / --profile)

Codex CLI의 `-p/--profile` 도 그대로 넘길 수 있다.

```bash
codexloop -p work "남은 버그를 마저 고쳐라"
codexloop resume --last --profile work "아까 하던 작업 계속해"
```

### 세션 이어서 실행

```bash
# codexloop가 현재 workdir에서 마지막으로 기록한 세션 이어하기
codexloop resume

# 위와 동일하되 명시적으로 --last 요청
codexloop resume --last

# 특정 세션 이어하기
codexloop resume 019cc69d-2e58-75a2-a786-a557e0e77be4

# 추가 지시와 함께 이어하기
codexloop resume 019cc69d-2e58-75a2-a786-a557e0e77be4 "아까 하던 거 계속해"

# workdir 구분 없이 raw Codex CLI의 최신 세션 기준으로 이어하기
codexloop resume --all

# workdir 구분 없이 최신 세션에 follow-up prompt 보내기
codexloop resume --all --last "아까 하던 거 이어서 해"
```

`resume` / `resume --last` 는 먼저 **codexloop가 같은 workdir에 대해 마지막으로 기록한 exact session ID**를 사용한다.
그 기록이 없을 때만 Codex CLI의 `--last` 로 fallback 한다.
raw Codex CLI `--last` 는 Codex가 기록한 **가장 최근 세션** 기준이라, 같은 세션을 다른 디렉토리에서 다시 resume하면 그 기록 기준이 이동할 수 있다.
그래서 `codexloop`은 가능한 한 workdir별 exact session ID 기록을 먼저 사용한다.
다만 `resume --all` 은 이 로컬 exact-session 우선 규칙을 우회하고, raw Codex CLI `--last --all` 의미를 그대로 따른다.
또한 raw Codex CLI와 동일하게 `--all` 자체는 positional parsing을 바꾸지 않는다.
즉 `codexloop resume --all foo` 에서 `foo` 는 prompt가 아니라 session/thread 식별자로 해석된다.
최신 cross-directory 세션에 follow-up prompt를 보내려면 `resume --all --last "PROMPT"` 형태를 사용해야 한다.

### 로컬 검증 명령 (-verify)

Codex가 "완료"라고 해도 **로컬 셸에서 실행한 검증 명령**이 통과해야 진짜 종료:

```bash
# Go: 테스트 통과해야 종료
codexloop --verify "go test ./..." "버그 전부 고쳐라"

# Node: 빌드+테스트 통과해야 종료
codexloop --verify "npm run build && npm test" "기능 X 구현해라"

# BUG/TODO 마커 없어야 종료
codexloop --verify '! grep -r "BUG\|TODO" --include="*.go" .' "TODO 전부 정리해라"
```

검증 실패하면 실패 내용을 Codex에 전달하고 다음 반복을 강제한다.

### 주요 옵션

| 옵션 | 설명 | 기본값 |
|------|------|--------|
| `-C <경로>`, `--cd <경로>` | 작업 디렉토리 | 현재 디렉토리 |
| `--max-iters <N>` | 최대 반복 횟수 | 20 |
| `--verify <명령>` | 완료 검증 셸 명령 | (없음) |
| `--sandbox <모드>`, `-s <모드>` | 루프 전체에 적용되는 샌드박스 모드 (`full-auto`, `none`) | `full-auto` |
| `--full-auto` | `--sandbox full-auto`의 shortcut | `false` |
| `--dangerously-bypass-approvals-and-sandbox` | `--sandbox none`의 shortcut | `false` |
| `--model <모델>`, `-m <모델>` | Codex 모델 지정 | Codex 기본값 |
| `--image <파일>`, `-i <파일>` | Codex 이미지 첨부 전달 (repeatable) | (없음) |
| `--ephemeral` | Codex 세션 파일 비영속 실행 | `false` |
| `--profile <프로필>`, `-p <프로필>` | Codex config profile 전달 | Codex 기본값 |
| `--enable <기능>` | Codex feature enable 전달 (repeatable) | (없음) |
| `--disable <기능>` | Codex feature disable 전달 (repeatable) | (없음) |
| `--config <key=value>`, `-c <key=value>` | Codex config override 전달 (repeatable) | (없음) |
| `resume --all` | raw Codex CLI `--last --all` 의미로 가장 최근 세션 선택 | workdir별 exact session 우선 |
| `--codex-bin <경로>` | Codex CLI 바이너리 경로 | `codex` |
| `--skip-git-repo-check` | Codex의 Git 저장소 검사 우회 | `false` |
| `--log-dir <경로>` | codexloop 로그 디렉토리 | `~/.codexloop/logs` |
| `--no-log` | 로그 파일 생성 비활성화 | `false` |

### 샌드박스 모드

| 모드 | 설명 |
|------|------|
| `full-auto` | `--sandbox full-auto` 또는 `--full-auto`. Codex CLI `--full-auto` 전달. 로컬 help 기준으로 low-friction sandboxed automatic execution (`-a on-request`, `--sandbox workspace-write`) |
| `none` | `--sandbox none` 또는 `--dangerously-bypass-approvals-and-sandbox`. 위험한 무제한 모드 |

서로 모순되는 샌드박스 선택(예: `--full-auto --sandbox none`)은 조용히 덮어쓰지 않고 **즉시 parse error**를 반환한다.

### resume와 샌드박스 제약

로컬 `codex exec resume --help` 기준으로 resume 경로는 `--sandbox`를 지원하지 않고 `--full-auto` / `--dangerously-bypass-approvals-and-sandbox`만 노출한다. 그래서 `codexloop`은 루프 전체에서 아래 두 모드만 지원한다.

- `full-auto`
- `none`

아래 값은 첫 턴만 따로 취급하지 않고 **즉시 명시적 에러**를 반환한다. 조용히 `full-auto`로 바꾸지 않는다.

- `read-only`
- `workspace-write`
- `danger-full-access`

## 동작 원리

매 반복마다 Codex에게 아래 형태의 JSON 응답을 강제한다:

```json
{"status":"continue","summary":"진행 상황 요약"}
```
```json
{"status":"stop","summary":"완료 이유"}
```

- `continue`: 다음 반복으로 resume
- `stop`: `--verify`가 있으면 검증 실행 → 통과하면 종료, 실패하면 재반복

## 로그

기본적으로 `~/.codexloop/logs/`에 타임스탬프 로그 파일을 생성한다.
샌드박스/권한 환경에 따라 실패할 수 있으며, 이 경우 경고만 출력하고 계속 진행한다.
로그 파일을 원하지 않으면 `--no-log`, 다른 위치를 원하면 `--log-dir`를 사용하면 된다.

- 각 iteration 시작 시 짧은 start 로그가 stderr/log에 남는다.
- 진행 중에는 todo/명령 기준의 짧은 progress summary만 제한적으로 stderr/log에 남는다.
- stdout에는 마지막 control JSON만 출력한다.
- Codex의 raw JSONL 이벤트나 긴 명령 전체를 그대로 찍지 않는다.

```
~/.codexloop/logs/2026-03-07T18_24_29.log
```

## Claude Code 스킬

Claude Code에서 `/codexloop`으로 Codex에 작업을 위임할 수 있다.

```bash
mkdir -p ~/.claude/skills/codexloop
cp skills/codexloop/SKILL.md ~/.claude/skills/codexloop/SKILL.md
```

## 참고

- 기본 샌드박스 모드는 Codex CLI의 `--full-auto`다. 이는 로컬 help 기준으로 `-a on-request`, `--sandbox workspace-write` 조합의 convenience alias이며, README에서는 이를 임의로 `auto-approve`라고 부르지 않는다.
- 이미지 첨부가 필요하면 `-i/--image` 를 반복해서 넘길 수 있다.
- 세션 파일 비영속 실행이 필요하면 `--ephemeral` 을 그대로 넘길 수 있다.
- Codex CLI 프로필을 쓰려면 `-p/--profile` 로 config.toml profile 이름을 그대로 넘길 수 있다. 예: `-p work`
- Codex CLI feature 토글이 필요하면 `--enable/--disable` 를 반복해서 넘길 수 있다.
- Codex CLI 설정 override가 필요하면 `-c/--config`를 반복해서 넘길 수 있다. 예: `-c 'model_reasoning_effort="xhigh"'`
- `resume --all` 은 codexloop의 workdir-local exact-session 기록보다 raw Codex CLI의 전역 최신 세션 선택 semantics를 우선한다.
- stdout에는 마지막 반복의 구조화된 제어 메시지만 출력
- 각 iteration 시작 로그와 짧은 progress summary는 stderr/log에만 남음
- 첫 턴에는 `--output-schema`로 JSON 스키마 강제, resume에서는 `-o`로 마지막 메시지 파싱
- Codex가 비정상 종료해도 유효한 구조화 응답이 있으면 해당 턴을 수용
- Codex가 현재 디렉토리를 Git 저장소로 신뢰하지 않으면 `--skip-git-repo-check`가 필요할 수 있다
