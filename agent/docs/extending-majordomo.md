# Extending Majordomo

## Overview

Majordomo is an AI-powered coding assistant that can read, edit, write, and execute shell commands. This document describes the available tools and how to create new ones.

## Built-in Tools

### read
Read the contents of a file.

Parameters:
- `path` (string, required): The file path to read

Example:
```
read(path="/path/to/file.txt")
```

### edit
Replace a block of text in a file with new text. The old text must match exactly (including whitespace).

Parameters:
- `path` (string, required): The file path to edit
- `oldText` (string, required): The exact text to find and replace
- `newText` (string, required): The text to replace with

Example:
```
edit(path="/path/to/file.txt", oldText="old content", newText="new content")
```

### write
Write content to a file, creating or overwriting it.

Parameters:
- `path` (string, required): The file path to write
- `content` (string, required): The content to write

Example:
```
write(path="/path/to/file.txt", content="file contents")
```

### bash
Execute a shell command and return its output.

Parameters:
- `cmd` (string, required): The shell command to execute

Example:
```
bash(cmd="ls -la")
```

## Creating New Tools

To add a new tool to Majordomo, you need to modify two places:

### 1. Define the tool in agent.go

Add a new entry to the `Tools` slice in the `New()` function. Each tool is an `llm.Tool` with:
- `Name`: The tool identifier
- `Description`: Instructions for the LLM on when and how to use the tool
- `Params`: A map of parameter names to `ParamSchema`

Example:
```go
{
    Name:        "my_tool",
    Description: "Description of when to use this tool",
    Params: map[string]llm.ParamSchema{
        "arg1": {Type: "string", Description: "First argument", Required: true},
        "arg2": {Type: "integer", Description: "Second argument", Required: false},
    },
}
```

### 2. Implement the tool handler

Add a new case to `executeTool()` and implement the handler method. The handler receives a `map[string]any` of arguments and returns a `ToolResult`.

Example:
```go
case "my_tool":
    return a.toolMyTool(args)

func (a *Agent) toolMyTool(args map[string]any) ToolResult {
    arg1, ok := args["arg1"].(string)
    if !ok || arg1 == "" {
        return ToolResult{Output: "error: 'arg1' argument is required"}
    }
    // ... implement logic ...
    return ToolResult{Output: "success: " + arg1}
}
```

### 3. Document the tool

Add documentation for the new tool in this file (agent/docs/) so the LLM knows about it.

## Available Filesystem Access

- `repo.ReadFile()`: Read files from the working directory
- `repo.WriteFile()`: Write files to the working directory

Use these for file operations outside of the built-in tools.
