package gol

import (
	"fmt"
	"time"

	"uk.ac.bris.cs/gameoflife/util"
)

type distributorChannels struct {
	events     chan<- Event
	ioCommand  chan<- ioCommand
	ioIdle     <-chan bool
	ioFilename chan<- string
	ioOutput   chan<- uint8
	ioInput    <-chan uint8
	keyPress   <-chan rune
}

// countNeighbours accesses the state of each of the 8 neighbours in GOL - respective to wrap arounds
func countNeighbours(y, x, h, w int, world [][]byte) int {

	// top will always be the y level above, if not it will wrap to the bottom
	top := y - 1
	if top < 0 {
		top = h - 1 // wrap to bottom
	}

	// bottom will always be the y level below, if not wrap to the top
	bottom := y + 1
	if bottom == h {
		bottom = 0 // wrap to top
	}

	// left will always be the x value behind, if not wrap to the right
	left := x - 1
	if left < 0 {
		left = w - 1 // wrap to right
	}

	// right will always be the x value in front, if not wrap to the left
	right := x + 1
	if right == w {
		right = 0 // wrap to left
	}

	return int(world[top][left]) +
		int(world[top][x]) +
		int(world[top][right]) +
		int(world[y][left]) +
		int(world[y][right]) +
		int(world[bottom][left]) +
		int(world[bottom][x]) +
		int(world[bottom][right])
}

// calculateNextState returns the next state of GOL for a given world slice
func calculateNextState(p Params, c distributorChannels, world [][]byte, startY, endY, turn int) [][]byte {

	imageWidth := p.ImageWidth   // width always stays the same as we have horizontal cuts
	imageHeight := endY - startY // the height will be the size of the worker slice - between the start and end y

	// will store the flipped cells
	cells := make([]util.Cell, imageHeight)

	// newWorld will store the slice the next state will be written to
	newWorld := make([][]byte, imageHeight)
	for i := 0; i < imageHeight; i++ {
		newWorld[i] = make([]byte, imageWidth)
	}

	// traverse each cell in the current state, count their neighbours, and change the state accordingly
	for y := startY; y < endY; y++ {
		for x := 0; x < imageWidth; x++ { //

			count := countNeighbours(y, x, p.ImageHeight, imageWidth, world)

			if world[y][x] == 255 { //starts live
				if (count/255) < 2 || (count/255) > 3 {
					newWorld[y-startY][x] = 0
					cells = append(cells, util.Cell{x, y}) // if theres a flip, add to the flipped cells array
				} else {
					newWorld[y-startY][x] = 255
				}
			}
			if world[y][x] == 0 {
				if (count / 255) == 3 {
					newWorld[y-startY][x] = 255
					cells = append(cells, util.Cell{x, y})
				} else {
					newWorld[y-startY][x] = 0
				}
			}
		}
	}
	c.events <- CellsFlipped{CompletedTurns: turn, Cells: cells}

	return newWorld
}

// calculateAliveCells calculates what cells are alive on the board
func calculateAliveCells(p Params, world [][]byte) []util.Cell {
	var cells []util.Cell
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			if world[y][x] == 255 {
				cells = append(cells, util.Cell{x, y})
			}
		}
	}
	return cells
}

// initWorld initialises a world, sending the relevant events and collecting input from a pgm file
func initWorld(p Params, c distributorChannels) [][]byte {

	world := make([][]byte, p.ImageHeight)
	for i := 0; i < p.ImageHeight; i++ {
		world[i] = make([]byte, p.ImageWidth)
	}

	// send a reading command to read the PGM image
	c.ioCommand <- ioInput
	// creates filename -> to allow file to be read in
	c.ioFilename <- fmt.Sprintf("%dx%d", p.ImageWidth, p.ImageHeight)

	// byte by byte we read the state of the image to the world
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			// gives the data cell by cell
			world[y][x] = <-c.ioInput
		}
	}

	return world
}

// worker function
func worker(p Params, c distributorChannels, startY, endY int, out chan<- [][]byte, job chan [][]byte, turnChan chan int) {

	// infinite loop to allow the worker to stay persistent
	for {
		// accept the next turn
		turn := <-turnChan
		// block until a world state arrives at a goroutine
		world, ok := <-job

		// if the worker has been closed, leave the loop
		if !ok {
			return
		}

		newWorld := calculateNextState(p, c, world, startY, endY, turn)

		// send the world slice back to distributor
		out <- newWorld

	}
}

// ticker
func ticker(p Params, c distributorChannels, ticker time.Ticker, done chan bool, tickerWorld chan [][]byte, turnChan chan int, tickerReady chan bool) {

	// infinite loop for goroutine to stay persistent
	for {

		select {
		case <-done:
			return
		case <-ticker.C:
			tickerReady <- true

			// block waiting for turn / world
			turn := <-turnChan
			world := <-tickerWorld
			c.events <- AliveCellsCount{
				CompletedTurns: turn,
				CellsCount:     len(calculateAliveCells(p, world)),
			}
		}

	}
}

// saves the current board and outputs as a pgm file
func saveCurrentBoard(p Params, c distributorChannels, world [][]byte, turn int, hasFinished bool) {

	// 4. REPORT FINAL STATE
	c.ioCommand <- ioOutput
	filename := fmt.Sprintf("%dx%dx%d", p.ImageWidth, p.ImageHeight, turn)
	c.ioFilename <- filename

	// byte by byte sends the world to be read into a PGM
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			// gives the data cell by cell
			c.ioOutput <- world[y][x]
		}
	}

	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- ImageOutputComplete{
		CompletedTurns: turn,
		Filename:       filename,
	}
}

