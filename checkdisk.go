package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
)

func main() {
	var cmd = exec.Command("/usr/sbin/badblocks", "-sv", "testdisk")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Printf("Error: %s\n", err.Error())
		return
	}
	//Start process
	if err = cmd.Start(); err != nil {
		log.Printf("Error starting process: %s\n", err.Error())
		return
	}
	//Prepare signal handler
	stopSignal := make(chan struct{})
	go signalHandler(stopSignal, cmd.Process)
	//Start separate reader routine
	report := make(chan *CheckState)
	go processHandler(stderr, report)
	//Waiting until the process finishes
	cmd.Wait()
	close(stopSignal)
	//Process reported state
	var state = <-report
	if block := state.InterruptBlock; block != nil {
		log.Printf("Check has been interrupted at block %d\n", *block)
	} else {
		log.Printf("Successful execution.\n")
	}
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

var startPattern = regexp.MustCompile(`^Checking blocks (\d+) to (\d+)$`)
var progressPattern = regexp.MustCompile(`^Checking for bad blocks[^:]*: `)
var progressDonePattern = regexp.MustCompile(`Checking for bad blocks[^:]*: done`)
var finishedPattern = regexp.MustCompile(`^Pass completed, (\d+) bad blocks found. \((\d+)/(\d+)/(\d+) errors\)$`)
var interruptedPattern = regexp.MustCompile(`^Interrupted at block (\d+)$`)

func processHandler(from io.ReadCloser, report chan<- *CheckState) {
	var out = make(chan []byte)
	go readLines(from, out)
	var state *CheckState
	for l := range out {
		if startPattern.Match(l) {
			var matches = startPattern.FindSubmatch(l)
			from, _ := strconv.ParseUint(string(matches[1]), 10, 64)
			to, _ := strconv.ParseUint(string(matches[2]), 10, 64)
			state = &CheckState{From: from, To: to, InterruptBlock: nil}
		} else if progressPattern.Match(l) {
			fmt.Println("Progress on disk checking.")
		} else if progressDonePattern.Match(l) {
			fmt.Println("Finalized disk checking.")
		} else if finishedPattern.Match(l) {
			fmt.Println("Check done.")
		} else if interruptedPattern.Match(l) {
			var matches = interruptedPattern.FindSubmatch(l)
			stopBlock, _ := strconv.ParseUint(string(matches[1]), 10, 64)
			state.InterruptBlock = &stopBlock
			fmt.Printf("Interrupted at %d\n", stopBlock)
		} else if len(l) == 0 {
			// Nothing to do
		} else {
			log.Printf("Ignoring unknown line: %s\n", l)
		}
	}
	report <- state
}

type CheckState struct {
	From           uint64
	To             uint64
	InterruptBlock *uint64
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
