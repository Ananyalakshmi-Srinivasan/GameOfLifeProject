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

// you would populate this array with the list of server addresses + port that you wish to divide the work between
// currently, it only includes the local address on port 8030
// ensure all AWS nodes can accept inbound traffic from port 8030
var servers = []string{
	"127.0.0.1:8030",
}

// dials a server and returns the client
func makeClient(serverAddress string) (*rpc.Client, error) {
	client, err := rpc.Dial("tcp", serverAddress)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	return client, err
}

// the listener is global in the broker struct. this is to ensure that broker connection accepting can be terminated globally (by any rpc request)
type Broker struct {
	Listener net.Listener
}

// these are all the global variables that we may access across RPC calls
type globals struct {
	world           [][]byte
	turn            int
	mu              sync.RWMutex // a RW mutex allows you to lock and unlock specifically for reading shared memory. means we can read data without blocking other go routines from reading it too!
	paused          bool
	shutdownChannel chan bool
	servers         []string
	params          gol.Params
	quit            bool
}

var g = globals{
	world:           [][]byte{},
	turn:            0,
	mu:              sync.RWMutex{},
	paused:          false,
	shutdownChannel: make(chan bool, 1),
	servers:         servers,
	quit:            false,
}

// calculates the alive cells of a given world
func calculateAliveCells(p gol.Params, world [][]byte) []util.Cell {
	var cells []util.Cell
	if len(world) == 0 {
		return cells
	}
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
		res.AliveCells = calculateAliveCells(g.params, g.world)
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

		// when a close request has been sent, we must iterate through all servers and send them a shutdown request - this will close them gracefully
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

			g.mu.Lock()
			g.quit = true
			g.mu.Unlock()

			// shut down the broker
			go func() {
				// small delay to allow function to return (OK signal to client) and then close the listener
				time.Sleep(2000 * time.Millisecond)
				b.Listener.Close()
			}()

		}

	}
	if req.KeyPressed == 'q' {
		g.mu.Lock()
		g.quit = true
		g.mu.Unlock()
	}

	return
}

// returns relevant ticker information when a ticker request has been received
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

func (b *Broker) ResetGOL(req gol.ResetRequest, res *gol.ResetResponse) (dispatchError error) {
	if req.Reset == true {

		g.mu.Lock()
		g.quit = true //
		g.mu.Unlock()

		// 2. WAIT FOR ZOMBIES TO DIE
		time.Sleep(100 * time.Millisecond)

		// 3. RESET STATE
		g.mu.Lock()
		g.world = [][]byte{}
		g.turn = 0
		g.paused = false // Clear pause state here
		g.quit = false   // Ready for new worker
		g.mu.Unlock()

		res.Failed = false
	}
	return
}

// performs the bulk of GOL handling and distribution of work
func (b *Broker) CallGolWorker(req gol.ClientRequest, res *gol.ClientResponse) (dispatchError error) {

	// initialises globals that will be used for different rpc handlers
	turn := 0
	g.mu.Lock()
	g.world = req.World
	g.turn = turn
	g.params = req.Params
	g.mu.Unlock()

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

	// calculates the slice to which each worker will operate on
	workerHeight := req.Params.ImageHeight / len(servers)

	// begins the main GOL turn loop
	for turn < req.Params.Turns {

		g.mu.RLock()
		if g.quit {
			g.mu.RUnlock()
			break
		}
		g.mu.RUnlock()

		// this newWorld will hold the computed state after a turn
		newWorld := make([][]byte, req.Params.ImageHeight)
		for i := 0; i < req.Params.ImageHeight; i++ {
			newWorld[i] = make([]byte, req.Params.ImageWidth)
		}

		// we check the state of our global variables
		g.mu.RLock()
		isPaused := g.paused
		currentWorld := g.world
		currentTurn := g.turn
		g.mu.RUnlock()

		// the main paused loop - it will poll as to not hoard CPU usage
		if isPaused {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// server wait group - waits for all servers to be done to assemble
		var wg sync.WaitGroup

		for i, _ := range servers {
			wg.Add(1) // a server has started work, let the wait group know

			// server starts as a separate thread go routine so that all servers can be started concurrently
			go func(i int) {
				defer wg.Done() // tell the broker that the server has finished once this function returns

				// we calculate the bounds a worker will work between and give this information to the worker
				startY := workerHeight * i
				endY := workerHeight * (i + 1)

				// formulates the rpc request that will give the worker the information needed to begin work
				brokerReq := gol.BrokerRequest{
					World:  currentWorld,
					Turn:   currentTurn,
					Params: req.Params,
					StartY: startY,
					EndY:   endY,
				}

				brokerResponse := new(gol.BrokerResponse)

				// calls each server to perform the game of life
				err := clients[i].Call("GOLOperations.ProcessGOL", brokerReq, brokerResponse)

				if err != nil { // this error will be triggered if the client tries to access the server after a server's been shutdown.
					return //exits the rpc call and allows broker to shut down gracefully.
					// doesnt allow GOL to continue with broken responses if it hasnt been able to call the server
				} //important - this error handling will stop the broker from accessing inaccessible response data if the servers have already been shutdown

				// access the response data and write to the relevant bounds of this turn's state
				for y := brokerReq.StartY; y < brokerReq.EndY; y++ {
					// y - startY maps the global row 'y' to the local 'part' row index
					newWorld[y] = brokerResponse.World[y-brokerReq.StartY]
				}

			}(i)
		}

		// wait for each server added to the wait group to finish work
		wg.Wait()

		// updates variables ready for next state
		g.mu.Lock()
		g.world = newWorld
		g.turn++
		turn = g.turn
		g.mu.Unlock()
	}

	// sends off final world once GOL has been complete
	g.mu.Lock()
	res.World = g.world
	res.AliveCells = (calculateAliveCells(req.Params, g.world))
	res.Turn = g.turn
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
