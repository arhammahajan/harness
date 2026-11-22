# Feature tracker

Tracks planned work for `harness`: a client-server architecture shipped as one Go binary, with the `harness` server owning multi-agent orchestration, the ACP and SDK client runtimes, and SQLite-backed token usage tracking across all harnesses, profiles, and model IDs, and thin clients (`acptui`, CLI commands, and `tmux-spaces`) connecting over the local server API. Check the code for current behavior.

| Status | Meaning |
| --- | --- |
| Shipped | Available in the current code |
| Partial | Some of the work is available |
| Proposed | Planned work with a design below |
| Deferred | Set aside for later |

## Where things live

| Area | Path or section |
| --- | --- |
| `harness` server, backend, protocol, and client SDK | `internal/harness/{server,backend,proto,client,workspace}`, §2.2 |
| Orchestration engine, drivers, and ownership handoff | `internal/harness/orch*`, §1 |
| Skills stubs and guides | `internal/harness/skills`, §1.8 |
| Embedded ACP protocol client and adapter registry | `internal/harness/orchacp`, §2.1 |
| Built-in ACP control pane TUI client | `internal/harness/acptui`, `cmd/harness/main.go`, §2.3 |
| Rich token usage tracking and SQLite DB (single writer in server) | `internal/harness/usage`, `internal/harness/usagestore`, §3 |

## Tracker

| ID | Area | Item | Status | Section |
| --- | --- | --- | --- | --- |
| ORC-1 | orchestration | Runs, tasks, mailboxes, dispatch instructions, and depth limits | Proposed | §1.2 |
| ORC-2 | orchestration | Provider SDK driver (preferred when an agent provides an SDK) | Proposed | §1.3 |
| ORC-3 | orchestration | ACP driver (fallback when no provider SDK exists) | Proposed | §1.3 |
| ORC-4 | orchestration | `orchd` API, event log, tokens, and waits | Proposed | §1.4 |
| ORC-5 | orchestration | `spaces task`, `watch`, and `skills` | Proposed | §1.5 |
| ORC-6 | orchestration | Pane watch grants (consent) | Proposed | §1.6 |
| ORC-7 | orchestration | TUI task board | Proposed | §1.7 |
| ORC-8 | orchestration | MCP toolkits and skills | Proposed | §1.8 |
| ORC-9 | orchestration | Recovery and task reconciliation | Proposed | §1.9 |
| ORC-10 | orchestration | Task ownership model and human takeover to a tmux CLI tab | Proposed | §1.10 |
| ACP-1 | acp | Full ACP JSON-RPC 2.0 client protocol, adapter registry, and FS/terminal callbacks in the `harness` server | Proposed | §2.1 |
| ACP-2 | acp-ui | Client-server entrypoint (`harness serve` / auto-start), bare `harness` TUI client, and `SessionController` | Proposed | §2.2 |
| ACP-3 | acp-ui | Bubble Tea control pane UI, session rail, structured timeline, and composer | Proposed | §2.3 |
| ACP-4 | acp-ui | Multi-agent switching, worker control, `convstore` resume, and native CLI pop-out | Proposed | §2.4 |
| USG-1 | usage | SQLite WAL token database (`usage.db`), schema, deduplication, and daily rollups | Proposed | §3.1 |
| USG-2 | usage | Multi-source ingestion (router JSONL, live ACP/SDK turns, and multi-profile transcripts) | Proposed | §3.2 |
| USG-3 | usage | Rich `harness usage` CLI and typed `--json` query API sortable by harness, profile, and model ID | Proposed | §3.3 |

---

## 1. Multi-agent orchestration

**Proposed.** The four implementation phases are in §1.13.

Use orchestration to assign a task with a written spec, supervise it, and record the result. When a user works directly with an agent, they type in its native CLI pane, steer the turn there, and answer permission prompts in the agent's own UI. The Inbox and `next` support that workflow.

### 1.1 Goal and the human boundary

A run groups tasks. Each task names an agent, profile, and working directory, and can depend on other tasks or name a parent. A user can create a task from the CLI or navigator. Agents can create tasks through the `spaces task` CLI or through MCP tools.

Human-owned sessions run each agent's native CLI inside tmux. Orchestrated worker tasks never drive terminals manually and never use `tmux send-keys` or screen scraping to dispatch or supervise agents. Every orchestrated worker runs headlessly through an agent-provided SDK when one exists, or through ACP otherwise. For example, when a user asks `antigravity` in a tmux pane to orchestrate `claude` workers, `antigravity` creates subtasks through `orchd`, and `orchd` drives each `claude` worker headlessly through the Claude Agent SDK or `claude-agent-acp` without opening a tmux pane. When `channel` is `auto`, `orchd` picks the provider SDK driver first if registered, falls back to the ACP driver if no SDK driver exists, and rejects dispatch with `orch_channel_unavailable` if the agent supports neither.

Every task has one explicit owner, either `orchestrator` or `human`. While `orchestrator` owns a worker, `orchd` drives the headless SDK or ACP process, streams updates to the task board and `spacecore`, relays questions, and wakes waiting callers. At any point, either after a turn completes or mid-turn, the user can transfer ownership of a headless worker to `human`. On takeover, `orchd` stops the headless SDK or ACP process and opens the same conversation in a tmux tab running the agent's native CLI (§1.10).

The `spaces task` CLI calls the daemon API and supports JSON output, stable exit and error codes, and server-side waits. The skill guides explain the commands to agents launched by `harness`.

Use an explicit task to run work in parallel, order work by dependencies, record an assignment and result, or dispatch into a space outside the current view. An assignment can also give the worker a parent for delegated tasks. Agents can start their own runs. The navigator opens the dispatch dialog on request.

### 1.2 Model (ORC-1)

A run records its creator, mailbox, and tasks. Creating a task outside a run opens a run. A task assigns one unit of work to one agent.

```jsonc
{
  "id": "t8f21a0c3d",
  "run_id": "r4d90bb17e",
  "parent": null,                 // parent task id when delegated
  "depth": 0,                     // parent-chain length, capped
  "title": "port screen fallback test",
  "spec": "TASK.md contents verbatim",
  "agent": "claude",              // canonical agents-registry id
  "profile": "default",           // harness profile name
  "model": "",                    // optional router model hint
  "cwd": "/home/u/dev/harness",
  "deps": ["t1c07ee510"],         // DAG, cycle-checked
  "channel": "auto",              // auto | sdk | acp
  "owner": "orchestrator",        // orchestrator | handing_off | human
  "ownership_epoch": 1,           // increments on every ownership transfer
  "pending_takeover": "",         // "" | "human" when queued for turn end
  "native_session_id": "sess_01", // provider conversation id for resume and takeover
  "created_by": "task:t0a11cc20e",// human | task:<id> | mcp:<id> | cli
  "creation_source": "mcp",       // cli | plugin | mcp | api
  "created_ms": 1765000000000,
  "state": "running",             // queued|ready|dispatching|running|
                                  // blocked|done|failed|cancelled|orphaned
  "kind": "",                     // blocked substate: question|permission|auth
  "dispatch": {
    "channel": "sdk",
    "adapter": "claude-agent-sdk",
    "session": "", "window": "", "pane": "", "pane_pid": 0,
    "started_ms": 1765000001000
  },
  "result": {"source": "worker", "text": "...", "exit_hint": "ok"},
  "allow_subtasks": true,
  "max_depth": 3
}
```

Task transitions depend on prerequisites and events from the driver.

