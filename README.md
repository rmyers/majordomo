# Majordomo

Majordomo is a local-first AI agent with a web interface. It can read files, edit them, write new files, and run shell commands — all through a conversational interface powered by a local LLM.

## Getting started

1. Install Go 1.23+
2. Start a local LLM server (ollama, LM Studio, or llama.cpp)
3. Clone and run:

```bash
go run ./cmd/majordomo
```

The server starts on `http://localhost:3636`. Open that URL in your browser.

## Configuration

### Web UI

Click the gear icon (settings) in the header to configure your LLM:

- **Provider** — `auto` (default), `ollama`, `lmstudio`, or `llamacpp`
- **Model** — Leave empty to use the server default (e.g. `llama3.2`)
- **Server URL** — Leave empty for provider default (ollama: `localhost:11434`)
- **API Key** — Optional bearer token for authenticated servers

Settings are persisted to `~/.majordomo/config.json`.

### CLI (file-based)

To configure manually, create `~/.majordomo/config.json`:

```json
{
  "llm": {
    "provider": "ollama",
    "model": "llama3.2",
    "url": "http://localhost:11434",
    "apiKey": ""
  }
}
```

## Tools

Majordomo can use four tools:

- **read** — Read file contents
- **edit** — Replace text within a file
- **write** — Write or overwrite a file
- **bash** — Execute shell commands

## Commands

```bash
# Run the web server (default port 3636)
go run ./cmd/majordomo

# Custom port and config
go run ./cmd/majordomo --port ":18080" --config ./my-config.json

# Build binary
go build -o bin/majordomo ./cmd/majordomo
```
