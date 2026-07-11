# Majordomo Architecture

Majordomo is a local-first AI agent that interacts with your filesystem through a web interface. It runs an agentic loop: receiving user input, calling an LLM, executing tool calls (read, edit, write, bash), and repeating until the task is complete.

## Design Principles

**Local-first.** Majordomo runs entirely on your machine. It connects to local LLM servers (ollama, LM Studio, llama.cpp) via the OpenAI v1 `/v1/chat/completions` API. No remote servers, no accounts, no telemetry.

**Minimal dependencies.** The entire project uses only Go standard library. No third-party packages — config is persisted as JSON. The HTTP server uses `net/http`. The web UI is vanilla HTML/CSS/JS.

**The agent loop is the core.** Majordomo doesn't just answer questions — it takes actions. It can read files, edit them, write new files, and execute shell commands. The LLM decides which tools to use and with what arguments.

## Project Structure

```
majordomo/
├── cmd/
│   └── majordomo/
│       └── main.go              # CLI entrypoint: loads config, starts server
├── internal/
│   ├── config/
│   │   └── config.go            # JSON config: LLM provider, model, URL, apiKey
│   ├── llm/
│   │   └── llm.go               # OpenAI-compatible client (Chat + StreamChat)
│   ├── agent/
│   │   └── agent.go             # Core: agentic loop with read/edit/write/bash tools
│   ├── repo/
│   │   └── repo.go              # File system utilities (walk, read, write, grep)
│   └── server/
│       └── server.go            # HTTP server: static UI + SSE agent stream + config API
├── web/
│   └── index.html               # Single-page web UI (HTML + CSS + JS)
├── go.mod
└── Makefile
```

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

| Tool | Description | Parameters |
|------|-------------|------------|
| `read` | Read file contents | `path` |
| `edit` | Replace text in a file | `path`, `oldText`, `newText` |
| `write` | Write/overwrite a file | `path`, `content` |
| `bash` | Execute a shell command | `cmd` |

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

## Server API

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | Serves the web UI |
| `/api/config` | GET | Returns current config as JSON |
| `/api/config` | POST | Saves new config (JSON body) |
| `/api/stream?query=...` | GET | SSE stream — starts agent loop, streams response |

SSE event types:
- `message` — JSON: `{content: "text chunk"}`
- `error` — JSON: `{message: "error description"}`
- `[DONE]` — literal string marking stream end
