# views.d — 리소스별 컬럼 재정의

리소스 목록 화면(Pods, Deployments, Services …)에 어떤 컬럼을 어떤 순서로 보여줄지 바꾸는 설정입니다.
코드를 고치지 않고 내장 컬럼을 빼거나 순서를 바꿀 수 있고, 라벨·주석·spec 값 같은 객체 필드를 새 컬럼으로 추가할 수 있습니다.

## 빠르게 시작하기

```bash
k3stui --columns pods                                   # pods에 쓸 수 있는 내장 컬럼 확인
cp conf/views.d/pods.yaml.example conf/views.d/pods.yaml # 예제를 복사해 고칩니다
k3stui --check                                          # 잘못된 값이 있으면 [WARN]으로 알려줍니다
```

설정은 시작할 때 한 번 읽습니다. 파일을 고친 뒤에는 k3stui를 다시 실행하세요.

## 파일 위치와 이름

- 메인 설정 파일(`k3stui.yaml`)과 같은 디렉터리의 `views.d/` 안에 둡니다.
  - 저장소에서 실행: `conf/views.d/`
  - 설치본: `/opt/k3stui/conf/views.d/`
  - 사용자 설정(`~/.config/k3stui/k3stui.yaml`)을 쓰는 경우: `~/.config/k3stui/views.d/`
- `*.yaml`, `*.yml` 파일만 읽습니다. `*.yaml.example`은 읽지 않으므로 예제로 남겨 둘 수 있습니다.
- 리소스 하나에 파일 하나를 둡니다. 파일 이름(확장자 제외)이 리소스 키가 되며, 파일 안에 `resource:`를 적으면 그 값이 우선합니다.
- 리소스 키 대신 별칭도 쓸 수 있습니다 (`po.yaml` = `pods.yaml`). 같은 리소스를 두 파일이 재정의하면 경고를 내고 파일 이름 순서상 나중 파일을 씁니다.

## 설정 항목

```yaml
resource: pods          # 선택. 비우면 파일 이름을 씁니다
columns:                # 필수. 적은 순서대로만 표시합니다
  - name: NAME          # 내장 컬럼: 이름만 적습니다
  - name: QOS           # 사용자 컬럼: 제목과 path를 적습니다
    path: status.qosClass
    width: 12           # 선택. 최대 폭
```

| 항목 | 필수 | 값 | 의미 |
|---|---|---|---|
| `resource` | 아니요 | 리소스 키 또는 별칭 | 재정의할 리소스입니다. 비우면 파일 이름을 씁니다. 대소문자를 구분하지 않습니다 |
| `columns` | 예 | 목록 | 표시할 컬럼입니다. **적은 컬럼만, 적은 순서대로** 표시합니다. 비어 있거나 모두 잘못되면 내장 컬럼을 그대로 씁니다 |
| `columns[].name` | 예 | 문자열 | 컬럼 제목입니다. `path`가 없으면 같은 이름의 **내장 컬럼**을 가져옵니다 (대소문자 무시). 화면에는 대문자로 표시합니다 |
| `columns[].path` | 아니요 | 필드 경로 | 객체에서 값을 꺼낼 경로입니다 (아래 「path 문법」). 있으면 `name`은 제목으로만 씁니다 |
| `columns[].width` | 아니요 | 정수 | 컬럼 최대 폭(글자 수)입니다. 넘치면 `…`로 자릅니다. 아래 표를 참고하세요 |

`width` 값의 의미:

| 값 | 내장 컬럼 | path 컬럼 |
|---|---|---|
| 적지 않음 / `0` | 내장 최대 폭을 씁니다 (IMAGES·PORTS·SUBJECTS 등은 60, 나머지는 제한 없음) | 제한 없음 |
| 양수 | 그 폭으로 제한합니다 | 그 폭으로 제한합니다 |
| `-1` | 제한 없음 | 제한 없음 |

화면 폭이 모자라면 폭 설정과 관계없이 가장 넓은 컬럼부터 줄여서 맞춥니다.

## 동작 규칙

- **NAMESPACE 컬럼**: 네임스페이스 범위 리소스는 네임스페이스가 `all`일 때 맨 앞에 NAMESPACE 컬럼이 자동으로 붙습니다. 재정의 대상이 아니므로 `columns`에 적지 않습니다.
- **행 색상·정렬**: 상태에 따른 행 색상(Running/오류/경고)과 정렬 순서는 내장 규칙을 그대로 따릅니다. 컬럼을 빼도 바뀌지 않습니다.
- **필터(`/`)**: 화면에 보이는 컬럼 값만 검색합니다. 라벨로 거르고 싶으면 라벨을 컬럼으로 추가하세요.
- **작업 키**: 컬럼 구성과 관계없이 모든 작업(로그, 편집, 삭제 등)을 그대로 쓸 수 있습니다.
- **CPU, MEM 컬럼**: metrics-server 값입니다. 재정의에서 빼면 화면에서만 사라지고 조회는 계속합니다.

## path 문법

`path`는 객체(YAML로 보이는 구조, `y` 키로 확인)에서 값을 꺼내는 경로입니다. kubectl의 jsonpath와 비슷하지만 더 단순합니다.

