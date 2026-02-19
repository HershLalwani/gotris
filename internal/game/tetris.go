package game

import (
	"math/rand"
	"time"
)

const (
	BoardWidth  = 10
	BoardHeight = 20
)

type PieceType int

const (
	PieceI PieceType = iota
	PieceO
	PieceT
	PieceS
	PieceZ
	PieceJ
	PieceL
)

type Piece struct {
	Type  PieceType
	Shape [][]bool
	X, Y  int
	Color int
}

var pieceShapes = map[PieceType][][]bool{
	PieceI: {
		{false, false, false, false},
		{true, true, true, true},
		{false, false, false, false},
		{false, false, false, false},
	},
	PieceO: {
		{true, true},
		{true, true},
	},
	PieceT: {
		{false, true, false},
		{true, true, true},
		{false, false, false},
	},
	PieceS: {
		{false, true, true},
		{true, true, false},
		{false, false, false},
	},
	PieceZ: {
		{true, true, false},
		{false, true, true},
		{false, false, false},
	},
	PieceJ: {
		{true, false, false},
		{true, true, true},
		{false, false, false},
	},
	PieceL: {
		{false, false, true},
		{true, true, true},
		{false, false, false},
	},
}

var pieceColors = map[PieceType]int{
	PieceI: 6,
	PieceO: 3,
	PieceT: 5,
	PieceS: 2,
	PieceZ: 1,
	PieceJ: 4,
	PieceL: 3,
}

func NewPiece(t PieceType) *Piece {
	shape := make([][]bool, len(pieceShapes[t]))
	for i := range pieceShapes[t] {
		shape[i] = make([]bool, len(pieceShapes[t][i]))
		copy(shape[i], pieceShapes[t][i])
	}
	return &Piece{
		Type:  t,
		Shape: shape,
		X:     BoardWidth/2 - len(shape[0])/2,
		Y:     0,
		Color: pieceColors[t],
	}
}

// PieceGenerator produces pieces using the 7-bag randomizer system.
// When created with the same seed, two generators produce identical sequences.
type PieceGenerator struct {
	rng *rand.Rand
	bag []PieceType
}

// NewPieceGenerator creates a seeded 7-bag piece generator.
func NewPieceGenerator(seed int64) *PieceGenerator {
	pg := &PieceGenerator{
		rng: rand.New(rand.NewSource(seed)),
	}
	return pg
}

// Next returns the next piece from the 7-bag.
func (pg *PieceGenerator) Next() *Piece {
	if len(pg.bag) == 0 {
		pg.refillBag()
	}
	t := pg.bag[0]
	pg.bag = pg.bag[1:]
	return NewPiece(t)
}

// Peek returns the next piece type without consuming it.
func (pg *PieceGenerator) Peek() PieceType {
	if len(pg.bag) == 0 {
		pg.refillBag()
	}
	return pg.bag[0]
}

func (pg *PieceGenerator) refillBag() {
	pg.bag = []PieceType{PieceI, PieceO, PieceT, PieceS, PieceZ, PieceJ, PieceL}
	// Fisher-Yates shuffle
	for i := len(pg.bag) - 1; i > 0; i-- {
		j := pg.rng.Intn(i + 1)
		pg.bag[i], pg.bag[j] = pg.bag[j], pg.bag[i]
	}
}

// RandomPiece returns a random piece (legacy, non-seeded).
func RandomPiece() *Piece {
	pieces := []PieceType{PieceI, PieceO, PieceT, PieceS, PieceZ, PieceJ, PieceL}
	return NewPiece(pieces[rand.Intn(len(pieces))])
}

func (p *Piece) Rotate() {
	n := len(p.Shape)
	rotated := make([][]bool, n)
	for i := range rotated {
		rotated[i] = make([]bool, n)
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			rotated[j][n-1-i] = p.Shape[i][j]
		}
	}
	p.Shape = rotated
}

type Cell struct {
	Filled bool
	Color  int
}

type Board struct {
	Cells  [][]Cell
	Width  int
	Height int
}

func NewBoard() *Board {
	cells := make([][]Cell, BoardHeight)
	for i := range cells {
		cells[i] = make([]Cell, BoardWidth)
	}
	return &Board{
		Cells:  cells,
		Width:  BoardWidth,
		Height: BoardHeight,
	}
}

func (b *Board) IsValidPosition(p *Piece, offsetX, offsetY int) bool {
	for y, row := range p.Shape {
		for x, cell := range row {
			if !cell {
				continue
			}
			newX := p.X + x + offsetX
			newY := p.Y + y + offsetY
			if newX < 0 || newX >= b.Width {
				return false
			}
			if newY >= b.Height {
				return false
			}
			if newY >= 0 && b.Cells[newY][newX].Filled {
				return false
			}
		}
	}
	return true
}

