# Majordomo Architecture

Majordomo is a local-first AI agent that interacts with your filesystem through a web interface. It runs an agentic loop: receiving user input, calling an LLM, executing tool calls (read, edit, write, bash), and repeating until the task is complete.

## Design Principles

**Local-first.** Majordomo runs entirely on your machine. It connects to local LLM servers (ollama, LM Studio, llama.cpp) via the OpenAI v1 `/v1/chat/completions` API. No remote servers, no accounts, no telemetry.

**Minimal dependencies.** The entire project uses only Go standard library. No third-party packages — config is persisted as JSON. The HTTP server uses `net/http`. The web UI is vanilla HTML/CSS/JS.

**The agent loop is the core.** Majordomo doesn't just answer questions — it takes actions. It can read files, edit them, write new files, and execute shell commands. The LLM decides which tools to use and with what arguments.

## Data Flow

```
User ──message──► Web UI ──SSE──► /api/stream
                                         │
                                         ▼
                                    Create LLM client
                                         │
                                         ▼
                                    Create Agent
                                         │
                                         ▼
                                    Agent.Run(messages)
                                         │
                    ┌────────────────────┘
                    ▼                    ▼
              LLM.Chat()          Execute tool calls
                    │                    │
                    ▼                    ▼
              Final response      [read/edit/write/bash]
                    │                    │
                    └────────────────────┘
                                         │
                                         ▼
                              Stream SSE events to UI
                                         │
                                         ▼
                                    User sees response
```

Each iteration of the loop:

1. **Agent** sends all accumulated messages (user + assistant + tool results) to the LLM
2. **LLM** returns either text content (final answer) or `tool_calls` (requests to use tools)
3. If tool calls: **Agent** executes each tool, appends results as `tool` role messages, and loops back to step 1
4. If no tool calls: **Agent** returns the final assistant message, which is streamed to the UI

## The Agent Loop

The `agent.Agent` struct manages the conversation state and tool execution:

```go
type Agent struct {
    Client llm.Client
    Tools  []llm.Tool  // tool definitions sent to LLM
}

func (a *Agent) Run(ctx context.Context, messages []llm.Message) ([]llm.Message, error)
```

**Tools:**

| Tool    | Description             | Parameters                   |
| ------- | ----------------------- | ---------------------------- |
| `read`  | Read file contents      | `path`                       |
| `edit`  | Replace text in a file  | `path`, `oldText`, `newText` |
| `write` | Write/overwrite a file  | `path`, `content`            |
| `bash`  | Execute a shell command | `cmd`                        |

Tool definitions are sent to the LLM as OpenAI `tool_use` schema (function name, description, parameters), enabling the model to invoke tools naturally.

## LLM Integration

The `llm.Client` interface supports both blocking and streaming modes:

```go
type Client interface {
    Chat(ctx context.Context, messages []Message) (*Message, error)
    StreamChat(ctx context.Context, messages []Message, handler func(text string, toolCalls []ToolCall)) error
    Name() string
}
```

**Auto-detection** probes three common local LLM endpoints:

- **Ollama** — `localhost:11434` (most common)
- **LM Studio** — `localhost:1234`
- **llama.cpp** — `localhost:8080`

The first to respond to `/v1/models` wins. If none are found, the server returns a 503 error.

**API keys** are sent as `Authorization: Bearer <key>` headers when configured.

## Configuration

Config is persisted to `~/.majordomo/config.json`:

```json
{
  "llm": {
    "provider": "auto",
    "model": "",
    "url": "",
    "apiKey": ""
  }
}
```

Configuration can also be managed through the web UI settings gear icon.

## Session Storage

Sessions are persisted as JSONL files under `~/.majordomo/sessions/`, mirroring the PI format. A session represents one conversation — all messages in a session follow the same session ID.

**Format (version 3):**

| Line Type      | Fields                                                                              |
| -------------- | ----------------------------------------------------------------------------------- |
| `session`      | `version`, `id`, `timestamp`, `cwd`                                                 |
| `model_change` | `id`, `timestamp`, `provider`, `model`                                              |
| `message`      | `id`, `parentId`, `timestamp`, `message: {role, content, tool_calls, tool_call_id}` |

**Lifecycle:**

1. The UI extracts the session ID from the URL (`/chat/<id>`).
2. The session ID is passed via `?session=<id>` on the stream URL.
3. If no session ID is provided, a new session is created and its ID is returned in a `session` SSE event.
4. All messages in a session share the same session ID.
5. The first user message is summarized to produce a session title.
6. When the stream ends, the session is closed and the file is flushed.
7. The full message history is loaded from the session file and prepended to the LLM context when resuming a session.

**Session title:** The first user message is summarized (first sentence up to 60 chars) and stored as the session's title. This is returned by `/api/sessions` in the summary objects.

## Server API

| Endpoint                             | Method | Description                                                               |
| ------------------------------------ | ------ | ------------------------------------------------------------------------- |
| `/`                                  | GET    | Redirects to `/chat/<latest>` (or shows empty state if no sessions)       |
| `/chat/{id}`                         | GET    | Serves the web UI for the specified session                               |
| `/api/config`                        | GET    | Returns current config as JSON                                            |
| `/api/config`                        | POST   | Saves new config (JSON body)                                              |
| `/api/sessions`                      | GET    | Returns list of session summaries `{id, title, timestamp}` (newest first) |
| `/api/sessions/{id}`                 | POST   | Creates a new session, returns `{id}`                                     |
| `/api/sessions/{id}/history`         | GET    | Returns full message history for a session                                |
| `/api/stream?query=...&session=<id>` | GET    | SSE stream — starts/resumes agent loop, streams response                  |

SSE event types:

- `message` — JSON: `{content: "text chunk"}`
- `error` — JSON: `{message: "error description"}`
- `[DONE]` — literal string marking stream end
