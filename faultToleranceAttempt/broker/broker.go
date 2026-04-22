package main

import (
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/rpc"
	"sync"
	"time"

	"uk.ac.bris.cs/gameoflife/gol"
	"uk.ac.bris.cs/gameoflife/util"
)

var servers = []string{
	//"107.22.99.237:8030",
	//	"54.87.205.120:8030",
	//	"3.93.192.169:8030",
	//	"13.221.189.8:8030",
	"127.0.0.1:8030",
}

var shutdownChannel = make(chan bool, 1)

func makeClient(serverAddress string) (*rpc.Client, error) {
	client, err := rpc.Dial("tcp", serverAddress)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	return client, err
}

type Broker struct {
	Listener net.Listener
}

// this struct is needed to maintain info needed for the ticker and keypress functions
// for ticker we need to know the alive cells every 2 seconds...without the correct processing of turn tests will fail.

type globals struct {
	world           [][]byte
	turn            int
	mu              sync.RWMutex // a RW mutex allows you to lock and unlock specifically for reading shared memory. means we can read data without blocking other go routines from reading it too!
	paused          bool
	shutdownChannel chan bool
	servers         []string
}

var g = globals{
	world:           [][]byte{},
	turn:            0,
	mu:              sync.RWMutex{},
	paused:          false,
	shutdownChannel: make(chan bool, 1),
	servers:         servers,
}

func calculateAliveCells(p gol.Params, world [][]byte) []util.Cell {
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

// ProcessKeyPress this is the core work done when a key press is pressed
func (b *Broker) ProcessKeyPress(req gol.KeyPressRequest, res *gol.KeyPressResponse) (err error) {

	if req.KeyPressed == 'p' {
		// send a pause signal: inverts the paused flag to say that the pause has been toggled (e.g false turns to true, if pressed again, true turns to false)

		g.mu.Lock()
		g.paused = !g.paused     // flip paused bool to pause or unpause
		res.Key = req.KeyPressed //set results Key to the value of the key pressed i.e. p .
		res.Turn = g.turn        // all return the turn at which it was paused or unpaused
		g.mu.Unlock()

	} else { // for key press s, q, and k we are expected to save the state

		// this saves the state//

		// RLock - only for reading the state (doesn't block other readers)
		g.mu.RLock()

		// make a copy of the world to pass to response
		res.World = make([][]byte, len(g.world))
		for i := range g.world {
			res.World[i] = make([]byte, len(g.world[i]))
			copy(res.World[i], g.world[i])
		}

		res.Key = req.KeyPressed
		res.Turn = g.turn
		g.mu.RUnlock()

		// essentially, send the right data down the right response.variables
	}

	if req.KeyPressed == 'k' {

		// shut down the broker
		go func() {
			// small delay to allow function to return (OK signal to client) and then close the listener
			time.Sleep(2000 * time.Millisecond)
			b.Listener.Close()
		}()

		for _, serverAddr := range servers {

			client, dialErr := rpc.Dial("tcp", serverAddr)
			if dialErr != nil {
				log.Printf("Skipping server as already closedt: %v", err)
				continue
			}

			shutDownReq := gol.ShutdownRequest{
				Shutdown: true,
			}

			shutdownResponse := new(gol.ShutdownResponse)
			shutdownError := client.Call("GOLOperations.Shutdown", shutDownReq, shutdownResponse)
			if shutdownError != nil {
				fmt.Printf("server shutdown failed: %v", shutdownError)
			}

			client.Close()

		}

	}

	return
}

func (b *Broker) ProcessTicker(req gol.TickerRequest, res *gol.TickerResponse) (err error) {

	// lock and unlock and take snapshots of data we need
	g.mu.RLock()
	worldSnapshot := g.world
	turnSnapshot := g.turn
	g.mu.RUnlock()

	// calculate the alive cells and gets the turn - sends them to response
	res.AliveCells = len(calculateAliveCells(req.Params, worldSnapshot))
	res.Turn = turnSnapshot

	return

}

func (b *Broker) CallGolWorker(req gol.ClientRequest, res *gol.ClientResponse) (dispatchError error) {

	turn := 0

	g.mu.Lock()
	g.world = req.World
	g.turn = turn
	g.paused = false
	g.mu.Unlock()

	// make workers //
	// storing the clients for the serves we connect to
	clients := make([]*rpc.Client, len(servers))

	// populate with clients for each of the servers. start connection for each server
	for i, serverAddr := range servers {

		client, err := makeClient(serverAddr)

		if err != nil {
			log.Fatalf("Error dialing server %s: %v", serverAddr, err)
		}
		clients[i] = client
		// defer will only close the client at the END of GOL (function returns)
		defer clients[i].Close()
	}

	responseWorld := make([][][]byte, len(servers))

	workerHeight := req.Params.ImageHeight / len(servers)

	for i := range responseWorld {
		responseWorld[i] = make([][]byte, workerHeight)
	}

	for turn < req.Params.Turns {
		newWorld := make([][]byte, req.Params.ImageHeight)

		for i := 0; i < req.Params.ImageHeight; i++ {
			newWorld[i] = make([]byte, req.Params.ImageWidth)
		}

		g.mu.RLock()
		isPaused := g.paused
		currentWorld := g.world
		currentTurn := g.turn
		g.mu.RUnlock()

		if isPaused {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// server wait group - waits for all servers to be done to assemble
		var wg sync.WaitGroup

		for i, _ := range servers {
			wg.Add(1) // a server has started work, let the wait group know

			// server starts as a separate thread go routine
			go func(i int) {
				defer wg.Done() // tell the broker that the server has finished once this function returns

				startY := workerHeight * i
				endY := workerHeight * (i + 1)

				brokerReq := gol.BrokerRequest{
					World:  currentWorld,
					Turn:   currentTurn,
					Params: req.Params,
					StartY: startY,
					EndY:   endY,
				}

				brokerResponse := new(gol.BrokerResponse)
				err := clients[i].Call("GOLOperations.ProcessGOL", brokerReq, brokerResponse)
				if err != nil { // this error will be triggered if the client tries to access the server after a server's been shutdown.
					return //exits the rpc call and allows broker to shut down gracefully.
					// doesnt allow GOL to continue with broken responses if it hasnt been able to call the server
				}
				for y := brokerReq.StartY; y < brokerReq.EndY; y++ {
					// y - startY maps the global row 'y' to the local 'part' row index
					newWorld[y] = brokerResponse.World[y-brokerReq.StartY]
				}

			}(i)
		}

		// wait for each server added to the wait group to finish work
		wg.Wait()

		g.mu.Lock()
		g.world = newWorld
		g.turn++
		turn = g.turn
		g.mu.Unlock()
	}

	g.mu.Lock()
	res.World = g.world
	res.AliveCells = (calculateAliveCells(req.Params, g.world))
	g.mu.Unlock()
	return
}

func main() {
	pAddr := "8080"
	rand.Seed(time.Now().UnixNano())

	listener, _ := net.Listen("tcp", ":"+pAddr)

	// adds listener to Broker struct so it can be accessed by the other routines
	broker := &Broker{
		Listener: listener,
	}
	rpc.Register(broker)

	rpc.Accept(listener)

}