```
queued ──deps satisfied──▶ ready ──dispatch──▶ dispatching ──confirmed──▶ running
                                                                  │
        ┌───────────────────────── channel evidence ─────────────┤
        ▼                         ▼                              ▼
     blocked(question|permission|auth)   channel lost      done | failed
        │  answer/cancel                  ▼                     ▲
        └────────▶ running ─────────▶ orphaned ──re-dispatch─▶ ready
```

- Use the hook store's blocked kinds for tasks. The kinds are `question`, `permission`, and `auth`.
- If the SDK or ACP process disappears before a terminal report, mark the task `orphaned`. `spaces task redispatch <id>` returns it to `ready`.
- Use the worker's `orch_done` or `spaces task done` report when it arrives first. If a headless turn ends without an explicit report call, set `result.source` to `"driver"` and record the final assistant output.
- Record state changes in the event log. Rebuild the stored view by replaying the log, as in T3 Code.

Each run and task has a mailbox. Message types are `status`, `question`, `answer`, `worker_done`, `escalation`, and `system`. `spaces task check --wait` blocks in the daemon until a message arrives for the caller, following Herdr's `events.wait`. The CLI takes its identity from the environment. It can address a run with `run:<id>` or a task with `task:<id>`. Each mailbox keeps 512 messages by default. When it reaches the limit, remove the oldest messages and record a `system` notice.

Prepend the worker identity and reporting commands to each dispatched spec, following Orca's approach.

```text
# Task assignment

Your task ID is t8f21a0c3d. The run ID is r4d90bb17e. The run creator is human:arham.

Report progress.

  spaces task send --task t8f21a0c3d --type status --text "…"

Ask a question, then wait for the answer.

  spaces task ask --task t8f21a0c3d "…"
  spaces task check --wait

Submit the report.

  spaces task done --task t8f21a0c3d --report-file REPORT.md [--exit fail]

Work on this assignment. Delegate only if the spec permits it. After you submit the report, stay idle.

## Delegate work

  spaces task create --agent AGENT --spec-file S.md --parent t8f21a0c3d
  spaces task wait t<child>
```

Include the delegation section when `allow_subtasks` is true and `depth < max_depth`. The default maximum depth is 3. Requests beyond the limit return `orch_depth_exceeded`.

### 1.3 Channels (ORC-2, ORC-3)

Both drivers implement the same Go interface in the root module.

```go
// internal/harness/orchapi
type Caps struct {
    Structured bool // protocol-level prompt/result
    Stream     bool // interim output observable
    Interrupt  bool // cancel mid-turn
    Resume     bool // reattach after restart
    AskRelay   bool // structured question relay
}

type ChannelEvent struct {
    Type string // state | output | question | usage | done | failed | orphaned
    Kind string // question | permission | auth
    Text string
    TsMs int64
}

type SessionRef struct {
    Agent           string
    Profile         string
    NativeSessionID string
    CWD             string
    ResumableInCLI  bool
}

type Driver interface {
    Name() string
    Probe(agent string) (Caps, bool)
    Dispatch(ctx context.Context, t orchapi.Task, preamble string) (Handle, error)
    Events(h Handle) <-chan ChannelEvent
    Answer(h Handle, questionID, answer string) error
    Cancel(h Handle, reason string) error
    CloseForTakeover(ctx context.Context, h Handle, mode string) (SessionRef, error)
    Release(h Handle) error
}
```

Drivers report events. `orchd` resolves task state, following Herdr's arbitration rule. With `channel: "auto"`, `orchd` selects `sdk` first when an agent-provided SDK adapter is registered, falls back to `acp` when no SDK adapter exists, and returns `orch_channel_unavailable` if neither exists. `orchd` never spawns or types into a tmux pane to drive an orchestrated worker.

#### Provider SDK driver (ORC-2)

Use the provider SDK driver whenever the target agent ships an official SDK or structured programmatic control server. Examples include the Claude Agent SDK for `claude`, Codex `app-server` for `codex`, and OpenCode's server SDK for `opencode`.

1. Resolve the worker's profile, shadow home, and router environment through `agents` and `spacelaunch`, matching `harness <agent> profile <profile>`.
2. Start the provider SDK worker bridge as a headless child process owned by `orchd` and attach the orchestration MCP bridge (`spaces __orch-mcp-bridge`) with the task token and `SPACES_ORCH_SOCKET`.
3. Create the session and capture `native_session_id` immediately from the SDK's session-init event so resume and human takeover (§1.10) open the exact on-disk conversation in the native CLI.
4. Send the preamble and spec through the SDK's prompt call. Stream structured turn events, tool calls, diffs, permission callbacks, token usage events (§3.2), and subagent records into `orchd`, `spacecore`, and `usagestore`.
5. Handle interrupts through the SDK's native cancel call and recovery through the SDK's session resume call.
6. Implement `CloseForTakeover` in `after-turn` and `now` modes. Wait for turn end or send the SDK interrupt, wait up to a bounded grace period for the SDK to flush its native session store to disk, close the child process so no file or SQLite locks remain, and return `SessionRef`.

#### ACP driver (ORC-3)

Use the ACP driver when no agent-provided SDK adapter exists, or when the caller sets `--channel acp`. Native ACP agents (`gemini --acp`, `opencode acp`, `vibe-acp`, `antigravity-acp`) and ACP wrappers (`claude-agent-acp`, `codex-acp`) speak line-delimited JSON-RPC 2.0 over stdio using the ACP client runtime in §2.1.

1. Resolve the worker's profile, shadow home, and router environment through `agents` and `spacelaunch`, matching `harness <agent> profile <profile>`.
2. Start the registered ACP subprocess owned by `orchd`, negotiate capabilities with `initialize`, and call `session/new` with the orchestration MCP bridge in `mcpServers`. Store the returned `native_session_id` on the task immediately.
3. Send the preamble and spec with `session/prompt`. Forward structured `session/update` notifications, tool calls, diffs, token usage events (§3.2), and `session/request_permission` callbacks into `orchd`, `spacecore`, and `usagestore`. Because headless workers have no tmux pane, the task board and Inbox display their live progress from these structured events.
4. An idle turn completion signals `done`, with the final assistant text recorded as the report. Call `session/cancel` to interrupt a turn and `session/load` or `session/resume` to reattach after a daemon restart or ownership return.
5. `CloseForTakeover` waits for turn completion in `after-turn` mode or calls `session/cancel` in `now` mode, waits up to a bounded grace period for persisted session state to flush, shuts down the ACP subprocess so no file locks remain, and returns `SessionRef`.

Read SDK and ACP adapter registrations from `~/.config/harness/acp.json` and probe installed binaries from `~/.t3/tools/*-acp` and the `agents` registry, including `vibe-acp` and `antigravity-acp`. Each registration declares its driver kind (`sdk` or `acp`) and whether its persisted session ID is directly resumable by the agent's interactive CLI.

### 1.4 Server daemon (`harness serve` / `orchd`) (ORC-4)

Run orchestration inside the local `harness` server daemon (`harness serve`, auto-started on demand by any client like the hook listener). Use `startstamp` to throttle restarts and `flock` to keep one server instance per user. The server owns all ACP and SDK subprocesses, orchestration runs and tasks, and `usage.db` writes. Daemon control commands are `harness server ensure|start|stop|status|schema` (aliased as `spaces orch ensure|start|stop|status|schema`).

