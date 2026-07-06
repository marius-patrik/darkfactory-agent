# DarkFactory Adapter Contract

## Source And Layering

DarkFactory PRD `One system` defines DarkFactory as the GitHub control-plane
adapter and Agents Harness as the orchestration engine. DarkFactory L0/L3/L6
may run standalone while it is Actions-first, but its internal loop must map to
this contract instead of growing a second scheduler or brain.

Boundaries:

- DarkFactory owns GitHub translation: PRDs, issues, labels, comments, PRs,
  checks, merge policy, dashboards, and `.darkfactory/` ledgers.
- Agents Harness owns orchestration state: work units, stream lanes, scheduling,
  worker dispatch, observer events, non-progress handling, and result verdicts.
- `agents-core` should own the shared protobuf/schema when this contract crosses
  a process boundary.

## DarkFactory Loop Mapping

| DarkFactory loop | Harness contract surface | Notes |
| --- | --- | --- |
| L0 Orchestrator | scheduling + streams + observers | DarkFactory sends reconstructed GitHub state and desired waves; harness plans runnable work and emits status/non-progress events. |
| L2 Review | observers + review verdicts | DarkFactory mirrors PR review/check status into harness verdict fields; harness can request review work without owning GitHub checks. |
| L3 Work | dispatch + worker results | DarkFactory translates a ready issue into a harness work unit; harness assigns workers and returns artifacts/verdicts. |
| L4 Planning | observers | DarkFactory remains PRD/backlog owner; harness consumes only the resulting work graph. |
| L5 Audit | observers + dispatch | Audit findings enter as observer events or work units; harness should not duplicate policy scans owned by manager/DarkFactory. |
| L6 Orchestration | streams + wave gates | DarkFactory streams and waves become harness stream lanes and scheduling gates. |

## API Surface

The initial surface can be implemented as JSON over a CLI or local service. The
field names below are the stable contract; transport can move to `agents-core`
protobuf later.

### Submit Work Unit

DarkFactory submits one GitHub-backed unit of work.

Required fields:

- `adapter`: constant such as `darkfactory`.
- `external_id`: stable source id, for example `github:marius-patrik/repo#123`.
- `repo`: `owner/name`.
- `title`: issue title.
- `body`: issue body or generated worker brief.
- `acceptance`: explicit acceptance criteria extracted from the issue.
- `priority`: `P0`, `P1`, `P2`, or numeric equivalent.
- `stream`: stream lane name, matching `stream:<name>` labels.
- `blocked_by`: list of `external_id` dependencies.
- `labels`: GitHub labels relevant to scheduling.
- `branch`: requested branch name, if the adapter precomputes it.
- `target_base`: target branch, normally `dev`.

Optional fields:

- `wave`: wave id or name.
- `concurrency_key`: resource key for lane/package caps.
- `risk`: low, normal, high, or security.
- `owner_question_policy`: `issue`, `comment`, or `pause`.
- `deadline` / `not_before`: scheduling hints.
- `metadata`: adapter-specific values that must round-trip unchanged.

Response fields:

- `work_unit_id`: harness id.
- `state`: initial harness state.
- `accepted`: boolean.
- `rejected_reason`: typed validation reason when rejected.
- `normalized_blocked_by`: dependencies resolved against known work units.

### Declare Stream

DarkFactory declares lanes and caps before submitting or dispatching work.

Fields:

- `stream_id`
- `repo_scope`: repo, org, or workspace id.
- `priority_order`: priority sort policy.
- `max_in_flight`: total lane cap.
- `max_per_repo`: optional repo-local cap.
- `wave_gates`: ordered gates such as `hygiene`, `enforcement`, `features`.
- `ready_labels`: labels that make work eligible.
- `blocked_labels`: labels that keep work out of dispatch.

Harness state:

- `open`, `paused`, `draining`, `closed`.
- `in_flight_count`.
- `waiting_blockers`.
- `current_wave`.

### Schedule Tick

DarkFactory calls this after reconstructing GitHub state or on a scheduled run.