| 형식 | 예 | 설명 |
|---|---|---|
| `a.b.c` | `spec.nodeName` | 점으로 하위 필드를 따라갑니다. 맨 앞의 점(`.spec.nodeName`)이나 `{}`로 감싼 형식도 받습니다 |
| `a["키"]` | `metadata.labels["app.kubernetes.io/name"]` | 점·슬래시가 들어간 키는 큰따옴표나 작은따옴표로 감쌉니다 |
| `a[n]` | `spec.containers[0].image` | 배열의 n번째 항목입니다 (0부터) |
| `a[*]` | `spec.containers[*].name` | 배열의 모든 항목입니다. 값은 쉼표로 이어서 표시합니다 |

값 표시 규칙:

| 값 종류 | 표시 |
|---|---|
| 문자열·숫자·true/false | 그대로 표시합니다 (`3`, `true`, `0.5`) |
| 맵 | `키=값`을 키 이름 순서로 쉼표로 이어서 표시합니다 (예: `metadata.labels` → `app=web,tier=front`) |
| 배열 | 항목을 쉼표로 이어서 표시합니다 |
| 없음 | 빈 칸으로 둡니다 (필드가 없는 객체도 오류가 아닙니다) |

자주 쓰는 path:

| 리소스 | 제목 예 | path |
|---|---|---|
| 모든 리소스 | APP | `metadata.labels["app.kubernetes.io/name"]` |
| 모든 리소스 | OWNER | `metadata.ownerReferences[0].name` |
| 모든 리소스 | CREATED | `metadata.creationTimestamp` |
| pods | QOS | `status.qosClass` |
| pods | SA | `spec.serviceAccountName` |
| pods | IMAGES | `spec.containers[*].image` |
| pods | HOST-IP | `status.hostIP` |
| pods | PRIORITY | `spec.priorityClassName` |
| deployments | STRATEGY | `spec.strategy.type` |
| deployments | SELECTOR | `spec.selector.matchLabels` |
| services | SELECTOR | `spec.selector` |
| services | TRAFFIC | `spec.externalTrafficPolicy` |
| nodes | ZONE | `metadata.labels["topology.kubernetes.io/zone"]` |
| nodes | INSTANCE | `metadata.labels["node.kubernetes.io/instance-type"]` |
| nodes | OS-IMAGE | `status.nodeInfo.osImage` |
| nodes | RUNTIME | `status.nodeInfo.containerRuntimeVersion` |
| nodes | TAINTS | `spec.taints[*].key` |
| persistentvolumeclaims | REQUEST | `spec.resources.requests.storage` |
| ingresses | TLS-HOSTS | `spec.tls[*].hosts` |
| cronjobs | TZ | `spec.timeZone` |

## 리소스 키와 내장 컬럼

`k3stui --columns`로도 같은 내용을 볼 수 있습니다. 리소스는 서버에 있을 때만 화면에 나타납니다 (예: Cilium·HelmChart CRD).

| 리소스 키 (파일 이름) | 별칭 | 범위 | 내장 컬럼 |
|---|---|---|---|
| `pods` | po, pod | 네임스페이스 | NAME, READY, STATUS, RESTARTS, CPU, MEM, IP, NODE, AGE |
| `deployments` | deploy, deployment | 네임스페이스 | NAME, READY, UP-TO-DATE, AVAILABLE, IMAGES, AGE |
| `statefulsets` | sts, statefulset | 네임스페이스 | NAME, READY, IMAGES, AGE |
| `daemonsets` | ds, daemonset | 네임스페이스 | NAME, DESIRED, CURRENT, READY, UP-TO-DATE, AVAILABLE, AGE |
| `replicasets` | rs, replicaset | 네임스페이스 | NAME, DESIRED, CURRENT, READY, AGE |
| `jobs` | job | 네임스페이스 | NAME, STATUS, COMPLETIONS, DURATION, AGE |
| `cronjobs` | cj, cronjob | 네임스페이스 | NAME, SCHEDULE, SUSPEND, ACTIVE, LAST SCHEDULE, AGE |
| `services` | svc, service | 네임스페이스 | NAME, TYPE, CLUSTER-IP, EXTERNAL-IP, PORTS, AGE |
| `ingresses` | ing, ingress | 네임스페이스 | NAME, CLASS, HOSTS, ADDRESS, PORTS, AGE |
| `endpointslices` | eps, ep, endpoints | 네임스페이스 | NAME, ADDRESSTYPE, PORTS, ENDPOINTS, AGE |
| `networkpolicies` | netpol, np | 네임스페이스 | NAME, POD-SELECTOR, AGE |
| `ciliumnetworkpolicies` | cnp | 네임스페이스 | NAME, VALID, AGE |
| `ciliumclusterwidenetworkpolicies` | ccnp | 클러스터 | NAME, VALID, AGE |
| `persistentvolumes` | pv | 클러스터 | NAME, CAPACITY, ACCESS, RECLAIM, STATUS, CLAIM, STORAGECLASS, PATH, AGE |
| `persistentvolumeclaims` | pvc | 네임스페이스 | NAME, STATUS, VOLUME, CAPACITY, ACCESS, STORAGECLASS, AGE |
| `storageclasses` | sc | 클러스터 | NAME, PROVISIONER, RECLAIM, BINDING, EXPANSION, AGE |
| `configmaps` | cm, configmap | 네임스페이스 | NAME, DATA, AGE |
| `secrets` | secret | 네임스페이스 | NAME, TYPE, DATA, AGE |
| `serviceaccounts` | sa | 네임스페이스 | NAME, AGE |
| `roles` | role | 네임스페이스 | NAME, AGE |
| `rolebindings` | rb | 네임스페이스 | NAME, ROLE, SUBJECTS, AGE |
| `clusterroles` | cr | 클러스터 | NAME, AGE |
| `clusterrolebindings` | crb | 클러스터 | NAME, ROLE, SUBJECTS, AGE |
| `nodes` | no, node | 클러스터 | NAME, STATUS, ROLES, CPU, MEM, VERSION, INTERNAL-IP, AGE |
| `namespaces` | ns, namespace | 클러스터 | NAME, STATUS, AGE |
| `events` | ev, event | 네임스페이스 | LAST SEEN, TYPE, REASON, OBJECT, MESSAGE |
| `helmcharts` | hc, helmchart | 네임스페이스 | NAME, CHART, VERSION, TARGET-NS, REPO, AGE |
| `helmchartconfigs` | hcc | 네임스페이스 | NAME, AGE |

