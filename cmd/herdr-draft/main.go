package main

import "fmt"

func version() string { return "0.1.0-dev" }

func main() { fmt.Println("herdr-draft", version()) }
