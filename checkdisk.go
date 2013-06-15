package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
)

func main() {
	var cmd = exec.Command("/usr/sbin/badblocks", "-sv", "/dev/sda")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		fmt.Printf("Error: %s", err.Error())
		return
	}
	//Start process
	if err = cmd.Start(); err != nil {
		fmt.Printf("Error starting process: %s", err.Error())
		return
	}
	//Prepare signal handler
	stopSignal := make(chan struct{})
	go signalHandler(stopSignal, cmd.Process)
	//Start separate reader routine
	go processHandler(stderr)
	//Waiting until the process finishes
	cmd.Wait()
	close(stopSignal)
}

func processHandler(from io.ReadCloser) {
	var out = make(chan []byte)
	go readLines(from, out)
	for l := range out {
		fmt.Printf("Line: %s\n", l)
	}
}

func readLines(from io.Reader, out chan<- []byte) {
	defer close(out)
	var buffer = make([]byte, 80)
	var fill = func() error {
		n, err := from.Read(buffer)
		if n < len(buffer) {
			buffer = buffer[:n]
		}
		return err
	}
	var line []byte
	var firstBackspace = true
	for {
		err := fill()
		for _, c := range buffer {
			switch c {
			case '\n':
				send(out, line)
				line = line[:0]
				firstBackspace = true
			case '\b':
				if firstBackspace {
					send(out, line)
					firstBackspace = false
				}
				if len(line) > 0 {
					line = line[:len(line)-1]
				}
			default:
				line = append(line, c)
				firstBackspace = true
			}
		}
		if err != nil {
			break
		}
	}
}

func send(to chan<- []byte, data []byte) {
	var result = make([]byte, len(data))
	copy(result, data)
	to <- result
}

func signalHandler(stopsig <-chan struct{}, proc *os.Process) {
	// Register for Interrupt signals.
	var sigchan = make(chan os.Signal)
	signal.Notify(sigchan, os.Interrupt)
	defer signal.Stop(sigchan)
	// Start signaling loop.
	var done = false
	for !done {
		select {
		case sig := <-sigchan:
			proc.Signal(sig)
		case <-stopsig:
			done = true
		}
	}
}
