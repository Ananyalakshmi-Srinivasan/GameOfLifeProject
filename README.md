# GameOfLifeProject
Here's an implementation of Conway's of Life in GoLang. I completed this as part of a pair programming project in Second Year

In this project we explored two different implementations of this game.

Parallel Implementation: 
Our first implementation mimicked parallel processing of the board by splitting the work a cross up to 16 worker threads. 

Distributed Implementation: 
Our second implementation split GOL processing across up to 4 AWS server nodes which received an input image from a client via a broker node.

+ **Fault Tolerance Attempt: ** This contains my attempt at implementing fault tolerance for Game Of Life after a user quits the game. I wanted to save and reload the current game state. Currently, this doesn't work correctly, but I plan to fix this by the end of this summer (Date: 22/04/26)

Our Report outlines the pros and cons of the parallel and distributed implementation and additional extensions we attempted (only the Fault Tolerance Attempt is added here as I completed this portion).  
