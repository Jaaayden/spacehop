//go:build !windows

package main

import "fmt"

// SpaceHop is a Windows program. Everything that does not need Windows
// (the engine, the interface logic, the drawing code) is built and tested
// on any system; see the README.
func main() { fmt.Println("SpaceHop runs on Windows. Build it with ./build.sh") }
