# k3stui

## 문서 규칙

- 설계(spec)는 `docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md`, 구현 계획(plan)은 `docs/superpowers/plans/YYYY-MM-DD-<topic>.md`에 씁니다.
- spec·plan을 추가하면 같은 커밋에서 `docs/superpowers/README.md` 색인에 한 줄을 추가합니다.
- 구현이 끝난 spec은 본문을 고치지 않고 끝에 `## 구현 결과`(설계와 달라진 점, 검증 결과)를 덧붙입니다.
- `docs/DESIGN.md`는 현재 상태만 적습니다. 동작이나 구조를 바꾸는 커밋에서 함께 고치고, 근거 spec을 링크합니다.
- 문서는 한국어로 씁니다. 파일 이름의 `<topic>`은 영어 소문자 kebab-case입니다.
