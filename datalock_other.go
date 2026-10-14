//go:build !unix && !windows

package main

import "os"

func tryLock(*os.File) error { return nil }