Keep daemon files beside the hook files in `SPACES_HOOKS_RUNTIME`. The default is `${XDG_RUNTIME_DIR}/spaces`, with `/tmp/spaces-<uid>` as the fallback.

| File | Role |
| --- | --- |
| `orch.sock`, `orch.endpoint.json` | NDJSON requests and responses. Check tokens and peer credentials as for `h.sock`. |
| `orch.store.json` | Runs, tasks, mailboxes, ownership state, and hashed tokens. Write a temporary file with mode 0600 and rename it under `orch.store.lock`. |
| `orch.log.jsonl` | Accepted commands and receipts, state events, ownership transfers, and queued effects. |
| `orch-spool.jsonl` | Commands accepted while the daemon is down. Replay on startup. |
| `orch.server.lock`, `orch.startstamp`, `orch.log` | Instance lock, start throttle, and daemon log. |

The method table defines the NDJSON API and its stable error envelope. `spaces orch schema --json` prints the schema.

`run.new|list|get|close`,
`task.create|get|list|dispatch|cancel|redispatch|answer|done|takeover|return`,
`mail.send|check|list`, `wait.task`, `events.subscribe`,
`watch.request|list|revoke`, `pane.snapshot|pane.events`,
`server.ping|schema|stop`.

General errors are `orch_not_running`, `orch_not_found`, `orch_depth_exceeded`, `orch_deps_unsatisfied`, `orch_bad_state`, `orch_agent_unknown`, `orch_channel_unavailable`, `orch_ownership_conflict`, `orch_resume_unavailable`, `orch_timeout`, and `orch_denied`. Pane capture errors are `orch_watch_pending`, `orch_watch_denied`, `orch_watch_revoked`, `orch_pane_identity_mismatch`, `orch_capture_stale`, and `orch_bounds_exceeded`. The CLI uses exit codes 0, 1, and 2.

A mutating request can include a `command_id`. Record its receipt in the log. A retry returns the recorded "already applied" result.

`spaces task wait` checks task state and waits for task transitions. The pane methods read or forward the hook service's resolved state. The hook service owns pane status and pane transitions.

### 1.5 CLI commands (ORC-5)

Install `spaces` in `~/.local/bin` for worker and scripting commands. Use `tmux-spaces` for interactive workspace views and bare `harness` for the ACP control pane.

```
spaces orch ensure|start|stop|status|schema [--json]

spaces task create --agent A [--profile P] [--spec-file F | --spec-string S]
                   [--cwd D] [--model M] [--deps t1,t2] [--parent tX]
                   [--channel auto|sdk|acp] [--allow-subtasks]
                   [--max-depth N] [--dispatch]
spaces task list [--run R] [--json] | show <id> | dispatch <id>
spaces task cancel <id> [--reason S] | redispatch <id>
spaces task takeover <id> [--mode after-turn|now] [--space S] [--focus]
spaces task return <id> [--summary S]
spaces task wait <id> [--until done|failed|blocked|terminal] [--timeout S]
spaces task check [--wait] [--timeout S] [--json]
spaces task ask --task T TEXT
spaces task answer --id <msgId> TEXT | allow | deny
spaces task send --to TASK --type status --text S
spaces task done --task T [--report-file F | --report-string S] [--exit ok|fail]

spaces watch request --pane P | list | revoke <grant-id>
spaces pane read P [--lines N] | events P --after-seq N

spaces skills list|get <topic>|install|update|installed|remove [--json]
```

`tmux-spaces` opens `tasks`, `inbox`, `agents`, and `popup`. The orchestration commands call `orch.sock`. Workers inherit `SPACES_TASK` and `SPACES_TOKEN`, so they can call the commands without passing their identity explicitly.

### 1.6 Pane watch grants (ORC-6)

Use zellij-agent-tools' consent model for read-only tmux pane observation when a headless worker or coordinator asks to inspect an existing human pane, such as a dev server or test runner pane.

- Watching any tmux pane requires an explicit, revocable grant.
- Show the requesting agent when a watch request appears. Approve with `y` or deny with `n`. Grant states are `pending`, `approved`, `denied`, `expired`, and `revoked`.
- Bind a grant to `session_id:window_id:pane_id:pane_pid`. Check that identity on every read so a reused pane ID cannot inherit a grant.
- `pane.events --after-seq` returns bounded, coalesced changes from `capture-pane`. `pane.snapshot --lines N` returns the tail.
- Reject reads when the grant is absent or revoked, or when the capture is stale or the pane identity differs. The watch API is read-only. It never sends keystrokes or drives a pane. Authenticate access to pane metadata.

### 1.7 TUI task board (ORC-7)

The Tasks view reads `orch.store.json` and follows `events.subscribe`. It uses the navigator's Catppuccin theme.

| Key | Action |
| --- | --- |
| `T` | Show runs and their tasks, with state, owner, channel, dependencies, blockers, and age. |
| `D` on an agent | Open the spec editor, then create and dispatch the task. |
| `H` on a task | Show recent mailbox messages and the report. |
| `O` on a task | Take ownership of a headless SDK or ACP worker and open its conversation in a tmux tab running the native CLI. Prompts for `after-turn` or `now` when a turn is running. |
| `R` on a human-owned task | Return ownership from the tmux CLI tab back to `orchd` after the CLI exits. |
| `x` | Cancel the task. |
| `Enter` | Focus the tmux pane for a human-owned task, or open task details and takeover actions for a headless SDK or ACP task. |
| `u` | Revoke the selected row's watch grant. |

A watch request opens an approval dialog with `y` and `n`. Agent rows show a task badge alongside the pane's attention state.

### 1.8 MCP and skills (ORC-8)

Pass `spaces __orch-mcp-bridge` to SDK and ACP workers at session start. The bridge forwards stdio requests to `orch.sock` using the task token from the environment. Issue a token at dispatch, store its SHA-256 hash, and revoke it when the task reaches a terminal state or transfers to human ownership. Tokens expire after 24 hours.

The MCP tools are `orch_delegate` (async with wake or blocking wait), `orch_task_status`, `orch_task_cancel`, `orch_ask`, `orch_check`, `orch_done`, `orch_watch_request`, and `orch_read_pane`. Workers and interactive human-driven coordinators can also invoke the corresponding `spaces task` CLI commands from shell tools when MCP is not configured.

`orchd` checks `owner == "orchestrator"` and `ownership_epoch` on every mutating coordinator call. If a human took over a child worker, coordinator writes return `orch_ownership_conflict`, and `orch_check` delivers the `ownership_transferred` system notice so the coordinator does not retry or overwrite the human session.

Each skill has a short `skills/<topic>/STUB.md` with `name` and `description` frontmatter, plus a `GUIDE.md` with instructions. Both ship in the binary through `go:embed`, so `spaces skills get <topic>` returns the guide for that binary's version. Install stubs under `~/.agents/skills/<name>/SKILL.md`. Use the agent registry to link them into harness directories and keep local edits on reinstall.

The first guides cover orchestration, tmux-spaces navigation and attention, and router model selection. Keep review loops, handoffs, and coordinator patterns in the guides.

### 1.9 Recovery (ORC-9)

At startup, replay `orch-spool.jsonl` and rebuild the stored views from `orch.log.jsonl`. Check every non-terminal task, retry queued effects without applying them twice, and write `system` messages.