내장 컬럼 중 의미가 덜 분명한 것:

| 컬럼 | 의미 |
|---|---|
| READY | 준비된 수/원하는 수입니다 (Pod는 컨테이너, 워크로드는 replica) |
| RESTARTS | 컨테이너 재시작 합계입니다. 마지막 재시작이 있으면 경과 시간을 함께 표시합니다 (`2 (27m ago)`) |
| CPU, MEM | metrics-server 사용량입니다. Node는 할당 가능량 대비 비율도 표시합니다. 조회할 수 없으면 `-`입니다 |
| IMAGES | Pod 템플릿의 컨테이너 이미지를 쉼표로 이은 값입니다 |
| PORTS (services) | `포트[:노드포트]/프로토콜` 형식입니다 |
| PATH (persistentvolumes) | local·hostPath 볼륨의 노드 경로 또는 CSI 볼륨 핸들입니다 |
| VALID (Cilium 정책) | Cilium이 정책을 받아들였는지(Valid 조건)입니다 |
| LAST SEEN (events) | 이벤트가 마지막으로 발생한 뒤 지난 시간입니다 |
| AGE | 객체 생성 후 지난 시간입니다 (`5m3s`, `4h`, `12d`) |

## 예제

이 디렉터리의 `*.yaml.example` 파일을 참고하세요.

| 파일 | 내용 |
|---|---|
| `pods.yaml.example` | QoS·서비스 계정·앱 라벨 추가, IP 제거 |
| `deployments.yaml.example` | 배포 전략과 셀렉터 추가, 이미지 폭 확대 |
| `nodes.yaml.example` | 영역(zone)·인스턴스 타입·OS·런타임·taint 추가 (클라우드 클러스터용) |
| `services.yaml.example` | 셀렉터와 트래픽 정책 추가 |
| `persistentvolumeclaims.yaml.example` | 요청 용량 추가 |

## 검증과 문제 해결

시작할 때 재정의를 검사해, 문제가 있으면 첫 화면 하단에 경고를 띄우고 로그(`k3stui.log`)에 남깁니다. 전체 목록은 `k3stui --check`로 봅니다.

| 경고 | 원인과 해결 |
|---|---|
| `알 수 없는 리소스 "xxx"` | 파일 이름이나 `resource` 값이 틀렸습니다. `k3stui --columns`로 이름을 확인하세요. 이 파일은 무시합니다 |
| `내장 컬럼 "XXX"가 없습니다` | `path` 없이 적은 이름이 내장 컬럼에 없습니다. 이름을 고치거나 `path`를 지정하세요. 이 컬럼만 빼고 나머지는 적용합니다 |
| `path가 잘못되었습니다` | 괄호·따옴표가 짝이 맞지 않거나 `[]` 안에 숫자·`*`·따옴표 키가 아닌 값이 있습니다. 이 컬럼만 뺍니다 |
| `쓸 수 있는 컬럼이 없어 내장 컬럼을 그대로 씁니다` | `columns`가 비었거나 모든 컬럼이 잘못되었습니다 |
| `재정의가 두 번 있습니다` | 같은 리소스를 두 파일(예: `pods.yaml`과 `po.yaml`)이 재정의했습니다. 하나를 지우세요 |
| 컬럼이 빈 칸입니다 | path가 가리키는 필드가 그 객체에 없습니다. 화면에서 `y`로 YAML을 열어 실제 경로를 확인하세요 |
| 설정 파일 파싱 실패 (시작 안 됨) | YAML 문법 오류입니다. 들여쓰기와 따옴표를 확인하세요. 오류 메시지에 파일 이름과 줄 번호가 나옵니다 |
