---
name: codexloop
description: Codex에게 작업을 위임하여 끝까지 완료시킴. Claude는 지시서 작성과 결과 검증만 담당. 무거운 작업은 Codex 토큰으로 처리.
---

# Codex 작업 위임 스킬

Codex CLI에게 작업을 위임하고, codexloop이 완료될 때까지 자동으로 반복 실행한다.
Claude 토큰 대신 Codex 토큰을 사용하므로 비용 효율적이다.

## 사용 시점

- 코드 수정, 버그 수정, 리팩토링 등 구체적인 작업을 Codex에 위임할 때
- Claude가 직접 하기에는 반복적이거나 기계적인 작업
- 작업량이 많아 Claude 토큰 소모가 클 때
- "이거 Codex한테 시켜", "Codex로 돌려" 같은 지시

### 사용하지 말아야 할 때

- 설계 판단이 필요한 작업 (Claude가 직접 해야 함)
- 간단한 수정 (직접 하는 게 빠름)
- 분석/질문만 필요한 경우 (`ask-codex` 스킬 사용)

## 워크플로우

### 1단계: 지시서 작성 (핵심)

Codex가 정확히 무엇을 해야 하는지 구체적인 프롬프트를 작성한다.

**좋은 지시서의 조건:**
- 무엇을 해야 하는지 명확히 기술
- 관련 파일/디렉토리 경로 포함
- 완료 조건 명시
- 하지 말아야 할 것 명시 (있다면)

**예시:**
```
pkg/codexloop/codexloop.go에서 ParseCodexOutput 함수를 수정해라.
에러 반환 시에도 ThreadID가 보존되어야 한다.
수정 후 go test ./... 가 전부 통과해야 한다.
기존 테스트를 수정하지 마라.
```

### 2단계: codexloop 실행

**중요: Claude 토큰 절약을 위해 stderr를 버리고, stdout(최종 결과)만 파일로 받는다.**
로그는 `~/.codexloop/logs/`에 매 실행마다 자동 생성된다.

```bash
codexloop -C <작업디렉토리> -verify "<검증명령>" "<지시서>" > /tmp/codexloop-result.txt 2>/dev/null
```

**옵션:**
- `-C <경로>`: 작업 디렉토리 (기본: 현재 디렉토리)
- `-max-iters <N>`: 최대 반복 횟수 (기본: 20)
- `-verify <명령>`: 완료 검증 명령 (실패하면 다음 반복 강제)
- `-sandbox <모드>`: 샌드박스 모드 (기본: `full-auto`)
  - `full-auto`: 샌드박스 내 자동 실행 (기본값, 권장)
  - `none`: 샌드박스 없이 실행 (위험)
- `-model <모델>`: Codex 모델 지정 (예: `gpt-5.4`, `gpt-5.3-codex`, `codex-mini`)
- `-p, --profile <프로필>`: Codex config profile 전달
- `-c, --config <key=value>`: Codex config override 전달 (repeatable)
- `-i, --image <파일>`: 이미지 첨부 전달 (repeatable)
- `--enable <기능>` / `--disable <기능>`: Codex feature 토글 전달 (repeatable)
- `--ephemeral`: Codex 세션 파일 비영속 실행

**기존 세션 이어서 작업:**
```bash
codexloop -C <작업디렉토리> resume > /tmp/codexloop-result.txt 2>/dev/null
```

**resume 동작 주의:**
- `resume` / `resume --last` 는 먼저 **현재 workdir에 대해 codexloop가 기록한 exact session ID**를 사용하고, 없을 때만 raw Codex CLI `--last` 로 fallback 한다.
- workdir 구분 없이 raw Codex CLI의 최신 세션 기준으로 이어가려면 `resume --all` 을 사용한다.
- raw Codex CLI와 동일하게 `resume --all foo` 에서 `foo` 는 prompt가 아니라 session/thread 식별자로 해석된다.
- 최신 cross-directory 세션에 follow-up prompt를 보내려면 `resume --all --last "PROMPT"` 형태를 사용한다.

**주의:**
- stdout/stderr를 직접 받으면 Claude가 전부 읽어서 토큰 소모 → 반드시 리다이렉트
- 로그는 `~/.codexloop/logs/`에 자동 저장, 필요 시 Read 도구로 확인
- 오래 걸릴 수 있으므로 `run_in_background: true`로 실행 권장
- timeout은 충분히 길게 설정 (최소 300000ms)
- 결과 확인은 Read 도구로 `/tmp/codexloop-result.txt` 읽기

### 3단계: 결과 검증

codexloop 완료 후 반드시 확인:

1. **변경 내용 확인**: `git diff`로 무엇이 바뀌었는지 확인
2. **테스트 실행**: 프로젝트의 테스트 명령 실행
3. **의도 검증**: 원래 목표대로 수정되었는지 코드 리뷰

문제가 있으면 추가 지시와 함께 `codexloop resume`로 재실행.

### 4단계: 결과 보고

사용자에게 다음을 보고:
- Codex가 무엇을 변경했는지
- 테스트 통과 여부
- 추가 조치가 필요한 사항

## 전체 예시

```bash
# 1. 작업 위임 (-verify로 테스트 통과 강제, 출력은 /tmp로)
codexloop -C /path/to/project -verify "go test ./..." "
이 프로젝트에서 다음 작업을 수행해라:
1. src/api/handler.go에서 에러 핸들링을 개선해라
2. 각 에러 케이스에 대한 테스트를 추가해라
3. go test ./... 가 전부 통과해야 한다
4. 기존 테스트를 삭제하거나 수정하지 마라
" > /tmp/codexloop-result.txt 2>/dev/null

# 2. 결과 확인 (Read 도구로 /tmp/codexloop-result.txt 읽기)
# 로그 필요 시: ~/.codexloop/logs/ 에서 최신 파일 확인

# 3. 변경 내용 확인
cd /path/to/project && git diff --stat

# 4. 부족하면 이어서 작업
codexloop -C /path/to/project resume "에러 로깅도 추가해라" > /tmp/codexloop-result.txt 2>/dev/null

# workdir 구분 없이 최신 세션에 follow-up prompt 보내기
codexloop -C /path/to/project resume --all --last "다른 디렉토리에서 하던 작업 이어서 해" > /tmp/codexloop-result.txt 2>/dev/null
```

## 설치 방법

```bash
# codexloop CLI 설치
go install github.com/sky1core/codexloop@latest

# 스킬 설치 (이 파일을 복사)
mkdir -p ~/.claude/skills/codexloop
cp skills/codexloop/SKILL.md ~/.claude/skills/codexloop/SKILL.md
```
