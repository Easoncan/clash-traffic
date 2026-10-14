package main

import (
	"errors"
	"os"
	"testing"
	"time"
)

// A second instance waits for the first to let go of the data (review #17).
func TestLockDataDir(t *testing.T) {
	dir := t.TempDir()
	first, err := lockDataDir(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockDataDir(dir, 200*time.Millisecond); err == nil {
		t.Fatal("two holders of the data directory")
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		first.Close() // the first instance exits
	}()
	start := time.Now()
	second, err := lockDataDir(dir, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if time.Since(start) < 250*time.Millisecond {
		t.Fatal("did not wait for the first instance")
	}
}

// Without the data directory, the app does not start (review B1).
func TestInitNeedsTheLock(t *testing.T) {
	dir := t.TempDir()
	var c Core
	if err := c.Init(dir, errors.New("held")); err == nil {
		t.Fatal("started without the data directory lock")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("touched the data directory: %v", entries)
	}
}
