package tests

import (
	"fmt"
	"os"
	"testing"

	"uk.ac.bris.cs/gameoflife/gol"
)

const benchLength = 1000

func BenchmarkStudentVersion(b *testing.B) {
	// FIX: Use os.DevNull instead of os.Pipe to prevent buffer deadlocks that crash the IDE
	nullFile, err := os.Open(os.DevNull)
	if err != nil {
		b.Fatalf("Failed to open dev null: %v", err)
	}
	defer nullFile.Close()

	oldStdout := os.Stdout
	os.Stdout = nullFile

	defer func() {
		os.Stdout = oldStdout
	}()

	p := gol.Params{
		Turns:       benchLength,
		Threads:     8,
		ImageWidth:  512,
		ImageHeight: 512,
	}

	name := fmt.Sprintf("%dx%dx%d-%d", p.ImageWidth, p.ImageHeight, p.Turns, p.Threads)

	b.Run(name, func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			events := make(chan gol.Event, 1000)
			keyPresses := make(chan rune)

			go func() {
				for range events {
				}
			}()

			gol.Run(p, events, keyPresses)
			close(keyPresses)
		}
	})
}