- For an `orchestrator`-owned SDK or ACP task, resume the saved `native_session_id` through the SDK's resume call or ACP `session/load` or `session/resume` if the adapter supports it. Mark other tasks `orphaned`.
- For a `human`-owned task, check `tmux list-panes -F` and the hook record. Keep the pane link active if the pane still exists and its compound identity matches. If the pane closed, retain the saved `native_session_id` so the user can reopen or return ownership.
- For a task stuck in `handing_off` across a daemon crash, verify that the headless process has exited and check whether a matching resumed tmux pane already exists. If the pane exists, finish setting `owner` to `human`. If no pane exists and `native_session_id` is saved, complete the takeover by opening the resumed tmux tab or mark the task `orphaned` for explicit retry.

`spaces orch status` and `harness doctor` report daemon, socket, store, and log health. They also check SDK and ACP registration and skill installation.

### 1.10 Ownership model and human takeover (ORC-10)

Every orchestrated conversation obeys a single-writer rule. A headless ACP or SDK process and an interactive native CLI process never hold the same `native_session_id` open at the same time. `orchd` enforces this rule with two fields on every task: `owner` (`orchestrator`, `handing_off`, or `human`) and a monotonic `ownership_epoch` that increments on every transfer.

```text
orchestrator (headless ACP/SDK in orchd)
    │
    │  task.takeover (after-turn or mid-turn now)
    ▼
handing_off (freeze coordinator writes, drain or cancel turn, flush & close ACP)
    │
    │  open tmux tab with native CLI resumed on native_session_id
    ▼
human (native CLI in tmux pane, observed passively by spacecore)
    │
    │  optional: task.return (after native CLI exits and releases session lock)
    ▼
orchestrator (reload native_session_id via ACP session/load or session/resume)
```

Transfer paths:

1. **Takeover after completion or at turn end (`--mode after-turn`).** When the task is already `done`, `failed`, `blocked`, or idle, `orchd` sets `owner` to `handing_off`, increments `ownership_epoch`, calls `CloseForTakeover(..., "after-turn")` on the headless adapter to flush and close the session, and opens a new tmux window in the chosen space using `spacelaunch` plus `spaceresume.ResumeArgs` with the task's `agent`, `profile`, `native_session_id`, and `cwd`. For a `claude` worker, the new tmux tab runs the native `claude` CLI resumed to that conversation instead of ACP. When a turn is still running and the user selects `after-turn`, `orchd` sets `pending_takeover` to `"human"`, blocks new coordinator prompts, waits for the active turn to finish, and then runs the same close-and-open transition.
2. **Mid-turn takeover (`--mode now`).** When the user wants to interrupt a running headless worker and steer it directly, `orchd` sets `owner` to `handing_off`, increments `ownership_epoch`, and calls `CloseForTakeover(..., "now")`. The driver sends `session/cancel` or the provider SDK interrupt, waits up to a bounded grace period for the agent to persist partial messages and completed tool results, terminates the subprocess if it does not exit cleanly, and opens the tmux tab with the native CLI resumed on `native_session_id`.
3. **Post-takeover observation and coordinator notice.** Once the tmux pane starts, `orchd` sets `owner` to `human`, writes the new pane identity (`session`, `window`, `pane`, `pane_pid`) into `dispatch`, and stops managing the conversation over ACP. From that point, `spacecore` observes the pane through hooks, `model-router`, OSC 7501, and screen rules. `orchd` revokes the worker's automated token and posts a `system` message (`ownership_transferred`) to the parent run and task mailboxes so the coordinator, such as `antigravity`, knows the human took control and stops waiting. Any stale coordinator write carrying an older `ownership_epoch` fails with `orch_ownership_conflict`.
4. **Fallback when exact CLI resume is unavailable.** If an adapter does not share an on-disk session store with its native CLI, or if a mid-turn cancel occurred before turn 1 persisted a `native_session_id`, `orchd` returns `orch_resume_unavailable` instead of silently dropping context. The CLI and Tasks view offer an explicit fallback that opens a fresh native CLI tab pre-seeded with the task spec, mailbox messages, and recent ACP transcript output.
5. **Optional return to the orchestrator (`spaces task return <id>`).** After finishing interactive steering in the tmux CLI tab, the user exits the native CLI so its session file locks close, then runs `spaces task return <id>` or presses `R` on the task board. `orchd` verifies the pane's CLI process has exited, increments `ownership_epoch`, reloads `native_session_id` over ACP or SDK via `session/load` or `session/resume`, records any optional human summary in the parent mailbox, and returns `owner` to `orchestrator`.

### 1.11 Deltas from the Zellij-era plan

| Zellij-era design | tmux equivalent |
| --- | --- |
| WASM plugin with a popup opener | Bubble Tea via `display-popup`. Shipped. |
| `SessionUpdate` and `GlobalAttentionUpdate` | `list-panes`, `list-windows -F`, and `@spaces_*` options. The hook store supplies authoritative state. |
| Server parses OSC 7501 | `spaceresume` writes `@spaces_*` pane options. `pipe-pane` and `__osc-watch` keep OSC as a fallback. |
| `zj prompt` sends to the focused pane | `send-keys -t %<pane>` targets a pane in any session. |
| `zellij action dump-screen` polling | `capture-pane -p -S -<n>` reads scrollback in attached or detached sessions. |
| Client-socket handoff between spaces | `switch-client -t <session>:<window>.<pane>` |
| KDL layout restore and `session_index.sqlite` | Native snapshots and restore in `internal/resurrect`. |
| `zj wait` polling | `tmux-spaces wait` polls the resolved snapshot. |
| Plugin permissions such as `RunCommands` | Same-user Unix socket checks and task tokens. |
| `zjcli`, `zjresume`, and `zjcore` behind `zj` | `spaces` for helpers and `tmux-spaces` for the navigator and interactive CLI. Shared packages are `spacecore`, `spacelaunch`, and `spaceresume`. |

The orchestration model carries over. It retains runs, tasks, mailboxes, dependencies, depth limits, the worker preamble, ACP, MCP, skills, the event log, and retryable commands and effects.

### 1.12 Package layout and ownership

Keep the daemon, drivers, bridge, and skills in `harness`. `tmux-spaces` keeps its task client and views behind its TUI `Backend` interface without implementing ACP or orchestration itself.

```text
internal/harness/orchapi/      wire types, method table, error codes, schema
internal/harness/orchcore/     state machine, ownership lock, store/log/spool, daemon, tokens, outbox
internal/harness/orchsdk/      agent-provided SDK drivers (Claude Agent SDK, Codex app-server, OpenCode)
internal/harness/orchacp/      ACP JSON-RPC 2.0 client, callbacks, adapter registry, and takeover flush
internal/harness/orchhandoff/  tmux tab opener for ORC-10 human takeover and ORC-6 read-only pane watch
internal/harness/orchmcp/      MCP bridge + toolkits
internal/harness/skills/       stubs/guides (go:embed), install fan-out
```

| Owner | Packages and responsibilities |
| --- | --- |
| E | `orchapi` and `orchcore`. State machine, ownership epochs, store, log, spool, daemon, tokens, waits, method table, and `noop` driver. |
| F | `orchsdk`, `orchacp`, `orchhandoff`, and `orchmcp`. Use E's packages, `spacelaunch`, `agents`, and `spaceresume` without editing them. |
| G | `skills`, dispatch for `orch`, `task`, `watch`, and `skills` in `cmd/harness`, and smoke scripts. |
| H | `tmux-spaces` task client, Tasks view, takeover actions, and watch approvals behind `Backend`. |