func (b *Board) LockPiece(p *Piece) {
	for y, row := range p.Shape {
		for x, cell := range row {
			if cell {
				boardY := p.Y + y
				boardX := p.X + x
				if boardY >= 0 && boardY < b.Height && boardX >= 0 && boardX < b.Width {
					b.Cells[boardY][boardX] = Cell{Filled: true, Color: p.Color}
				}
			}
		}
	}
}

func (b *Board) ClearLines() int {
	linesCleared := 0
	newCells := make([][]Cell, 0, b.Height)

	for y := b.Height - 1; y >= 0; y-- {
		full := true
		for x := 0; x < b.Width; x++ {
			if !b.Cells[y][x].Filled {
				full = false
				break
			}
		}
		if !full {
			newCells = append([][]Cell{b.Cells[y]}, newCells...)
		} else {
			linesCleared++
		}
	}

	for len(newCells) < b.Height {
		newCells = append([][]Cell{make([]Cell, b.Width)}, newCells...)
	}

	b.Cells = newCells
	return linesCleared
}

func (b *Board) AddGarbageLines(count int, holeX int) {
	for i := 0; i < count; i++ {
		b.Cells = b.Cells[1:]
		newLine := make([]Cell, b.Width)
		for x := range newLine {
			if x == holeX {
				newLine[x] = Cell{Filled: false}
			} else {
				newLine[x] = Cell{Filled: true, Color: 8}
			}
		}
		b.Cells = append(b.Cells, newLine)
	}
}

// ToFlat returns the board as a flat array of color indices (0 = empty).
func (b *Board) ToFlat() []int {
	flat := make([]int, b.Height*b.Width)
	for y := 0; y < b.Height; y++ {
		for x := 0; x < b.Width; x++ {
			if b.Cells[y][x].Filled {
				flat[y*b.Width+x] = b.Cells[y][x].Color
			}
		}
	}
	return flat
}

// BoardFromFlat reconstructs a Board from a flat color-index array.
func BoardFromFlat(flat []int, width, height int) *Board {
	b := &Board{
		Width:  width,
		Height: height,
		Cells:  make([][]Cell, height),
	}
	for y := 0; y < height; y++ {
		b.Cells[y] = make([]Cell, width)
		for x := 0; x < width; x++ {
			idx := y*width + x
			if idx < len(flat) && flat[idx] != 0 {
				b.Cells[y][x] = Cell{Filled: true, Color: flat[idx]}
			}
		}
	}
	return b
}

func (b *Board) IsGameOver(p *Piece) bool {
	return !b.IsValidPosition(p, 0, 0)
}

type GameState struct {
	Board        *Board
	CurrentPiece *Piece
	NextPiece    *Piece
	HoldPiece    *Piece
	CanHold      bool
	Score        int
	Level        int
	Lines        int
	GarbageQueue int
	IsGameOver   bool
	IsWinner     bool
	PlayerID     string
	PlayerName   string
	AttackPower  int
	PieceGen     *PieceGenerator
}

// NewGameState creates a game state with legacy random piece generation.
func NewGameState(playerID, playerName string) *GameState {
	return &GameState{
		Board:        NewBoard(),
		CurrentPiece: RandomPiece(),
		NextPiece:    RandomPiece(),
		HoldPiece:    nil,
		CanHold:      true,
		Score:        0,
		Level:        1,
		Lines:        0,
		GarbageQueue: 0,
		IsGameOver:   false,
		IsWinner:     false,
		PlayerID:     playerID,
		PlayerName:   playerName,
		AttackPower:  0,
	}
}

// NewSeededGameState creates a game state with a deterministic 7-bag generator.
func NewSeededGameState(playerID, playerName string, seed int64) *GameState {
	gen := NewPieceGenerator(seed)
	return &GameState{
		Board:        NewBoard(),
		CurrentPiece: gen.Next(),
		NextPiece:    gen.Next(),
		HoldPiece:    nil,
		CanHold:      true,
		Score:        0,
		Level:        1,
		Lines:        0,
		GarbageQueue: 0,
		IsGameOver:   false,
		IsWinner:     false,
		PlayerID:     playerID,
		PlayerName:   playerName,
		AttackPower:  0,
		PieceGen:     gen,
	}
}

func (gs *GameState) MoveLeft() bool {
	if gs.Board.IsValidPosition(gs.CurrentPiece, -1, 0) {
		gs.CurrentPiece.X--
		return true
	}
	return false
}

func (gs *GameState) MoveRight() bool {
	if gs.Board.IsValidPosition(gs.CurrentPiece, 1, 0) {
		gs.CurrentPiece.X++
		return true
	}
	return false
}

func (gs *GameState) MoveDown() bool {
	if gs.Board.IsValidPosition(gs.CurrentPiece, 0, 1) {
		gs.CurrentPiece.Y++
		return true
	}
	return false
}

