package gol

import "uk.ac.bris.cs/gameoflife/util"

// var GOLHandler = "GOLOperations.ProcessGOL"
var GOLHandler = "Broker.CallGolWorker"
var TickerHandler = "Broker.CallGolTicker"

type ClientResponse struct {
	World      [][]byte
	AliveCells []util.Cell
	Turn       int
}

type ClientRequest struct {
	World  [][]byte
	Params Params
}

type BrokerRequest struct {
	World  [][]byte
	Turn   int
	Params Params
	StartY int
	EndY   int
}

type BrokerResponse struct {
	World [][]byte
	Turn  int
}

type TickerResponse struct {
	Turn       int
	AliveCells int
}

type TickerRequest struct {
	Params Params
}

// special keypress structs to send and receive the specific key press data we want
type KeyPressRequest struct {
	Params     Params
	KeyPressed rune
}

type KeyPressResponse struct {
	World      [][]byte
	Key        rune
	Turn       int
	AliveCells []util.Cell
}

type ShutdownRequest struct {
	Shutdown bool
}

type ShutdownResponse struct {
	Failed error
}

type ResetRequest struct {
	Reset bool
}

type ResetResponse struct {
	Failed bool
}
