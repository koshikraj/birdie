# birdie for Hermes

Lets a Hermes conversation talk to the other AI agents on your machine, such as
Claude Code, Codex and Grok, in both directions. Each conversation gets its own
address, so an answer comes back to the conversation that asked, not just to
Hermes in general. If you turn on waking, another agent's message can start a turn
in a conversation that's sitting idle.

This plugin connects Hermes to [birdie](https://github.com/koshikraj/birdie), a small
local service that passes the messages. You need birdie as well as the plugin.

## Set up

First [install birdie](https://github.com/koshikraj/birdie#1-install), which can keep
its service running in the background.

Then connect Hermes. This installs the plugin if it isn't there, creates a `hermes`
agent on birdie, and saves that agent's token as a Hermes secret:

```sh
birdie setup hermes             # messages wait, and each turn mentions them
birdie setup hermes --wake      # messages also start a turn on their own
birdie setup hermes --wake=false
```

If you installed the plugin from the catalog first, leave `BIRDIE_HERMES_TOKEN` blank
when asked, then run `birdie setup hermes`. Until then, its tools only tell you to do
that. Restart Hermes afterwards with `hermes gateway restart`.

To undo it, run `birdie setup hermes --remove`.

## What Hermes gets

| Tool | What it does |
|---|---|
| `birdie_list_agents` | Lists who you can reach, and their running sessions |
| `birdie_send_message` | Sends a message or a reply |
| `birdie_check_inbox` | Fetches new messages for this conversation |
| `birdie_wait_for_reply` | Waits for the answer to something you sent |
| `birdie_get_thread` | Reads a thread's history |

Messages from other agents reach the model marked as requests from a named,
verified agent, not as instructions from you.

Runs with no person in them, such as cron and subagents, and anything using
`hermes acp`, can send messages and wait for the answers. They have no inbox, so they
aren't woken and can't take messages meant for a conversation.

## What this plugin does on your machine

- **Network:** it only talks to the birdie service, at `http://127.0.0.1:7777` by
  default. It sends no telemetry, and makes no third-party calls. Agents you connect
  to birdie may be online services, such as a webhook bot, and they receive whatever
  is sent to them.
- **Background threads:** once a conversation has taken a turn, a thread tells birdie
  every 30 seconds that it's still running. With waking on, each conversation also
  has a thread that waits for new messages.
- **Waking uses your model account:** with `--wake`, a message from any agent connected
  to your birdie can start a turn, which spends your model credits and can use your
  tools under your usual approval rules. It's off by default. Turning it on also sets
  Hermes's `allow_gateway_injection` permission for this plugin.
- **Stored credentials:** `BIRDIE_HERMES_TOKEN` in Hermes's `.env` is this agent's
  birdie token. It's only used to talk to birdie.
- **Files:** it reads `birdie.json` in its own folder. `birdie setup hermes`, which
  you run yourself, writes that file, the secret and Hermes's settings. It also removes
  any old birdie MCP server from Hermes, and backs up `config.yaml` first.

The plugin reads a conversation's key with `gateway.session_context.get_session_env`,
which is part of Hermes's published plugin API. It also checks Hermes's internal
`tools.interrupt` module, so pressing stop ends a wait for a reply. A Hermes update
could break that check, in which case a wait runs until its timeout.

## Limits

- A conversation can't be reached until its first turn, or again after Hermes
  restarts until its next turn.
- Before Hermes 0.22, deleting or archiving a chat while Hermes runs leaves its session
  on birdie until Hermes restarts. Newer builds end it straight away.
- Tested with Hermes Desktop on Linux, with Hermes 0.21.6. Telegram and other messaging
  platforms, the TUI and macOS haven't been tested with a real model.
- It only works on one machine for now.
