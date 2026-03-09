---
name: codexloop
description: codexloop CLI로 Codex를 반복 실행하여 작업을 끝까지 완료시킴. 지시서 작성과 결과 검증만 담당.
---

# codexloop 반복 실행 스킬

## 사용 시점

- 작업량이 많아 Codex를 반복 실행해야 할 때
- 반복적·기계적 작업을 codexloop으로 자동화할 때
- "codexloop으로 돌려", "codexloop 시켜" 같은 지시

**사용하지 말 것:** 설계 판단이 필요한 작업, 간단한 수정, 일회성 Codex 호출로 충분한 경우

## 워크플로우

### 1. 지시서 작성

구체적인 프롬프트: 무엇을 해야 하는지, 관련 파일 경로, 완료 조건, 금지 사항 포함.

### 2. codexloop 실행

**출력이 길어질 수 있으므로 stderr를 버리고 stdout만 파일로 받는다.**

```bash
# 새 작업
codexloop -C <작업디렉토리> -verify "<검증명령>" "<지시서>" > /tmp/codexloop-result.txt 2>/dev/null

# 이어서 작업
codexloop -C <작업디렉토리> resume > /tmp/codexloop-result.txt 2>/dev/null

# 이어서 작업 + 추가 지시
codexloop -C <작업디렉토리> resume "추가 지시" > /tmp/codexloop-result.txt 2>/dev/null
```

주요 옵션: `-max-iters <N>` (기본 20), `-verify <명령>`, `-sandbox full-auto|none`, `-model <모델>`

- `run_in_background: true`로 실행 권장, timeout은 최소 300000ms
- 로그는 `~/.codexloop/logs/`에 자동 저장

### 3. 결과 검증

Read 도구로 `/tmp/codexloop-result.txt`를 읽고, `git diff`와 테스트로 확인. 부족하면 resume으로 재실행.

### 4. 결과 보고

변경 내용, 테스트 통과 여부, 추가 조치 사항을 사용자에게 보고.
