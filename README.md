# codexloop

Codex CLI를 자동으로 반복 실행하는 래퍼. Codex가 `{"status":"stop"}`을 반환하고, `--verify` 검증까지 통과할 때까지 같은 세션을 이어간다.

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

`codex` CLI가 별도로 설치되어 있어야 한다.

## 사용법

```bash
# 기본 실행
codexloop "남은 버그 전부 고쳐라"

# 검증 명령과 함께
codexloop --verify "go test ./..." "버그 전부 고쳐라"

# 작업 디렉토리 지정
codexloop -C /path/to/project "기능 X 구현해라"

# stdin에서 프롬프트
echo "리뷰하고 마무리해라" | codexloop
```

### 세션 이어서 실행

```bash
codexloop resume                    # 현재 workdir의 마지막 세션
codexloop resume "추가 지시"         # 추가 프롬프트와 함께
codexloop resume <session-id>       # 특정 세션
codexloop resume --all              # workdir 무관, 전역 최신 세션
```

`resume`은 codexloop이 workdir별로 기록한 세션 ID를 우선 사용하고, 없으면 Codex CLI `--last`로 fallback한다.
`resume --all` 뒤의 positional 인자는 프롬프트가 아니라 세션 ID로 해석된다. 프롬프트를 보내려면 `resume --all --last "프롬프트"` 형태를 사용해야 한다.

### 옵션

| 옵션 | 설명 | 기본값 |
|------|------|--------|
| `-C`, `--cd` | 작업 디렉토리 | 현재 디렉토리 |
| `--max-iters <N>` | 최대 반복 횟수 | 20 |
| `--verify <명령>` | 완료 검증 셸 명령 | (없음) |
| `--sandbox <모드>` | 샌드박스 모드 (`full-auto`, `none`) | `full-auto` |
| `--model`, `-m` | Codex 모델 지정 | Codex 기본값 |
| `--config`, `-c` | Codex config override (repeatable) | (없음) |
| `--image`, `-i` | 이미지 첨부 (repeatable) | (없음) |
| `--profile`, `-p` | Codex config profile | Codex 기본값 |
| `--enable` / `--disable` | Codex feature 토글 (repeatable) | (없음) |
| `--ephemeral` | 세션 파일 비영속 실행 | `false` |
| `--codex-bin` | Codex CLI 바이너리 경로 | `codex` |
| `--skip-git-repo-check` | Git 저장소 검사 우회 | `false` |
| `--log-dir` | 로그 디렉토리 | `~/.codexloop/logs` |
| `--no-log` | 로그 비활성화 | `false` |

`--full-auto`는 `--sandbox full-auto`의, `--dangerously-bypass-approvals-and-sandbox`는 `--sandbox none`의 shortcut이다. 모순되는 조합은 즉시 에러를 반환한다.

## 동작 원리

매 반복마다 Codex에게 JSON 응답을 강제한다:

- `{"status":"continue","summary":"..."}` → 다음 반복으로 resume
- `{"status":"stop","summary":"..."}` → `--verify` 있으면 검증 → 통과 시 종료, 실패 시 재반복

## Claude Code 스킬

```bash
mkdir -p ~/.claude/skills/codexloop
cp skills/codexloop/SKILL.md ~/.claude/skills/codexloop/SKILL.md
```
