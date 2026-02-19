package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/hersh/gotris/internal/game"
	"github.com/hersh/gotris/internal/protocol"
)

var (
	colors = []string{
		"0",
		"196",
		"46",
		"226",
		"21",
		"201",
		"51",
		"248",
		"245", // garbage line color
	}

	blockChars = []string{"  ", "██"}

	boardStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("15"))

	infoStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(lipgloss.Color("15"))

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("51"))

	readyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("46"))

	notReadyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	gameOverStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("196"))

	winnerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("226"))
)

func RenderBoard(gs *game.GameState, width, height int) string {
	var sb strings.Builder

	displayHeight := min(height, game.BoardHeight)
	displayWidth := min(width, game.BoardWidth)

	ghostY := gs.GetGhostY()

	for y := 0; y < displayHeight; y++ {
		for x := 0; x < displayWidth; x++ {
			cell := gs.Board.Cells[y][x]
			char := "  "
			color := "0"

			if cell.Filled {
				char = "██"
				color = colors[cell.Color]
			}

			for py, row := range gs.CurrentPiece.Shape {
				for px, filled := range row {
					if filled && gs.CurrentPiece.Y+py == y && gs.CurrentPiece.X+px == x {
						char = "██"
						color = colors[gs.CurrentPiece.Color]
					} else if filled && ghostY+py == y && gs.CurrentPiece.X+px == x && !cell.Filled {
						char = "[]"
						color = "244"
					}
				}
			}

			sb.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color(color)).
				Render(char))
		}
		if y < displayHeight-1 {
			sb.WriteString("\n")
		}
	}

	return boardStyle.Render(sb.String())
}

func RenderPiece(p *game.Piece) string {
	if p == nil {
		return "Empty"
	}

	var sb strings.Builder
	pieceStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors[p.Color]))

	for y, row := range p.Shape {
		for _, filled := range row {
			if filled {
				sb.WriteString(pieceStyle.Render("██"))
			} else {
				sb.WriteString("  ")
			}
		}
		if y < len(p.Shape)-1 {
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

func RenderInfo(gs *game.GameState) string {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("GOTRIS") + "\n\n")
	sb.WriteString(infoStyle.Render(fmt.Sprintf("Player: %s", gs.PlayerName)) + "\n")
	sb.WriteString(infoStyle.Render(fmt.Sprintf("Score: %d", gs.Score)) + "\n")
	sb.WriteString(infoStyle.Render(fmt.Sprintf("Level: %d", gs.Level)) + "\n")
	sb.WriteString(infoStyle.Render(fmt.Sprintf("Lines: %d", gs.Lines)) + "\n\n")

	sb.WriteString(titleStyle.Render("NEXT") + "\n")
	sb.WriteString(RenderPiece(gs.NextPiece) + "\n\n")

	sb.WriteString(titleStyle.Render("HOLD") + "\n")
	sb.WriteString(RenderPiece(gs.HoldPiece) + "\n")

	if gs.GarbageQueue > 0 {
		sb.WriteString("\n")
		sb.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Render(fmt.Sprintf("INCOMING: %d", gs.GarbageQueue)))
	}

	return sb.String()
}

func RenderLobby(playerNames []string, ready map[string]bool, currentPlayer string) string {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("=== LOBBY ===") + "\n\n")
	sb.WriteString(infoStyle.Render("Players in lobby:") + "\n\n")

	for _, name := range playerNames {
		status := notReadyStyle.Render("[ ]")
		if ready[name] {
			status = readyStyle.Render("[✓]")
		}

		marker := ""
		if name == currentPlayer {
			marker = " <"
		}

		sb.WriteString(fmt.Sprintf("%s %s%s\n", status, name, marker))
	}

	sb.WriteString("\n")
	sb.WriteString(infoStyle.Render("Press SPACE to toggle ready") + "\n")
	sb.WriteString(infoStyle.Render("Press Q to quit") + "\n")

	return sb.String()
}

