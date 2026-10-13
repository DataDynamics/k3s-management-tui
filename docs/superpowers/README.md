# 설계·계획 문서

superpowers 작업 흐름(brainstorming → writing-plans → 구현)에서 나오는 문서를 둡니다.
현재 시스템의 모습은 [../DESIGN.md](../DESIGN.md)를 먼저 보세요. 여기 있는 문서는 각 시점의 결정과 기록입니다.

## 규칙

| 종류 | 위치·이름 | 내용 |
|---|---|---|
| spec | `specs/YYYY-MM-DD-<topic>-design.md` | 기능 하나의 설계. 날짜는 작성일입니다 |
| plan | `plans/YYYY-MM-DD-<topic>.md` | spec을 구현하는 단계별 계획. 첫 줄에 근거 spec을 링크합니다 |
| 현재 상태 | `../DESIGN.md` | 지금 main의 모습만. 이력을 쌓지 않습니다 |

1. **spec은 시점을 고정합니다.** 구현이 끝나면 설계 본문은 고치지 않고, 끝에 `## 구현 결과`를 덧붙여 설계와 달라진 점과 검증 결과를 적습니다.
   설계를 다시 바꿔야 하면 새 spec을 쓰고 이전 spec을 링크합니다.
2. **DESIGN.md는 기능을 머지하는 커밋에서 함께 고칩니다.** 각 절에 근거 spec을 링크합니다.
3. **이 색인은 spec·plan을 추가하는 커밋에서 함께 고칩니다.**
4. `<topic>`은 영어 소문자 kebab-case로 씁니다. 본문은 한국어로 씁니다.
5. 작은 수정(bounded 작업)은 spec·plan 없이 커밋 메시지로 충분합니다.

## spec 목록

| 날짜 | 문서 | 상태 | 비고 |
|---|---|---|---|
| 2026-10-05 | [k3stui 최초 설계 (M1~M5)](specs/2026-10-05-k3stui-initial-design.md) | 구현 완료 | 이관 전 DESIGN.md 1–12장 |
| 2026-10-05 | [일반 Kubernetes 지원](specs/2026-10-05-generic-kubernetes-design.md) | 구현 완료 | 이관 전 DESIGN.md 13장 |
| 2026-10-05 | [kubeadm 노드 관리](specs/2026-10-05-kubeadm-node-management-design.md) | 구현 완료 | 이관 전 DESIGN.md 14장 |
| 2026-10-06 | [RKE2 노드 관리와 배포판 정리](specs/2026-10-06-rke2-node-management-design.md) | 구현 완료 | 이관 전 DESIGN.md 15장 |
| 2026-10-06 | [CI/CD — GitHub Actions](specs/2026-10-06-ci-github-actions-design.md) | 구현 완료 | 이관 전 docs/CI-DESIGN.md |

이관한 문서는 원문을 그대로 두었으므로 본문의 "13장", "14·15장 참고" 같은 장 번호는 위 비고 열의 장 번호를 가리킵니다.

## plan 목록

| 날짜 | 문서 | 근거 spec | 상태 |
|---|---|---|---|
| | (아직 없음) | | |
