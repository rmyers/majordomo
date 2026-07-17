# Agent Guidelines

## Building and Running

Use the `Makefile` at the project root:

| Command       | Description             |
| ------------- | ----------------------- |
| `make dev`    | Run the web server locally |
| `make build`  | Build the binary to `bin/majordomo` |
| `make test`   | Run all tests (`go test ./...`) |
| `make help`   | Show available commands |

## Project Structure

| Directory   | Purpose                              |
| ----------- | ------------------------------------ |
| `cmd/`      | Application entry point              |
| `server/`   | HTTP server and route handlers       |
| `llm/`      | LLM client interface and API calls   |
| `agent/`    | Agent loop and tool definitions      |
| `session/`  | Session storage (JSONL persistence)  |
| `config/`   | Configuration management             |
| `repo/`     | Repository utilities                 |
| `docs/`     | Architecture docs and references     |

## Documentation

Full architecture documentation is in `docs/ARCHITECTURE.md`. It covers:

- The agent loop (receive input, call LLM, execute tools, repeat)
- LLM integration (auto-detection of Ollama, LM Studio, llama.cpp)
- Configuration (`~/.majordomo/config.json`)
- Session storage (`~/.majordomo/sessions/`)
- Server API endpoints

## Key Details

- Pure Go standard library — no third-party dependencies.
- Config persisted as JSON; sessions as JSONL files.
- Web UI is vanilla HTML/CSS/JS served by `net/http`.
