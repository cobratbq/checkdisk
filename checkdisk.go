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
	go signalHandler(cmd.Process)
	//Start separate reader routine
	go processReader(stderr)
	//Waiting until the process finishes
	cmd.Wait()
}

func processReader(from io.ReadCloser) {
	fmt.Printf("Started cli reader.\n")
	var out = make(chan []byte)
	go readLines(from, out)
	for l := range out {
		fmt.Printf("Line: %s\n", l)
	}
	fmt.Printf("Stopped cli reader.\n")
}

func readLines(from io.Reader, out chan<- []byte) {
	var send = func(data []byte) {
		var result = make([]byte, len(data))
		copy(result, data)
		out <- result
	}
	var buffer = make([]byte, 10)
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
			if c == '\n' {
				line = append(line, c)
				send(line)
				line = line[:0]
			} else if c == '\b' && len(line) > 0 {
				if firstBackspace {
					send(line)
				}
				line = line[:len(line)-1]
				firstBackspace = false
			} else {
				line = append(line, c)
				firstBackspace = true
			}
		}
		if err != nil {
			break
		}
	}
	close(out)
}

func signalHandler(proc *os.Process) {
	var sigchan = make(chan os.Signal)
	signal.Notify(sigchan, os.Interrupt)
	sig := <-sigchan
	fmt.Printf("Signal caught! Passing on ...")
	proc.Signal(sig)
}
