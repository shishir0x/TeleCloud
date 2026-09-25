package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBuildRestartCommand_PreservesArgs(t *testing.T) {
	execPath := "C:\\Program Files\\TeleCloud\\telecloud.exe"
	if runtime.GOOS != "windows" {
		execPath = "/usr/local/bin/telecloud"
	}

	args := []string{execPath, "-port=9090", "-version"}
	cmd := buildRestartCommand(execPath, args)

	if cmd.Path != execPath {
		t.Errorf("expected cmd.Path to be %s, got %s", execPath, cmd.Path)
	}

	expectedArgs := []string{execPath, "-port=9090", "-version"}
	if len(cmd.Args) != len(expectedArgs) {
		t.Fatalf("expected %d args, got %d", len(expectedArgs), len(cmd.Args))
	}
	for i, arg := range expectedArgs {
		if cmd.Args[i] != arg {
			t.Errorf("arg[%d]: expected %s, got %s", i, arg, cmd.Args[i])
		}
	}

	currentDir, _ := os.Getwd()
	if cmd.Dir != currentDir {
		t.Errorf("expected cmd.Dir to be %s, got %s", currentDir, cmd.Dir)
	}
}

func TestBuildRestartCommand_Execution(t *testing.T) {
	currentExec, err := os.Executable()
	if err != nil {
		t.Fatalf("failed to get current executable: %v", err)
	}

	// Run current test binary with a flag that exits immediately (-test.list=.)
	args := []string{currentExec, "-test.list=^$"}
	cmd := buildRestartCommand(currentExec, args)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start restart command process: %v", err)
	}

	if cmd.Process == nil || cmd.Process.Pid <= 0 {
		t.Fatalf("expected valid running process PID")
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("spawned process exited with error: %v", err)
	}
}

func TestBuildRestartCommand_InvalidExecutable(t *testing.T) {
	invalidExec := filepath.Join(t.TempDir(), "non_existent_executable_12345.exe")
	cmd := buildRestartCommand(invalidExec, []string{invalidExec})

	if err := cmd.Start(); err == nil {
		_ = cmd.Process.Kill()
		t.Fatalf("expected error starting non-existent executable, but got nil")
	}
}