Input fields:

- `streams`: declared streams.
- `work_units`: submitted or updated work units.
- `observations`: latest PR/check/comment/label events.
- `caps`: global and per-stream concurrency caps.

Output fields:

- `dispatches`: work units the harness selected to run.
- `blocked`: work units still blocked, with reasons.
- `owner_questions`: questions DarkFactory must mirror to GitHub as
  `df:ask-owner` issues or comments.
- `status_events`: dashboard-ready state changes.

### Dispatch Worker

Harness dispatches work without caring whether DarkFactory currently runs the
worker in GitHub Actions, a self-hosted runner, or a future harness-managed
cluster.

Fields:

- `work_unit_id`
- `worker_kind`: implementer, reviewer, auditor, planner.
- `model_policy`: optional model/provider constraints.
- `repo_checkout`: repo and ref to check out.
- `brief`: issue/acceptance text and context pointers.
- `expected_outputs`: branch, PR, artifact, or review verdict.
- `validation`: commands or gates required before result submission.

DarkFactory adapter obligations:

- Create or update GitHub branch/PR/comment/check surfaces.
- Preserve one issue to one branch to one PR for implementation work.
- Mirror worker state back through observer events.

### Submit Worker Result

Fields:

- `work_unit_id`
- `worker_run_id`
- `state`: `succeeded`, `blocked`, `failed`, `needs_review`.
- `artifact_refs`: PR URL, commit SHA, proof file, logs, or dashboard links.
- `validation`: command results and check conclusions.
- `review_verdict`: pass/fail plus blocking findings when applicable.
- `blocked_reason`: typed reason when blocked.
- `owner_question`: question payload when human input is required.

Harness result states:

- `queued`
- `ready`
- `running`
- `blocked`
- `needs_owner`
- `needs_review`
- `validated`
- `merged`
- `failed`
- `killed_non_progress`

## Observer Events

DarkFactory mirrors GitHub facts as events; harness emits orchestration facts
back. Events are append-only and idempotent by `(source, external_event_id)`.

Inbound event examples:

- issue labeled/unlabeled.
- issue body changed.
- PR opened/synchronized/merged/closed.
- check run completed.
- comment command received.
- owner question answered.

Outbound event examples:

- work unit accepted/rejected.
- dispatch started.
- worker heartbeat missing.
- non-progress suspected or confirmed.
- review verdict recorded.
- owner question requested.
- stream cap reached.
- wave gate opened or closed.
- dashboard status changed.

## Composition With Harness Roadmap

- #1319 subagents/orchestration: DarkFactory L3 work becomes work units that
  can be executed by harness workers or recursive subagents. DarkFactory does
  not manage subagent recursion.
- #1320 non-progress kill: harness owns stuck-run detection and emits
  `killed_non_progress` or `needs_owner`; DarkFactory mirrors that to labels and
  comments.
- #1341 observability: harness emits metrics/traces/status events; DarkFactory
  uses GitHub dashboards as one observer sink.
- #1342 domain runtime: DarkFactory is an adapter/domain using the generic
  runtime, not a separate orchestration engine.

## Andromeda Concept Propagation