Go's module boundary keeps `tmux-spaces` from importing `harness`'s internal packages. `tmux-spaces` communicates over `orch.sock` or `harness` CLI JSON outputs.

### 1.13 Phasing

1. **M1.** Build `orchapi` and `orchcore` with the v1 method table, state machine, ownership fields, store, event log, spool replay, and mailboxes. Wire `spaces orch`, `spaces task`, and `check --wait`. Run every command against fake tasks using the `noop` driver.
2. **M2.** Implement the provider SDK driver (`ORC-2`) and ACP driver (`ORC-3`) with `auto` selection preferring `sdk` over `acp`, protocol negotiation, `native_session_id` capture, structured updates into `spacecore`, cancellation, resume, and the Tasks view (`ORC-7`).
3. **M3.** Add `ORC-10` ownership transfer (`spaces task takeover` in `after-turn` and `now` modes, plus `spaces task return`) opening resumed native CLI tabs, and add read-only pane watch grants (`ORC-6`).
4. **M4.** Add task tokens, the `orch_*` MCP tools (`ORC-8`), ownership-epoch enforcement on coordinator writes, async delegation with wake, blocking waits, and question relay. Ship the orchestration guide through `spaces skills`.

### 1.14 Testing bar

- Run `go test ./...` with temporary directories and environment overrides. Use fake SDK adapters, fake ACP processes, and fake tmux runners so tests need no real home directory, network, mux, or agent.
- Table-test command and event sequences for depth limits, dependency order, cycle rejection, stale receipts, ownership epochs, and deterministic replay.
- Test the Unix socket in a temporary runtime directory. Use `net.Pipe` for fake peers and an injectable clock for long-poll wake. Cover authorization and peer credentials.
- Test `auto` driver selection. Verify that an agent with both SDK and ACP adapters picks `sdk`, an agent with only ACP picks `acp`, and an agent with neither returns `orch_channel_unavailable` without opening a tmux pane.
- Use fake SDK and ACP adapters for protocol negotiation, prompts, `native_session_id` persistence, updates, question callbacks, cancellation, and `CloseForTakeover`.
- Test `ORC-10` takeover and return end-to-end with fake SDK/ACP drivers and a fake tmux tab opener. Verify `after-turn` waits for turn completion, `now` cancels and flushes before opening the resumed tmux tab, no headless process stays alive once the tmux tab opens, stale coordinator calls return `orch_ownership_conflict`, missing session IDs return `orch_resume_unavailable`, crash recovery settles `handing_off`, and `task return` refuses while the native CLI PID is still active.
- Test `ORC-6` read-only watch grants for compound pane identity checks, revocation, and rejection of ungranted reads.
- Verify MCP tool calls produce the expected commands and events. Test token issue, revocation on takeover, and expiry with an injected clock.
- Test skill planning and installation in a fake home. Confirm that reinstalls preserve local edits and leave the same symlinks.
- In `scripts/spaces-orch-smoke.sh`, create a task, dispatch it to a fake headless adapter, complete it, and receive the result through `check --wait`.

### 1.15 Non-goals and future work

- Run one `orchd`. The headless SDK and ACP workers plus the MCP bridge account for the extra processes needed per task.
- Do not drive terminals manually for agent orchestration. `orchd` never dispatches or controls orchestrated workers via `tmux send-keys` or screen scraping. Every worker runs through an agent-provided SDK or ACP; tmux tabs open only when a human takes ownership under `ORC-10`.
- Keep explicit `harness <agent>` launches on each agent's native CLI. Reserve the built-in ACP control pane (§2) for bare `harness` invocations and headless worker inspection.
- Use the JSON store and size-limited JSONL log for `orchd`. If volume calls for SQLite, swap the store that reads and writes the current view.
- Add `--worktree new` after basic dispatch is ready.
- Prove the local workflow before adding remote hosts. Keep the Unix socket method table usable by a future relay.

---

## 2. ACP protocol implementation and built-in control pane

**Proposed.** Builds on `ORC-2` and `ORC-3` (§1.3), `ORC-4` (§1.4), `ORC-10` (§1.10), `convstore`, `spacecore`, and `usagestore` (§3).

Running bare `harness` with no agent argument on an interactive terminal opens a built-in Bubble Tea ACP control pane inside the `harness` Go binary. Explicit invocations such as `harness claude` or `harness codex profile work` still exec each agent's native CLI untouched. Bare `harness` gives the user one native Go TUI that drives any registered SDK or ACP agent directly, monitors and approves headless `orchd` workers, records token usage into `usage.db`, and pops any session out into a native CLI tmux tab on demand.

### 2.1 Full ACP protocol client implementation (ACP-1)

**Proposed.**

Implement a complete, reusable ACP client in `internal/harness/orchacp` running inside the `harness` server. One server-hosted ACP runtime serves both human interactive sessions opened from `acptui` (§2.2) and orchestrated worker tasks (§1.3). Sessions survive TUI detach or crash, and no client (`acptui`, CLI scripts, or `tmux-spaces`) implements ACP itself.

- **Transport and framing.** Speak line-delimited JSON-RPC 2.0 over child process stdio (`stdin`/`stdout`), capturing `stderr` into a bounded ring buffer for diagnostics and crash reports. Support bidirectional request/response correlation (client requests to agent and agent callbacks to client), cancellation, request timeouts, and clean process-group teardown on shutdown.
- **Adapter registry and discovery.** Load custom registrations from `~/.config/harness/acp.json` and auto-probe native ACP binaries (`gemini --acp`, `opencode acp`, `vibe-acp`, `antigravity-acp`) and ACP wrappers (`claude-agent-acp`, `codex-acp`) from `~/.t3/tools/*-acp` and the `agents` registry. Resolve each adapter's profile, shadow home, and router environment through `agents` and `spacelaunch`.
- **Capability negotiation and session lifecycle.**
  - `initialize`: exchange protocol version and client capabilities (`fs.readTextFile`, `fs.writeTextFile`, `terminal`, `mcp`).
  - `session/new`: pass `cwd`, configured `mcpServers`, and profile metadata; capture `sessionId` (`native_session_id`) immediately.
  - `session/load` and `session/resume`: reattach prior conversations when advertised by the adapter.
  - `session/prompt` and `session/cancel`: submit structured prompt blocks (text, workspace file attachments, images when supported) and interrupt running turns.
  - Runtime configuration: apply `session/set_mode` and `session/set_model` when advertised by the adapter.
- **Inbound `session/update` stream.** Normalize all agent notifications into typed Go events:
  - Assistant text deltas and thinking/reasoning deltas.
  - Tool call lifecycle (`tool_call`, `tool_call_update` with status, live stdout/stderr, and file diffs).
  - Plan and subagent progress updates.
  - Per-turn and cumulative token usage notifications, forwarded directly to `usagestore` (§3.2).
- **Permission callbacks (`session/request_permission`).** Route incoming permission requests to the active UI or `orchd` approval queue with the tool name, command or file diff, and adapter-provided option IDs. Publish `blocked` with kind `permission` to `spacecore` while awaiting a decision, and return the chosen option ID or `cancelled`.
- **Client filesystem callbacks (`fs/*`).** Implement `fs/read_text_file` (with line offset and limit) and `fs/write_text_file` scoped to the session workspace root or linked Git worktree. Reject out-of-tree paths unless explicitly approved and compute unified diffs for timeline rendering.
- **Client terminal callbacks (`terminal/*`).** Implement `terminal/create`, `terminal/output`, `terminal/wait_for_exit`, `terminal/kill`, and `terminal/release` over bounded PTY or pipe runners. Stream live command output into collapsible tool blocks and terminate child process groups cleanly on `Esc` or session close.

