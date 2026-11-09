<div align="center">

<a href="https://getbirdie.xyz#gh-light-mode-only"><img src=".github/assets/logo-light.webp" width="236" alt="birdie"></a>
<a href="https://getbirdie.xyz#gh-dark-mode-only"><img src=".github/assets/logo-dark.webp" width="236" alt="birdie"></a>

<p><strong>Give your agents a way to talk.</strong></p>

<p>
<a href="https://getbirdie.xyz">Website</a> ·
<a href="#1-install">Install</a> ·
<a href="https://github.com/koshikraj/birdie/releases/latest">Latest release</a>
</p>

<a href="https://getbirdie.xyz#gh-light-mode-only"><img src=".github/assets/hero-light.webp" width="860" alt="App icons for Claude Code, Codex, Hermes and Grok Bot land on a wire and turn into birds. They pass letters to each other, and a sleeping bird wakes when one reaches it."></a>
<a href="https://getbirdie.xyz#gh-dark-mode-only"><img src=".github/assets/hero-dark.webp" width="860" alt="App icons for Claude Code, Codex, Hermes and Grok Bot land on a wire and turn into birds. They pass letters to each other, and a sleeping bird wakes when one reaches it."></a>

</div>

Lets the AI agents on your machine talk to each other, in both directions.
Ask Claude Code to "check with Grok Bot", have Grok Bot ask Claude to review a
PR, or let Grok answer questions headlessly, all through one small local
service.

```
  Claude Code ──MCP──┐                         ┌──webhook──► Grok Bot
  Codex / Grok ──MCP─┤                         │
                     ▼                         │
              birdie serve  (127.0.0.1:7777, SQLite)
                     ▲                         │
  Grok Bot ───HTTP───┘                         └──ACP──────► grok (headless)
```

Every message has a verified sender, a thread, and an optional "reply to",
so questions and answers stay paired, and answers return to the session that
asked.

> **Name clash:** the unrelated Python package `birdie-agent` also installs a
> command called `birdie`. If you have both, whichever comes first on your PATH
> wins, so put this one first or remove the other.

## 1. Install

On Linux or macOS:

```sh
curl -fsSL https://getbirdie.xyz/install.sh | sh
```

This downloads the latest release to `~/.local/bin`, checks its checksum, and
starts `birdie onboard`. It doesn't need Go or sudo. Add `-s -- --no-onboard`
after `sh` to skip the guided setup, or `-s -- --version vX.Y.Z` to pick a version.

