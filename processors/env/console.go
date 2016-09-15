package env

import (
	"fmt"
	"io"
	"os"
	"os/signal"

	syscall "github.com/docker/docker/pkg/signal"
	"github.com/docker/docker/pkg/term"
	"github.com/jcelliott/lumber"
	"github.com/nanobox-io/golang-docker-client"

	"github.com/nanobox-io/nanobox/models"
	"github.com/nanobox-io/nanobox/processors/provider"
	"github.com/nanobox-io/nanobox/util/display"
)

// Console ...
func Console(componentModel *models.Component, consoleConfig ConsoleConfig) error {
	// setup docker client
	if err := provider.Init(); err != nil {
		return err
	}

	// print the MOTD before dropping into the container
	if err := printMOTD(consoleConfig); err != nil {
		return fmt.Errorf("failed to print MOTD: %s", err.Error())
	}

	// set the default shell
	if consoleConfig.Shell == "" {
		consoleConfig.Shell = "bash"
	}

	// this is the default command to run in the container
	cmd := []string{"/bin/bash"}

	// check to see if there are any optional meta arguments that need to be handled
	switch {

	// if a current working directory (cwd) is provided then modify the command to
	// change into that directory before executing
	case consoleConfig.Cwd != "":
		cmd = append(cmd, "-c", fmt.Sprintf("cd %s; exec \"%s\"", consoleConfig.Cwd, consoleConfig.Shell))

	// if a command is provided then modify the command to exec that command after
	// running the base command
	case consoleConfig.Command != "":
		cmd = append(cmd, "-c", consoleConfig.Command)
	}

	// establish file descriptors for std streams
	stdInFD, isTerminal := term.GetFdInfo(os.Stdin)
	stdOutFD, _ := term.GetFdInfo(os.Stdout)

	// initiate a docker exec
	execConfig := docker.ExecConfig{
		ID:     componentModel.ID,
		User:   "gonano",
		Cmd:    cmd,
		Stdin:  true,
		Stdout: true,
		Stderr: true,
		Tty:    isTerminal,
	}

	exec, resp, err := docker.ExecStart(execConfig)
	if err != nil {
		lumber.Error("dockerexecerror: %s", err)
		return err
	}
	defer resp.Conn.Close()

	// if we are using a term, lets upgrade it to RawMode
	if isTerminal {
		go monitor(stdOutFD, exec.ID)

		oldInState, err := term.SetRawTerminal(stdInFD)
		if err == nil {
			defer term.RestoreTerminal(stdInFD, oldInState)
		}

		oldOutState, err := term.SetRawTerminalOutput(stdOutFD)
		if err == nil {
			defer term.RestoreTerminal(stdOutFD, oldOutState)
		}
	}

	go io.Copy(resp.Conn, os.Stdin)
	io.Copy(os.Stdout, resp.Reader)

	return nil
}

// printMOTD prints the motd with information for the user to connect
func printMOTD(consoleConfig ConsoleConfig) error {

	// print the MOTD
	display.MOTD()
	
	if consoleConfig.IsDev {
		// print the dev message
		display.InfoDevContainer(consoleConfig.DevIP)
		return nil
	}
	
	// print the generic message
	display.InfoLocalContainer()
	return nil
}

// monitor ...
func monitor(stdOutFD uintptr, execID string) {
	sigs := make(chan os.Signal, 1)

	signal.Notify(sigs, syscall.SIGWINCH)
	defer signal.Stop(sigs)

	// inform the server what the starting size is
	resize(stdOutFD, execID)

	// resize the tty for any signals received
	for range sigs {
		resize(stdOutFD, execID)
	}
}

func resize(fd uintptr, execID string) {
	ws, err := term.GetWinsize(fd)
	if err != nil {
		lumber.Error("env:console:resize():docker.ContainerExecResize(%d): %s", fd, err)
		return
	}

	// extract height and width
	w := int(ws.Width)
	h := int(ws.Height)

	err = docker.ContainerExecResize(execID, h, w)
	if err != nil {
		lumber.Error("env:console:resize():docker.ContainerExecResize(%s, %d, %d): %s", execID, h, w, err)
		return
	}
}
