module github.com/unxed/f4

go 1.24.0

require (
	github.com/tetratelabs/wazero v1.11.0
	github.com/unxed/vtinput v0.0.0
	github.com/unxed/vtui v0.0.0
	github.com/yuin/gopher-lua v1.1.1
	golang.org/x/term v0.40.0
)

require (
	github.com/mattn/go-runewidth v0.0.15 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
)

replace github.com/unxed/vtinput => ../vtinput

replace github.com/unxed/vtui => ../vtui