### 2.2 Client-server architecture modeled on Crush and OpenCode (ACP-2)

**Proposed.**

`harness` ships as one Go binary using the client-server architecture of Crush (`internal/{server,backend,proto,client,workspace}`) and OpenCode (`packages/server`): an HTTP/2 + SSE local server hosting an embedded ACP and SDK backend, with thin clients connecting over a per-user Unix domain socket or TCP.

#### Transport, addressing, and server lifecycle

- **Default socket address.** Bind `unix://$XDG_RUNTIME_DIR/harness-<uid>.sock` by default, falling back to `/tmp/harness-<uid>.sock` when `XDG_RUNTIME_DIR` is unset or the path exceeds 104 bytes (the macOS `sun_path` limit). Support explicit `--host http://127.0.0.1:<port>` or `--host tcp://...` on `harness server` and `--host` on clients for remote or headless setups.
- **Protocol and security.** Serve HTTP/1.1 and unencrypted HTTP/2 (h2c) over the Unix socket or TCP with compression disabled on local sockets. Enforce same-UID peer credential checks (`SO_PEERCRED` on Linux, `getpeereid` on BSD/macOS) on Unix sockets, and bearer token / pairing authentication when bound to TCP.
- **Auto-start, spawn lock, and safe shutdown.** Clients dial the default socket first. If no server is listening, the client acquires a spawn lock file, starts a detached `harness server` child process, and waits for `/v1/health` to return `200`. If a running server reports an older version via `/v1/version`, the client requests `POST /v1/control` with `shutdown_if_idle` so an idle stale server restarts cleanly without interrupting active ACP sessions. Once shutdown begins, `/v1/health` returns `503` and the server gives active handlers a 15-second drain budget. `SIGHUP` reloads config without killing running sessions.

#### Workspace-scoped `/v1` REST + SSE API

Define all routes in one endpoint table (`internal/harness/server/endpoints.go`) that drives both the HTTP mux and `/v1/docs/openapi.json`:

- **System & presence.**
  - `GET /v1/health`, `GET /v1/version`, `GET /v1/config`
  - `POST /v1/control` (`shutdown`, `shutdown_if_idle`)
  - `DELETE /v1/clients/{client_id}` (retires a client UUID and releases its presence claims)
- **Workspaces & sessions (multi-workspace server).**
  - `GET/POST /v1/workspaces`, `GET/DELETE /v1/workspaces/{id}`
  - `POST /v1/workspaces/{id}/current-session?client_id=<uuid>` (per-client presence tracking)
  - `GET /v1/workspaces/{id}/events` (workspace SSE stream) and `GET /v1/events` (global SSE feed for `tmux-spaces` across all workspaces)
  - `GET/POST /v1/workspaces/{id}/sessions`, `GET/PUT/DELETE /v1/workspaces/{id}/sessions/{sid}`
  - `POST /v1/workspaces/{id}/sessions/{sid}/prompt`, `POST /v1/workspaces/{id}/sessions/{sid}/cancel`, `POST /v1/workspaces/{id}/sessions/{sid}/takeover`
  - `GET /v1/workspaces/{id}/sessions/{sid}/messages`
  - `POST /v1/workspaces/{id}/permissions/grant`, `POST /v1/workspaces/{id}/questions/answer`
- **Orchestration & token usage.**
  - `GET/POST /v1/runs`, `GET/POST /v1/tasks`, `POST /v1/tasks/{tid}/dispatch|cancel|takeover|return|answer|done`
  - `GET /v1/usage` (query `usage.db` grouped and sorted by `harness`, `profile`, `model_id`, `day`, `session`, or `cwd`)
  - `POST /v1/usage/sync` (trigger incremental cursor sync from `spacecore` `Stop` hooks)

#### Embedded ACP backend, pubsub fan-out, and client reconnect

- **Backend with embedded ACP client (`internal/harness/backend`).** Whereas Crush's backend runs an in-process LLM loop per workspace, `harness`'s backend embeds `orchacp` (§2.1) and `orchsdk` (§1.3). Each session in a workspace owns a server-managed ACP or SDK subprocess. All inbound ACP `session/update` streams, `session/request_permission` callbacks, `fs/*` diffs, and `terminal/*` outputs publish onto the workspace `pubsub` bus, which fans out simultaneously to:
  1. Typed `proto` SSE envelopes for all attached clients (`acptui`, CLI, `tmux-spaces`).
  2. `usagestore` (single-writer `usage.db` updates on every turn completion, §3.2).
  3. `spacecore` (`h.sock` and `@spaces_*` tmux pane options, zero screen scraping).
- **Frontend `Workspace` interface and reconnect loop (`internal/harness/workspace`, `internal/harness/client`).** Every frontend (`acptui`, CLI commands, MCP bridge) programs against a `Workspace` interface with a per-process `client_id` UUID, backed by `ClientWorkspace` over the HTTP+SSE SDK (plus an in-process `BackendWorkspace` option for tests). The SSE subscription loop handles `ErrStreamClosed`, `ErrWorkspaceGone`, and `ErrServerUnreachable` by emitting `ConnectionDegraded` and `ConnectionRecovered` messages to Bubble Tea, re-registering the workspace if the server restarted, and resyncing missed state without killing running ACP agents.
- **Entrypoint (`cmd/harness/main.go`).** Keep `HARNESS_AGENT` direct-exec resolution first so external callers such as T3 Code keep working unchanged. Explicit `harness <agent>` still execs that agent's native CLI. Bare `harness` checks whether stdin and stdout are TTYs: non-TTY prints usage to stderr and exits with code 2; TTY auto-starts or attaches to the local `harness` server and launches `acptui.Run()`. Add `harness server [--host ...]` and `harness ui [--host ...] [--agent ...] [--profile ...] [--cwd ...] [--session ...]`.

```text
Thin clients (any number attached simultaneously, each with a client_id UUID):
  ├── bare `harness` (TTY) ──▶ internal/harness/acptui (Bubble Tea TUI)
  │                              └── internal/harness/workspace (ClientWorkspace + SSE reconnect)
  ├── CLI / MCP bridge     ──▶ internal/harness/client (HTTP+SSE SDK)
  └── tmux-spaces          ──▶ GET /v1/events, GET /v1/usage, POST /v1/tasks/*
            │
            ▼  HTTP/1.1 + h2c REST & SSE over unix://$XDG_RUNTIME_DIR/harness-<uid>.sock (or TCP)
     internal/harness/server (`harness server`, auto-started with spawn lock)
      └── internal/harness/backend (multi-workspace manager + pubsub fan-out)
            ├── Embedded ACP/SDK ──▶ orchacp (ACP-1) / orchsdk (ORC-2)
            ├── Orchestration    ──▶ orchcore (ORC-1..ORC-10)
            ├── History & resume ──▶ convstore (SQLite WAL + FTS5)
            ├── Pop-out handoff  ──▶ orchhandoff (resumed native CLI tmux tab)
            ├── Token accounting ──▶ usagestore (single writer to usage.db, §3)
            └── Live telemetry   ──▶ spacecore h.sock + @spaces_*
```

### 2.3 Control pane UI, timeline, and composer (ACP-3)

**Proposed.**