Issue #1350 is the durable propagation audit for Andromeda whole-system
concepts. The attached audit is
https://github.com/marius-patrik/agents-harness/issues/1350#issuecomment-4877906397.
It covered the VS2-VS6 harness roadmap issues (#1263-#1343), retired
Andromeda plan/design material, data-agentos `andromeda/wiki` and
`andromeda/research`, inference-engine and llm-gateway docs, plugin-rommie,
dream, and the DarkFactory M1-M5 roadmap.

Concrete source concepts from that audit map into the integrated architecture
as follows:

| Source concept | Source pointers | Integrated owner |
| --- | --- | --- |
| CAP-01/CAP-02/CAP-12/CAP-13 local runtime foundation: node identity, runtime root, backups, CLI rooting, credential materialization, redaction, and runner setup | #1263-#1269, #1272, #1273, #1283, #1290, #1291, Andromeda design 19 runtime structure | agents-core owns shared identity/schema; agents-manager owns materialization, credentials, setup, runners, and policy gates; harness consumes the resulting runtime state |
| D6/D15 no-false-green worker semantics: status vocabulary, single-worker loop, edit confinement, stop-guidance, validation verdicts | #1276, #1278-#1286, #1300 | inference-engine owns the agent loop; harness owns adapter acceptance/result contracts and non-progress status propagation |
| Gateway/provider orthogonality: local routing, switchers, Postgres model registry, OAuth dispatch, shared OAuth token source | #1270, #1271, #1274, #1287-#1289, #1294-#1301 | llm-gateway owns routing/registry/cloud dispatch; agents-manager owns secrets; harness records model policy constraints only |
| VS3 cluster substrate: Postgres primary/standby, k3s/kine, mTLS, secret sync, self-hosted runners, Dynamo/vLLM GPU tier, llama.cpp RAM tier, Knative detached compute, NATS liveness, leader election | #1275, #1277, #1302-#1311 | inference-engine owns runtime substrate and deploy proof; harness owns orchestration gates over substrate health |
| VS4 concurrent brain: thought lane, worker lane, blackboard, persistent voice slot, bulk-context lane, parity TUI, expandable brain view | #1312-#1316, Andromeda design 22 | harness owns brain/orchestration policy and client/control contracts; gateway and inference packages provide model execution |
| Memory and reflection: temporal replay, layered memory, retrieval, day-dream/deep-sleep, corpus batch | #1317, #1318, Andromeda design D7, plugin-rommie, dream | plugin-rommie and dream own memory/replay behavior; harness owns runtime integration points |
| Recursive subagents, orchestration role, and non-progress kill | #1319, #1320 | harness owns subagent/work-unit orchestration and stuck-run handling |
| VS4b heavy reasoning tier: KTransformers RAM-MoE, coder-unify benchmark, exclusive-GPU swap, hard reasoning proof | #1322-#1325 | inference-engine owns engine/fabric execution; harness scheduling policy chooses lanes and records benchmark gates |
| VS5 autolearn flywheel: trace store, strict train/test wall, eval brake, QLoRA/RAM-offload trainer, canary promotion, operator confirm, auto-revert, adapter-aware serving | #1326-#1332, Andromeda design 17 | harness owns domain/runtime policy and promotion contract; inference-engine and llm-gateway own training/serving substrate details |
| VS6 capstone: continuous live proof, self-hosting, closed-loop autolearn enablement, synthetic node-down/recover, hardening, release cutover | #1333-#1339 | whole integrated system proof across harness, inference-engine, llm-gateway, agents-manager, agents-core, plugin-rommie, and dream |
| Cross-cutting observability, generic domain runtime, agent seeding, and agent/provider orthogonality | #1340-#1343 | harness exposes orchestration/domain/agent registry contracts; telemetry sinks, provider dispatch, and memory hooks stay with their owners |
| QFT/autoresearch agenda, proof ledger, and research next steps | data-agentos `andromeda/research`, agentos-data#6, #1342 | data-agentos owns research/provenance; harness domain runtime can execute future research domains without vendoring the archive |

Per issue, the audit result is:

| Issues | Propagation result |
| --- | --- |
| #1263-#1269 | Runtime foundation represented, with backups/runners/setup/credential materialization moved out of harness scope through #1352, #1353, and #1354. |
| #1270-#1274 | VS1 engine/gateway salvage and S2 identity/CLI work represented by llm-gateway, inference-engine, agents-core, and harness compatibility entrypoints. |
| #1275-#1277 | GPU CDI and real smoke/CI concerns represented by VS3 substrate and integration proof, owned by inference-engine with harness acceptance visibility. |
| #1278-#1286 | VS2 server stack, Postgres bring-up, no-false-green state machine, single-worker loop, stop-guidance, and redaction represented by inference-engine plus manager-owned policy/materialization. |
| #1287-#1301 | Gateway, OAuth dispatch, shared token file, inline edit semantics, and Postgres registry represented by llm-gateway, agents-manager, and inference-engine; harness carries only adapter-facing model policy/result fields. |
| #1302-#1311 | VS3 cluster/runners/tiered fabric/pause-resume represented by inference-engine substrate and harness orchestration gates. |
| #1312-#1321 | VS4 brain/TUI/memory/subagents/non-progress represented by harness orchestration contracts, plugin-rommie/dream memory owners, and client/control surface issues. |
| #1322-#1325 | VS4b KTransformers heavy tier and coder scheduling represented by inference-engine fabric plus harness scheduling policy. |
| #1326-#1332 | VS5 autolearn represented by harness domain/runtime and promotion contracts over inference/gateway training and serving substrate. |
| #1333-#1339 | VS6 capstone represented as integrated-system proof and release cutover, not a harness-only implementation. |
| #1340-#1343 | Observability, domain runtime, and agent/provider orthogonality represented by harness-visible telemetry/domain/agent contracts with provider and memory behavior delegated. |

Issue-by-issue coverage index:

| Issue | Source concept audited | Propagated layer |
| --- | --- | --- |
| #1263 | S2 runtime foundation stream: CAP-02/01/12/13, CI runners, desktop archive risk | agents-manager plus agents-core; harness consumes managed runtime state |
| #1264 | Backup gate and pre-migrator snapshots | agents-manager operational setup/audit |
| #1265 | CAP-02 migrator to runtime schema | agents-core schema plus agents-manager materialization |
| #1266 | CAP-01 identity and `rommie` CLI entrypoint | agents-core identity schema plus slim harness CLI compatibility |
| #1267 | CAP-12 deploy keys, secrets materialization, redaction | agents-manager secrets and audit gates |
| #1268 | CAP-13 CLI wrapper and setup verification | agents-manager CLI rooting, harness delegates |
| #1269 | Self-hosted CI runners on s001/s002 | agents-manager/inference-engine runner substrate |
| #1270 | VS1 engine adapters: vLLM and llama.cpp | inference-engine execution substrate |
| #1271 | VS1 gateway salvage: local routing and switchers | llm-gateway routing |
| #1272 | CAP-02 migrator dry-run build | agents-core schema plus agents-manager migrator |
| #1273 | Rommie identity CLI grammar | agents-core identity contract plus harness CLI compatibility |
| #1274 | VS1 acceptance and TTFT benchmarks | inference-engine and llm-gateway proof surface |
| #1275 | GPU-in-Docker CDI reboot fix | inference-engine VS3 fabric |
| #1276 | RunStatus/status vocabulary reconciliation | agents-core contract plus inference-engine loop state |
| #1277 | Real gateway/engine smoke tests | inference-engine and llm-gateway integration proof |
| #1278 | VS2 single-node server stack | inference-engine agent loop and substrate |
| #1279 | Live single-node Postgres bring-up | inference-engine substrate |
| #1280 | Single-node Postgres live on s002 | inference-engine substrate |
| #1281 | No-false-green status state-machine core-lib | inference-engine loop contracts plus harness result semantics |
| #1282 | No-false-green status state-machine duplicate/carry-forward | inference-engine loop contracts plus harness result semantics |
| #1283 | Persistence-boundary redaction and secret materialization | agents-manager secrets/materialization |
| #1284 | VS2 single-worker agent loop | inference-engine agent loop |
| #1285 | Stop-guidance for open-ended small-model loops | inference-engine loop plus harness non-progress policy |
| #1286 | VS2 single-worker loop live proof | inference-engine proof with harness-visible verdicts |
| #1287 | Full gateway: PG registry, degrade-to-local, OAuth loader, switchers | llm-gateway registry/routing |
| #1288 | Cloud OAuth dispatch credential-gated follow-up | llm-gateway dispatch plus agents-manager secrets |
| #1289 | Full gateway autonomous scope | llm-gateway |
| #1290 | CLI wrapper structure, `rommie cli`, `rommie setup` | agents-manager CLI rooting; harness delegates |
| #1291 | Credential materialization live-proven auth | agents-manager credentials |
| #1292 | Capability/skills loader and registry sync | plugin policy and manager/plugin fixture split |
| #1293 | Go daemon and minimal coordination | inference-engine coordination plus harness bridge |
| #1294 | Kimi OAuth dispatch enablement | llm-gateway provider dispatch |
| #1295 | Kimi live loop proof | llm-gateway plus inference-engine proof |
| #1296 | Claude OAuth wiring and live proof | llm-gateway provider dispatch |
| #1297 | Codex Responses/thin adapter proof | llm-gateway provider dispatch |
| #1298 | Gemini/AGY adapter proof | llm-gateway provider dispatch |
| #1299 | Single shared OAuth token file | agents-manager secrets plus llm-gateway consumption |
| #1300 | Inline edit semantics and confinement split | inference-engine tool semantics plus harness acceptance |
| #1301 | Gateway Postgres registry cutover | llm-gateway plus agents-core contracts |
| #1302 | VS3 two-node k3s and tiered fabric epic | inference-engine cluster substrate |
| #1303 | Postgres primary and sync standby | inference-engine substrate |
| #1304 | k3s datastore on kine/Postgres | inference-engine substrate |
| #1305 | mTLS, cert-manager, secret sync, self-hosted runners | inference-engine plus agents-manager secrets/runners |
| #1306 | Dynamo GPU tier and cross-node tensor parallelism | inference-engine fabric |
| #1307 | llama.cpp RAM/GGUF tier | inference-engine fabric |
| #1308 | Knative Serving and detached-compute lane | inference-engine fabric |
| #1309 | Docker-to-k3s workload migration and model-route cutover | inference-engine plus llm-gateway registry |
| #1310 | Pause/resume, NATS liveness, coordination leader | harness coordination contract over inference substrate |
| #1311 | VS3 integration CI acceptance | inference-engine proof with harness orchestration gates |
| #1312 | VS4 concurrent brain, parity TUI, memory, multi-agent epic | harness orchestration engine |
| #1313 | Concurrent brain core: thought/workers/blackboard | harness orchestration engine |
| #1314 | Voice slot persistence and bulk-context lane | harness brain policy over gateway/fabric |
| #1315 | TUI renderer and session-protocol client | harness client/control surface |
| #1316 | Parity surface and expandable brain view | harness client/control surface |
| #1317 | Reflection engine and temporal replay | harness memory integration plus dream/plugin-rommie |
| #1318 | Memory store, retrieval, dreams, corpus batch | plugin-rommie/dream plus harness memory integration |
| #1319 | Recursive subagents and orchestration | harness orchestration engine |
| #1320 | Non-progress detection and kill | harness orchestration engine |
| #1321 | VS4 integration proof and CI | harness orchestration proof |
| #1322 | KTransformers RAM-MoE heavy tier | inference-engine fabric plus harness scheduling policy |
| #1323 | KTransformers engine integration on s002 | inference-engine fabric |
| #1324 | Coder-unify benchmark or exclusive-GPU swap | harness scheduling policy over inference-engine fabric |
| #1325 | VS4b hard-reasoning/GPU contention proof | inference-engine proof plus harness gates |
| #1326 | VS5 autolearn epic, brakes-first | harness domain/runtime policy |
| #1327 | Curated trace store and strict train/test wall | harness autolearn policy plus data boundary |
| #1328 | Eval brake and planted-bad-adapter acceptance | harness promotion/eval gates |
| #1329 | QLoRA and RAM-offload trainer | inference-engine training substrate plus harness policy |
| #1330 | Canary, operator-confirm, auto-revert promotion | harness promotion contract |
| #1331 | Adapter-aware serving and domain runtime | harness domain runtime plus llm-gateway serving |
| #1332 | VS5 acceptance proof | integrated autolearn proof |
| #1333 | VS6 4.0 prove-and-release capstone | whole integrated system proof |
| #1334 | Continuous live capstone harness | whole integrated system proof |
| #1335 | Rommie self-hosting proof | harness orchestration over integrated runtime |
| #1336 | Live autolearn cycle and closed-loop auto-promote | harness autolearn policy over integrated runtime |
| #1337 | Synthetic node-down/recover injection | inference-engine substrate plus harness pause/resume gates |
| #1338 | Stage-6 hardening and observability completeness | harness-visible observability plus owner package telemetry |
| #1339 | Release v4.0 cutover and publish gate | integrated release proof |
| #1340 | Cross-cutting observability, domain runtime, agent seeding epic | harness cross-cutting contracts |
| #1341 | Prometheus/Grafana/OTel/log observability stack | harness-visible telemetry over substrate services |
| #1342 | Generic multi-domain runtime | harness domain runtime |
| #1343 | Agent seeding and agent/provider orthogonality | harness agent registry plus plugin-rommie/gateway surfaces |

The audit found one true harness-side orphan and six ownership splits. These
were filed as verifiable follow-up issues:

| Issue | Title | Captures |
| --- | --- | --- |
| https://github.com/marius-patrik/agents-harness/issues/1351 | Represent control-plane work-unit adapter contract for DarkFactory streams | The missing harness-side work-unit/stream adapter contract for DarkFactory. |
| https://github.com/marius-patrik/agents-harness/issues/1352 | Split: move CLI rooting and credential setup to os/agents-manager | Provider CLI rooting, CLI execution, setup, and credential materialization ownership. |
| https://github.com/marius-patrik/agents-harness/issues/1353 | Split: move node identity and runtime-root schema to os/agents-core | Shared node identity/runtime-root schema and manager materialization. |
| https://github.com/marius-patrik/agents-harness/issues/1354 | Split: move audit gates to os/agents-manager enforcement | Source/secrets audit gates and no-false-green enforcement ownership. |
| https://github.com/marius-patrik/agents-harness/issues/1355 | Split: move context-engine memory plugin to plugins/plugin-rommie | Layered memory plugin behavior and temporal replay ownership. |
| https://github.com/marius-patrik/agents-harness/issues/1356 | Split: move sample plugin and skill fixtures out of agents-harness | Demo commands, sample plugins, and skill fixtures. |
| https://github.com/marius-patrik/agents-harness/issues/1357 | Split: move non-orchestration Andromeda docs to owning packages | Gateway, inference, manager, plugin, research, and root one-system docs currently outside harness ownership. |

No additional unfiled harness orphan remained after #1351-#1357. DarkFactory
keeps GitHub translation/enforcement; its runtime loops migrate onto the
harness surfaces below instead of retaining an independent scheduler or brain.

## Migration Sequence

1. **Contract-only:** DarkFactory continues Actions-first execution and links
   M3 to this document and #1351. The adapter logs the work-unit, stream,
   result, and observer fields using the names above.
2. **L0 scheduler handoff:** DarkFactory sends stream/work-unit snapshots to
   the harness scheduler. Harness VS3 gates (#1302-#1311) decide which work is
   runnable under node, runner, and fabric health; DarkFactory mirrors the
   chosen dispatches to GitHub.
3. **L3 worker handoff:** implementation, review, auditor, and planner workers
   run through harness dispatch and subagent orchestration (#1319). DarkFactory
   keeps branch, PR, check, and comment mirroring.
4. **L5 audit handoff:** audit findings enter as observer events or audit work
   units. Harness non-progress (#1320), observability (#1341), memory/reflection
   hooks (#1317, #1318), and autolearn signals (#1326-#1332) produce verdicts;
   DarkFactory posts those verdicts back to issues and PRs.
5. **Shared schema:** promote stable JSON fields into `agents-core` protobufs
   once two adapters or a remote process boundary need them.

## Non-Goals

- Harness does not parse PRDs or own GitHub label semantics.
- Harness does not bypass DarkFactory merge/review policy.
- DarkFactory does not implement a second worker scheduler, subagent brain, or
  non-progress detector once the harness surface is available.
