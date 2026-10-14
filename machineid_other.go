//go:build !darwin && !linux && !windows

package main

func machineID() string { return "" }
