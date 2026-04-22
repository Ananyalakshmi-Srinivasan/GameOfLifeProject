package gol

import (
	"fmt"
	"log"
	"net/rpc"
	"time"
)

type distributorChannels struct {
	Events     chan<- Event
	ioCommand  chan<- ioCommand
	ioIdle     <-chan bool
	ioFilename chan<- string
	ioOutput   chan<- uint8
	ioInput    <-chan uint8
	keyPress   <-chan rune
}

// make normal calls
func makeCall(client *rpc.Client, world [][]byte, params Params) (*ClientResponse, error) {
	request := ClientRequest{
		World:  world,
		Params: params,
	} // using new broker request struct instead of request struct.

	response := new(ClientResponse)

	err := client.Call("Broker.CallGolWorker", request, response)
	return response, err

}

// make ticker calls
func makeTickerCall(client *rpc.Client, params Params) (*TickerResponse, error) {
	request := TickerRequest{
		Params: params,
	} // using new broker request struct instead of request struct.

	response := new(TickerResponse)
	err := client.Call("Broker.ProcessTicker", request, response)
	return response, err

}

// make keypress calls
func makeKeyPressCall(client *rpc.Client, params Params, keyPressed rune) (*KeyPressResponse, error) {
	request := KeyPressRequest{
		Params:     params,
		KeyPressed: keyPressed,
	}

	response := new(KeyPressResponse)
	err := client.Call("Broker.ProcessKeyPress", request, response)
	return response, err

}

// initialise a new world and collect its input from an input pgm file
func initWorld(p Params, c distributorChannels) [][]byte {
	// need to create newWorld variable to host the nextState.
	world := make([][]byte, p.ImageHeight) // initialises outer slice. to image height --> initialises rows!!
	for i := 0; i < p.ImageHeight; i++ {
		world[i] = make([]byte, p.ImageWidth) // initialises each individual column//
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

func initServer() *rpc.Client { // initial client connection is made to BROKER
	broker := "127.0.0.1:8080" // localhost broker connection
	client, err := rpc.Dial("tcp", broker)
	if err != nil {
		log.Fatal("dialing:", err)
	}

	return client
}

// the ticker goroutine will stay alive for the course of GOL, sending off ticker requests on each tick
func ticker(p Params, c distributorChannels, ticker time.Ticker, done chan bool, client *rpc.Client) {
	for {

		select {
		case <-done:
			// when ticker is done, get the alive cells one last time.
			return
		case <-ticker.C:
			response, err := makeTickerCall(client, p)
			if err != nil {
				log.Fatalf("Ticker RPC call failed: %v", err)
			}
			c.Events <- AliveCellsCount{
				CompletedTurns: response.Turn,
				CellsCount:     response.AliveCells,
			}
		}
	}

}

// save as a PGM file
func saveAsPGM(p Params, c distributorChannels, world [][]byte, turn int) {

	if len(world) == 0 {
		return
	}
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

	c.Events <- ImageOutputComplete{
		CompletedTurns: turn,
		Filename:       filename,
	}
}

// called as a go routine. this will run and pick up on keypresses.
// when the relevant keypresses are made, it will make RPC calls that will affect the current GOL computation based on the key pressed
func keyPress(p Params, c distributorChannels, client *rpc.Client, done chan bool) {

	var paused = false

	for {
		select {
		case <-done:
			return
		case key, ok := <-c.keyPress:
			if !ok {
				return
			}

			// RPC call to broker
			response, err := makeKeyPressCall(client, p, key)
			if err != nil {
				//log.Fatalf("Keypress RPC call failed: %v", err)
			}

			// if it detects the key pressed was 's', saves and outputs as a pgm
			if response.Key == 's' {
				if len(response.World) > 0 {
					saveAsPGM(p, c, response.World, response.Turn)
				}
			} else if response.Key == 'p' {

				// if already paused, then unpause and vice versa
				if paused {
					paused = false
					fmt.Printf("\nContinuing\n")
					c.Events <- StateChange{
						CompletedTurns: response.Turn,
						NewState:       Executing,
					}
				} else {
					paused = true
					c.Events <- StateChange{
						CompletedTurns: response.Turn,
						NewState:       Paused,
					}
					fmt.Printf("\nCurrent turn: %d\n", response.Turn)
				}

				// quit handling alongside client closing
			} else if response.Key == 'q' || response.Key == 'k' {

				// just return - saving as a PGM is already handled at the end
				return
			}

		}
	}
}

func onDistributorClose(p Params, c distributorChannels, world [][]byte, turn int) {
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	//c.Events <- StateChange{
	//	turn, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	saveAsPGM(p, c, world, turn)
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels) {
	world := initWorld(p, c)
	turn := 0

	// we signal the executing event; this only needs to happen once
	// to signal execution is happening for all turns
	c.Events <- StateChange{turn, Executing}

	// initialise the client to begin dialling the server
	client := initServer()
	defer client.Close()

	resetReq := ResetRequest{Reset: true}
	resetResp := new(ResetResponse)
	client.Call("Broker.ResetGOL", resetReq, resetResp)

	tickerClient := initServer()
	defer tickerClient.Close()

	keyPressClient := initServer()
	defer keyPressClient.Close()

	// send a ticker go routine to make calls to count alive cells
	newTicker := time.NewTicker(2 * time.Second)
	tickerDone := make(chan bool, 1)
	go ticker(p, c, *newTicker, tickerDone, tickerClient)

	// send keypress go function to await keypresses
	keyPressDone := make(chan bool, 1)
	go keyPress(p, c, keyPressClient, keyPressDone)

	time.Sleep(150 * time.Millisecond)

	response, err := makeCall(client, world, p) // passing in server's address instead channels

	// if there was no error, perform work on the response.
	if err == nil {

		// copy the world that has been iterated over
		world = response.World

		// indicate the final turn has been completed
		c.Events <- FinalTurnComplete{
			CompletedTurns: response.Turn,
			Alive:          response.AliveCells,
		}

		onDistributorClose(p, c, world, response.Turn)
	}

	// finish ticker
	tickerDone <- true
	keyPressDone <- true

	c.Events <- StateChange{
		response.Turn, Quitting}
	close(c.Events)

}