// calls at end of processing
func finalTurnComplete(p Params, c distributorChannels, world [][]byte, turn int) {

	// 3. SIGNAL FINAL TURN HAS BEEN COMPLETED
	c.events <- FinalTurnComplete{
		CompletedTurns: p.Turns,
		Alive:          calculateAliveCells(p, world),
	}

	// save the current board as a PGM
	// pass in true = we have finished a final turn, no need to send an output file.
	saveCurrentBoard(p, c, world, turn, true)

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{
		p.Turns, Quitting}
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels) {

	// 1. INITIALISE NEW WORLD
	world := initWorld(p, c)
	turn := 0

	// first cells flipped check
	cells := calculateAliveCells(p, world)
	c.events <- CellsFlipped{CompletedTurns: turn, Cells: cells}

	c.events <- StateChange{turn, Executing}

	// 2. ITERATE OVER TURNS AND COMPUTE
	if p.Threads == 1 {

		for turn < p.Turns {
			world = calculateNextState(p, c, world, 0, p.ImageHeight, turn)
			turn++
		}

	} else {
		// parallel implementation
		// channels
		output := make([]chan [][]byte, p.Threads) // make a slice to contain channel data  type 2d byte, of size threads//
		for i := range output {
			output[i] = make(chan [][]byte) // populate slice with channels
		}

		// make a slice of channels - each channel is assigned to a go-routine
		jobs := make([]chan [][]byte, p.Threads)
		turnChannels := make([]chan int, p.Threads)

		for i := 0; i < p.Threads; i++ {
			jobs[i] = make(chan [][]byte, 1)
			turnChannels[i] = make(chan int, 1)

			workerHeight := p.ImageHeight / p.Threads
			startY := workerHeight * i

			endY := workerHeight * (i + 1)
			if i == p.Threads-1 {
				// Ensure the last worker takes all remaining rows
				endY = p.ImageHeight
			}

			// begin the go-routines relative to the no' of threads
			go worker(p, c, startY, endY, output[i], jobs[i], turnChannels[i])

		}

		// send a ticker go routine to count alive cells
		newTicker := time.NewTicker(2 * time.Second)
		done := make(chan bool)
		turnChan := make(chan int)
		tickerWorld := make(chan [][]byte)
		tickerReady := make(chan bool)
		go ticker(p, c, *newTicker, done, tickerWorld, turnChan, tickerReady)

		// main turn loop
		for turn < p.Turns {

			// start pausing
			pauseLoop := true
			keepPause := false

			// checks if 'q' has been pressed in order to complete the current turn
			terminateAtEndOfTurn := false
			stopExecuting := false

			// start pausing
			for pauseLoop {

				// if it hasn't been signalled to keep pausing, break from the loop
				if !keepPause {
					pauseLoop = false
				}

				select {

				// if the ticker has signalled it is ready, send it the required information it needs to output the current alive cells
				case <-tickerReady:
					turnChan <- turn
					tickerWorld <- world
				case key := <-c.keyPress:

					if key == 's' {
						// save the current board and continue execution
						go saveCurrentBoard(p, c, world, turn, false)
					} else if key == 'q' {

						// if GOL has not been paused,
						if !keepPause {

							// terminate at end of the turn
							terminateAtEndOfTurn = true
						} else {

							// stop execution
							stopExecuting = true

							// unpause the program
							keepPause = false
							pauseLoop = false
						}
					} else if key == 'p' {

						// if already paused and 'p' has been pressed, unpause the loop
						if keepPause {

							// unpause
							pauseLoop = false
							keepPause = false

							// signal a state change
							c.events <- StateChange{
								CompletedTurns: turn,
								NewState:       Executing,
							}

							// if unpaused and 'p' has been pressed
						} else {

							// pause
							pauseLoop = true
							keepPause = true

							// signal a state change
							c.events <- StateChange{
								CompletedTurns: turn,
								NewState:       Paused,
							}
						}
					}
					// otherwise, continue execution if no keypresses
				default:
					break // breaks out of the select statement, which will continue looping if not signalled to unpause
				}
			}

			// if signalled to stop executing immediately, break out of the loop
			// e.g if paused and the user wants to quit, it will skip to world state saving
			if stopExecuting {
				break
			}

			newWorld := make([][]byte, p.ImageHeight)
			for i := 0; i < p.ImageHeight; i++ {
				newWorld[i] = make([]byte, p.ImageWidth)
			}

			// for each turn, send the current world and current turn to all of the goroutines to begin work
			for i := 0; i < p.Threads; i++ {
				jobs[i] <- world
				turnChannels[i] <- turn
			}

			// then wait for the output after all goroutines have done work this turn
			for i := 0; i < p.Threads; i++ {
				part := <-output[i] // This is the slice from worker i

				// Recalculate this worker's bounds
				workerHeight := p.ImageHeight / p.Threads
				startY := workerHeight * i
				endY := workerHeight * (i + 1)

				// if image height is not perfectly divisible by the no' of threads, ensures the remainder row is assigned to the last worker
				if i == p.Threads-1 {
					endY = p.ImageHeight
				}

				// Copy the part's rows into the correct place
				for y := startY; y < endY; y++ {
					// y - startY maps the global row 'y' to the local 'part' row index
					newWorld[y] = part[y-startY]
				}
			}

			world = newWorld

			// if true, the program will have completed the turn and started saving and terminating the program
			c.ioCommand <- ioCheckIdle
			<-c.ioIdle

			c.events <- TurnComplete{CompletedTurns: turn}
			turn++

			// if signalled to terminate at the end of the turn, it will break out of the loop and stop execution
			// e.g if unpaused and quit has been pressed, do this
			if terminateAtEndOfTurn {
				break
			}

		}

		for i := 0; i < p.Threads; i++ {
			close(jobs[i])
		}

		done <- true
		newTicker.Stop()

	}

	finalTurnComplete(p, c, world, turn)

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
