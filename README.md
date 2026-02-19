# Gotris

Multiplayer Tetris TUI game inspired by Tetris 99. Play with friends over SSH!

## Features

- **Single Player Mode** - Practice mode for testing and casual play
- Real-time multiplayer Tetris over SSH
- Lobby system with player ready status
- Tetris 99-style garbage line attacks
- Bubble Tea TUI with colorful blocks
- Supports multiple concurrent games

## Requirements

- Go 1.21+
- SSH client (to connect)

## Installation

```bash
# Clone the repository
git clone https://github.com/hersh/gotris.git
cd gotris

# Build
go build -o gotris .

# Generate SSH host key (if not exists)
mkdir -p .ssh
ssh-keygen -t ed25519 -f .ssh/id_ed25519 -N ""
```

## Running

### Start the server

```bash
./gotris
```

The server will start on `localhost:2222` by default.

### Connect to play

Open multiple terminals and connect:

```bash
ssh -p 2222 localhost
```

Or connect from another machine:

```bash
ssh -p 2222 user@your-server-ip
```

## Gameplay

### Game Modes

1. **Single Player** - Press `1` or `S` at the welcome screen for practice mode
2. **Multiplayer** - Press `2` or `ENTER` to join the multiplayer lobby

### Controls

| Key | Action |
|-----|--------|
| `←` / `h` | Move left |
| `→` / `l` | Move right |
| `↓` / `j` | Soft drop |
| `↑` / `x` | Rotate |
| `Space` | Hard drop |
| `z` | Hold piece |
| `q` | Quit |

### Multiplayer Lobby

1. Select Multiplayer mode at welcome screen
2. Press `SPACE` to toggle ready status
3. Game starts when all players are ready (minimum 2 players)

### Attack System (Tetris 99 style)

- Clear lines to send garbage to opponents
- 2 lines = 1 garbage row
- 3 lines = 2 garbage rows
- 4 lines (Tetris) = 4 garbage rows
- Last player standing wins!

## Project Structure

```
gotris/
├── main.go                    # Server entry point
├── internal/
│   ├── game/
│   │   └── tetris.go          # Core Tetris game logic
│   ├── player/
│   │   └── lobby.go           # Player management
│   ├── server/
│   │   └── match.go           # Multiplayer game coordination
│   └── tui/
│       ├── model.go           # Bubble Tea model
│       └── render.go          # Rendering functions
└── .ssh/
    └── id_ed25519             # SSH host key
```

## License

MIT
