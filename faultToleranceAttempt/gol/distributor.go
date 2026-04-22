package gol

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/rpc"
	"os"
	"path/filepath"
	"strconv"
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

func makeKeyPressCall(client *rpc.Client, params Params, keyPressed rune) (*KeyPressResponse, error) {
	request := KeyPressRequest{
		Params:     params,
		KeyPressed: keyPressed,
	}

	response := new(KeyPressResponse)
	err := client.Call("Broker.ProcessKeyPress", request, response)
	return response, err

}

func printError(err error) error {
	return fmt.Errorf("Error: %v", err)
}

func copyFile(src string, dest string) {
	srcFile, err := os.Open(src + ".pgm")
	err = printError(err)
	defer srcFile.Close()

	destFile, err := os.Create(dest)
	err = printError(err)
	defer destFile.Close()

	_, err = io.Copy(destFile, srcFile)
	err = printError(err)
}

func initWorld(p Params, c distributorChannels, turnChan chan int) [][]byte {

	turn := 0
	destFile := ""

	_, err := os.Stat("faultTolerance.txt")
	if err == nil {

		file, err := os.Open("faultTolerance.txt")
		if err != nil {
			fmt.Println("Error opening faultTolerance.txt")
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)

		contents := []string{}

		for scanner.Scan() {
			contents = append(contents, scanner.Text())
		}
		turn, _ = strconv.Atoi(contents[0])
		p.ImageWidth, _ = strconv.Atoi(contents[1])
		p.ImageHeight, _ = strconv.Atoi(contents[2])
		srcFile := filepath.Join("out", contents[3])

		destFile = filepath.Join("images", fmt.Sprintf("%dx%d", p.ImageWidth, p.ImageHeight)) + ".pgm"

		copyFile(srcFile, destFile)

		// delete fault tolerance file after --> to ensure there isn't a used file there when starting a new round...
		// i.e. when gol terminates without quitting/killing
		//delErr := os.Remove("faultTolerance.txt")
		//if delErr != nil {
		//	fmt.Println("Error removing faultTolerance.txt")
		//}
	}

	// need to create newWorld variable to host the nextState.
	world := make([][]byte, p.ImageHeight) // initialises outer slice. to image height --> initialises rows!!
	for i := 0; i < p.ImageHeight; i++ {
		world[i] = make([]byte, p.ImageWidth) // initialises each individual column//
	}

	// send a reading command to read the PGM image
	// creates filename -> to allow file to be read in

	c.ioFilename <- fmt.Sprintf("%dx%d", p.ImageWidth, p.ImageHeight)
	c.ioCommand <- ioInput

	// byte by byte we read the state of the image to the world
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			// gives the data cell by cell
			world[y][x] = <-c.ioInput
		}
	}
	turnChan <- turn
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

func saveAsPGM(p Params, c distributorChannels, world [][]byte, turn int) {
	filename := fmt.Sprintf("%dx%dx%d", p.ImageWidth, p.ImageHeight, turn)
	fmt.Println("Quitting and fault tolerance")

	// fault tolerance //
	outputFilePath := "faultTolerance.txt"
	file, err := os.Create(outputFilePath)
	printError(err)
	defer file.Close()

	_, err = file.WriteString(fmt.Sprintf("%d", turn) + "\n")
	printError(err)

	_, err = file.WriteString(fmt.Sprintf("%d", p.ImageWidth) + "\n")
	printError(err)

	_, err = file.WriteString(fmt.Sprintf("%d", p.ImageHeight) + "\n")
	printError(err)

	_, err = file.WriteString(filename + "\n")
	printError(err)

	// 4. REPORT FINAL STATE
	c.ioCommand <- ioOutput
	c.ioFilename <- filename
	//faultToleranceChan <- filename

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
			// sends off as a go routine
			// this is so that it can receive another key press when it wants to
			go func(key rune) {
				// RPC call to broker
				response, err := makeKeyPressCall(client, p, key)
				if err != nil {
					//log.Fatalf("Keypress RPC call failed: %v", err)
				}

				// if it detects the key pressed was 's', saves and outputs as a pgm
				if response.Key == 's' {
					saveAsPGM(p, c, response.World, response.Turn)
				} else if response.Key == 'p' {

					if paused {
						paused = false
						fmt.Printf("\nContinuing\n")
					} else {
						paused = true
						fmt.Printf("\nCurrent turn: %d\n", response.Turn)
					}

				} else if response.Key == 'q' || response.Key == 'k' {

					onDistributorClose(p, c, response.World, response.Turn)

					err = client.Close()
					if err != nil {
						log.Fatalf("Client closing failed %v", err)
					}
				}

			}(key) // this means that we're passing the value of 'key' into this anonymous go function

		}
	}
}

func onDistributorClose(p Params, c distributorChannels, world [][]byte, turn int) {
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.Events <- StateChange{
		turn, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	saveAsPGM(p, c, world, turn)
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels) {
	turnChan := make(chan int, 1)
	world := initWorld(p, c, turnChan)
	turn := 0
	// we signal the executing event; this only needs to happen once
	// to signal execution is happening for all turns
	c.Events <- StateChange{turn, Executing}

	// initialise the client to begin dialling the server
	client := initServer()
	defer client.Close()

	tickerClient := initServer()
	defer tickerClient.Close()

	keyPressClient := initServer()
	defer keyPressClient.Close()

	// send a ticker go routine to make calls to count alive cells
	newTicker := time.NewTicker(2 * time.Second)
	tickerDone := make(chan bool)
	go ticker(p, c, *newTicker, tickerDone, tickerClient)

	// send keypress go function to await keypresses
	keyPressDone := make(chan bool)
	go keyPress(p, c, keyPressClient, keyPressDone)

	response, err := makeCall(client, world, p) // passing in server's address instead channels

	// if there was no error, perform work on the response.
	if err == nil {

		// copy the world that has been iterated over
		world = response.World

		// indicate the final turn has been completed
		c.Events <- FinalTurnComplete{
			CompletedTurns: p.Turns,
			Alive:          response.AliveCells,
		}

		onDistributorClose(p, c, world, p.Turns)
	}

	// finish ticker
	tickerDone <- true
	keyPressDone <- true

	close(c.Events)

}
