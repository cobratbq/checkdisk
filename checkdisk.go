package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Please specify which device to check.")
		return
	}
	c := initConfig()
	var cmd *exec.Cmd
	if c.State.InterruptBlock != nil {
		log.Printf("Resuming an earlier check.\n")
		cmd = exec.Command("/usr/sbin/badblocks", "-sv", c.Device, fmt.Sprintf("%d", c.State.To), fmt.Sprintf("%d", *c.State.InterruptBlock))
	} else {
		log.Printf("Starting a new check.\n")
		cmd = exec.Command("/usr/sbin/badblocks", "-sv", c.Device)
	}
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
	var state = <-report
	cmd.Wait()
	close(stopSignal)
	//Process reported state
	updateState(c, state)
	if block := state.InterruptBlock; block != nil {
		log.Printf("Check has been interrupted at block %d\n", *block)
		log.Printf("So far (%d, %d, %d) errors have been found.\n", state.Errors[0], state.Errors[1], state.Errors[2])
	}
}

const ConfigFileName = "checkdisk.conf"

type config struct {
	Device string
	State  *CheckState
}

func initConfig() *config {
	var c config
	c.Device = os.Args[1]
	conffile, err := os.Open(ConfigFileName)
	if err != nil {
		log.Printf("Failed to open config file: %s\n", err.Error())
		c.State = new(CheckState)
	} else {
		defer conffile.Close()
		data, err := ioutil.ReadAll(conffile)
		var states map[string]*CheckState
		jsonErr := json.Unmarshal(data, &states)
		if err != nil {
			log.Printf("JSON unmarshal error: %s\n", jsonErr.Error())
		}
		state, ok := states[c.Device]
		if ok {
			c.State = state
		} else {
			c.State = new(CheckState)
		}
	}
	return &c
}

func updateState(c *config, state *CheckState) {
	if state == nil {
		log.Printf("State is nil. There is no state to persist.")
		return
	}
	var states = make(map[string]*CheckState)
	readconf, err := os.Open(ConfigFileName)
	if err == nil {
		data, err := ioutil.ReadAll(readconf)
		if err == nil {
			if err = json.Unmarshal(data, &states); err != nil {
				states = make(map[string]*CheckState)
			}
		}
		readconf.Close()
	}
	states[c.Device] = state
	data, err := json.Marshal(states)
	if err != nil {
		log.Printf("Failed to marshal check state: %s\n", err.Error())
		return
	}
	conffile, err := os.Create(ConfigFileName)
	if err != nil {
		log.Printf("Failed to open config file: %s\n", err.Error())
		return
	}
	defer conffile.Close()
	_, err = conffile.Write(data)
	if err != nil {
		log.Printf("Failed to write config file: %s\n", err.Error())
		return
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

var errorsNotation = `\((\d+)/(\d+)/(\d+)\ errors\)`

var startPattern = regexp.MustCompile(`^Checking blocks (\d+) to (\d+)$`)
var emptyProgressPattern = regexp.MustCompile(`^Checking for bad blocks[^:]*: `)
var progressPattern = regexp.MustCompile(`^Checking for bad blocks[^:]*: +(\d+\.\d+)% done, (\d+:\d+) elapsed. ` + errorsNotation + `$`)
var finishedPattern = regexp.MustCompile(`^Checking for bad blocks[^:]*: done`)
var summaryPattern = regexp.MustCompile(`^Pass completed, (\d+) bad blocks found. ` + errorsNotation + `$`)
var interruptedPattern = regexp.MustCompile(`^Interrupted at block (\d+)$`)

func processHandler(from io.ReadCloser, report chan<- *CheckState) {
	var out = make(chan []byte)
	go readLines(from, out)
	var state *CheckState
	for l := range out {
		switch {
		case startPattern.Match(l):
			var matches = startPattern.FindSubmatch(l)
			from, err := strconv.ParseUint(string(matches[1]), 10, 64)
			if err != nil {
				log.Printf("Error: %s\n", err.Error())
			}
			to, err := strconv.ParseUint(string(matches[2]), 10, 64)
			if err != nil {
				log.Printf("Error: %s\n", err.Error())
			}
			state = &CheckState{From: from, To: to}
		case progressPattern.Match(l):
			var matches = progressPattern.FindSubmatch(l)
			progress, err := strconv.ParseFloat(string(matches[1]), 0)
			if err != nil {
				log.Printf("Error: %s\n", err.Error())
			}
			extractErrorNumbers(state.Errors[:], matches, 3)
			log.Printf("Progress: %2.2f%%\n", progress)
		case summaryPattern.Match(l):
			var matches = summaryPattern.FindSubmatch(l)
			extractErrorNumbers(state.Errors[:], matches, 2)
			log.Printf("Check done. %s errors found.", matches[1])
		case interruptedPattern.Match(l):
			var matches = interruptedPattern.FindSubmatch(l)
			stopBlock, err := strconv.ParseUint(string(matches[1]), 10, 64)
			if err != nil {
				log.Printf("Error: %s\n", err.Error())
			}
			state.InterruptBlock = &stopBlock
		case emptyProgressPattern.Match(l):
		case finishedPattern.Match(l):
		case len(l) == 0:
			// Nothing to do since nothing actually got reported ...
		default:
			log.Printf("Ignoring unknown line: '%s'\n", l)
		}
	}
	report <- state
}

func extractErrorNumbers(data []uint64, matches [][]byte, offset uint) {
	for i := uint(0); i < 3; i++ {
		value, err := strconv.ParseUint(string(matches[offset+i]), 10, 64)
		if err != nil {
			log.Printf("Error: %s\n", err.Error())
		}
		data[i] = value
	}
}

type CheckState struct {
	From           uint64
	To             uint64
	InterruptBlock *uint64
	Errors         [3]uint64
}

func readLines(from io.Reader, out chan<- []byte) {
	defer close(out)
	var send = func(data []byte) {
		var result = make([]byte, len(data))
		copy(result, data)
		out <- result
	}
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
	var err error
	for err == nil {
		err = fill()
		for _, c := range buffer {
			switch c {
			case '\n':
				send(line)
				line = line[:0]
				firstBackspace = true
			case '\b':
				if firstBackspace {
					send(line)
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
	}
}
