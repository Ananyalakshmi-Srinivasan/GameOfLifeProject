package main

import (
	"log"
	"math/rand"
	"net"
	"net/rpc"
	"time"

	"uk.ac.bris.cs/gameoflife/gol"
)

func countNeighbours(y, x, h, w int, world [][]byte) int {

	top := y - 1
	if top < 0 {
		top = h - 1 // wrap to bottom
	}

	bottom := y + 1
	if bottom == h {
		bottom = 0 // wrap to bottom
	}

	left := x - 1
	if left < 0 {
		left = w - 1 // wrap to right
	}

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

func CalculateNextState(p gol.Params, world [][]byte, startY, endY int) [][]byte {

	imageWidth := p.ImageWidth
	imageHeight := endY - startY

	// need to create  newWorld variable to host the nextState.
	newWorld := make([][]byte, imageHeight) // initialises outer slice. to image height --> initialises rows!!
	for i := 0; i < imageHeight; i++ {
		newWorld[i] = make([]byte, imageWidth) // initialises each individual column//
	}

	for y := startY; y < endY; y++ { // game of life traverses row by row...
		for x := 0; x < imageWidth; x++ { //
			count := countNeighbours(y, x, p.ImageHeight, p.ImageWidth, world)

			if world[y][x] == 255 { //starts live
				if (count/255) < 2 || (count/255) > 3 {
					newWorld[y-startY][x] = 0
				} else {
					newWorld[y-startY][x] = 255
				}
			}
			if world[y][x] == 0 {
				if (count / 255) == 3 {
					newWorld[y-startY][x] = 255
				} else {
					newWorld[y-startY][x] = 0
				}
			}
		}
	}
	return newWorld
}

// GOLOperations the listener will be inside our GOLoperations struct as a state that can be modified by the processes
type GOLOperations struct {
	Listener net.Listener
}

// simply calculates the next state of a given world slice and returns it via response
func (s *GOLOperations) ProcessGOL(req gol.BrokerRequest, res *gol.BrokerResponse) (err error) {

	subWorld := req.World
	subWorld = CalculateNextState(req.Params, subWorld, req.StartY, req.EndY)

	//turn++
	res.World = subWorld

	return

}

// simply closes the listener if given a properly formatted shutdown request
func (s *GOLOperations) Shutdown(req gol.ShutdownRequest, res *gol.ShutdownResponse) (err error) {
	if req.Shutdown {
		go func() {
			// small delay to allow function to return (OK signal to client) and then close the listener
			time.Sleep(100 * time.Millisecond)
			res.Failed = s.Listener.Close()
		}()
	}
	return
}

func main() {

	pAddr := "8030"
	rand.Seed(time.Now().UnixNano())
	listener, err := net.Listen("tcp", ":"+pAddr)

	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	golOps := &GOLOperations{
		Listener: listener,
	}
	rpc.Register(golOps)

	rpc.Accept(listener)

}