To build from source instead, you need [Go](https://go.dev/dl/) 1.27 or newer:

```sh
git clone https://github.com/koshikraj/birdie && cd birdie
make install            # puts birdie in ~/.local/bin (make sure that's on your PATH)
birdie onboard
```

If `make` says `go: not found`, add Go's `bin` folder to your PATH. On Windows,
download the binary from the Releases page and run `birdie serve` yourself.

## 2. Onboard

```sh
birdie onboard
```

Nothing changes until you confirm. It walks through:

1. **Background service:** keeps the bus running and starts it at login, using
   systemd on Linux or launchd on macOS.
2. **What to connect:** a checklist of Claude Code, Codex, Grok CLI and Hermes if
   they're on your PATH, plus headless Grok, headless Hermes and Grok Bot, which
   start unticked.
3. **Details:** whether messages may wake Hermes, and a working folder for each
   headless agent.
4. **Changes:** everything it's about to do, for you to confirm.
5. **Grok Bot, if you ticked it:** a prompt to give it, then its webhook URL and
   key. See [Finish Grok Bot](#4-finish-grok-bot).
6. **Health check:** the same report as `birdie doctor`.

You can run it again at any time. Agents that are connected start ticked, and
unticking one disconnects it; that's listed with the changes before you confirm.
Claude Code, Codex, Grok CLI and Hermes keep their agent and token when
disconnected. Headless agents and Grok Bot lose theirs, so Grok Bot's routine would then need a
new token.

Changed files are backed up to `~/.config/birdie/backups`. Restart any open
sessions afterwards.

**In a script,** skip the questions:

```sh
birdie service install               # the background service
birdie setup --all                   # every tool found on PATH
birdie setup claude                  # or just one; add --as NAME, --remove or --dry-run
birdie setup grok-bot                # Grok Bot, which --all leaves out; see "Finish Grok Bot"
birdie doctor                        # exits non-zero if something's broken
```

## 3. How messages reach sessions

Now any Claude session can do things like *"ask Grok Bot if I have meetings
today"*; you don't have to mention birdie.

**Other agents can reach your open sessions too**, with no special flags. A
message appears in the session on its own: at once if it's idle, or at its next
tool call if it's busy.

| A message is... | It goes to... |
|---|---|
| An answer | The session that asked |
| Addressed to one session, `claude/api` | That session. It's named after its folder, or what you set with `/rename` |
| A new conversation for `claude` | The default session: the most recently active one, unless you chose one |

```sh
birdie session list                  # who's running, what each is doing, and which is the default
birdie session list --all            # also the sessions that stopped in the last day
birdie session default claude api    # new conversations go to the session named api
birdie session default claude        # back to the most recently active
```

Claude Code sessions show as `busy`, `idle 12m`, or `waiting 3m` when they're
stopped until you answer them, as at a permission prompt. Other agents' sessions
show as `live`, because birdie can't tell what they're doing.

If a session closes, its waiting messages go to the default session. If none is
open, they wait for one.

To keep one session from being interrupted, give its MCP server `--no-push`;
messages then wait until it calls `check_inbox`.

A session that skips permission prompts holds incoming messages for you to
review rather than acting on them. birdie leaves this in place on purpose.

**How this works:** birdie posts to an inbox socket Claude Code keeps for each
session. It has no public docs and could change. If it does, or on Windows, add
`--channel` to the MCP server's command and start one session with
`claude --dangerously-load-development-channels server:birdie`.

## 4. Finish Grok Bot

If you ticked Grok Bot, onboarding finishes it in this order:

1. **It registers `grok-bot`** and creates its token.
2. **It gives you one prompt.** You choose whether to see it in the terminal or save
   it to a private file, `~/.config/birdie/grok-bot-prompt.md`. Paste it into a Grok
   Bot chat.
3. **Grok Bot sets up the routine.** The prompt has the two secrets the routine needs,
   `BIRDIE_URL` and `BIRDIE_TOKEN`, and its instructions, which cover starting
   conversations, answering messages, and not replying to answers (so bots don't
   loop). Grok Bot then checks that it can reach birdie and gives you the routine's
   webhook URL and key. If it can't do any of this itself, it tells you where to do it
   in its settings.
4. **You enter the webhook URL and key** back in onboarding. Give the path to a file
   holding the key, or paste it. Pasted keys aren't shown, and are saved to
   `~/.config/grok-bot/sender-key`.

To stop partway, press Enter at the webhook question. Messages to `grok-bot` wait until
you finish by running `birdie setup grok-bot`.

The prompt includes the token, so treat it like a password, and delete the file once
you've used it. Your Grok Bot chat will keep a copy too. To get it again:

```sh
birdie agent prompt grok-bot          # print it
birdie agent prompt grok-bot --save   # save it to the private file
```

Messages to `grok-bot` are POSTed to the webhook (retried if it's down).

To do only this part, without the rest of onboarding, run the same steps on their own:

```sh
birdie setup grok-bot
```

In a script, or anywhere with no terminal to ask in, it's two steps, with the prompt in
between:

```sh
birdie setup grok-bot                                    # registers it
birdie agent prompt grok-bot                             # give this to Grok Bot
birdie setup grok-bot --webhook URL --auth-file PATH     # enter what it gives you
```

`--webhook` and `--auth-file` also change the webhook later, and keep the token.
`birdie setup grok-bot --remove` deletes the agent and its token, so its routine would
need a new token if you reconnect.

## 5. Try it

In any Claude session:

> Ask Grok Bot what the weather is in Bangalore.

From Grok Bot's chat, with the listening Claude session running:

> Ask Claude how many open PRs koshikraj/logr has.

Or from a terminal:

```sh
birdie agent add me
birdie send --as me --to claude "What's 12 × 12?"
birdie inbox --as me --wait 60s
```

To see where a message got to:

```sh
birdie status                        # what's running, how delivery is going, and what's still waiting
birdie thread MSG_ID                 # each message in its thread, and how far it got
```

## Other agents

**Grok CLI, answering headlessly (ACP).** No window needs to be open: for each
message, birdie runs Grok, keeps one Grok session per thread, and posts its
answer back.

```sh
birdie agent add grok --acp "grok agent stdio" --cwd ~/grok-work \
  --description "Grok CLI: coding questions and quick research"
```

| Flag | Default | Meaning |
|---|---|---|
| `--cwd` | home folder | Folder Grok works in; `grok -r <session>` there opens a conversation |
| `--permission` | `reject` | Tool calls that need approval: `reject` or `allow` |
| `--timeout` | `20m` | Longest one answer may take |
| `--max-replies` | `10` | Answers per thread before it stops (stops bots looping); `0` = no limit |

Any agent that speaks the [Agent Client Protocol](https://agentclientprotocol.com)
works the same way, e.g. Codex via `codex-acp` once installed.

**Codex, interactively.** Messages wake an open session, once it has used an
birdie tool at least once; that's how the bridge learns which conversation
it's in. Until then they wait.

```sh
birdie setup codex
```

**Grok CLI, interactively.** Messages appear at its next tool call or when it
finishes a turn. Nothing can wake an idle Grok window, so the message waits for
you to prompt it. Use the headless setup above if you need an answer with
nothing open.

```sh
birdie setup grok    # registers it as grok-tui; choose another name with --as
```

**Hermes.** `birdie setup hermes` installs a native Hermes plugin instead of an
MCP server, because only a plugin can tell conversations apart. Each conversation
becomes its own session once it has taken a turn, with the tools `birdie_send_message`,
`birdie_check_inbox`, `birdie_wait_for_reply`, `birdie_get_thread` and
`birdie_list_agents`. Its token is saved as a Hermes secret, in `.env`. Restart Hermes
to load it. See [its README](integrations/hermes/README.md) for what it does on your
machine.

```sh
birdie setup hermes             # messages wait, and each turn mentions them
birdie setup hermes --wake      # messages also start a turn on their own
birdie setup hermes --wake=false
```

Waking is off by default, because it lets other agents start work on your model
account. It also sets Hermes's own `allow_gateway_injection` permission for the
plugin. Runs with no person in them, such as cron and subagents, and anything using
`hermes acp`, can send messages and wait for the answers. They have no inbox, so
they aren't woken and can't take messages meant for a conversation. It follows
`HERMES_HOME` and the active profile, so repeat it for each profile. To have Hermes
answer with nothing open, add it over ACP:

```sh
birdie agent add hermes-headless --acp "hermes acp" --cwd ~/hermes-work
```

**Any other MCP client.** Add `birdie mcp --as NAME` to its MCP servers after
`birdie agent add NAME`. It can send and wait for answers, and its tool
results mention when messages are waiting for `check_inbox`.

## Upgrading from agent-bus

This project used to be called agent-bus. To upgrade:

1. Install `birdie` and run `birdie onboard`. Your data moves from
   `~/.config/agent-bus` to `~/.config/birdie`, keeping your agents, tokens and
   messages, and a link stays at the old path so anything still running keeps
   working. Keep Claude Code, Codex and Grok CLI ticked: onboarding replaces their
   old `agent-bus` entries.
2. Stop the old `agent-bus serve`, start the bus from the new binary, and restart
   open sessions. Their tools are now named `mcp__birdie__*`, so update any
   permission rules that name `mcp__agent-bus__*`.
3. In Grok Bot's routine, rename the secrets to `BIRDIE_URL` and `BIRDIE_TOKEN`,
   and paste the new routine text from `docs/grok-bot-routine.md`. Your old routine
   still receives messages, but it won't recognize notices from the bus.
4. Delete `~/.local/bin/agent-bus` once nothing uses it.

## Commands

```
birdie serve [--addr 127.0.0.1:7777] [--loop-limit 12]
birdie onboard
birdie status
birdie doctor
birdie setup claude|codex|grok|hermes|--all [--as NAME] [--remove] [--dry-run] [--wake]
birdie setup grok-bot [--webhook URL --auth-file PATH] [--remove] [--dry-run]
birdie service install|remove|status
birdie agent add NAME [--description TEXT] [--webhook URL --auth-file PATH | --acp "COMMAND" ...]
birdie agent describe NAME TEXT
birdie agent list | rm NAME
birdie agent prompt grok-bot [--save]
birdie session list [--all] | default AGENT [NAME]
birdie send  --as NAME --to AGENT[/SESSION] MESSAGE
birdie reply --as NAME MSG_ID MESSAGE
birdie inbox --as NAME [--wait 30s]
birdie thread THREAD_ID|MSG_ID [--full]
birdie mcp   --as NAME [--no-push] [--channel [--server-name birdie]]
birdie hook  grok post-tool-use|stop --as NAME
```

MCP tools: `send_message`, `wait_for_reply`, `check_inbox`, `get_thread`,
`list_agents`.

HTTP API (Bearer token): `POST /v1/messages`, `GET /v1/inbox?wait=30s`,
`GET /v1/unread`, `POST /v1/messages/{id}/ack|nack`, `GET /v1/threads/{id}`,
`GET /v1/agents`, `GET /v1/me`, `PUT|DELETE /v1/sessions/{id}`.

Pass `session` and `lease` to `/v1/inbox` to act as one session. Messages taken
that way come back unless confirmed with `ack`.

## Troubleshooting

| Problem | Check |
|---|---|
| `connection refused` | Is `birdie serve` running? |
| A message went to the wrong session | `birdie session list` shows the default; choose one with `session default`, or address it as `agent/session` |
| Claude never wakes up | `session list` shows it as `push`. If it shows `pull` or isn't listed, restart it. `claude -p` runs can't be woken |
| Codex shows `pull` | It hasn't used a birdie tool yet; ask it to run `list_agents` |
| A message never arrived | `birdie thread MSG_ID` shows how far it got and which session took it. `birdie status` lists everything still waiting |
| Grok Bot gets nothing | `birdie agent list` shows `grok-bot` as `webhook`, and why its last delivery failed. After 8 failed tries the sender gets a notice from `birdie` saying why |
| Headless Grok doesn't answer | The service log shows `acp: turn failed`; the sender also gets a reply explaining why |
| `too many messages` | Two agents sent 12 messages in 10 minutes, which looks like a loop. Change it with `serve --loop-limit` (`0` = off) |

## Security

- Listens on 127.0.0.1 only. Each agent has its own token; only a hash is stored.
- The sender of every message is set by the bus from the token, so it can't be faked.
- Only the agents in a thread can read it or post to it.
- Agents are capped at 12 messages to the same agent in 10 minutes, so they
  can't loop unnoticed.
- Messages from other agents are treated as requests, not as your
  instructions: agents are told not to share secrets or personal information,
  or do anything destructive, without checking with you.

## How it fits together

```
cmd/birdie       CLI
internal/bus        messages, threads, inboxes, delivery loop (no transport code)
internal/store      SQLite storage
internal/httpapi    HTTP API
internal/mcpserver  MCP server, and how each agent program is reached (Claude, Codex, Grok)
internal/adapter    outbound delivery: webhook, ACP (A2A next)
skills/             talk-to-agents skill for Claude, Grok and Codex
docs/               Grok Bot routine
```

New protocols plug in as adapters: outbound ones implement `bus.Deliverer`
(`Kind()` + `Deliver()`) and are picked by an agent's delivery kind; inbound
ones call `Bus.Send` / `Bus.Receive`, as the HTTP and MCP servers do.

**Beyond one machine.** birdie only connects agents on one machine today.
Addresses are `agent[/session][@node]` so other machines can be added without
changing existing names; `@node` is refused for now. Rules about who may message
whom live in one place (`Bus.admit`), and a remote agent can be another
`bus.Deliverer`.

## License

[MIT](LICENSE). The names and logos of other companies' products, used to show
which agents birdie works with, belong to their owners and aren't covered by it.
