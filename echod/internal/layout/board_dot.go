//go:build dot && !cannoli

package layout

// buildBoard is the board this unit is, read off it (detectBoard): the Dot's build serves the Echo Dot
// 2nd gen, the Echo 2nd gen, the Echo Dot 3rd gen and the Echo Input.
func buildBoard() (board, model, name string) { return boardInfo(detectBoard()) }

// Cannoli is whether this is the Echo Dot 5th gen, which has a build of its own (board_cannoli.go):
// never on this one.
func Cannoli() bool { return false }
