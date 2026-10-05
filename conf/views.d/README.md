# views.d — 리소스별 컬럼 재정의

파일 이름(확장자 제외) 또는 `resource:` 값이 리소스 키(pods, deployments, services ...)입니다.
`columns`를 적으면 그 순서대로만 표시합니다.

- `name`만 있으면 내장 컬럼 중 같은 제목을 씁니다 (NAME, READY, STATUS ...).
- `path`가 있으면 객체에서 점(.) 경로로 값을 꺼냅니다 (예: `spec.nodeName`, `metadata.labels`).
- `width`는 최대 폭입니다 (0 = 제한 없음).

`*.yaml.example` 파일은 읽지 않습니다. 이름을 `*.yaml`로 바꾸면 적용됩니다.