func RenderCountdown(count int) string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("51")).
		Align(lipgloss.Center).
		Render(fmt.Sprintf("\n\n\n     %d     \n\n\n", count))
}

func RenderGameOver(isWinner bool, score int, rank int) string {
	if isWinner {
		return lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("226")).
			Align(lipgloss.Center).
			Render(fmt.Sprintf("\n\n\n     WINNER!     \n     Score: %d     \n\n\n", score))
	}
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("196")).
		Align(lipgloss.Center).
		Render(fmt.Sprintf("\n\n\n     GAME OVER     \n     Score: %d     \n     Rank: #%d     \n\n\n", score, rank))
}

// RenderNetOpponentPreview renders a mini-board from a network OpponentState.
// Shows the full board width (10 cols) and the bottom portion where pieces stack.
func RenderNetOpponentPreview(opp protocol.OpponentState) string {
	previewWidth := game.BoardWidth // full 10 columns
	previewHeight := 10             // bottom 10 rows of the 20-row board
	startY := game.BoardHeight - previewHeight

	var sb strings.Builder

	nameStyle := lipgloss.NewStyle().
		MaxWidth(previewWidth).
		Foreground(lipgloss.Color("15"))

	sb.WriteString(nameStyle.Render(opp.PlayerName) + "\n")

	if !opp.Alive {
		for y := 0; y < previewHeight; y++ {
			for x := 0; x < previewWidth; x++ {
				sb.WriteString("·")
			}
			sb.WriteString("\n")
		}
		sb.WriteString(gameOverStyle.Render("OUT"))
		return sb.String()
	}

	for y := startY; y < game.BoardHeight; y++ {
		for x := 0; x < previewWidth; x++ {
			idx := y*game.BoardWidth + x
			colorIdx := 0
			if idx < len(opp.Board) {
				colorIdx = opp.Board[idx]
			}
			if colorIdx != 0 {
				c := "248"
				if colorIdx < len(colors) {
					c = colors[colorIdx]
				}
				sb.WriteString(lipgloss.NewStyle().
					Foreground(lipgloss.Color(c)).
					Render("█"))
			} else {
				sb.WriteString("·")
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString(infoStyle.Render(fmt.Sprintf("S:%d L:%d", opp.Score, opp.Lines)))

	return sb.String()
}

// RenderNetOpponents renders a grid of opponent previews from network state.
func RenderNetOpponents(opponents []protocol.OpponentState, maxDisplay int) string {
	if len(opponents) == 0 {
		return ""
	}

	display := opponents
	if len(display) > maxDisplay {
		display = display[:maxDisplay]
	}

	var sb strings.Builder
	row := ""
	col := 0
	cols := 4

	for _, opp := range display {
		preview := RenderNetOpponentPreview(opp)
		row += lipgloss.NewStyle().
			Padding(0, 1).
			Render(preview)

		col++
		if col >= cols {
			sb.WriteString(row + "\n")
			row = ""
			col = 0
		}
	}

	if row != "" {
		sb.WriteString(row)
	}

	return sb.String()
}

func RenderWelcome() string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("51")).
		Align(lipgloss.Center).
		Render(`
╔══════════════════════════════╗
║          G O T R I S         ║
║    Multiplayer Tetris TUI    ║
╚══════════════════════════════╝

   [1] Single Player (Practice)
   [2] Multiplayer (vs Others)

   Press 1/2 or S/ENTER to select
   Press Q to quit
`)
}

func RenderSingleGameOver(score int) string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("196")).
		Align(lipgloss.Center).
		Render(fmt.Sprintf("\n\n\n     GAME OVER     \n     Score: %d     \n\n\n", score))
}

func RenderControls() string {
	return infoStyle.Render(`
Controls:
  ← →    Move left/right
  ↓      Soft drop
  Space  Hard drop
  ↑/X    Rotate
  Z      Hold piece
  Q      Quit
`)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
