# Profile Examples

`agent-engine` stores profiles in JSON. The examples below show the shape expected by `.agent-engine.json`.

## Go Project

```json
{
  "version": 1,
  "profile": "go-default",
  "profiles": {
    "go-default": {
      "name": "go-default",
      "description": "Go project default profile",
      "executor": {
        "command": ["go", "test", "./..."]
      },
      "verify": {
        "command": ["go", "test", "./..."]
      },
      "sensitive_paths": [".env", ".env.*", "secrets", "certs", "credentials"]
    }
  }
}
```

## Node Project

```json
{
  "version": 1,
  "profile": "node-default",
  "profiles": {
    "node-default": {
      "name": "node-default",
      "description": "Node project default profile",
      "executor": {
        "command": ["npm", "test"]
      },
      "verify": {
        "command": ["npm", "test"]
      },
      "sensitive_paths": [".env", ".env.*", "secrets", "certs", "credentials"]
    }
  }
}
```

## Custom Web App

```json
{
  "version": 1,
  "profile": "web-app",
  "profiles": {
    "web-app": {
      "name": "web-app",
      "description": "Custom web application profile",
      "executor": {
        "command": ["pnpm", "test"]
      },
      "verify": {
        "command": ["pnpm", "test:ci"]
      },
      "sensitive_paths": [".env", ".env.*", "secrets", "certs", "credentials", "private/**"]
    }
  }
}
```

## Notes

- If the repository contains `go.mod`, `agent-engine` can detect a Go default profile automatically.
- If the repository contains `package.json`, `agent-engine` can detect a Node default profile automatically.
- You can override a built-in profile by adding a project-local `.agent-engine.json`.