Match the Catppuccin Mocha palette and keybindings from `tmux-spaces` so bare `harness` fits alongside `tmux-spaces` popups.

- **Header bar.** Show the active agent, profile, canonical model ID, access level, Git branch or worktree, running token and estimated cost totals from `usagestore` (§3), and total active sessions.
- **Collapsible session rail.** Toggle with `Ctrl-b` or switch focus with `Tab`. List direct local sessions and live `orchd` workers with attention status icons (`blocked`, `working`, `done`, `failed`), channel badge (`sdk` or `acp`), and elapsed turn time. Support `n` to open a new session picker for any registered SDK or ACP agent and profile.
- **Structured streaming timeline.** Render streamed assistant Markdown incrementally, collapse thinking blocks behind a toggle, display tool calls with live stdout/stderr and exit status, render colored unified diffs for file edits, and show subagent rows. Scroll with `PgUp`/`PgDn` or mouse wheel without losing auto-follow when at the bottom.
- **Composer and slash commands.** Support multiline editing, bracketed paste, `@` fuzzy workspace file attachment, and slash commands: `/agent`, `/profile`, `/model`, `/new`, `/resume`, `/diff`, `/usage`, `/cancel`, `/handoff`, and `/quit`. Submitting while a turn is running adds the prompt to a local editable follow-up queue, and `Esc` sends `session/cancel` or the SDK interrupt call.

### 2.4 Orchestration integration, resume, and native CLI pop-out (ACP-4)

**Proposed.**

- **Inline control of `orchd` workers.** Selecting an `orchd` worker in the session rail displays its live structured stream, mailbox messages, and pending approval or question cards. The user can approve or deny requests, answer `orch_ask` questions, or cancel the worker directly from the control pane without switching to the `tmux-spaces` popup.
- **Pop-out to a native CLI tmux tab (`Ctrl-o` or `/handoff`).** Any direct local session or `orchd` worker can be handed off to the agent's native CLI in a tmux tab. The control pane calls `CloseForTakeover` (prompting for `after-turn` or `now` if a turn is running), waits for `native_session_id` to flush and subprocess locks to close, and opens a new tmux window running the resumed native CLI via `spacelaunch` plus `spaceresume.ResumeArgs`. For `orchd` workers, this invokes `task.takeover` under `ORC-10` so `owner` moves to `human` and the parent coordinator receives `ownership_transferred`. Outside tmux, `/handoff` prints the exact `harness <agent> profile <p> --resume <id>` command or replaces the current process via `exec`.
- **Conversation resume via `convstore`.** Running `/resume` queries the local `convstore` SQLite index filtered by workspace or agent and reopens compatible sessions through `session/load` or SDK resume.

### 2.5 Package layout, phasing, and testing bar

```text
internal/harness/orchacp/
  client.go      JSON-RPC 2.0 stdio transport, request/response correlation, cancellation
  protocol.go    ACP wire types (initialize, session/*, fs/*, terminal/*, usage)
  callbacks.go   workspace-scoped fs/* and bounded PTY/pipe terminal/* handlers
  registry.go    ~/.config/harness/acp.json loader and binary prober
  driver.go      orchapi.Driver implementation and CloseForTakeover flush

internal/harness/proto/
  proto.go       versioned /v1 wire types for workspaces, sessions, messages, permissions, usage, and SSE envelopes
  server.go      VersionInfo, ServerControl (shutdown, shutdown_if_idle), client presence

internal/harness/backend/
  backend.go     multi-workspace lifecycle, per-workspace pubsub bus, client presence & claim tracking
  session.go     session lifecycle delegating to embedded orchacp / orchsdk drivers
  events.go      bridges ACP/SDK events to pubsub, usagestore, and spacecore

internal/harness/server/
  server.go      HTTP/1.1 + h2c server over unix://$XDG_RUNTIME_DIR/harness-<uid>.sock or TCP, SIGHUP reload, 15s drain
  endpoints.go   single-source-of-truth /v1 route table + /v1/docs/openapi.json
  events.go      typed SSE stream writer with overflow handling for workspace and global feeds
  peer_unix.go   SO_PEERCRED / peer UID verification on Unix sockets; bearer auth on TCP

internal/harness/client/
  client.go      HTTP+SSE SDK client with per-process client_id UUID, Unix/TCP dialer, health/version/control
  proto.go       typed /v1 request/response and SSE subscription methods

internal/harness/workspace/
  workspace.go         frontend Workspace interface, ConnectionState (Degraded/Recovered), sentinel errors
  client_workspace.go  HTTP+SSE implementation with background reconnect and resync loop
  backend_workspace.go in-process implementation for headless tests

internal/harness/acptui/
  app.go         Bubble Tea root model, layout, keymap, TTY entry, server auto-start/attach
  rail.go        session list showing interactive sessions and orchestrated workers
  timeline.go    incremental Markdown, thinking, tool call, diff, and approval view
  composer.go    multiline editor, @file picker, /command router, follow-up queue
  theme.go       Catppuccin Mocha palette
```

Phases:

1. **Phase 1.** Implement `orchacp` JSON-RPC 2.0 transport, `initialize`, `session/new`, `session/prompt`, `session/cancel`, `session/update` normalization, and adapter registry inside `internal/harness/server`. Wire `harness serve` auto-start and bare `harness` TTY detection in `cmd/harness/main.go`, and build the thin `acptui` client with streaming timeline, multiline composer, and server-side `spacecore` and `usagestore` publishing.
2. **Phase 2.** Add inline `session/request_permission` cards, workspace-scoped `fs/*` and `terminal/*` client callbacks, unified file diffs, `@` file mentions, and follow-up queuing.
3. **Phase 3.** Add the collapsible multi-session rail, `/resume` over `convstore`, `/usage` summary view, and `Ctrl-o` / `/handoff` pop-out to a native CLI tmux tab using `CloseForTakeover`.
4. **Phase 4.** Expose orchestrated workers (`orchcore`) in the same server session stream so `acptui` and `tmux-spaces` monitor headless workers, answer worker prompts inline, and execute `ORC-10` ownership transfers.

Testing bar:

- Unit-test `orchacp` transport over `io.Pipe` with a fake ACP subprocess covering request correlation, concurrent notifications, malformed lines, stderr capture, turn cancellation, and subprocess teardown.
- Unit-test `cmd/harness` bare invocation with fake TTY and non-TTY streams. Verify that piped stdin or stdout prints usage and exits with code 2, `HARNESS_AGENT` skips the TUI, and a TTY auto-starts or attaches to the local `harness` server and enters `acptui.Run`.
- Drive `acptui` headless through Bubble Tea `Update` and `View` calls against an in-process `harness` server backed by fake `orchsdk` and `orchacp` drivers, covering streaming Markdown chunks, tool blocks, unified diffs, permission card accept and deny, `Esc` turn cancellation, follow-up queue flushing, and reattaching after a TUI disconnect.
- Test `fs/*` callbacks for workspace path traversal rejection and `terminal/*` callbacks for bounded output capture and subprocess cleanup on cancel or quit.
- Verify the `harness` server emits expected `spacecore` events (`prompt`, `pre_tool`, `permission`, `stop`) to a temporary `h.sock` and writes token events into `usage.db` on turn completion.
- Test `Ctrl-o` pop-out with a fake tmux runner. Verify the headless SDK or ACP process exits and flushes before the resumed native CLI command launches.

---

## 3. Rich token usage tracking and SQLite token database

**Proposed.** Extends [usage.go](../internal/harness/usage/usage.go) and complements `convstore`.

