# gotris

Multiplayer Tetris in the terminal, written in Go. Play solo or against friends over the network -- all from your terminal.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) for the TUI and [gorilla/websocket](https://github.com/gorilla/websocket) for multiplayer.

## How to play

### Single-player

Just run it:

```
go run . [name]
```

### Multiplayer

Start the server:

```
go run ./cmd/server
```

Then each player connects with the client:

```
go run ./cmd/client --server ws://localhost:8080/ws --name yourname
```

The `--server` flag defaults to `ws://localhost:8080/ws` and `--name` defaults to your OS username, so locally you can just do:

```
go run ./cmd/client
```

Once everyone is in the lobby, press space to ready up. The game starts when all players are ready (minimum 2).

## Controls

| Key | Action |
|---|---|
| Left / Right | Move piece |
| Down | Soft drop |
| Up | Rotate |
| Space | Hard drop |
| C | Hold piece |
| Q / Ctrl+C | Quit |

## How multiplayer works

All players in a match receive the same random seed, so the 7-bag piece sequence is identical for everyone. The server coordinates lobby state, broadcasts board snapshots between opponents, and handles garbage line attacks.

When you clear 2+ lines, garbage gets sent to a random opponent. Their board gets pushed up with junk rows that have a single gap. Last player alive wins.

## Project layout

```
main.go                    single-player entry point
cmd/
  server/main.go           WebSocket game server
  client/main.go           multiplayer client entry point
internal/
  game/tetris.go           core Tetris logic (board, pieces, 7-bag, scoring)
  tui/model.go             Bubble Tea model, input handling, game loop
  tui/render.go            all the rendering (board, lobby, opponents, etc.)
  netclient/client.go      WebSocket client wrapper
  player/lobby.go          server-side lobby/player management
  protocol/messages.go     shared message types for client-server protocol
```

## Requirements

- Go 1.25+
- A terminal that supports alternate screen and basic ANSI colors (basically anything modern)