func (gs *GameState) GetGhostY() int {
	ghostY := gs.CurrentPiece.Y
	for gs.Board.IsValidPosition(gs.CurrentPiece, 0, ghostY-gs.CurrentPiece.Y+1) {
		ghostY++
	}
	return ghostY
}

func (gs *GameState) HardDrop() {
	for gs.MoveDown() {
		gs.Score += 2
	}
	gs.LockPiece()
}

func (gs *GameState) Rotate() bool {
	original := gs.CurrentPiece.Shape
	gs.CurrentPiece.Rotate()

	if !gs.Board.IsValidPosition(gs.CurrentPiece, 0, 0) {
		if gs.Board.IsValidPosition(gs.CurrentPiece, -1, 0) {
			gs.CurrentPiece.X--
			return true
		}
		if gs.Board.IsValidPosition(gs.CurrentPiece, 1, 0) {
			gs.CurrentPiece.X++
			return true
		}
		if gs.Board.IsValidPosition(gs.CurrentPiece, -2, 0) {
			gs.CurrentPiece.X -= 2
			return true
		}
		if gs.Board.IsValidPosition(gs.CurrentPiece, 2, 0) {
			gs.CurrentPiece.X += 2
			return true
		}
		gs.CurrentPiece.Shape = original
		return false
	}
	return true
}

func (gs *GameState) Hold() bool {
	if !gs.CanHold {
		return false
	}

	gs.CanHold = false

	if gs.HoldPiece == nil {
		gs.HoldPiece = NewPiece(gs.CurrentPiece.Type)
		gs.CurrentPiece = gs.NextPiece
		gs.NextPiece = gs.nextPiece()
	} else {
		currentType := gs.CurrentPiece.Type
		gs.CurrentPiece = NewPiece(gs.HoldPiece.Type)
		gs.CurrentPiece.X = BoardWidth/2 - len(gs.CurrentPiece.Shape[0])/2
		gs.CurrentPiece.Y = 0
		gs.HoldPiece = NewPiece(currentType)
	}

	return true
}

// nextPiece returns the next piece using the generator if available, else random.
func (gs *GameState) nextPiece() *Piece {
	if gs.PieceGen != nil {
		return gs.PieceGen.Next()
	}
	return RandomPiece()
}

func (gs *GameState) LockPiece() int {
	gs.Board.LockPiece(gs.CurrentPiece)
	linesCleared := gs.Board.ClearLines()

	gs.Lines += linesCleared
	gs.Score += gs.calculateScore(linesCleared)
	gs.Level = gs.Lines/10 + 1

	if linesCleared > 0 {
		gs.AttackPower = gs.calculateAttack(linesCleared)
	} else {
		gs.AttackPower = 0
	}

	gs.CurrentPiece = gs.NextPiece
	gs.NextPiece = gs.nextPiece()
	gs.CanHold = true

	if gs.GarbageQueue > 0 {
		holeX := rand.Intn(BoardWidth)
		gs.Board.AddGarbageLines(gs.GarbageQueue, holeX)
		gs.GarbageQueue = 0
	}

	if gs.Board.IsGameOver(gs.CurrentPiece) {
		gs.IsGameOver = true
	}

	return linesCleared
}

func (gs *GameState) calculateScore(lines int) int {
	baseScores := map[int]int{
		1: 100,
		2: 300,
		3: 500,
		4: 800,
	}
	if score, ok := baseScores[lines]; ok {
		return score * gs.Level
	}
	return 0
}

func (gs *GameState) calculateAttack(lines int) int {
	attackTable := map[int]int{
		1: 0,
		2: 1,
		3: 2,
		4: 4,
	}
	if attack, ok := attackTable[lines]; ok {
		return attack
	}
	return 0
}

func (gs *GameState) ReceiveGarbage(lines int) {
	gs.GarbageQueue += lines
}

func (gs *GameState) Tick() bool {
	if gs.IsGameOver {
		return false
	}

	if !gs.MoveDown() {
		gs.LockPiece()
		return false
	}
	return true
}

func (gs *GameState) GetDropSpeed() time.Duration {
	speeds := []time.Duration{
		800 * time.Millisecond,
		720 * time.Millisecond,
		630 * time.Millisecond,
		550 * time.Millisecond,
		470 * time.Millisecond,
		380 * time.Millisecond,
		300 * time.Millisecond,
		220 * time.Millisecond,
		130 * time.Millisecond,
		100 * time.Millisecond,
		80 * time.Millisecond,
		80 * time.Millisecond,
		80 * time.Millisecond,
		70 * time.Millisecond,
		70 * time.Millisecond,
		70 * time.Millisecond,
		50 * time.Millisecond,
		50 * time.Millisecond,
		50 * time.Millisecond,
		30 * time.Millisecond,
	}

	if gs.Level > len(speeds) {
		return speeds[len(speeds)-1]
	}
	return speeds[gs.Level-1]
}