Track token spending across every harness, every isolated profile, and every model ID in one local SQLite WAL database (`usage.db`). Both `harness` and external consumers such as `tmux-spaces` query this store (directly or via `harness usage --json`) to inspect and sort spend by harness, profile, model ID, session, or day.

Quota and rate-limit tracking is intentionally omitted from this plan and deferred until its design is decided.

### 3.1 SQLite WAL token store and schema (USG-1)

**Proposed.**

Persist normalized token accounting records in a dedicated SQLite WAL database (`usage.db`) alongside `conversations.db` so high-frequency token ingestion never blocks conversation search or bloats FTS5 tables.

- **Source cursor table (`usage_sources`).** Track each ingested file by `source_key` (`<source_type>:<agent>:<profile>:<clean_path>`) with `size_bytes`, `mtime_ns`, `byte_offset`, `prefix_hash`, and `updated_ms`. Reuse the rotation-safe append-only tailing pattern from `convstore`: when `prefix_hash` matches, read only newly appended bytes; when a file rotates or truncates, reset the offset and reparse safely.
- **Raw token events table (`token_events`).** Store one row per turn or API request:
  - Identity and attribution: `id`, `ts_ms`, `day_utc` (`YYYY-MM-DD`), `source` (`router` | `acp` | `sdk` | `transcript`), `harness` (canonical agent ID), `profile`, `model_id`, `provider`, `session_id`, `task_id`, `run_id`, `cwd`.
  - Token and cost metrics: `requests`, `errors`, `input_tokens`, `output_tokens`, `cached_tokens`, `cache_write_tokens`, `reasoning_tokens`, `total_tokens`, `cost_usd`, `duration_ms`.
  - Provenance and deduplication: `source_key`, `dedupe_key` (unique index to prevent double-counting when the same turn appears in both the router log and an ACP event or transcript).
- **Indexes.** Index `(harness, profile, model_id, ts_ms)`, `(day_utc, harness, profile, model_id)`, `(session_id, ts_ms)`, `(task_id, ts_ms)`, and `(cwd, ts_ms)` so any sort or filter combination executes without full-table scans.
- **Daily rollup table (`token_daily_rollups`).** Maintain pre-aggregated daily sums keyed by `(day_utc, harness, profile, model_id)` so multi-month summaries stay fast and survive optional raw-event retention pruning.

### 3.2 Multi-source token ingestion (USG-2)

**Proposed.**

Feed `usage.db` from the `harness` server as the single writer across three sources under a single normalized schema:

1. **Live ACP and SDK turns.** Whenever the `harness` server receives a turn completion from `orchacp` (§2.1) or `orchsdk` (§1.3), it writes the `token_events` row and updates `token_daily_rollups` immediately with `harness`, `profile`, `model_id`, `session_id`, and (for orchestrated workers) `task_id` and `run_id` so subagent spend rolls up cleanly.
2. **Model router accounting JSONL.** Incrementally tail the router's `usage.jsonl` (replacing one-shot full-file reads in `usage.Load`), mapping router metadata and correlation headers to `harness`, `profile`, `model_id`, `provider`, and `session_id`.
3. **Per-agent transcript adapters across all profiles.** Reuse `convstore`'s `ProfileShadowHomes` and `MarkRealPath` discovery to incrementally tail native agent transcripts across default homes, `profiles.json` shadow homes, and `~/.harness/profiles/<agent>/*/home`. This captures `--no-router` direct sessions and sessions run outside `harness` without double-counting symlinked overlay paths.

For native CLI sessions outside ACP, `spacecore` triggers a fast incremental cursor sync in the `harness` server on every `Stop` hook so `usage.db` updates after every turn completion. Querying `harness usage` also runs an incremental sync (unless `--no-sync` is passed) to pick up sessions run without hooks.

Deduplication rule: prefer direct live records (`acp`, `sdk`, `router`) over `transcript` backfills when both report the same turn. Compute `dedupe_key` from `(source_request_id)` or `(session_id, turn_id)` when available, falling back to a stable hash of `(harness, profile, model_id, ts_bucket, input_tokens, output_tokens)`.

### 3.3 Rich `harness usage` CLI and consumer contract (USG-3)

**Proposed.**

Extend `harness usage` to query `usage.db` (triggering a fast incremental cursor sync before querying unless `--no-sync` is passed):

- **Grouping and sorting.** Support `--group-by harness|profile|model|day|session|cwd` (combinable, such as `--group-by harness,profile,model`) and `--sort total|input|output|cached|cost|requests|harness|profile|model` with `--desc` / `--asc`.
- **Filtering.** Support `--harness <id>`, `--profile <name>`, `--model <id>`, `--cwd <path>`, `--session <id>`, `--run <id>`, `--since <date>`, and `--days <N>`.
- **Output formats.**
  - Default human table with humanized token counts (`1.2M`, `48.3k`), cache hit ratio, estimated USD spend, and grand totals.
  - Typed `--json` output with stable field names (`rows`, `totals`, `filters`, `generated_ms`) so `tmux-spaces` can render its own sortable usage view across all harnesses, profiles, and model IDs by calling `harness usage --json` or reading `usage.db` directly.

### 3.4 Package layout, phasing, and testing bar

```text
internal/harness/usagestore/
  schema.go      SQLite WAL schema (usage_sources, token_events, token_daily_rollups)
  store.go       Open/migrate, transactional insert + rollup update, dedupe, retention pruning
  query.go       Filter, multi-key grouping, and sorting across harness, profile, and model_id
  sync.go        Incremental cursor sync for router JSONL and transcript adapters
  pricing.go     Canonical model pricing table for estimated cost_usd calculation
  adapters/      Per-agent transcript token parsers (claude, codex, antigravity, gemini, opencode, ...)
```

Phases:

1. **Phase 1.** Implement `usagestore` SQLite WAL schema, incremental router `usage.jsonl` ingestion, deduplication, daily rollups, and `harness usage` sorting/filtering by harness, profile, and model ID with `--json` output.
2. **Phase 2.** Add per-agent transcript token adapters across all isolated profiles using `convstore`'s profile discovery helpers so direct `--no-router` sessions appear alongside router-accounted sessions.
3. **Phase 3.** Wire live ACP (`orchacp`) and SDK (`orchsdk`) turn usage emission into `usagestore` with `session_id`, `task_id`, and `run_id` attribution, and expose `/usage` inside `acptui` (§2.3) and `tmux-spaces`.

Testing bar:

- Table-test incremental JSONL and transcript tailing against partial trailing lines, appends, file rotation/truncation (`prefix_hash` mismatch), malformed lines, and symlinked profile homes (`MarkRealPath`).
- Verify deduplication prevents double-counting when the same turn is ingested from both router `usage.jsonl` and an ACP or transcript record.
- Verify multi-column grouping and sorting (`harness`, `profile`, `model_id`, `total_tokens`, `cost_usd`) and confirm `token_daily_rollups` matches raw `token_events` sums after retention pruning.
- Verify concurrent writes from live `harness` sessions and concurrent reads from `harness usage --json` / `tmux-spaces` succeed cleanly under SQLite WAL mode.

## 4. Milestones

| Milestone | What the user can do |
| --- | --- |
| ACP control pane | Run bare `harness` with no agent argument to open a built-in Bubble Tea ACP and SDK control pane, drive direct sessions or `orchd` workers, approve permissions inline, and pop any session out to a native CLI tmux tab. |
